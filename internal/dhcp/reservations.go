// SPDX-License-Identifier: GPL-3.0-or-later

package dhcp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"strconv"
	"strings"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/dhcp/kea"
	"github.com/joernott/dcdash/internal/httpx"
	"github.com/joernott/dcdash/internal/netbox"
	"github.com/joernott/dcdash/internal/rbac"
)

// ReservationView is a reservation in API responses.
type ReservationView struct {
	// ID identifies the reservation: subnet and identifier.
	ID string `json:"id"`
	// Version changes with every change of the reservation (optimistic
	// locking).
	Version        string   `json:"version"`
	SubnetID       uint32   `json:"subnetId"`
	Subnet         string   `json:"subnet"`
	IdentifierType string   `json:"identifierType"`
	Identifier     string   `json:"identifier"`
	IPAddresses    []string `json:"ipAddresses"`
	Prefixes       []string `json:"prefixes,omitempty"`
	Hostname       string   `json:"hostname,omitempty"`
	ClientClasses  []string `json:"clientClasses,omitempty"`
	// Options is the number of option values; dcdash shows but does not edit them.
	Options int `json:"options,omitempty"`
	// Source is "config" (configuration file, read-only) or "database".
	Source   string `json:"source"`
	ReadOnly bool   `json:"readOnly"`
	// NetBox is the result of the write-back to NetBox after a change;
	// missing if changes aren't written to NetBox.
	NetBox *netbox.Result `json:"netbox,omitempty"`
}

// ReservationList is the response of GET .../reservations.
type ReservationList struct {
	Reservations []ReservationView `json:"reservations"`
}

// ReservationRequest is the body of POST and PUT .../reservations. For PUT,
// subnet and identifier come from the ID and must not change.
type ReservationRequest struct {
	SubnetID       uint32   `json:"subnetId"`
	IdentifierType string   `json:"identifierType"`
	Identifier     string   `json:"identifier"`
	IPAddresses    []string `json:"ipAddresses"`
	Prefixes       []string `json:"prefixes,omitempty"`
	Hostname       string   `json:"hostname,omitempty"`
	Version        string   `json:"version,omitempty"`
}

// reservationID encodes subnet and identifier.
func reservationID(subnetID uint32, typ, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatUint(uint64(subnetID), 10) + "|" + typ + "|" + id))
}

func parseReservationID(s string) (subnetID uint32, typ, id string, err error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return 0, "", "", errors.New("invalid reservation ID")
	}
	parts := strings.SplitN(string(b), "|", 3)
	if len(parts) != 3 {
		return 0, "", "", errors.New("invalid reservation ID")
	}
	n, err := strconv.ParseUint(parts[0], 10, 32)
	if err != nil {
		return 0, "", "", errors.New("invalid reservation ID")
	}
	return uint32(n), parts[1], parts[2], nil
}

// reservationVersion is a hash of the reservation as Kea returns it.
func reservationVersion(res kea.Reservation) string {
	b, _ := json.Marshal(res)
	sum := sha256.Sum256(b)
	return base64.RawURLEncoding.EncodeToString(sum[:12])
}

func (q request) reservationView(res kea.Reservation) ReservationView {
	typ, id := res.Identifier()
	ips := res.IPAddresses
	if res.IPAddress != "" && res.IPAddress != "0.0.0.0" {
		ips = append([]string{res.IPAddress}, ips...)
	}
	if ips == nil {
		ips = []string{}
	}
	return ReservationView{
		ID: reservationID(res.SubnetID, typ, id), Version: reservationVersion(res),
		SubnetID: res.SubnetID, Subnet: subnetName(q.subnets, res.SubnetID),
		IdentifierType: typ, Identifier: id, IPAddresses: ips, Prefixes: res.Prefixes, Hostname: res.Hostname,
		ClientClasses: res.ClientClasses, Options: len(res.Options), Source: res.Source,
		ReadOnly: res.Source != kea.SourceDatabase || !q.info.Capabilities.ReservationWrite || !q.can(res.SubnetID, rbac.ActionWrite),
	}
}

