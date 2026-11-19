// SPDX-License-Identifier: GPL-3.0-or-later

// Package nbfake is an in-memory fake of the NetBox REST API for tests. It
// implements the calls of docs/adr/0006-netbox.md, including the behavior
// dcdash depends on: VM names unique per cluster, IP addresses unique per
// VRF, virtual disks summed up in the VM's disk field, deleting interfaces
// and VMs cascades to their addresses, and change log messages.
package nbfake

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"math/big"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// Object types of the fake (keys of the store).
const (
	Clusters   = "virtualization/clusters"
	VMs        = "virtualization/virtual-machines"
	Interfaces = "virtualization/interfaces"
	Disks      = "virtualization/virtual-disks"
	MACs       = "dcim/mac-addresses"
	Sites      = "dcim/sites"
	IPs        = "ipam/ip-addresses"
	Prefixes   = "ipam/prefixes"
	VLANs      = "ipam/vlans"
	VLANGroups = "ipam/vlan-groups"
	VRFs       = "ipam/vrfs"
	Tags       = "extras/tags"
)

var types = []string{Clusters, VMs, Interfaces, Disks, MACs, Sites, IPs, Prefixes, VLANs, VLANGroups, VRFs, Tags}

// Version is reported by /api/status/.
const Version = "4.7.5"

// Object is a stored object: JSON fields with foreign keys as IDs (int64).
type Object map[string]any

// Change is an entry of the change log.
type Change struct {
	Action  string // create, update, delete
	Type    string
	ID      int64
	Message string
}

// Server is the fake.
type Server struct {
	token string

	mu      sync.Mutex
	objects map[string]map[int64]Object
	nextID  map[string]int64
	changes []Change
	fail    map[string]int // "METHOD type" -> HTTP status of the next request
}

// New creates the fake. Requests must send the token (v1 or v2 header).
func New(token string) *Server {
	s := &Server{token: token, objects: map[string]map[int64]Object{}, nextID: map[string]int64{}, fail: map[string]int{}}
	for _, t := range types {
		s.objects[t] = map[int64]Object{}
	}
	return s
}

// Seed adds an object without validation and returns its ID. Foreign keys
// are given as int64 IDs.
func (s *Server) Seed(typ string, o Object) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.insert(typ, o)
}

func (s *Server) insert(typ string, o Object) int64 {
	s.nextID[typ]++
	id := s.nextID[typ]
	o = maps.Clone(o)
	o["id"] = id
	s.objects[typ][id] = o
	return id
}

// Objects returns copies of all objects of a type, ordered by ID.
func (s *Server) Objects(typ string) []Object {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sorted(typ)
}

func (s *Server) sorted(typ string) []Object {
	ids := slices.Sorted(maps.Keys(s.objects[typ]))
	out := make([]Object, 0, len(ids))
	for _, id := range ids {
		out = append(out, maps.Clone(s.objects[typ][id]))
	}
	return out
}

// Changes returns the change log.
func (s *Server) Changes() []Change {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.changes)
}

