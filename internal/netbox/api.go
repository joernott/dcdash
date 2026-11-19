// SPDX-License-Identifier: GPL-3.0-or-later

package netbox

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/rs/zerolog/hlog"

	"github.com/joernott/dcdash/internal/auth"
	"github.com/joernott/dcdash/internal/httpx"
	"github.com/joernott/dcdash/internal/rbac"
)

// Module is the permission module of NetBox: "netbox:<name>:ipam:read" reads
// VLANs and free addresses, "netbox:<name>:sync:read" shows the comparison
// view, "netbox:<name>:sync:write" updates NetBox from it.
const Module = "netbox"

// Permission resources.
const (
	ResourceIPAM = "ipam"
	ResourceSync = "sync"
)

// Register adds the routes of the module.
func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/netbox", s.handleInfo)
	mux.HandleFunc("GET /api/v1/netbox/vlans/{vid}", s.handleVLAN)
	mux.HandleFunc("GET /api/v1/netbox/compare/guests/{cluster}", s.handleCompareGuests)
	mux.HandleFunc("POST /api/v1/netbox/compare/guests/{cluster}/{type}/{vmid}", s.handleApplyGuest)
	mux.HandleFunc("DELETE /api/v1/netbox/compare/guests/{cluster}/vms/{id}", s.handleDeleteVM)
	mux.HandleFunc("GET /api/v1/netbox/compare/addresses", s.handleCompareAddresses)
	mux.HandleFunc("GET /api/v1/netbox/addresses/{address}/projects", s.handleAddressProjects)
}

// Info describes the NetBox connection in GET /api/v1/netbox.
type Info struct {
	Name      string `json:"name"`
	WriteBack bool   `json:"writeBack"`
	// Clusters are the Proxmox clusters that are mapped to NetBox clusters.
	Clusters []string `json:"clusters"`
}

func (s *Service) handleInfo(w http.ResponseWriter, r *http.Request) {
	if auth.SubjectFromContext(r.Context()) == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, Info{Name: s.cfg.Name, WriteBack: s.cfg.WriteBack, Clusters: s.cfg.ClusterNames()})
}