func (s *Service) handleReservations(w http.ResponseWriter, r *http.Request) {
	q, ok := s.server(w, r, rbac.ActionRead)
	if !ok {
		return
	}
	if !q.info.Capabilities.Reservations {
		unsupported(w, "the DHCP server does not offer reservations (host_cmds hook)")
		return
	}
	subnet := int64(-1)
	if v := r.URL.Query().Get("subnet"); v != "" {
		n, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "invalid subnet ID")
			return
		}
		if !q.check(w, r, uint32(n), rbac.ActionRead) {
			return
		}
		subnet = int64(n)
	}
	list, err := q.srv.backend.Reservations(r.Context(), subnet)
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := ReservationList{Reservations: []ReservationView{}}
	for _, res := range list {
		if q.can(res.SubnetID, rbac.ActionRead) {
			out.Reservations = append(out.Reservations, q.reservationView(res))
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

var (
	hexBytes = regexp.MustCompile(`^[0-9a-f]{2}(:[0-9a-f]{2})*$`)
	hostname = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)
)

// normalizeHex accepts hex bytes separated by ':' or '-', or not separated,
// and returns lower-case colon-separated hex.
func normalizeHex(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "-", ":")
	if !strings.Contains(s, ":") && len(s)%2 == 0 {
		if _, err := hex.DecodeString(s); err == nil {
			var parts []string
			for i := 0; i < len(s); i += 2 {
				parts = append(parts, s[i:i+2])
			}
			s = strings.Join(parts, ":")
		}
	}
	return s, hexBytes.MatchString(s)
}

// validationError is a 400 with a message for the user.
type validationError struct{ msg string }

func (e *validationError) Error() string { return e.msg }

func invalid(format string, args ...any) error { return &validationError{fmt.Sprintf(format, args...)} }

// reservation checks a request and builds the Kea reservation.
func (q request) reservation(req ReservationRequest) (kea.Reservation, error) {
	family := q.srv.backend.Family()
	res := kea.Reservation{SubnetID: req.SubnetID}
	prefix, known := resource(q.subnets, req.SubnetID)
	if !known {
		return res, invalid("unknown subnet %d", req.SubnetID)
	}
	var pfx netip.Prefix
	if req.SubnetID != 0 {
		p, err := netip.ParsePrefix(prefix)
		if err != nil {
			return res, invalid("subnet %d has no valid prefix", req.SubnetID)
		}
		pfx = p
	}

	id, ok := normalizeHex(req.Identifier)
	switch {
	case !ok:
		return res, invalid("the identifier must be hex bytes, e.g. aa:bb:cc:dd:ee:ff")
	case req.IdentifierType == "hw-address":
		mac, err := hwAddr(id)
		if err != nil {
			return res, err
		}
		res.HWAddress = mac
	case req.IdentifierType == "client-id" && family == "v4":
		res.ClientID = id
	case req.IdentifierType == "duid" && family == "v6":
		res.DUID = id
	default:
		return res, invalid("identifier type %q is not supported for DHCP%s", req.IdentifierType, strings.TrimPrefix(family, "v"))
	}

	for _, s := range req.IPAddresses {
		ip, err := netip.ParseAddr(strings.TrimSpace(s))
		switch {
		case err != nil:
			return res, invalid("%q is not an IP address", s)
		case family == "v4" && !ip.Is4(), family == "v6" && (!ip.Is6() || ip.Is4In6()):
			return res, invalid("%s is not an IPv%s address", s, strings.TrimPrefix(family, "v"))
		case pfx.IsValid() && !pfx.Contains(ip):
			return res, invalid("%s is not in subnet %s", ip, prefix)
		}
		if family == "v4" {
			res.IPAddress = ip.String()
		} else {
			res.IPAddresses = append(res.IPAddresses, ip.String())
		}
	}
	if family == "v4" && len(req.IPAddresses) > 1 {
		return res, invalid("a DHCPv4 reservation has at most one address")
	}
	for _, s := range req.Prefixes {
		p, err := netip.ParsePrefix(strings.TrimSpace(s))
		if family != "v6" || err != nil || !p.Addr().Is6() || p.Masked() != p {
			return res, invalid("%q is not an IPv6 prefix", s)
		}
		res.Prefixes = append(res.Prefixes, p.String())
	}
	h := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(req.Hostname), "."))
	if h != "" && (len(h) > 253 || !hostname.MatchString(h)) {
		return res, invalid("%q is not a valid host name", req.Hostname)
	}
	res.Hostname = h
	if res.IPAddress == "" && len(res.IPAddresses) == 0 && len(res.Prefixes) == 0 && h == "" {
		return res, invalid("give an address, a prefix or a host name")
	}
	return res, nil
}

