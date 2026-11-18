// SPDX-License-Identifier: GPL-3.0-or-later

package proxmox

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/httpx"
	"github.com/joernott/dcdash/internal/rbac"
)

// HTTP API for changing VMs (see docs/adr/0005-proxmox-vm-configuration.md).

// configTimeout is how long a change waits for its Proxmox task.
var configTimeout = 60 * time.Second

func (s *Service) registerHardware(mux *http.ServeMux, base string) {
	g := base + "/{cluster}/guests/{type}/{vmid}/hardware"
	mux.HandleFunc("GET "+g, s.handleHardware)
	mux.HandleFunc("POST "+g+"/options", s.handleOptions)
	mux.HandleFunc("POST "+g+"/disks", s.handleCreateDisk)
	mux.HandleFunc("PUT "+g+"/disks/{disk}", s.handleUpdateDisk)
	mux.HandleFunc("POST "+g+"/disks/{disk}/detach", s.handleDetachDisk)
	mux.HandleFunc("POST "+g+"/disks/{disk}/attach", s.handleAttachDisk)
	mux.HandleFunc("DELETE "+g+"/disks/{disk}", s.handleDeleteDisk)
	mux.HandleFunc("POST "+g+"/networks", s.handleCreateNetwork)
	mux.HandleFunc("PUT "+g+"/networks/{net}", s.handleUpdateNetwork)
	mux.HandleFunc("DELETE "+g+"/networks/{net}", s.handleDeleteNetwork)
}

// vm returns the VM of the request after checking action on it.
func (s *Service) vm(w http.ResponseWriter, r *http.Request, action rbac.Action) (*Cluster, Resource, bool) {
	c, res, ok := s.guest(w, r, action)
	if !ok {
		return nil, Resource{}, false
	}
	if res.Type != TypeQEMU {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "only virtual machines can be changed")
		return nil, Resource{}, false
	}
	return c, res, true
}

// configurableVM is vm for changes: the cluster must have a provisioning
// configuration, which sets the limits.
func (s *Service) configurableVM(w http.ResponseWriter, r *http.Request, action rbac.Action) (*Cluster, Resource, config.ProxmoxProvisioning, bool) {
	c, res, ok := s.vm(w, r, action)
	if !ok {
		return nil, Resource{}, config.ProxmoxProvisioning{}, false
	}
	p, on := c.Provisioning()
	if !on {
		httpx.WriteError(w, http.StatusConflict, "provisioning_disabled", "changing guests is not enabled for this cluster")
		return nil, Resource{}, p, false
	}
	return c, res, p, true
}

func (s *Service) handleHardware(w http.ResponseWriter, r *http.Request) {
	c, res, ok := s.vm(w, r, rbac.ActionRead)
	if !ok {
		return
	}
	entries, err := c.Pending(r.Context(), res.Node, res.VMID)
	if err != nil {
		upstreamError(w, r, err)
		return
	}
	st, err := c.GuestStatus(r.Context(), res.Node, TypeQEMU, res.VMID)
	if err != nil {
		upstreamError(w, r, err)
		return
	}
	hw := buildHardware(entries)
	hw.Running = liveStatus(st) == "running"
	hw.Storages, hw.Bridges, hw.VLANs = []string{}, []string{}, []string{}
	if p, on := c.Provisioning(); on {
		hw.Configurable = true
		hw.Storages, hw.Bridges, hw.VLANs = p.Storages, p.Bridges, p.VLANs.Strings()
		hw.Limits = ProvisioningLimits{Cores: p.Limits.Cores, Memory: p.Limits.Memory.MiB(), Disk: p.Limits.Disk.GiB()}
	}
	httpx.WriteJSON(w, http.StatusOK, hw)
}

// ConfigResult is the response of a change.
type ConfigResult struct {
	// Key is the disk or network device that was created or attached.
	Key  string `json:"key,omitempty"`
	UPID string `json:"upid,omitempty"`
	Node string `json:"node"`
	// Done is false if the last task was still running after the waiting
	// time; the task list shows its end.
	Done bool `json:"done"`
}

