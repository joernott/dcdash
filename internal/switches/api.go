// SPDX-License-Identifier: GPL-3.0-or-later

package switches

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/rs/zerolog/hlog"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/auth"
	"github.com/joernott/dcdash/internal/httpx"
	"github.com/joernott/dcdash/internal/rbac"
	"github.com/joernott/dcdash/internal/switches/aoscx"
)

// Audit actions.
const (
	AuditPortUpdate = "switch.port.update"
	AuditVLANCreate = "switch.vlan.create"
	AuditVLANUpdate = "switch.vlan.update"
	AuditVLANDelete = "switch.vlan.delete"
)

// Limits of names and descriptions (AOS-CX: VLAN names up to 32, port
// descriptions up to 64 characters).
const (
	maxVLANName    = 32
	maxDescription = 64
	// minMACQuery is the minimum number of hex digits of a MAC search.
	minMACQuery = 4
)

var vlanNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.\-/]+( [A-Za-z0-9_.\-/]+)*$`)

// PortResource and VLANResource are the permission resources of a port and a VLAN.
func PortResource(name string) string { return "port/" + name }

// VLANResource is the permission resource of a VLAN.
func VLANResource(id int) string { return "vlan/" + strconv.Itoa(id) }

func (s *Service) registerNetwork(mux *http.ServeMux) {
	const base = "/api/v1/switches/{switch}"
	mux.HandleFunc("GET "+base+"/ports", s.handlePorts)
	mux.HandleFunc("PUT "+base+"/ports/{port...}", s.handleUpdatePort)
	mux.HandleFunc("GET "+base+"/vlans", s.handleVLANs)
	mux.HandleFunc("POST "+base+"/vlans", s.handleCreateVLAN)
	mux.HandleFunc("PUT "+base+"/vlans/{id}", s.handleRenameVLAN)
	mux.HandleFunc("DELETE "+base+"/vlans/{id}", s.handleDeleteVLAN)
	mux.HandleFunc("GET /api/v1/switches/macs", s.handleMACs)
}

// lookup returns the switch of the request, or writes 404 if it doesn't exist or
// the user may not see it.
func (s *Service) lookup(w http.ResponseWriter, r *http.Request) (*sw, *rbac.Subject, bool) {
	subj := auth.SubjectFromContext(r.Context())
	if subj == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
		return nil, nil, false
	}
	name := r.PathValue("switch")
	x := s.switches[name]
	if x == nil || !subj.Permissions.AllowsAny(Module, name, rbac.ActionRead) {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "switch not found")
		return nil, nil, false
	}
	return x, subj, true
}

// writeError maps an error of a switch to a response.
func (s *Service) writeError(w http.ResponseWriter, r *http.Request, err error) {
	hlog.FromRequest(r).Warn().Err(err).Msg("switch request failed")
	var e *aoscx.Error
	switch {
	case errors.Is(err, aoscx.ErrUnavailable), errors.Is(err, aoscx.ErrUnauthorized), errors.Is(err, aoscx.ErrSessionLimit):
		httpx.WriteError(w, http.StatusBadGateway, "upstream_unavailable", errorText(err))
	case errors.Is(err, aoscx.ErrForbidden):
		httpx.WriteError(w, http.StatusBadGateway, "upstream_forbidden", "the switch account may not do this (role operators?)")
	case errors.As(err, &e) && e.Status == http.StatusBadRequest:
		httpx.WriteError(w, http.StatusUnprocessableEntity, "rejected", "the switch rejected the change: "+e.Text)
	default:
		httpx.WriteError(w, http.StatusBadGateway, "upstream_error", errorText(err))
	}
}

// PortView is a port in GET /api/v1/switches/{switch}/ports.
type PortView struct {
	aoscx.Port
	// Uplink: a trunk or a port to another switch (LLDP).
	Uplink bool `json:"uplink"`
	// CanOperate: the user may enable and disable the port; CanWrite: may change it.
	CanOperate bool `json:"canOperate"`
	CanWrite   bool `json:"canWrite"`
	// NetBox is the port's interface in NetBox, if NetBox knows the switch.
	NetBox *PortNetBox `json:"netbox,omitempty"`
}

// PortNetBox links a port to its NetBox interface.
type PortNetBox struct {
	ID       int64    `json:"id"`
	Projects []string `json:"projects"`
}

// PortList is the response of GET /api/v1/switches/{switch}/ports.
type PortList struct {
	Switch string     `json:"switch"`
	Ports  []PortView `json:"ports"`
	VLANs  []VLANView `json:"vlans"`
	// NetBoxError is set if the NetBox interfaces could not be read.
	NetBoxError string `json:"netboxError,omitempty"`
}

func (s *Service) handlePorts(w http.ResponseWriter, r *http.Request) {
	x, subj, ok := s.lookup(w, r)
	if !ok {
		return
	}
	name := x.backend.Name()
	ports, err := x.backend.Ports(r.Context())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	vlans, err := x.backend.VLANs(r.Context())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	out := PortList{Switch: name, Ports: []PortView{}, VLANs: s.vlanViews(subj, name, vlans)}
	nb, nbErr := s.netboxPorts(r.Context(), x)
	if nbErr != nil {
		out.NetBoxError = nbErr.Error()
	}
	for _, p := range ports {
		res := PortResource(p.Name)
		if !subj.Can(rbac.P(Module, name, res, rbac.ActionRead)) {
			continue
		}
		v := PortView{Port: p, Uplink: p.Uplink(), CanOperate: subj.Can(rbac.P(Module, name, res, rbac.ActionOperate)),
			CanWrite: subj.Can(rbac.P(Module, name, res, rbac.ActionWrite)), NetBox: nb[p.Name]}
		out.Ports = append(out.Ports, v)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// PortUpdate is the body of PUT /api/v1/switches/{switch}/ports/{port}; missing
// fields stay unchanged.
type PortUpdate struct {
	Enabled     *bool   `json:"enabled,omitempty"`
	Description *string `json:"description,omitempty"`
	// Mode is "access" or "trunk".
	Mode *string `json:"mode,omitempty"`
	// VLAN is the access VLAN or the native VLAN of a trunk.
	VLAN *int `json:"vlan,omitempty"`
	// TrunkVLANs are the allowed VLANs of a trunk; an empty list allows all.
	TrunkVLANs *[]int `json:"trunkVlans,omitempty"`
}

// ChangeResult is the response of a change: the switch saved the
// configuration unless SaveError is set.
type ChangeResult struct {
	Saved     bool   `json:"saved"`
	SaveError string `json:"saveError,omitempty"`
	// NetBox is the outcome of the write-back, if NetBox is configured.
	NetBox *NetBoxResult `json:"netbox,omitempty"`
}

// PortResult is the response of PUT .../ports/{port}.
type PortResult struct {
	ChangeResult
	Port PortView `json:"port"`
}

// onlyEnabled reports whether the update changes only the admin state.
func (u PortUpdate) onlyEnabled() bool {
	return u.Enabled != nil && u.Description == nil && u.Mode == nil && u.VLAN == nil && u.TrunkVLANs == nil
}

func (s *Service) handleUpdatePort(w http.ResponseWriter, r *http.Request) {
	x, subj, ok := s.lookup(w, r)
	if !ok {
		return
	}
	name, portName := x.backend.Name(), r.PathValue("port")
	var u PortUpdate
	if err := httpx.DecodeJSON(w, r, &u); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	// Enabling and disabling needs operate, other changes write.
	action := rbac.ActionWrite
	if u.onlyEnabled() {
		action = rbac.ActionOperate
	}
	if !auth.Check(w, r, rbac.P(Module, name, PortResource(portName), action)) {
		return
	}
	x.writeMu.Lock()
	defer x.writeMu.Unlock()
	ctx := r.Context()
	before, err := x.backend.Port(ctx, portName)
	if errors.Is(err, aoscx.ErrNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "port not found")
		return
	}
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	vlans, err := x.backend.VLANs(ctx)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if msg := validatePortUpdate(u, before, vlans); msg != "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_port", msg)
		return
	}
	ch := aoscx.PortChange{Enabled: u.Enabled, Description: u.Description, Mode: u.Mode, VLAN: u.VLAN, TrunkVLANs: u.TrunkVLANs}
	if u.Description != nil {
		d := strings.TrimSpace(*u.Description)
		ch.Description = &d
	}
	tgt := name + "/" + portName
	details := map[string]any{"before": portSummary(before), "change": u}
	if err := x.backend.UpdatePort(ctx, portName, ch); err != nil {
		details["error"] = err.Error()
		s.audit.RecordRequest(r, AuditPortUpdate, tgt, audit.ResultFailure, details)
		s.writeError(w, r, err)
		return
	}
	res := PortResult{ChangeResult: s.save(ctx, x, details)}
	after, err := x.backend.Port(ctx, portName)
	if err == nil {
		details["after"] = portSummary(after)
		res.NetBox = s.netboxPort(ctx, r, x, after)
		res.Port = PortView{Port: after, Uplink: after.Uplink(), CanOperate: true, CanWrite: action == rbac.ActionWrite ||
			subj.Can(rbac.P(Module, name, PortResource(portName), rbac.ActionWrite))}
	}
	if res.NetBox != nil {
		details["netbox"] = res.NetBox.Status
	}
	s.audit.RecordRequest(r, AuditPortUpdate, tgt, audit.ResultSuccess, details)
	httpx.WriteJSON(w, http.StatusOK, res)
}

// save writes the running configuration to the startup configuration and
// notes the outcome in the audit details.
func (s *Service) save(ctx context.Context, x *sw, details map[string]any) ChangeResult {
	if err := x.backend.SaveConfig(ctx); err != nil {
		s.log.Error().Err(err).Str("switch", x.backend.Name()).Msg("saving the configuration failed")
		details["saved"], details["saveError"] = false, err.Error()
		return ChangeResult{SaveError: errorText(err)}
	}
	details["saved"] = true
	return ChangeResult{Saved: true}
}

// portSummary is how a port appears in audit details.
func portSummary(p aoscx.Port) map[string]any {
	m := map[string]any{"enabled": p.Enabled, "description": p.Description, "mode": p.Mode, "vlan": p.VLAN}
	if p.Mode == aoscx.ModeTrunk {
		m["trunkVlans"], m["allVlans"] = p.TrunkVLANs, p.AllVLANs
	}
	return m
}

func validatePortUpdate(u PortUpdate, p aoscx.Port, vlans []aoscx.VLAN) string {
	exists := func(id int) bool { return slices.ContainsFunc(vlans, func(v aoscx.VLAN) bool { return v.ID == id }) }
	if u.Description != nil {
		d := strings.TrimSpace(*u.Description)
		if len(d) > maxDescription {
			return fmt.Sprintf("the description is longer than %d characters", maxDescription)
		}
		for _, c := range d {
			if c > unicode.MaxASCII || !unicode.IsPrint(c) {
				return "the description may only contain printable ASCII characters"
			}
		}
	}
	vlanChange := u.Mode != nil || u.VLAN != nil || u.TrunkVLANs != nil
	if vlanChange && p.LAG != "" {
		return "the port belongs to " + p.LAG + "; change the VLANs of the LAG"
	}
	mode := p.Mode
	if u.Mode != nil {
		if *u.Mode != aoscx.ModeAccess && *u.Mode != aoscx.ModeTrunk {
			return "mode must be access or trunk"
		}
		mode = *u.Mode
	}
	if u.VLAN != nil && !exists(*u.VLAN) {
		return fmt.Sprintf("VLAN %d does not exist on the switch", *u.VLAN)
	}
	if u.TrunkVLANs != nil {
		if mode != aoscx.ModeTrunk {
			return "trunk VLANs need mode trunk"
		}
		for _, id := range *u.TrunkVLANs {
			if !exists(id) {
				return fmt.Sprintf("VLAN %d does not exist on the switch", id)
			}
		}
	}
	return ""
}

// VLANView is a VLAN in the responses.
type VLANView struct {
	aoscx.VLAN
	CanWrite bool `json:"canWrite"`
}

func (s *Service) vlanViews(subj *rbac.Subject, name string, vlans []aoscx.VLAN) []VLANView {
	out := make([]VLANView, 0, len(vlans))
	for _, v := range vlans {
		out = append(out, VLANView{VLAN: v, CanWrite: subj.Can(rbac.P(Module, name, VLANResource(v.ID), rbac.ActionWrite))})
	}
	return out
}

// VLANList is the response of GET /api/v1/switches/{switch}/vlans.
type VLANList struct {
	Switch string     `json:"switch"`
	VLANs  []VLANView `json:"vlans"`
	// Usage maps a VLAN ID to the ports that use it (access, native or trunk).
	Usage map[string][]string `json:"usage"`
}

func (s *Service) handleVLANs(w http.ResponseWriter, r *http.Request) {
	x, subj, ok := s.lookup(w, r)
	if !ok {
		return
	}
	vlans, err := x.backend.VLANs(r.Context())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	ports, err := x.backend.Ports(r.Context())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	out := VLANList{Switch: x.backend.Name(), VLANs: s.vlanViews(subj, x.backend.Name(), vlans), Usage: map[string][]string{}}
	for _, v := range vlans {
		if u := usage(v.ID, ports); len(u) > 0 {
			out.Usage[strconv.Itoa(v.ID)] = u
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// usage returns the ports that use a VLAN. A trunk that allows all VLANs only
// counts for its native VLAN.
func usage(id int, ports []aoscx.Port) []string {
	var out []string
	for _, p := range ports {
		if p.VLAN == id || slices.Contains(p.TrunkVLANs, id) {
			out = append(out, p.Name)
		}
	}
	return out
}

// VLANRequest is the body of POST and PUT .../vlans.
type VLANRequest struct {
	ID   int    `json:"id,omitempty"`
	Name string `json:"name"`
}

// VLANResult is the response of a VLAN change.
type VLANResult struct {
	ChangeResult
	VLAN *aoscx.VLAN `json:"vlan,omitempty"`
}

func validateVLANName(name string) string {
	if len(name) > maxVLANName || !vlanNamePattern.MatchString(name) {
		return fmt.Sprintf("the name must have 1 to %d letters, digits, '_', '.', '-', '/' or single spaces", maxVLANName)
	}
	return ""
}

func (s *Service) handleCreateVLAN(w http.ResponseWriter, r *http.Request) {
	x, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	var req VLANRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	name := x.backend.Name()
	if req.ID < 2 || req.ID > 4094 {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_vlan", "the VLAN ID must be between 2 and 4094")
		return
	}
	if !auth.Check(w, r, rbac.P(Module, name, VLANResource(req.ID), rbac.ActionWrite)) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if msg := validateVLANName(req.Name); msg != "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_vlan", msg)
		return
	}
	x.writeMu.Lock()
	defer x.writeMu.Unlock()
	ctx := r.Context()
	vlans, err := x.backend.VLANs(ctx)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if slices.ContainsFunc(vlans, func(v aoscx.VLAN) bool { return v.ID == req.ID }) {
		httpx.WriteError(w, http.StatusConflict, "exists", fmt.Sprintf("VLAN %d already exists on the switch", req.ID))
		return
	}
	tgt := name + "/" + VLANResource(req.ID)
	details := map[string]any{"id": req.ID, "name": req.Name}
	if err := x.backend.CreateVLAN(ctx, req.ID, req.Name); err != nil {
		details["error"] = err.Error()
		s.audit.RecordRequest(r, AuditVLANCreate, tgt, audit.ResultFailure, details)
		s.writeError(w, r, err)
		return
	}
	res := VLANResult{ChangeResult: s.save(ctx, x, details), VLAN: &aoscx.VLAN{ID: req.ID, Name: req.Name, Type: "static"}}
	res.NetBox = s.netboxVLAN(ctx, r, x, req.ID, req.Name, false)
	s.audit.RecordRequest(r, AuditVLANCreate, tgt, audit.ResultSuccess, withNetBox(details, res.NetBox))
	httpx.WriteJSON(w, http.StatusCreated, res)
}

// vlanID parses the VLAN ID of the path and checks the permission.
func (s *Service) vlanID(w http.ResponseWriter, r *http.Request, name string) (int, bool) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id < 1 || id > 4094 {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "VLAN not found")
		return 0, false
	}
	return id, auth.Check(w, r, rbac.P(Module, name, VLANResource(id), rbac.ActionWrite))
}

// findVLAN returns a VLAN of the switch or writes 404.
func (s *Service) findVLAN(w http.ResponseWriter, r *http.Request, x *sw, id int) (aoscx.VLAN, []aoscx.VLAN, bool) {
	vlans, err := x.backend.VLANs(r.Context())
	if err != nil {
		s.writeError(w, r, err)
		return aoscx.VLAN{}, nil, false
	}
	for _, v := range vlans {
		if v.ID == id {
			return v, vlans, true
		}
	}
	httpx.WriteError(w, http.StatusNotFound, "not_found", "VLAN not found")
	return aoscx.VLAN{}, nil, false
}

func (s *Service) handleRenameVLAN(w http.ResponseWriter, r *http.Request) {
	x, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	name := x.backend.Name()
	id, ok := s.vlanID(w, r, name)
	if !ok {
		return
	}
	var req VLANRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if msg := validateVLANName(req.Name); msg != "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_vlan", msg)
		return
	}
	x.writeMu.Lock()
	defer x.writeMu.Unlock()
	ctx := r.Context()
	v, _, ok := s.findVLAN(w, r, x, id)
	if !ok {
		return
	}
	tgt := name + "/" + VLANResource(id)
	details := map[string]any{"id": id, "name": req.Name, "previousName": v.Name}
	if err := x.backend.RenameVLAN(ctx, id, req.Name); err != nil {
		details["error"] = err.Error()
		s.audit.RecordRequest(r, AuditVLANUpdate, tgt, audit.ResultFailure, details)
		s.writeError(w, r, err)
		return
	}
	v.Name = req.Name
	res := VLANResult{ChangeResult: s.save(ctx, x, details), VLAN: &v}
	res.NetBox = s.netboxVLAN(ctx, r, x, id, req.Name, false)
	s.audit.RecordRequest(r, AuditVLANUpdate, tgt, audit.ResultSuccess, withNetBox(details, res.NetBox))
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (s *Service) handleDeleteVLAN(w http.ResponseWriter, r *http.Request) {
	x, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	name := x.backend.Name()
	id, ok := s.vlanID(w, r, name)
	if !ok {
		return
	}
	if id == aoscx.DefaultVLAN {
		httpx.WriteError(w, http.StatusConflict, "default_vlan", "VLAN 1 cannot be deleted")
		return
	}
	x.writeMu.Lock()
	defer x.writeMu.Unlock()
	ctx := r.Context()
	v, _, ok := s.findVLAN(w, r, x, id)
	if !ok {
		return
	}
	ports, err := x.backend.Ports(ctx)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	// Deleting a VLAN in use would cut off its ports; dcdash refuses.
	if u := usage(id, ports); len(u) > 0 {
		httpx.WriteError(w, http.StatusConflict, "vlan_in_use", "the VLAN is used by "+strings.Join(u, ", "))
		return
	}
	tgt := name + "/" + VLANResource(id)
	details := map[string]any{"id": id, "name": v.Name}
	if err := x.backend.DeleteVLAN(ctx, id); err != nil {
		details["error"] = err.Error()
		s.audit.RecordRequest(r, AuditVLANDelete, tgt, audit.ResultFailure, details)
		s.writeError(w, r, err)
		return
	}
	res := VLANResult{ChangeResult: s.save(ctx, x, details)}
	res.NetBox = s.netboxVLAN(ctx, r, x, id, v.Name, true)
	s.audit.RecordRequest(r, AuditVLANDelete, tgt, audit.ResultSuccess, withNetBox(details, res.NetBox))
	httpx.WriteJSON(w, http.StatusOK, res)
}

func withNetBox(details map[string]any, nb *NetBoxResult) map[string]any {
	if nb != nil {
		details["netbox"] = nb.Status
		if nb.Error != "" {
			details["netboxError"] = nb.Error
		}
	}
	return details
}

// MACResult is a MAC address found on a switch port.
type MACResult struct {
	Switch          string `json:"switch"`
	MAC             string `json:"mac"`
	VLAN            int    `json:"vlan"`
	Port            string `json:"port"`
	PortDescription string `json:"portDescription,omitempty"`
	// Uplink: the port leads to another switch, so the device is behind it.
	Uplink bool   `json:"uplink"`
	From   string `json:"from"`
}

// SwitchError is a switch that could not be searched.
type SwitchError struct {
	Switch string `json:"switch"`
	Error  string `json:"error"`
}

// MACSearch is the response of GET /api/v1/switches/macs.
type MACSearch struct {
	Query   string        `json:"query"`
	Results []MACResult   `json:"results"`
	Errors  []SwitchError `json:"errors"`
}

// normalizeMAC returns the hex digits of a (partial) MAC address in lower
// case, or "" if it contains other characters.
func normalizeMAC(q string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(strings.TrimSpace(q)) {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f':
			b.WriteRune(c)
		case c == ':', c == '-', c == '.':
		default:
			return ""
		}
	}
	return b.String()
}

func (s *Service) handleMACs(w http.ResponseWriter, r *http.Request) {
	subj := auth.SubjectFromContext(r.Context())
	if subj == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
		return
	}
	q := normalizeMAC(r.URL.Query().Get("q"))
	if len(q) < minMACQuery {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_query",
			fmt.Sprintf("enter at least %d hex digits of the MAC address", minMACQuery))
		return
	}
	out := MACSearch{Query: q, Results: []MACResult{}, Errors: []SwitchError{}}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, n := range s.order {
		if !subj.Permissions.AllowsAny(Module, n, rbac.ActionRead) {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := s.searchMAC(r.Context(), subj, s.switches[n], q)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				out.Errors = append(out.Errors, SwitchError{Switch: n, Error: errorText(err)})
				return
			}
			out.Results = append(out.Results, res...)
		}()
	}
	wg.Wait()
	// Access ports first: that's where the device is connected.
	sort.Slice(out.Results, func(i, j int) bool {
		a, b := out.Results[i], out.Results[j]
		if a.Uplink != b.Uplink {
			return !a.Uplink
		}
		if a.MAC != b.MAC {
			return a.MAC < b.MAC
		}
		if a.Switch != b.Switch {
			return a.Switch < b.Switch
		}
		return a.Port < b.Port
	})
	sort.Slice(out.Errors, func(i, j int) bool { return out.Errors[i].Switch < out.Errors[j].Switch })
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (s *Service) searchMAC(ctx context.Context, subj *rbac.Subject, x *sw, q string) ([]MACResult, error) {
	name := x.backend.Name()
	macs, err := x.backend.MACs(ctx)
	if err != nil {
		return nil, err
	}
	var hits []aoscx.MACEntry
	for _, m := range macs {
		if strings.Contains(strings.ReplaceAll(m.MAC, ":", ""), q) &&
			subj.Can(rbac.P(Module, name, PortResource(m.Port), rbac.ActionRead)) {
			hits = append(hits, m)
		}
	}
	if len(hits) == 0 {
		return nil, nil
	}
	ports, err := x.backend.Ports(ctx)
	if err != nil {
		return nil, err
	}
	byName := map[string]aoscx.Port{}
	for _, p := range ports {
		byName[p.Name] = p
	}
	out := make([]MACResult, 0, len(hits))
	for _, m := range hits {
		p := byName[m.Port]
		// A port in a LAG is an uplink if the LAG is.
		uplink := p.Uplink() || (p.LAG != "" && byName[p.LAG].Uplink())
		out = append(out, MACResult{Switch: name, MAC: m.MAC, VLAN: m.VLAN, Port: m.Port, PortDescription: p.Description,
			Uplink: uplink, From: m.From})
	}
	return out, nil
}
