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