// taskFn starts one Proxmox task and returns its ID ("" if it ran
// synchronously).
type taskFn func(ctx context.Context) (string, error)

// run executes steps one after the other, waiting for each task, audits the
// result as action and writes the response. Only the last task is limited
// by configTimeout, so that a change never stops half-way.
func (s *Service) run(w http.ResponseWriter, r *http.Request, c *Cluster, res Resource, action, key string,
	details map[string]any, steps ...taskFn) {
	target := c.Name() + "/" + guestResource(res.Type, res.VMID)
	details["node"] = res.Node
	out := ConfigResult{Key: key, Node: res.Node, Done: true}
	var err error
	for i, st := range steps {
		var upid string
		if upid, err = st(r.Context()); err != nil || upid == "" {
			if err != nil {
				break
			}
			continue
		}
		out.UPID = upid
		ctx, cancel := r.Context(), context.CancelFunc(func() {})
		if i == len(steps)-1 {
			ctx, cancel = context.WithTimeout(r.Context(), configTimeout)
		}
		err = c.WaitTask(ctx, upid)
		cancel()
		if errors.Is(err, context.DeadlineExceeded) && r.Context().Err() == nil {
			out.Done, err = false, nil
		}
		if err != nil {
			break
		}
	}
	if out.UPID != "" {
		details["upid"] = out.UPID
	}
	if err != nil {
		details["error"] = err.Error()
		s.audit.RecordRequest(r, action, target, audit.ResultFailure, details)
		writeConfigError(w, r, err)
		return
	}
	s.audit.RecordRequest(r, action, target, audit.ResultSuccess, details)
	status := http.StatusOK
	if !out.Done {
		status = http.StatusAccepted
	}
	httpx.WriteJSON(w, status, out)
}

// configStep is a taskFn that posts form to the configuration.
func configStep(c *Cluster, res Resource, form url.Values) taskFn {
	return func(ctx context.Context) (string, error) { return c.ConfigureVM(ctx, res.Node, res.VMID, form) }
}

// writeConfigError maps errors of a change; a configuration that changed in
// the meantime (digest mismatch) is a conflict the user resolves by reloading.
func writeConfigError(w http.ResponseWriter, r *http.Request, err error) {
	var (
		te  *TaskError
		pe  *Error
		msg string
	)
	switch {
	case errors.As(err, &te):
		msg = te.ExitStatus
	case errors.As(err, &pe):
		msg = pe.Message
	}
	if strings.Contains(msg, "checksum mismatch") || strings.Contains(msg, "modified configuration") || strings.Contains(msg, "digest") {
		httpx.WriteError(w, http.StatusConflict, "config_changed", "the configuration was changed in the meantime; reload it and try again")
		return
	}
	if te != nil {
		httpx.WriteError(w, http.StatusBadGateway, "upstream_error", te.ExitStatus)
		return
	}
	writeRequestError(w, r, err)
}

// decode reads the request body; on error it writes the response.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := httpx.DecodeJSON(w, r, v); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return false
	}
	return true
}

// withDigest adds the digest to a form.
func withDigest(form url.Values, digest string) url.Values {
	if digest != "" {
		form.Set("digest", digest)
	}
	return form
}

// --- options -----------------------------------------------------------------

// OptionsRequest is the body of POST .../hardware/options.
type OptionsRequest struct {
	Digest string            `json:"digest"`
	Set    map[string]string `json:"set,omitempty"`
	Delete []string          `json:"delete,omitempty"`
	// Revert drops pending changes of these keys (options, disks or network
	// devices).
	Revert []string `json:"revert,omitempty"`
}