func hwAddr(s string) (string, error) {
	mac, err := net.ParseMAC(s)
	if err != nil || len(mac) != 6 {
		return "", invalid("%q is not a MAC address", s)
	}
	return mac.String(), nil
}

// writeTarget returns the server that stores reservation changes: the
// requested server, or another server of its HA group if it cannot (the
// partners of an HA pair share the host database).
func (s *Service) writeTarget(ctx context.Context, q request) (Backend, error) {
	if q.info.Capabilities.ReservationWrite {
		return q.srv.backend, nil
	}
	for _, p := range s.partners(q.name) {
		if info, _, err := s.state(ctx, p, false); err == nil && info.Capabilities.ReservationWrite {
			return p.backend, nil
		}
	}
	return nil, errNoHostDatabase
}

var errNoHostDatabase = errors.New("no host database")

func reservationDetails(res kea.Reservation) map[string]any {
	typ, id := res.Identifier()
	d := map[string]any{"subnetId": res.SubnetID, "identifierType": typ, "identifier": id}
	if res.IPAddress != "" {
		d["ipAddress"] = res.IPAddress
	}
	if len(res.IPAddresses) > 0 {
		d["ipAddresses"] = res.IPAddresses
	}
	if len(res.Prefixes) > 0 {
		d["prefixes"] = res.Prefixes
	}
	if res.Hostname != "" {
		d["hostname"] = res.Hostname
	}
	return d
}

// writeChangeError writes the response for a failed reservation change.
func writeChangeError(w http.ResponseWriter, r *http.Request, err error) {
	var ve *validationError
	switch {
	case errors.As(err, &ve):
		httpx.WriteError(w, http.StatusBadRequest, "invalid_reservation", ve.msg)
	case errors.Is(err, errNoHostDatabase):
		httpx.WriteError(w, http.StatusConflict, "unsupported", "reservations can only be changed on servers with a host database")
	default:
		writeError(w, r, err)
	}
}

func (s *Service) handleCreateReservation(w http.ResponseWriter, r *http.Request) {
	q, ok := s.server(w, r, rbac.ActionWrite)
	if !ok {
		return
	}
	var req ReservationRequest
	if !decode(w, r, &req) {
		return
	}
	if !q.check(w, r, req.SubnetID, rbac.ActionWrite) {
		return
	}
	res, err := q.reservation(req)
	if err != nil {
		writeChangeError(w, r, err)
		return
	}
	tgt := target(q.name, subnetName(q.subnets, res.SubnetID))
	details := reservationDetails(res)
	b, err := s.writeTarget(r.Context(), q)
	if err == nil {
		details["server"] = b.Name()
		err = b.AddReservation(r.Context(), res)
	}
	if err != nil {
		details["error"] = err.Error()
		s.audit.RecordRequest(r, "dhcp.reservation.create", tgt, audit.ResultFailure, details)
		writeChangeError(w, r, err)
		return
	}
	s.audit.RecordRequest(r, "dhcp.reservation.create", tgt, audit.ResultSuccess, details)
	nb := s.netbox.SyncReservation(r.Context(), netbox.Base(r), tgt, nbReservation(q.name, res), nil)
	s.respondReservation(w, r, q, b, res, http.StatusCreated, nb)
}

// respondReservation answers with the stored reservation as Kea returns it.
func (s *Service) respondReservation(w http.ResponseWriter, r *http.Request, q request, b Backend, res kea.Reservation, status int, nb *netbox.Result) {
	typ, id := res.Identifier()
	stored, found, err := b.Reservation(r.Context(), res.SubnetID, typ, id, kea.SourceDatabase)
	if err != nil || !found {
		stored = res
		stored.Source = kea.SourceDatabase
	}
	v := q.reservationView(stored)
	v.NetBox = nb
	httpx.WriteJSON(w, status, v)
}

