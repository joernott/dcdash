// SPDX-License-Identifier: GPL-3.0-or-later

package netbox

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/joernott/dcdash/internal/audit"
)

// Comparison of NetBox with the connected systems (task 12.4,
// docs/adr/0006-netbox.md). The modules provide their state through the
// source interfaces, so that this package doesn't depend on them.

// GuestSource provides the guests of the Proxmox clusters (the Proxmox
// module).
type GuestSource interface {
	// NetBoxGuests returns the state of all guests of a cluster.
	NetBoxGuests(ctx context.Context, cluster string) ([]Guest, error)
	// NetBoxGuest returns the state of one guest.
	NetBoxGuest(ctx context.Context, cluster string, container bool, vmid int) (Guest, error)
}

// ReservationSource provides the DHCP reservations (the DHCP module).
type ReservationSource interface {
	NetBoxReservations(ctx context.Context) ([]Reservation, error)
}

// RecordSource provides the A and AAAA records (the DNS module).
type RecordSource interface {
	NetBoxRecords(ctx context.Context) ([]DNSRecord, error)
}

// SetSources connects the modules whose state is compared; nil for modules
// that aren't configured.
func (s *Service) SetSources(g GuestSource, r ReservationSource, d RecordSource) {
	s.guests, s.reservations, s.records = g, r, d
}

// States of a compared guest or address.
const (
	StateOK      = "ok"
	StateMissing = "missing" // not in NetBox
	StateExtra   = "extra"   // only in NetBox
	StateDiffers = "differs"
)

// Difference is a field whose value differs.
type Difference struct {
	Field  string `json:"field"`
	Actual string `json:"actual"`
	NetBox string `json:"netbox"`
}

// GuestDiff compares a guest with its NetBox VM.
type GuestDiff struct {
	// Type is qemu or lxc; empty for a VM that exists only in NetBox.
	Type string `json:"type,omitempty"`
	VMID int    `json:"vmid,omitempty"`
	Name string `json:"name"`
	// State is ok, missing, extra or differs.
	State       string       `json:"state"`
	Differences []Difference `json:"differences"`
	// NetBoxID is the ID of the NetBox VM, 0 if it doesn't exist.
	NetBoxID int64 `json:"netboxId,omitempty"`
	// Tagged is true if the NetBox VM has the dcdash tag (it can then be
	// deleted from the comparison view).
	Tagged bool `json:"tagged"`
}

// GuestComparison is the response of GET /api/v1/netbox/compare/guests/{cluster}.
type GuestComparison struct {
	Cluster       string      `json:"cluster"`
	NetBoxCluster string      `json:"netboxCluster"`
	Guests        []GuestDiff `json:"guests"`
}

// nbState is the NetBox side of a guest.
type nbState struct {
	vm    VirtualMachine
	disks []VirtualDisk
	ifcs  []Interface
	ips   []IPAddress
}