func (s *Service) handleOptions(w http.ResponseWriter, r *http.Request) {
	c, res, p, ok := s.configurableVM(w, r, rbac.ActionWrite)
	if !ok {
		return
	}
	var req OptionsRequest
	if !decode(w, r, &req) {
		return
	}
	if len(req.Set) == 0 && len(req.Delete) == 0 && len(req.Revert) == 0 {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "nothing to change")
		return
	}
	for k, v := range req.Set {
		if err := validateOption(k, v, p); err != nil {
			writeRequestError(w, r, err)
			return
		}
	}
	for _, k := range req.Delete {
		if !slices.Contains(Options, k) || k == "name" {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_request", k+": can't be removed")
			return
		}
		if _, ok := req.Set[k]; ok {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_request", k+": set and removed at once")
			return
		}
	}
	for _, k := range req.Revert {
		if !configKeyPattern.MatchString(k) {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "revert: invalid key")
			return
		}
	}
	entries, err := c.Pending(r.Context(), res.Node, res.VMID)
	if err != nil {
		upstreamError(w, r, err)
		return
	}
	if err := checkResources(req, entries, p); err != nil {
		writeRequestError(w, r, err)
		return
	}

	form := url.Values{}
	keys := make([]string, 0, len(req.Set))
	for k, v := range req.Set {
		form.Set(k, v)
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if len(req.Delete) > 0 {
		form.Set("delete", strings.Join(req.Delete, ","))
	}
	if len(req.Revert) > 0 {
		form.Set("revert", strings.Join(req.Revert, ","))
	}
	details := map[string]any{"set": keys, "delete": req.Delete, "revert": req.Revert}
	s.run(w, r, c, res, "proxmox.guest.configure", "", details, configStep(c, res, withDigest(form, req.Digest)))
}

// checkResources checks CPU and memory after the change against the limits.
func checkResources(req OptionsRequest, entries []PendingEntry, p config.ProxmoxProvisioning) error {
	after := func(key, def string) int64 {
		v, ok := req.Set[key]
		if !ok && !slices.Contains(req.Delete, key) {
			v, ok = effective(entries, key)
		}
		if !ok || v == "" {
			v = def
		}
		n, _ := strconv.ParseInt(v, 10, 64)
		return n
	}
	if cores := after("cores", "1") * after("sockets", "1"); cores > int64(p.Limits.Cores) {
		return badRequest("cores × sockets: at most %d", p.Limits.Cores)
	}
	if mem, balloon := after("memory", "512"), after("balloon", "0"); balloon > mem {
		return badRequest("balloon: at most the memory (%d MiB)", mem)
	}
	return nil
}

// --- disks -------------------------------------------------------------------

// DiskRequest is the body of POST .../hardware/disks.
type DiskRequest struct {
	Digest   string `json:"digest"`
	Bus      string `json:"bus"`
	Storage  string `json:"storage"`
	SizeGiB  int64  `json:"sizeGiB"`
	Discard  bool   `json:"discard,omitempty"`
	SSD      bool   `json:"ssd,omitempty"`
	IOThread bool   `json:"iothread,omitempty"`
	// NoBackup excludes the disk from backups.
	NoBackup bool `json:"noBackup,omitempty"`
}

// DiskUpdate is the body of PUT .../hardware/disks/{disk}. Nil fields stay
// unchanged; SizeGiB 0 keeps the size.
type DiskUpdate struct {
	Digest   string `json:"digest"`
	SizeGiB  int64  `json:"sizeGiB,omitempty"`
	Discard  *bool  `json:"discard,omitempty"`
	SSD      *bool  `json:"ssd,omitempty"`
	IOThread *bool  `json:"iothread,omitempty"`
	NoBackup *bool  `json:"noBackup,omitempty"`
}

// DiskAction is the body of the detach, attach and delete calls.
type DiskAction struct {
	Digest string `json:"digest"`
	// Bus for attach.
	Bus string `json:"bus,omitempty"`
	// Confirm for delete: the disk key, e.g. "scsi1".
	Confirm string `json:"confirm,omitempty"`
}