// FailNext makes the next request with that method on that type fail with
// the status (500: server error, 403: permission denied).
func (s *Server) FailNext(method, typ string, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail[method+" "+typ] = status
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Authorization")
	if auth != "Bearer "+s.token && auth != "Token "+s.token {
		writeJSON(w, http.StatusForbidden, map[string]any{"detail": "Invalid v2 token"})
		return
	}
	path := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/"), "/")
	if path == "status" {
		writeJSON(w, http.StatusOK, map[string]any{"netbox-version": Version})
		return
	}
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		writeJSON(w, http.StatusNotFound, map[string]any{"detail": "Not found."})
		return
	}
	typ := parts[0] + "/" + parts[1]
	if _, ok := s.objects[typ]; !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"detail": "Not found."})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.fail[r.Method+" "+typ]; ok {
		delete(s.fail, r.Method+" "+typ)
		writeJSON(w, st, map[string]any{"detail": fmt.Sprintf("injected failure %d", st)})
		return
	}
	var body Object
	if r.Body != nil && r.Method != http.MethodGet {
		data, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if len(data) > 0 {
			if err := json.Unmarshal(data, &body); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "JSON parse error - " + err.Error()})
				return
			}
		}
	}
	switch {
	case len(parts) == 2 && r.Method == http.MethodGet:
		s.list(w, r, typ)
	case len(parts) == 2 && r.Method == http.MethodPost:
		s.create(w, typ, body)
	case len(parts) == 4 && typ == Prefixes && parts[3] == "available-ips" && r.Method == http.MethodGet:
		s.availableIPs(w, r, parts[2])
	case len(parts) == 3:
		id, err := strconv.ParseInt(parts[2], 10, 64)
		o, ok := s.objects[typ][id]
		if err != nil || !ok {
			writeJSON(w, http.StatusNotFound, map[string]any{"detail": "No matching object found."})
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, s.render(typ, o))
		case http.MethodPatch:
			s.update(w, typ, o, body)
		case http.MethodDelete:
			s.delete(typ, id, str(body["changelog_message"]))
			w.WriteHeader(http.StatusNoContent)
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"detail": "Method not allowed."})
		}
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"detail": "Method not allowed."})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// list implements GET on a list endpoint with filters, limit and offset.
func (s *Server) list(w http.ResponseWriter, r *http.Request, typ string) {
	q := r.URL.Query()
	var match []Object
	for _, o := range s.sorted(typ) {
		if s.matches(typ, o, q) {
			match = append(match, s.render(typ, o))
		}
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	offset = min(max(offset, 0), len(match))
	end := min(offset+limit, len(match))
	var next any
	if end < len(match) {
		next = fmt.Sprintf("http://netbox/api/%s/?limit=%d&offset=%d", typ, limit, end)
	}
	results := match[offset:end]
	if results == nil {
		results = []Object{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(match), "next": next, "previous": nil, "results": results})
}

// matches applies the filters of q that the fake knows; others are ignored
// (but brief, limit and offset are no filters).
func (s *Server) matches(typ string, o Object, q map[string][]string) bool {
	for k, vals := range q {
		if k == "limit" || k == "offset" || k == "brief" || k == "ordering" {
			continue
		}
		if k == "contains" && typ == Prefixes {
			p, err1 := netip.ParsePrefix(str(o["prefix"]))
			a, err2 := netip.ParseAddr(vals[0])
			if err1 != nil || err2 != nil || !p.Contains(a) {
				return false
			}
			continue
		}
		got, ok := s.field(typ, o, k)
		if !ok {
			continue
		}
		if !slices.ContainsFunc(vals, func(v string) bool { return got == v || (k == "mac_address" && strings.EqualFold(got, v)) }) {
			return false
		}
	}
	return true
}

// field returns the value of a filter for an object.
func (s *Server) field(typ string, o Object, filter string) (string, bool) {
	fk := func(f string) string {
		if v, ok := o[f]; ok && v != nil {
			return strconv.FormatInt(toInt(v), 10)
		}
		return "null"
	}
	switch {
	case filter == "id":
		return fk("id"), true
	case filter == "name" || filter == "slug" || filter == "dns_name":
		return str(o[filter]), true
	case strings.HasPrefix(filter, "cf_") && typ == VMs:
		cf, _ := o["custom_fields"].(map[string]any)
		v := cf[strings.TrimPrefix(filter, "cf_")]
		if f, ok := v.(float64); ok {
			return strconv.FormatInt(int64(f), 10), true
		}
		return fmt.Sprint(v), true
	case filter == "tag":
		for _, t := range toInts(o["tags"]) {
			if tag, ok := s.objects[Tags][t]; ok {
				return str(tag["slug"]), true
			}
		}
		return "", true
	}
	switch typ + " " + filter {
	case VMs + " cluster_id", Clusters + " id":
		return fk("cluster"), true
	case Interfaces + " virtual_machine_id", Disks + " virtual_machine_id":
		return fk("virtual_machine"), true
	case Interfaces + " cluster_id":
		return s.field(VMs, s.objects[VMs][toInt(o["virtual_machine"])], "cluster_id")
	case Interfaces + " mac_address":
		for _, m := range s.objects[MACs] {
			if str(m["assigned_object_type"]) == "virtualization.vminterface" && toInt(m["assigned_object_id"]) == toInt(o["id"]) {
				return str(m["mac_address"]), true
			}
		}
		return "", true
	case MACs + " mac_address":
		return str(o["mac_address"]), true
	case MACs + " assigned_object_id", MACs + " vminterface_id", IPs + " vminterface_id":
		return fk("assigned_object_id"), true
	case IPs + " address":
		return strings.SplitN(str(o["address"]), "/", 2)[0], true
	case IPs + " vrf_id", Prefixes + " vrf_id":
		return fk("vrf"), true
	case IPs + " virtual_machine_id":
		if str(o["assigned_object_type"]) != "virtualization.vminterface" {
			return "null", true
		}
		return fk2(s.objects[Interfaces][toInt(o["assigned_object_id"])], "virtual_machine"), true
	case Prefixes + " vlan_id":
		return fk("vlan"), true
	case VLANs + " vid":
		return fk("vid"), true
	case VLANs + " group_id":
		return fk("group"), true
	case VLANs + " site_id":
		return fk("site"), true
	}
	return "", false
}

func fk2(o Object, f string) string {
	if o == nil || o[f] == nil {
		return "null"
	}
	return strconv.FormatInt(toInt(o[f]), 10)
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func toInt(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	}
	return 0
}

func toInts(v any) []int64 {
	switch l := v.(type) {
	case []int64:
		return l
	case []any:
		out := make([]int64, 0, len(l))
		for _, x := range l {
			out = append(out, toInt(x))
		}
		return out
	}
	return nil
}

// fields that are foreign keys, per type: field -> referenced type.
var foreignKeys = map[string]map[string]string{
	VMs:        {"cluster": Clusters, "primary_ip4": IPs, "primary_ip6": IPs},
	Interfaces: {"virtual_machine": VMs, "untagged_vlan": VLANs, "primary_mac_address": MACs},
	Disks:      {"virtual_machine": VMs},
	IPs:        {"vrf": VRFs},
	Prefixes:   {"vrf": VRFs, "vlan": VLANs},
	VLANs:      {"group": VLANGroups, "site": Sites},
}

var required = map[string][]string{
	Clusters:   {"name"},
	VMs:        {"name"},
	Interfaces: {"virtual_machine", "name"},
	Disks:      {"virtual_machine", "name", "size"},
	MACs:       {"mac_address"},
	IPs:        {"address"},
	Prefixes:   {"prefix"},
	VLANs:      {"vid", "name"},
	VLANGroups: {"name", "slug"},
	Sites:      {"name", "slug"},
	VRFs:       {"name"},
	Tags:       {"name", "slug"},
}

var statusLabels = map[string]string{
	"active": "Active", "offline": "Offline", "planned": "Planned", "staged": "Staged", "failed": "Failed",
	"decommissioning": "Decommissioning", "reserved": "Reserved", "deprecated": "Deprecated", "dhcp": "DHCP",
	"slaac": "SLAAC",
}

type fieldErrors map[string][]string

func (e fieldErrors) add(k, msg string) { e[k] = append(e[k], msg) }

// apply validates the fields of a create or update body and writes them to o.
func (s *Server) apply(typ string, o, body Object, create bool) fieldErrors {
	errs := fieldErrors{}
	for k, v := range body {
		switch {
		case k == "id" || k == "changelog_message":
			continue
		case k == "tags":
			ids, err := s.tagIDs(v)
			if err != "" {
				errs.add("tags", err)
				continue
			}
			o["tags"] = ids
		case foreignKeys[typ][k] != "":
			if v == nil {
				o[k] = nil
				continue
			}
			ref := toInt(v)
			if m, ok := v.(map[string]any); ok {
				ref = toInt(m["id"])
			}
			if _, ok := s.objects[foreignKeys[typ][k]][ref]; !ok {
				errs.add(k, fmt.Sprintf("Related object not found using the provided numeric ID: %d", ref))
				continue
			}
			o[k] = ref
		case k == "assigned_object_id":
			if v == nil {
				o[k] = nil
			} else {
				o[k] = toInt(v)
			}
		case k == "custom_fields":
			cf, _ := o["custom_fields"].(map[string]any)
			cf = maps.Clone(cf)
			if cf == nil {
				cf = map[string]any{}
			}
			if m, ok := v.(map[string]any); ok {
				maps.Copy(cf, m)
			}
			o["custom_fields"] = cf
		case typ == VMs && k == "disk" && v != nil && s.diskSum(toInt(o["id"])) >= 0 && toInt(v) != s.diskSum(toInt(o["id"])):
			errs.add("disk", fmt.Sprintf("The specified disk size (%d) must match the aggregate size of assigned virtual disks (%d).",
				toInt(v), s.diskSum(toInt(o["id"]))))
		default:
			o[k] = v
		}
	}
	if create {
		for _, f := range required[typ] {
			if o[f] == nil || o[f] == "" {
				errs.add(f, "This field is required.")
			}
		}
	}
	if len(errs) == 0 {
		s.validate(typ, o, errs)
	}
	return errs
}

func (s *Server) tagIDs(v any) ([]int64, string) {
	l, ok := v.([]any)
	if !ok {
		return nil, "Expected a list of items."
	}
	var ids []int64
	for _, x := range l {
		var found int64
		for id, t := range s.objects[Tags] {
			if m, ok := x.(map[string]any); ok && (str(m["slug"]) == str(t["slug"]) && str(m["slug"]) != "" ||
				str(m["name"]) == str(t["name"]) && str(m["name"]) != "") || toInt(x) == id {
				found = id
			}
		}
		if found == 0 {
			return nil, fmt.Sprintf("Related object not found using the provided attributes: %v", x)
		}
		ids = append(ids, found)
	}
	return ids, ""
}

// validate checks the rules of NetBox that dcdash depends on.
func (s *Server) validate(typ string, o Object, errs fieldErrors) {
	id := toInt(o["id"])
	switch typ {
	case VMs:
		for oid, x := range s.objects[VMs] {
			if oid != id && str(x["name"]) == str(o["name"]) && toInt(x["cluster"]) == toInt(o["cluster"]) {
				errs.add("__all__", "Virtual machine name must be unique per cluster and tenant.")
			}
		}
		if o["status"] == nil {
			o["status"] = "active"
		}
		if _, ok := statusLabels[str(o["status"])]; !ok {
			errs.add("status", fmt.Sprintf("%q is not a valid choice.", str(o["status"])))
		}
		for _, f := range []string{"primary_ip4", "primary_ip6"} {
			if o[f] == nil {
				continue
			}
			ip := s.objects[IPs][toInt(o[f])]
			ifc := s.objects[Interfaces][toInt(ip["assigned_object_id"])]
			if str(ip["assigned_object_type"]) != "virtualization.vminterface" || toInt(ifc["virtual_machine"]) != id {
				errs.add(f, "The specified IP address is not assigned to this VM.")
			}
		}
	case Interfaces:
		for oid, x := range s.objects[Interfaces] {
			if oid != id && str(x["name"]) == str(o["name"]) && toInt(x["virtual_machine"]) == toInt(o["virtual_machine"]) {
				errs.add("__all__", "Virtual machine interface with this Virtual machine and Name already exists.")
			}
		}
		if m := o["primary_mac_address"]; m != nil {
			mac := s.objects[MACs][toInt(m)]
			if str(mac["assigned_object_type"]) != "virtualization.vminterface" || toInt(mac["assigned_object_id"]) != id {
				errs.add("primary_mac_address", "The specified MAC address is not assigned to this interface.")
			}
		}
		if o["mode"] != nil && o["mode"] != "" && o["mode"] != "access" && o["mode"] != "tagged" && o["mode"] != "tagged-all" {
			errs.add("mode", fmt.Sprintf("%q is not a valid choice.", str(o["mode"])))
		}
	case Disks:
		for oid, x := range s.objects[Disks] {
			if oid != id && str(x["name"]) == str(o["name"]) && toInt(x["virtual_machine"]) == toInt(o["virtual_machine"]) {
				errs.add("__all__", "Virtual disk with this Virtual machine and Name already exists.")
			}
		}
	case MACs:
		if hw, err := parseMAC(str(o["mac_address"])); err != nil {
			errs.add("mac_address", "Enter a valid MAC address.")
		} else {
			o["mac_address"] = hw
		}
		s.checkAssigned(o, errs)
	case IPs:
		p, err := netip.ParsePrefix(str(o["address"]))
		if err != nil {
			errs.add("address", "Enter a valid IPv4 or IPv6 address with optional mask.")
			return
		}
		for oid, x := range s.objects[IPs] {
			q, _ := netip.ParsePrefix(str(x["address"]))
			if oid != id && q.Addr() == p.Addr() && toInt(x["vrf"]) == toInt(o["vrf"]) {
				table := "global table"
				if o["vrf"] != nil {
					table = "VRF " + str(s.objects[VRFs][toInt(o["vrf"])]["name"])
				}
				errs.add("address", fmt.Sprintf("Duplicate IP address found in %s: %s", table, str(x["address"])))
			}
		}
		if o["status"] == nil {
			o["status"] = "active"
		}
		s.checkAssigned(o, errs)
	case Prefixes:
		if _, err := netip.ParsePrefix(str(o["prefix"])); err != nil {
			errs.add("prefix", "Enter a valid IPv4 or IPv6 address with optional mask.")
		}
	}
}

func (s *Server) checkAssigned(o Object, errs fieldErrors) {
	if o["assigned_object_id"] == nil {
		return
	}
	if str(o["assigned_object_type"]) != "virtualization.vminterface" {
		errs.add("assigned_object_type", "The fake supports only virtualization.vminterface.")
		return
	}
	if _, ok := s.objects[Interfaces][toInt(o["assigned_object_id"])]; !ok {
		errs.add("assigned_object_id", "Related object not found.")
	}
}

func parseMAC(s string) (string, error) {
	s = strings.ToUpper(strings.ReplaceAll(s, "-", ":"))
	parts := strings.Split(s, ":")
	if len(parts) != 6 {
		return "", fmt.Errorf("invalid MAC %q", s)
	}
	for _, p := range parts {
		if len(p) != 2 || strings.Trim(p, "0123456789ABCDEF") != "" {
			return "", fmt.Errorf("invalid MAC %q", s)
		}
	}
	return s, nil
}

// diskSum returns the sum of the virtual disks of a VM, -1 without disks.
func (s *Server) diskSum(vm int64) int64 {
	sum, n := int64(0), 0
	for _, d := range s.objects[Disks] {
		if toInt(d["virtual_machine"]) == vm {
			sum += toInt(d["size"])
			n++
		}
	}
	if n == 0 {
		return -1
	}
	return sum
}

func (s *Server) create(w http.ResponseWriter, typ string, body Object) {
	o := Object{}
	if errs := s.apply(typ, o, body, true); len(errs) > 0 {
		writeJSON(w, http.StatusBadRequest, errs)
		return
	}
	id := s.insert(typ, o)
	s.changes = append(s.changes, Change{Action: "create", Type: typ, ID: id, Message: str(body["changelog_message"])})
	writeJSON(w, http.StatusCreated, s.render(typ, s.objects[typ][id]))
}

func (s *Server) update(w http.ResponseWriter, typ string, o, body Object) {
	n := maps.Clone(o)
	if errs := s.apply(typ, n, body, false); len(errs) > 0 {
		writeJSON(w, http.StatusBadRequest, errs)
		return
	}
	id := toInt(o["id"])
	s.objects[typ][id] = n
	s.changes = append(s.changes, Change{Action: "update", Type: typ, ID: id, Message: str(body["changelog_message"])})
	writeJSON(w, http.StatusOK, s.render(typ, n))
}

// delete removes an object and the objects that NetBox deletes with it. As in
// NetBox, the dependent objects are deleted first.
func (s *Server) delete(typ string, id int64, msg string) {
	defer func() {
		delete(s.objects[typ], id)
		s.changes = append(s.changes, Change{Action: "delete", Type: typ, ID: id, Message: msg})
	}()
	cascade := func(t string, match func(Object) bool) {
		for oid, o := range s.objects[t] {
			if match(o) {
				s.delete(t, oid, "")
			}
		}
	}
	clearRef := func(t, f string) {
		for _, o := range s.objects[t] {
			if o[f] != nil && toInt(o[f]) == id {
				o[f] = nil
			}
		}
	}
	assignedHere := func(o Object) bool {
		return str(o["assigned_object_type"]) == "virtualization.vminterface" && toInt(o["assigned_object_id"]) == id
	}
	switch typ {
	case VMs:
		cascade(Interfaces, func(o Object) bool { return toInt(o["virtual_machine"]) == id })
		cascade(Disks, func(o Object) bool { return toInt(o["virtual_machine"]) == id })
	case Interfaces:
		cascade(IPs, assignedHere)
		cascade(MACs, assignedHere)
	case IPs:
		clearRef(VMs, "primary_ip4")
		clearRef(VMs, "primary_ip6")
	case MACs:
		clearRef(Interfaces, "primary_mac_address")
	case VLANs:
		clearRef(Interfaces, "untagged_vlan")
		clearRef(Prefixes, "vlan")
	}
}

// availableIPs implements GET /api/ipam/prefixes/{id}/available-ips/.
func (s *Server) availableIPs(w http.ResponseWriter, r *http.Request, idStr string) {
	id, _ := strconv.ParseInt(idStr, 10, 64)
	o, ok := s.objects[Prefixes][id]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"detail": "No Prefix matches the given query."})
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	p := netip.MustParsePrefix(str(o["prefix"])).Masked()
	used := map[netip.Addr]bool{}
	for _, ip := range s.objects[IPs] {
		q, _ := netip.ParsePrefix(str(ip["address"]))
		if toInt(ip["vrf"]) == toInt(o["vrf"]) {
			used[q.Addr()] = true
		}
	}
	var vrf any
	if o["vrf"] != nil {
		vrf = map[string]any{"id": toInt(o["vrf"]), "name": s.objects[VRFs][toInt(o["vrf"])]["name"]}
	}
	family := 4
	if p.Addr().Is6() {
		family = 6
	}
	out := []map[string]any{}
	a := p.Addr()
	last := lastAddr(p)
	// NetBox skips the network address (and the broadcast address of IPv4)
	// of prefixes larger than /31 (/127).
	if p.Bits() < p.Addr().BitLen()-1 {
		a = a.Next()
		if family == 4 {
			last = last.Prev()
		}
	}
	for ; a.IsValid() && p.Contains(a) && a.Compare(last) <= 0 && len(out) < limit; a = a.Next() {
		if !used[a] {
			out = append(out, map[string]any{"family": family, "address": netip.PrefixFrom(a, p.Bits()).String(), "vrf": vrf})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func lastAddr(p netip.Prefix) netip.Addr {
	b := p.Addr().AsSlice()
	n := new(big.Int).SetBytes(b)
	host := p.Addr().BitLen() - p.Bits()
	n.Or(n, new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint(host)), big.NewInt(1))) // #nosec G115 -- host is 0..128
	out := n.FillBytes(make([]byte, len(b)))
	a, _ := netip.AddrFromSlice(out)
	return a
}