// CompareGuests compares the guests of a Proxmox cluster with the VMs of the
// mapped NetBox cluster. visible filters the guests the user may see.
func (s *Service) CompareGuests(ctx context.Context, cluster string, visible func(container bool, vmid int) bool) (GuestComparison, error) {
	out := GuestComparison{Cluster: cluster, NetBoxCluster: s.cfg.Clusters[cluster], Guests: []GuestDiff{}}
	clusterID, ok, err := s.clusterID(ctx, cluster)
	if !ok {
		return out, fmt.Errorf("NetBox: %w: the cluster %s isn't mapped", ErrNotFound, cluster)
	}
	if err != nil {
		return out, err
	}
	if s.guests == nil {
		return out, errors.New("the Proxmox module isn't configured")
	}
	guests, err := s.guests.NetBoxGuests(ctx, cluster)
	if err != nil {
		return out, err
	}
	nb, err := s.netboxState(ctx, clusterID)
	if err != nil {
		return out, err
	}
	used := map[int64]bool{}
	for _, g := range guests {
		if !visible(g.Container, g.VMID) {
			continue
		}
		d := GuestDiff{Type: guestType(g.Container), VMID: g.VMID, Name: g.Name, Differences: []Difference{}}
		st, found := s.match(g, nb, used)
		if !found {
			d.State = StateMissing
		} else {
			used[st.vm.ID] = true
			d.NetBoxID, d.Tagged = st.vm.ID, st.vm.HasTag(s.cfg.Tag)
			d.Differences = differences(g, st, s.cfg.Tag)
			d.State = StateOK
			if len(d.Differences) > 0 {
				d.State = StateDiffers
			}
		}
		out.Guests = append(out.Guests, d)
	}
	for _, st := range nb {
		if used[st.vm.ID] {
			continue
		}
		vmid := 0
		if s.cfg.VMIDField != "" {
			vmid, _ = strconv.Atoi(fmt.Sprint(st.vm.CustomFields[s.cfg.VMIDField]))
		}
		out.Guests = append(out.Guests, GuestDiff{Name: st.vm.Name, VMID: vmid, State: StateExtra, Differences: []Difference{},
			NetBoxID: st.vm.ID, Tagged: st.vm.HasTag(s.cfg.Tag)})
	}
	slices.SortStableFunc(out.Guests, func(a, b GuestDiff) int {
		if a.VMID != b.VMID {
			return a.VMID - b.VMID
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out, nil
}

func guestType(container bool) string {
	if container {
		return "lxc"
	}
	return "qemu"
}

// match finds the NetBox VM of a guest: by the VMID custom field, otherwise
// by name.
func (s *Service) match(g Guest, nb []*nbState, used map[int64]bool) (*nbState, bool) {
	if f := s.cfg.VMIDField; f != "" {
		for _, st := range nb {
			if !used[st.vm.ID] && fmt.Sprint(st.vm.CustomFields[f]) == strconv.Itoa(g.VMID) {
				return st, true
			}
		}
	}
	for _, st := range nb {
		if !used[st.vm.ID] && st.vm.Name == g.Name {
			return st, true
		}
	}
	return nil, false
}

// netboxState loads the VMs of a NetBox cluster with their disks, interfaces
// and addresses.
func (s *Service) netboxState(ctx context.Context, clusterID int64) ([]*nbState, error) {
	cl := strconv.FormatInt(clusterID, 10)
	vms, err := s.client.VMs(ctx, url.Values{"cluster_id": {cl}})
	if err != nil {
		return nil, err
	}
	ifcs, err := s.client.Interfaces(ctx, url.Values{"cluster_id": {cl}})
	if err != nil {
		return nil, err
	}
	byID := map[int64]*nbState{}
	out := make([]*nbState, 0, len(vms))
	ids := make([]string, 0, len(vms))
	for _, vm := range vms {
		st := &nbState{vm: vm}
		byID[vm.ID] = st
		out = append(out, st)
		ids = append(ids, strconv.FormatInt(vm.ID, 10))
	}
	for _, i := range ifcs {
		if st := byID[i.VirtualMachine.ID]; st != nil {
			st.ifcs = append(st.ifcs, i)
		}
	}
	// Disks and addresses are filtered by VM, in batches.
	const batch = 100
	for start := 0; start < len(ids); start += batch {
		q := url.Values{"virtual_machine_id": ids[start:min(start+batch, len(ids))]}
		disks, err := s.client.Disks(ctx, q)
		if err != nil {
			return nil, err
		}
		for _, d := range disks {
			if st := byID[d.VirtualMachine.ID]; st != nil {
				st.disks = append(st.disks, d)
			}
		}
		ips, err := s.client.IPAddresses(ctx, q)
		if err != nil {
			return nil, err
		}
		ifcVM := map[int64]int64{}
		for _, i := range ifcs {
			ifcVM[i.ID] = i.VirtualMachine.ID
		}
		for _, ip := range ips {
			if ip.AssignedObjectID == nil || ip.AssignedObjectType != TypeVMInterface {
				continue
			}
			if st := byID[ifcVM[*ip.AssignedObjectID]]; st != nil {
				st.ips = append(st.ips, ip)
			}
		}
	}
	return out, nil
}

// differences compares a guest with its NetBox state. Interfaces are matched as the
// write-back does (see matchInterfaces); interfaces dcdash leaves alone are not compared.
func differences(g Guest, st *nbState, tag string) []Difference {
	var out []Difference
	add := func(field, actual, netbox string) {
		if actual != netbox {
			out = append(out, Difference{Field: field, Actual: actual, NetBox: netbox})
		}
	}
	add("name", g.Name, st.vm.Name)
	if g.VCPUs > 0 {
		add("vcpus", fmtFloat(g.VCPUs), fmtFloatPtr(st.vm.VCPUs))
	}
	add("memory", strconv.FormatInt(g.MemoryMiB, 10), fmtIntPtr(st.vm.Memory))
	var disks, nbDisks []string
	for _, d := range g.Disks {
		disks = append(disks, d.Name+" "+strconv.FormatInt(d.SizeMiB, 10))
	}
	for _, d := range st.disks {
		nbDisks = append(nbDisks, d.Name+" "+strconv.FormatInt(d.Size, 10))
	}
	add("disks", joinSorted(disks), joinSorted(nbDisks))
	var nets, nbNets, addrs, nbAddrs []string
	match := matchInterfaces(g.Networks, st.ifcs, tag)
	foreign := map[int64]bool{}
	for _, i := range match.foreign {
		foreign[i.ID] = true
	}
	for _, n := range g.Networks {
		name := match.name(n)
		nets = append(nets, netString(name, n.MAC, n.VLAN))
		for _, a := range n.Addresses {
			addrs = append(addrs, name+" "+a)
		}
	}
	ifcName := map[int64]string{}
	for _, i := range st.ifcs {
		ifcName[i.ID] = i.Name
		if foreign[i.ID] {
			continue
		}
		mac, vlan := "", 0
		if i.PrimaryMAC != nil {
			mac = i.PrimaryMAC.MACAddress
		}
		if i.UntaggedVLAN != nil {
			vlan = i.UntaggedVLAN.VID
		}
		nbNets = append(nbNets, netString(i.Name, mac, vlan))
	}
	add("interfaces", joinSorted(nets), joinSorted(nbNets))
	for _, ip := range st.ips {
		nbAddrs = append(nbAddrs, ifcName[*ip.AssignedObjectID]+" "+ip.Address)
	}
	// Static addresses of the guest must be in NetBox; NetBox may have more
	// (for example DHCP reservations assigned to the interface).
	var missing []string
	for _, a := range addrs {
		if !slices.Contains(nbAddrs, a) {
			missing = append(missing, a)
		}
	}
	if len(missing) > 0 {
		out = append(out, Difference{Field: "addresses", Actual: joinSorted(addrs), NetBox: joinSorted(nbAddrs)})
	}
	if out == nil {
		return []Difference{}
	}
	return out
}

func netString(name, mac string, vlan int) string {
	s := name + " " + strings.ToUpper(mac)
	if vlan != 0 {
		s += " VLAN " + strconv.Itoa(vlan)
	}
	return s
}

func joinSorted(l []string) string {
	l = slices.Clone(l)
	slices.Sort(l)
	return strings.Join(l, ", ")
}

func fmtFloat(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

func fmtFloatPtr(f *float64) string {
	if f == nil {
		return ""
	}
	return fmtFloat(*f)
}

func fmtIntPtr(i *int64) string {
	if i == nil {
		return ""
	}
	return strconv.FormatInt(*i, 10)
}

// ApplyGuest writes the current state of a guest to NetBox, from the
// comparison view.
func (s *Service) ApplyGuest(ctx context.Context, base audit.Entry, cluster string, container bool, vmid int) (*Result, error) {
	if s.guests == nil {
		return nil, errors.New("the Proxmox module isn't configured")
	}
	if _, ok := s.cfg.Clusters[cluster]; !ok {
		return nil, fmt.Errorf("NetBox: %w: the cluster %s isn't mapped", ErrNotFound, cluster)
	}
	g, err := s.guests.NetBoxGuest(ctx, cluster, container, vmid)
	if err != nil {
		return nil, err
	}
	var changes []string
	err = s.syncGuest(ctx, message(base, "update "+g.Name), g, &changes)
	base.Action = AuditApply
	return s.finish(ctx, base, guestTarget(cluster, container, vmid), map[string]any{"name": g.Name, "changes": nonNil(changes)}, err), nil
}

// DeleteVM deletes a NetBox VM that exists only in NetBox. Only VMs with the
// dcdash tag are deleted.
func (s *Service) DeleteVM(ctx context.Context, base audit.Entry, cluster string, id int64) (*Result, error) {
	clusterID, ok, err := s.clusterID(ctx, cluster)
	if !ok {
		return nil, fmt.Errorf("NetBox: %w: the cluster %s isn't mapped", ErrNotFound, cluster)
	}
	if err != nil {
		return nil, err
	}
	vm, err := s.client.FindVM(ctx, url.Values{"id": {strconv.FormatInt(id, 10)}, "cluster_id": {strconv.FormatInt(clusterID, 10)}})
	if err != nil {
		return nil, err
	}
	if !vm.HasTag(s.cfg.Tag) {
		return nil, ErrNotTagged
	}
	if s.guests != nil {
		gs, err := s.guests.NetBoxGuests(ctx, cluster)
		if err != nil {
			return nil, err
		}
		if _, found := s.belongs(vm, gs); found {
			return nil, ErrGuestExists
		}
	}
	err = s.client.Delete(ctx, pathVMs, vm.ID, message(base, "delete "+vm.Name))
	base.Action = AuditApply
	return s.finish(ctx, base, cluster+"/netbox/"+strconv.FormatInt(vm.ID, 10),
		map[string]any{"name": vm.Name, "changes": []string{"vm deleted"}}, err), nil
}

// belongs reports whether a NetBox VM belongs to one of the guests.
func (s *Service) belongs(vm VirtualMachine, guests []Guest) (Guest, bool) {
	for _, g := range guests {
		if s.cfg.VMIDField != "" && fmt.Sprint(vm.CustomFields[s.cfg.VMIDField]) == strconv.Itoa(g.VMID) || vm.Name == g.Name {
			return g, true
		}
	}
	return Guest{}, false
}

// Errors of DeleteVM.
var (
	ErrNotTagged   = errors.New("the NetBox VM doesn't have the dcdash tag")
	ErrGuestExists = errors.New("the guest exists in Proxmox")
)

// AuditApply is the audit action of an update from the comparison view.
const AuditApply = "netbox.apply"

// AddressDiff is a reserved address or an A/AAAA record that is missing in
// NetBox or has another DNS name there.
type AddressDiff struct {
	Address string `json:"address"`
	// Source is "dhcp:<server>" or "dns".
	Source string `json:"source"`
	// Name is the record's name (DNS only).
	Name string `json:"name,omitempty"`
	// State is missing or differs (another DNS name in NetBox).
	State  string `json:"state"`
	NetBox string `json:"netbox,omitempty"`
}

// AddressComparison is the response of GET /api/v1/netbox/compare/addresses.
type AddressComparison struct {
	Addresses []AddressDiff `json:"addresses"`
	// Errors are sources that couldn't be read.
	Errors []string `json:"errors"`
}

// CompareAddresses compares the reserved addresses and the A/AAAA records
// with the IP addresses in NetBox. visible filters by source ("dhcp:<server>"
// or "dns:<provider>").
func (s *Service) CompareAddresses(ctx context.Context) (AddressComparison, error) {
	out := AddressComparison{Addresses: []AddressDiff{}, Errors: []string{}}
	var (
		mu    sync.Mutex
		wg    sync.WaitGroup
		resv  []Reservation
		recs  []DNSRecord
		errsS []string
	)
	if s.reservations != nil {
		wg.Go(func() {
			l, err := s.reservations.NetBoxReservations(ctx)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errsS = append(errsS, "DHCP: "+err.Error())
			}
			resv = l
		})
	}
	if s.records != nil {
		wg.Go(func() {
			l, err := s.records.NetBoxRecords(ctx)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errsS = append(errsS, "DNS: "+err.Error())
			}
			recs = l
		})
	}
	wg.Wait()
	out.Errors = append(out.Errors, errsS...)
	vrf, err := s.vrfFilter(ctx)
	if err != nil {
		return out, err
	}
	ips, err := s.client.IPAddresses(ctx, url.Values{"vrf_id": {vrf}})
	if err != nil {
		return out, err
	}
	byAddr := map[netip.Addr]IPAddress{}
	for _, ip := range ips {
		if p, err := netip.ParsePrefix(ip.Address); err == nil {
			byAddr[p.Addr()] = ip
		}
	}
	// Servers of an HA pair report the same reservations: each address once.
	seen := map[netip.Addr]bool{}
	for _, r := range resv {
		for _, a := range r.Addresses {
			addr, err := netip.ParseAddr(a)
			if err != nil || seen[addr] {
				continue
			}
			seen[addr] = true
			if _, ok := byAddr[addr]; !ok {
				out.Addresses = append(out.Addresses, AddressDiff{Address: a, Source: "dhcp:" + r.Server, State: StateMissing})
			}
		}
	}
	for _, r := range recs {
		addr, err := netip.ParseAddr(r.Address)
		if err != nil {
			continue
		}
		ip, ok := byAddr[addr]
		switch {
		case !ok:
			out.Addresses = append(out.Addresses, AddressDiff{Address: r.Address, Source: "dns", Name: r.name(), State: StateMissing})
		case !strings.EqualFold(ip.DNSName, r.name()):
			out.Addresses = append(out.Addresses, AddressDiff{Address: r.Address, Source: "dns", Name: r.name(), State: StateDiffers,
				NetBox: ip.DNSName})
		}
	}
	slices.SortStableFunc(out.Addresses, func(a, b AddressDiff) int {
		pa, _ := netip.ParseAddr(a.Address)
		pb, _ := netip.ParseAddr(b.Address)
		return pa.Compare(pb)
	})
	return out, nil
}