// checkDiskOptions rejects options the bus doesn't support.
func checkDiskOptions(bus string, ssd, iothread bool) error {
	if ssd && bus == "virtio" {
		return badRequest("ssd: not possible on virtio")
	}
	if iothread && bus != "scsi" && bus != "virtio" {
		return badRequest("iothread: only on scsi and virtio")
	}
	return nil
}

func boolOpt(b bool, on string) string {
	if b {
		return on
	}
	return ""
}

func (s *Service) handleCreateDisk(w http.ResponseWriter, r *http.Request) {
	c, res, p, ok := s.configurableVM(w, r, rbac.ActionWrite)
	if !ok {
		return
	}
	var req DiskRequest
	if !decode(w, r, &req) {
		return
	}
	maxIndex, known := DiskBuses[req.Bus]
	switch {
	case !known:
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "bus: scsi, virtio, sata or ide")
		return
	case !slices.Contains(p.Storages, req.Storage):
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "storage "+strconv.Quote(req.Storage)+": not allowed")
		return
	case req.SizeGiB < 1 || req.SizeGiB > p.Limits.Disk.GiB():
		writeRequestError(w, r, badRequest("sizeGiB: between 1 and %d", p.Limits.Disk.GiB()))
		return
	}
	if err := checkDiskOptions(req.Bus, req.SSD, req.IOThread); err != nil {
		writeRequestError(w, r, err)
		return
	}
	entries, err := c.Pending(r.Context(), res.Node, res.VMID)
	if err != nil {
		upstreamError(w, r, err)
		return
	}
	key, free := freeKey(usedKeys(entries), req.Bus, maxIndex)
	if !free {
		httpx.WriteError(w, http.StatusConflict, "no_free_slot", "no free "+req.Bus+" slot")
		return
	}
	ps := []prop{{Value: req.Storage + ":" + strconv.FormatInt(req.SizeGiB, 10)}}
	ps = setProp(ps, "discard", boolOpt(req.Discard, "on"))
	ps = setProp(ps, "ssd", boolOpt(req.SSD, "1"))
	ps = setProp(ps, "iothread", boolOpt(req.IOThread, "1"))
	ps = setProp(ps, "backup", boolOpt(req.NoBackup, "0"))
	details := map[string]any{"disk": key, "storage": req.Storage, "sizeGiB": req.SizeGiB}
	s.run(w, r, c, res, "proxmox.disk.create", key, details,
		configStep(c, res, withDigest(url.Values{key: {formatProps(ps)}}, req.Digest)))
}

// attachedDisk returns the attached disk of the path with its raw value.
func attachedDisk(w http.ResponseWriter, r *http.Request, entries []PendingEntry) (HardwareDisk, string, bool) {
	key := r.PathValue("disk")
	e, found := entry(entries, key)
	value, active := valueString(e.Value)
	d, ok := parseDisk(key, value)
	if !isDiskKey(key) || !found || !active || !ok {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "no such disk")
		return HardwareDisk{}, "", false
	}
	return d, value, true
}