// render returns the API representation of an object with nested objects.
func (s *Server) render(typ string, o Object) Object {
	out := maps.Clone(o)
	ref := func(t string, id any, fields ...string) any {
		if id == nil {
			return nil
		}
		x, ok := s.objects[t][toInt(id)]
		if !ok {
			return nil
		}
		r := map[string]any{"id": toInt(id)}
		for _, f := range fields {
			r[f] = x[f]
		}
		return r
	}
	choice := func(v any) any {
		if v == nil || v == "" {
			return nil
		}
		label := statusLabels[str(v)]
		if label == "" {
			label = strings.ToUpper(str(v)[:1]) + str(v)[1:]
		}
		return map[string]any{"value": v, "label": label}
	}
	tags := []any{}
	for _, t := range toInts(o["tags"]) {
		tags = append(tags, ref(Tags, t, "name", "slug"))
	}
	switch typ {
	case VMs:
		out["cluster"] = ref(Clusters, o["cluster"], "name")
		out["status"] = choice(o["status"])
		out["primary_ip4"] = ref(IPs, o["primary_ip4"], "address")
		out["primary_ip6"] = ref(IPs, o["primary_ip6"], "address")
		if sum := s.diskSum(toInt(o["id"])); sum >= 0 {
			out["disk"] = sum
		}
		for _, f := range []string{"vcpus", "memory", "disk", "description"} {
			if _, ok := out[f]; !ok {
				out[f] = nil
			}
		}
		if out["custom_fields"] == nil {
			out["custom_fields"] = map[string]any{}
		}
		out["tags"] = tags
	case Interfaces:
		out["virtual_machine"] = ref(VMs, o["virtual_machine"], "name")
		out["untagged_vlan"] = ref(VLANs, o["untagged_vlan"], "vid", "name")
		out["primary_mac_address"] = ref(MACs, o["primary_mac_address"], "mac_address")
		out["mode"] = choice(o["mode"])
		if _, ok := o["enabled"]; !ok {
			out["enabled"] = true
		}
	case Disks:
		out["virtual_machine"] = ref(VMs, o["virtual_machine"], "name")
	case MACs:
		out["assigned_object"] = nil
		if ifc, ok := s.objects[Interfaces][toInt(o["assigned_object_id"])]; ok && o["assigned_object_id"] != nil {
			out["assigned_object"] = map[string]any{
				"id": ifc["id"], "name": ifc["name"], "virtual_machine": ref(VMs, ifc["virtual_machine"], "name"),
			}
		}
	case IPs:
		out["vrf"] = ref(VRFs, o["vrf"], "name")
		out["status"] = choice(o["status"])
		if _, ok := out["dns_name"]; !ok {
			out["dns_name"] = ""
		}
		out["tags"] = tags
	case Prefixes:
		out["vrf"] = ref(VRFs, o["vrf"], "name")
		out["vlan"] = ref(VLANs, o["vlan"], "vid", "name")
	case VLANs:
		out["group"] = ref(VLANGroups, o["group"], "name", "slug")
		out["site"] = ref(Sites, o["site"], "name", "slug")
	}
	return out
}