// VLANView is a VLAN with its prefixes in GET /api/v1/netbox/vlans/{vid}.
type VLANView struct {
	VID         int          `json:"vid"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Prefixes    []PrefixView `json:"prefixes"`
}

// PrefixView is a prefix of a VLAN with its first free addresses.
type PrefixView struct {
	Prefix      string `json:"prefix"`
	Family      int    `json:"family"`
	Description string `json:"description"`
	// Free are the first free addresses with prefix length (up to maxFree,
	// one for each network device of a new guest); empty if the prefix is
	// full.
	Free []string `json:"free"`
}

func (s *Service) handleVLAN(w http.ResponseWriter, r *http.Request) {
	if !auth.Check(w, r, rbac.P(Module, s.cfg.Name, ResourceIPAM, rbac.ActionRead)) {
		return
	}
	vid, err := strconv.Atoi(r.PathValue("vid"))
	if err != nil || vid < 1 || vid > 4094 {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "vid: 1-4094")
		return
	}
	v, err := s.VLAN(r.Context(), vid)
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "not_found", "no VLAN "+strconv.Itoa(vid)+" in NetBox")
	case err != nil:
		s.writeError(w, r, err)
	default:
		httpx.WriteJSON(w, http.StatusOK, v)
	}
}

// maxFree is the number of free addresses per prefix in a VLANView.
const maxFree = 8

// VLAN returns the VLAN with that ID (in the configured VLAN group or site)
// with its prefixes in the configured VRF and their first free addresses.
func (s *Service) VLAN(ctx context.Context, vid int) (VLANView, error) {
	q, err := s.vlanQuery(ctx, vid)
	if err != nil {
		return VLANView{}, err
	}
	vlans, err := s.client.VLANs(ctx, q)
	if err != nil {
		return VLANView{}, err
	}
	if len(vlans) == 0 {
		return VLANView{}, ErrNotFound
	}
	// Without a VLAN group or site, the VLAN ID may be used more than once;
	// the first one wins (as NetBox sorts them).
	vl := vlans[0]
	out := VLANView{VID: vl.VID, Name: vl.Name, Description: vl.Description, Prefixes: []PrefixView{}}
	vrf, err := s.vrfFilter(ctx)
	if err != nil {
		return VLANView{}, err
	}
	prefixes, err := s.client.Prefixes(ctx, url.Values{"vlan_id": {strconv.FormatInt(vl.ID, 10)}, "vrf_id": {vrf}})
	if err != nil {
		return VLANView{}, err
	}
	for _, p := range prefixes {
		pp, err := netip.ParsePrefix(p.Prefix)
		if err != nil {
			continue
		}
		pv := PrefixView{Prefix: p.Prefix, Family: 4, Description: p.Description, Free: []string{}}
		if pp.Addr().Is6() {
			pv.Family = 6
		}
		free, err := s.client.AvailableIPs(ctx, p.ID, maxFree)
		if err != nil {
			return VLANView{}, err
		}
		for _, a := range free {
			pv.Free = append(pv.Free, a.Address)
		}
		out.Prefixes = append(out.Prefixes, pv)
	}
	slices.SortStableFunc(out.Prefixes, func(a, b PrefixView) int { return a.Family - b.Family })
	return out, nil
}

// writeError maps an error of the client to a response.
func (s *Service) writeError(w http.ResponseWriter, r *http.Request, err error) {
	hlog.FromRequest(r).Warn().Err(err).Msg("netbox request failed")
	switch {
	case errors.Is(err, ErrUnavailable):
		httpx.WriteError(w, http.StatusBadGateway, "upstream_unavailable", "NetBox not reachable")
	case errors.Is(err, ErrForbidden):
		httpx.WriteError(w, http.StatusBadGateway, "upstream_forbidden", "NetBox refused the request: "+err.Error())
	default:
		httpx.WriteError(w, http.StatusBadGateway, "upstream_error", strings.TrimPrefix(err.Error(), "NetBox: "))
	}
}

// The comparison view (task 12.4). It needs netbox:<name>:sync:read, updates
// need sync:write. Guests are shown only if the user may read them in the
// Proxmox module (proxmox:<cluster>:<type>/<vmid>:read).

func (s *Service) syncPermission(action rbac.Action) rbac.Permission {
	return rbac.P(Module, s.cfg.Name, ResourceSync, action)
}

// canReadGuest reports whether the user may read a guest in the Proxmox
// module.
func canReadGuest(subj *rbac.Subject, cluster string, container bool, vmid int) bool {
	return subj.Can(rbac.P("proxmox", cluster, guestType(container)+"/"+strconv.Itoa(vmid), rbac.ActionRead))
}

func (s *Service) handleCompareGuests(w http.ResponseWriter, r *http.Request) {
	if !auth.Check(w, r, s.syncPermission(rbac.ActionRead)) {
		return
	}
	subj := auth.SubjectFromContext(r.Context())
	cluster := r.PathValue("cluster")
	if _, ok := s.cfg.Clusters[cluster]; !ok {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "the cluster isn't mapped to NetBox")
		return
	}
	out, err := s.CompareGuests(r.Context(), cluster, func(container bool, vmid int) bool {
		return canReadGuest(subj, cluster, container, vmid)
	})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (s *Service) handleApplyGuest(w http.ResponseWriter, r *http.Request) {
	if !auth.Check(w, r, s.syncPermission(rbac.ActionWrite)) {
		return
	}
	subj := auth.SubjectFromContext(r.Context())
	cluster, typ := r.PathValue("cluster"), r.PathValue("type")
	vmid, err := strconv.Atoi(r.PathValue("vmid"))
	if err != nil || (typ != "qemu" && typ != "lxc") {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "type qemu or lxc and a numeric VMID")
		return
	}
	if _, ok := s.cfg.Clusters[cluster]; !ok || !canReadGuest(subj, cluster, typ == "lxc", vmid) {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "unknown guest")
		return
	}
	res, err := s.ApplyGuest(r.Context(), Base(r), cluster, typ == "lxc", vmid)
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "not_found", "unknown guest")
	case err != nil:
		s.writeError(w, r, err)
	default:
		httpx.WriteJSON(w, http.StatusOK, WriteBack{NetBox: res})
	}
}

// WriteBack is the response of an update from the comparison view.
type WriteBack struct {
	NetBox *Result `json:"netbox"`
}

func (s *Service) handleDeleteVM(w http.ResponseWriter, r *http.Request) {
	if !auth.Check(w, r, s.syncPermission(rbac.ActionWrite)) {
		return
	}
	cluster := r.PathValue("cluster")
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "numeric ID required")
		return
	}
	res, err := s.DeleteVM(r.Context(), Base(r), cluster, id)
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "not_found", "unknown NetBox VM")
	case errors.Is(err, ErrNotTagged):
		httpx.WriteError(w, http.StatusConflict, "not_tagged", err.Error())
	case errors.Is(err, ErrGuestExists):
		httpx.WriteError(w, http.StatusConflict, "guest_exists", err.Error())
	case err != nil:
		s.writeError(w, r, err)
	default:
		httpx.WriteJSON(w, http.StatusOK, WriteBack{NetBox: res})
	}
}

func (s *Service) handleCompareAddresses(w http.ResponseWriter, r *http.Request) {
	if !auth.Check(w, r, s.syncPermission(rbac.ActionRead)) {
		return
	}
	out, err := s.CompareAddresses(r.Context())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// AddressProjects is the response of GET /api/v1/netbox/addresses/{address}/projects.
type AddressProjects struct {
	Projects []string `json:"projects"`
}

// handleAddressProjects returns the projects of an address in NetBox, for
// the DHCP and DNS dialogs. Any logged-in user may read them.
func (s *Service) handleAddressProjects(w http.ResponseWriter, r *http.Request) {
	if auth.SubjectFromContext(r.Context()) == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
		return
	}
	a, err := netip.ParseAddr(r.PathValue("address"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "invalid address")
		return
	}
	ps, err := s.addressProjects(r.Context(), []string{a.String()})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if ps == nil {
		ps = []string{}
	}
	httpx.WriteJSON(w, http.StatusOK, AddressProjects{Projects: ps})
}