func (s *Service) handleUpdateDisk(w http.ResponseWriter, r *http.Request) {
	c, res, p, ok := s.configurableVM(w, r, rbac.ActionWrite)
	if !ok {
		return
	}
	var req DiskUpdate
	if !decode(w, r, &req) {
		return
	}
	entries, err := c.Pending(r.Context(), res.Node, res.VMID)
	if err != nil {
		upstreamError(w, r, err)
		return
	}
	d, value, ok := attachedDisk(w, r, entries)
	if !ok {
		return
	}
	const gib = 1 << 30
	curGiB := (d.SizeBytes + gib - 1) / gib
	if req.SizeGiB != 0 && (req.SizeGiB <= curGiB || req.SizeGiB > p.Limits.Disk.GiB()) {
		writeRequestError(w, r, badRequest("sizeGiB: more than %d (disks can't shrink) and at most %d", curGiB, p.Limits.Disk.GiB()))
		return
	}
	pick := func(b *bool, cur bool) bool {
		if b == nil {
			return cur
		}
		return *b
	}
	ssd, iothread := pick(req.SSD, d.SSD), pick(req.IOThread, d.IOThread)
	if err := checkDiskOptions(d.Bus, ssd, iothread); err != nil {
		writeRequestError(w, r, err)
		return
	}
	ps := parseProps(value)
	ps = setProp(ps, "discard", boolOpt(pick(req.Discard, d.Discard), "on"))
	ps = setProp(ps, "ssd", boolOpt(ssd, "1"))
	ps = setProp(ps, "iothread", boolOpt(iothread, "1"))
	ps = setProp(ps, "backup", boolOpt(pick(req.NoBackup, !d.Backup), "0"))

	var steps []taskFn
	details := map[string]any{"disk": d.Key}
	digest := req.Digest
	if nv := formatProps(ps); nv != value {
		steps = append(steps, configStep(c, res, withDigest(url.Values{d.Key: {nv}}, digest)))
		details["options"] = true
		digest = "" // the configuration changes with the first step
	}
	if req.SizeGiB != 0 {
		size := strconv.FormatInt(req.SizeGiB, 10) + "G"
		steps = append(steps, func(ctx context.Context) (string, error) {
			return c.ResizeVMDisk(ctx, res.Node, res.VMID, d.Key, size, digest)
		})
		details["sizeGiB"] = req.SizeGiB
	}
	if len(steps) == 0 {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "nothing to change")
		return
	}
	action := "proxmox.disk.update"
	if req.SizeGiB != 0 && details["options"] == nil {
		action = "proxmox.disk.resize"
	}
	s.run(w, r, c, res, action, d.Key, details, steps...)
}

func (s *Service) handleDetachDisk(w http.ResponseWriter, r *http.Request) {
	c, res, _, ok := s.configurableVM(w, r, rbac.ActionWrite)
	if !ok {
		return
	}
	var req DiskAction
	if !decode(w, r, &req) {
		return
	}
	entries, err := c.Pending(r.Context(), res.Node, res.VMID)
	if err != nil {
		upstreamError(w, r, err)
		return
	}
	d, _, ok := attachedDisk(w, r, entries)
	if !ok {
		return
	}
	details := map[string]any{"disk": d.Key, "volume": d.Volume}
	s.run(w, r, c, res, "proxmox.disk.detach", d.Key, details,
		configStep(c, res, withDigest(url.Values{"delete": {d.Key}}, req.Digest)))
}

// unusedDisk returns the unused disk of the path.
func unusedDisk(w http.ResponseWriter, r *http.Request, entries []PendingEntry) (HardwareDisk, bool) {
	key := r.PathValue("disk")
	e, found := entry(entries, key)
	value, active := valueString(e.Value)
	d, ok := parseDisk(key, value)
	if !unusedKeyPattern.MatchString(key) || !found || !active || !ok {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "no such unused disk")
		return HardwareDisk{}, false
	}
	return d, true
}

func (s *Service) handleAttachDisk(w http.ResponseWriter, r *http.Request) {
	c, res, _, ok := s.configurableVM(w, r, rbac.ActionWrite)
	if !ok {
		return
	}
	var req DiskAction
	if !decode(w, r, &req) {
		return
	}
	maxIndex, known := DiskBuses[req.Bus]
	if !known {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "bus: scsi, virtio, sata or ide")
		return
	}
	entries, err := c.Pending(r.Context(), res.Node, res.VMID)
	if err != nil {
		upstreamError(w, r, err)
		return
	}
	d, ok := unusedDisk(w, r, entries)
	if !ok {
		return
	}
	key, free := freeKey(usedKeys(entries), req.Bus, maxIndex)
	if !free {
		httpx.WriteError(w, http.StatusConflict, "no_free_slot", "no free "+req.Bus+" slot")
		return
	}
	details := map[string]any{"disk": key, "volume": d.Volume, "unused": d.Key}
	s.run(w, r, c, res, "proxmox.disk.attach", key, details,
		configStep(c, res, withDigest(url.Values{key: {d.Volume}}, req.Digest)))
}