// current returns the stored reservation of the ID in the request, checked
// for the action and the expected version.
func (s *Service) current(w http.ResponseWriter, r *http.Request, q request, version string) (Backend, kea.Reservation, bool) {
	subnetID, typ, id, err := parseReservationID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return nil, kea.Reservation{}, false
	}
	if !q.check(w, r, subnetID, rbac.ActionWrite) {
		return nil, kea.Reservation{}, false
	}
	b, err := s.writeTarget(r.Context(), q)
	if err != nil {
		writeChangeError(w, r, err)
		return nil, kea.Reservation{}, false
	}
	res, found, err := b.Reservation(r.Context(), subnetID, typ, id, kea.SourceDatabase)
	switch {
	case err != nil:
		writeError(w, r, err)
		return nil, kea.Reservation{}, false
	case !found:
		if _, inConfig, _ := b.Reservation(r.Context(), subnetID, typ, id, kea.SourceConfig); inConfig {
			httpx.WriteError(w, http.StatusConflict, "read_only", "the reservation is in the configuration file and cannot be changed here")
		} else {
			httpx.WriteError(w, http.StatusNotFound, "not_found", "reservation not found")
		}
		return nil, kea.Reservation{}, false
	case version == "":
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "version required")
		return nil, kea.Reservation{}, false
	case reservationVersion(res) != version:
		httpx.WriteError(w, http.StatusConflict, "conflict", "changed in the meantime, reload and try again")
		return nil, kea.Reservation{}, false
	}
	return b, res, true
}

func (s *Service) handleUpdateReservation(w http.ResponseWriter, r *http.Request) {
	q, ok := s.server(w, r, rbac.ActionWrite)
	if !ok {
		return
	}
	var req ReservationRequest
	if !decode(w, r, &req) {
		return
	}
	b, cur, ok := s.current(w, r, q, req.Version)
	if !ok {
		return
	}
	typ, id := cur.Identifier()
	if nid, _ := normalizeHex(req.Identifier); req.SubnetID != cur.SubnetID || req.IdentifierType != typ || nid != id {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_reservation", "subnet and identifier cannot be changed; delete the reservation and add a new one")
		return
	}
	res, err := q.reservation(req)
	if err != nil {
		writeChangeError(w, r, err)
		return
	}
	// Keep what dcdash does not edit.
	res.ClientClasses, res.Options = cur.ClientClasses, cur.Options
	res.NextServer, res.ServerHostname, res.BootFileName = cur.NextServer, cur.ServerHostname, cur.BootFileName
	tgt := target(q.name, subnetName(q.subnets, res.SubnetID))
	details := reservationDetails(res)
	details["server"] = b.Name()
	details["previous"] = reservationDetails(cur)
	if err := b.UpdateReservation(r.Context(), res); err != nil {
		details["error"] = err.Error()
		s.audit.RecordRequest(r, "dhcp.reservation.update", tgt, audit.ResultFailure, details)
		writeChangeError(w, r, err)
		return
	}
	s.audit.RecordRequest(r, "dhcp.reservation.update", tgt, audit.ResultSuccess, details)
	prev := nbReservation(q.name, cur)
	nb := s.netbox.SyncReservation(r.Context(), netbox.Base(r), tgt, nbReservation(q.name, res), &prev)
	s.respondReservation(w, r, q, b, res, http.StatusOK, nb)
}

func (s *Service) handleDeleteReservation(w http.ResponseWriter, r *http.Request) {
	q, ok := s.server(w, r, rbac.ActionWrite)
	if !ok {
		return
	}
	b, cur, ok := s.current(w, r, q, r.URL.Query().Get("version"))
	if !ok {
		return
	}
	typ, id := cur.Identifier()
	tgt := target(q.name, subnetName(q.subnets, cur.SubnetID))
	details := reservationDetails(cur)
	details["server"] = b.Name()
	if _, err := b.DeleteReservation(r.Context(), cur.SubnetID, typ, id); err != nil {
		details["error"] = err.Error()
		s.audit.RecordRequest(r, "dhcp.reservation.delete", tgt, audit.ResultFailure, details)
		writeError(w, r, err)
		return
	}
	s.audit.RecordRequest(r, "dhcp.reservation.delete", tgt, audit.ResultSuccess, details)
	if nb := s.netbox.DeleteReservation(r.Context(), netbox.Base(r), tgt, nbReservation(q.name, cur)); nb != nil {
		httpx.WriteJSON(w, http.StatusOK, DeleteResult{NetBox: nb})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