func (s *Service) handleDeleteDisk(w http.ResponseWriter, r *http.Request) {
	c, res, _, ok := s.configurableVM(w, r, rbac.ActionAdmin)
	if !ok {
		return
	}
	var req DiskAction
	if !decode(w, r, &req) {
		return
	}
	key := r.PathValue("disk")
	if req.Confirm != key {
		httpx.WriteError(w, http.StatusBadRequest, "confirmation_mismatch", "the confirmation doesn't match the disk")
		return
	}
	entries, err := c.Pending(r.Context(), res.Node, res.VMID)
	if err != nil {
		upstreamError(w, r, err)
		return
	}
	var d HardwareDisk
	if unusedKeyPattern.MatchString(key) {
		if d, ok = unusedDisk(w, r, entries); !ok {
			return
		}
		s.run(w, r, c, res, "proxmox.disk.delete", key, map[string]any{"disk": key, "volume": d.Volume},
			configStep(c, res, withDigest(url.Values{"delete": {key}}, req.Digest)))
		return
	}
	if d, _, ok = attachedDisk(w, r, entries); !ok {
		return
	}
	st, err := c.GuestStatus(r.Context(), res.Node, TypeQEMU, res.VMID)
	if err != nil {
		upstreamError(w, r, err)
		return
	}
	if liveStatus(st) != "stopped" {
		httpx.WriteError(w, http.StatusConflict, "guest_running", "detach the disk first or stop the VM")
		return
	}
	// Detach, then destroy the unused disk the volume became.
	destroy := func(ctx context.Context) (string, error) {
		now, err := c.Pending(ctx, res.Node, res.VMID)
		if err != nil {
			return "", err
		}
		for _, e := range now {
			if v, _ := valueString(e.Value); unusedKeyPattern.MatchString(e.Key) && strings.HasPrefix(v, d.Volume) {
				return c.ConfigureVM(ctx, res.Node, res.VMID, url.Values{"delete": {e.Key}})
			}
		}
		return "", errors.New("the detached disk was not found among the unused disks")
	}
	s.run(w, r, c, res, "proxmox.disk.delete", key, map[string]any{"disk": key, "volume": d.Volume},
		configStep(c, res, withDigest(url.Values{"delete": {key}}, req.Digest)), destroy)
}

// --- network devices ---------------------------------------------------------

// NetworkRequest is the body of POST and PUT .../hardware/networks.
type NetworkRequest struct {
	Digest   string `json:"digest"`
	Model    string `json:"model"`
	Bridge   string `json:"bridge"`
	VLAN     int    `json:"vlan,omitempty"`
	Firewall bool   `json:"firewall,omitempty"`
	LinkDown bool   `json:"linkDown,omitempty"`
	// MAC of a new device; empty generates one. A change keeps the MAC.
	MAC string `json:"mac,omitempty"`
}

// NetworkAction is the body of DELETE .../hardware/networks/{net}.
type NetworkAction struct {
	Digest string `json:"digest"`
}

// validateNetwork checks a network device against the provisioning limits.
func validateNetwork(req *NetworkRequest, p config.ProxmoxProvisioning) error {
	if !slices.Contains(NetModels, req.Model) {
		return badRequest("model: one of %s", strings.Join(NetModels, ", "))
	}
	if !slices.Contains(p.Bridges, req.Bridge) {
		return badRequest("bridge %q: not allowed", req.Bridge)
	}
	if req.VLAN != 0 && !p.VLANs.Contains(req.VLAN) {
		return badRequest("vlan %d: not allowed", req.VLAN)
	}
	req.MAC = strings.ToUpper(strings.TrimSpace(req.MAC))
	if req.MAC != "" {
		first, _ := strconv.ParseUint(req.MAC[:min(2, len(req.MAC))], 16, 8)
		if !macPattern.MatchString(req.MAC) || first&1 == 1 {
			return badRequest("mac: a unicast MAC address like BC:24:11:12:34:56")
		}
	}
	return nil
}

func (s *Service) handleCreateNetwork(w http.ResponseWriter, r *http.Request) {
	c, res, p, ok := s.configurableVM(w, r, rbac.ActionWrite)
	if !ok {
		return
	}
	var req NetworkRequest
	if !decode(w, r, &req) {
		return
	}
	if err := validateNetwork(&req, p); err != nil {
		writeRequestError(w, r, err)
		return
	}
	if req.MAC == "" {
		mac, err := newMAC()
		if err != nil {
			upstreamError(w, r, err)
			return
		}
		req.MAC = mac
	}
	entries, err := c.Pending(r.Context(), res.Node, res.VMID)
	if err != nil {
		upstreamError(w, r, err)
		return
	}
	key, free := freeKey(usedKeys(entries), "net", maxNetDevices-1)
	if !free {
		httpx.WriteError(w, http.StatusConflict, "no_free_slot", "no free network slot")
		return
	}
	n := NetworkDevice{Model: req.Model, MAC: req.MAC, Bridge: req.Bridge, VLAN: req.VLAN, Firewall: req.Firewall, LinkDown: req.LinkDown}
	details := map[string]any{"net": key, "mac": req.MAC, "bridge": req.Bridge, "vlan": req.VLAN}
	s.run(w, r, c, res, "proxmox.network.create", key, details,
		configStep(c, res, withDigest(url.Values{key: {netValue(n, "")}}, req.Digest)))
}

// networkDevice returns the network device of the path with its raw value.
func networkDevice(w http.ResponseWriter, r *http.Request, entries []PendingEntry) (NetworkDevice, string, bool) {
	key := r.PathValue("net")
	e, found := entry(entries, key)
	value, active := valueString(e.Value)
	if !netKeyPattern.MatchString(key) || !found || !active {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "no such network device")
		return NetworkDevice{}, "", false
	}
	return parseNet(key, value), value, true
}

func (s *Service) handleUpdateNetwork(w http.ResponseWriter, r *http.Request) {
	c, res, p, ok := s.configurableVM(w, r, rbac.ActionWrite)
	if !ok {
		return
	}
	var req NetworkRequest
	if !decode(w, r, &req) {
		return
	}
	req.MAC = ""
	if err := validateNetwork(&req, p); err != nil {
		writeRequestError(w, r, err)
		return
	}
	entries, err := c.Pending(r.Context(), res.Node, res.VMID)
	if err != nil {
		upstreamError(w, r, err)
		return
	}
	cur, value, ok := networkDevice(w, r, entries)
	if !ok {
		return
	}
	n := NetworkDevice{Model: req.Model, MAC: cur.MAC, Bridge: req.Bridge, VLAN: req.VLAN, Firewall: req.Firewall, LinkDown: req.LinkDown}
	details := map[string]any{"net": cur.Key, "mac": cur.MAC, "bridge": req.Bridge, "vlan": req.VLAN}
	s.run(w, r, c, res, "proxmox.network.update", cur.Key, details,
		configStep(c, res, withDigest(url.Values{cur.Key: {netValue(n, value)}}, req.Digest)))
}

func (s *Service) handleDeleteNetwork(w http.ResponseWriter, r *http.Request) {
	c, res, _, ok := s.configurableVM(w, r, rbac.ActionWrite)
	if !ok {
		return
	}
	var req NetworkAction
	if !decode(w, r, &req) {
		return
	}
	entries, err := c.Pending(r.Context(), res.Node, res.VMID)
	if err != nil {
		upstreamError(w, r, err)
		return
	}
	cur, _, ok := networkDevice(w, r, entries)
	if !ok {
		return
	}
	details := map[string]any{"net": cur.Key, "mac": cur.MAC}
	s.run(w, r, c, res, "proxmox.network.delete", cur.Key, details,
		configStep(c, res, withDigest(url.Values{"delete": {cur.Key}}, req.Digest)))
}
