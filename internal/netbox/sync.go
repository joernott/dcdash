// SPDX-License-Identifier: GPL-3.0-or-later

package netbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/config"
)

// Write-back of changes made in dcdash (see docs/adr/0006-netbox.md). It runs
// after the change succeeded; a failure doesn't undo the change, but is
// returned to the user and audited as netbox.sync.

// Result is the outcome of a write-back, returned with the response of the
// change that caused it.
type Result struct {
	// Status is "ok" or "failed".
	Status string `json:"status"`
	// Error is the error message of NetBox if Status is "failed".
	Error string `json:"error,omitempty"`
}

// Result states.
const (
	StatusOK     = "ok"
	StatusFailed = "failed"
)

// syncTimeout limits a write-back.
const syncTimeout = 15 * time.Second

// AuditSync is the audit action of a write-back.
const AuditSync = "netbox.sync"

// Guest is the state of a Proxmox guest to write to NetBox.
type Guest struct {
	// Cluster is dcdash's name of the Proxmox cluster.
	Cluster string
	VMID    int
	Name    string
	// OldName is the name before a rename, to find the VM without a VMID
	// custom field.
	OldName   string
	Container bool
	// VCPUs is sockets × cores (0: unknown, not written).
	VCPUs     float64
	MemoryMiB int64
	Disks     []GuestDisk
	Networks  []GuestNetwork
}

// GuestDisk is a disk of a guest.
type GuestDisk struct {
	// Name is the configuration key, e.g. scsi0, rootfs, mp0.
	Name    string
	SizeMiB int64
}

// GuestNetwork is a network device of a guest.
type GuestNetwork struct {
	// Name is the interface name: netN of a VM, the name inside a container.
	Name string
	MAC  string
	// VLAN is the VLAN tag, 0 for untagged.
	VLAN int
	// Addresses are static addresses with prefix length.
	Addresses []string
}

// active reports whether write-back applies to a Proxmox cluster.
func (s *Service) active(cluster string) bool {
	if s == nil || !s.cfg.WriteBack {
		return false
	}
	_, ok := s.cfg.Clusters[cluster]
	return ok
}

// Applies reports whether changes of a Proxmox cluster are written to
// NetBox.
func (s *Service) Applies(cluster string) bool { return s.active(cluster) }

// message is the change log message of a write-back.
func message(base audit.Entry, what string) string {
	actor := base.Actor
	if actor == "" {
		actor = "dcdash"
	}
	return "dcdash (" + actor + "): " + what
}

// finish audits a write-back and returns its result.
func (s *Service) finish(ctx context.Context, base audit.Entry, target string, details map[string]any, err error) *Result {
	e := base
	if e.Action == "" {
		e.Action = AuditSync
	}
	e.Target, e.Result = target, audit.ResultSuccess
	e.Details = details
	res := &Result{Status: StatusOK}
	if err != nil {
		e.Result = audit.ResultFailure
		details["error"] = err.Error()
		res = &Result{Status: StatusFailed, Error: err.Error()}
		s.log.Warn().Err(err).Str("target", target).Msg("netbox write-back failed")
	}
	if s.audit != nil {
		if aerr := s.audit.Record(context.WithoutCancel(ctx), e); aerr != nil {
			s.log.Error().Err(aerr).Msg("audit netbox.sync")
		}
	}
	return res
}

// Failed audits a write-back that failed before it reached NetBox, for
// example because the configuration of a guest couldn't be read.
func (s *Service) Failed(ctx context.Context, base audit.Entry, target string, err error) *Result {
	return s.finish(ctx, base, target, map[string]any{}, err)
}

// SyncGuest writes the state of a guest to NetBox: the VM, its disks,
// interfaces, MAC addresses, VLANs and static addresses. It returns nil if the
// guest's cluster isn't written to NetBox.
func (s *Service) SyncGuest(ctx context.Context, base audit.Entry, g Guest) *Result {
	if !s.active(g.Cluster) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, syncTimeout)
	defer cancel()
	var changes []string
	err := s.syncGuest(ctx, message(base, "update "+g.Name), g, &changes)
	return s.finish(ctx, base, guestTarget(g.Cluster, g.Container, g.VMID),
		map[string]any{"name": g.Name, "changes": nonNil(changes)}, err)
}

func guestTarget(cluster string, container bool, vmid int) string {
	t := "qemu"
	if container {
		t = "lxc"
	}
	return cluster + "/" + t + "/" + strconv.Itoa(vmid)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// findVM returns the NetBox VM of a guest, or ErrNotFound.
func (s *Service) findVM(ctx context.Context, clusterID int64, vmid int, names ...string) (VirtualMachine, error) {
	cl := strconv.FormatInt(clusterID, 10)
	if s.cfg.VMIDField != "" {
		vm, err := s.client.FindVM(ctx, url.Values{"cluster_id": {cl}, "cf_" + s.cfg.VMIDField: {strconv.Itoa(vmid)}})
		if !errors.Is(err, ErrNotFound) {
			return vm, err
		}
	}
	for _, n := range names {
		if n == "" {
			continue
		}
		vm, err := s.client.FindVM(ctx, url.Values{"cluster_id": {cl}, "name": {n}})
		if !errors.Is(err, ErrNotFound) {
			return vm, err
		}
	}
	return VirtualMachine{}, fmt.Errorf("NetBox: %w", ErrNotFound)
}

func (s *Service) syncGuest(ctx context.Context, msg string, g Guest, changes *[]string) error {
	clusterID, _, err := s.clusterID(ctx, g.Cluster)
	if err != nil {
		return err
	}
	tag, err := s.tagID(ctx)
	if err != nil {
		return err
	}
	vm, err := s.findVM(ctx, clusterID, g.VMID, g.OldName, g.Name)
	switch {
	case errors.Is(err, ErrNotFound):
		body := map[string]any{"name": g.Name, "cluster": clusterID, "status": "active", "memory": g.MemoryMiB,
			"tags": []any{tag}, "changelog_message": msg}
		if g.VCPUs > 0 {
			body["vcpus"] = g.VCPUs
		}
		if s.cfg.VMIDField != "" {
			body["custom_fields"] = map[string]any{s.cfg.VMIDField: g.VMID}
		}
		if err := s.client.Create(ctx, pathVMs, body, &vm); err != nil {
			return fmt.Errorf("create the VM %s: %w", g.Name, err)
		}
		*changes = append(*changes, "vm created")
	case err != nil:
		return err
	default:
		body := map[string]any{}
		if vm.Name != g.Name {
			body["name"] = g.Name
		}
		if g.VCPUs > 0 && (vm.VCPUs == nil || *vm.VCPUs != g.VCPUs) {
			body["vcpus"] = g.VCPUs
		}
		if vm.Memory == nil || *vm.Memory != g.MemoryMiB {
			body["memory"] = g.MemoryMiB
		}
		if f := s.cfg.VMIDField; f != "" && fmt.Sprint(vm.CustomFields[f]) != strconv.Itoa(g.VMID) {
			body["custom_fields"] = map[string]any{f: g.VMID}
		}
		if len(body) > 0 {
			body["changelog_message"] = msg
			if err := s.client.Update(ctx, pathVMs, vm.ID, body, &vm); err != nil {
				return fmt.Errorf("update the VM %s: %w", g.Name, err)
			}
			*changes = append(*changes, "vm updated")
		}
	}
	if err := s.syncDisks(ctx, msg, vm.ID, g.Disks, changes); err != nil {
		return err
	}
	return s.syncInterfaces(ctx, msg, vm, g.Networks, tag, changes)
}

func (s *Service) syncDisks(ctx context.Context, msg string, vmID int64, disks []GuestDisk, changes *[]string) error {
	have, err := s.client.Disks(ctx, url.Values{"virtual_machine_id": {strconv.FormatInt(vmID, 10)}})
	if err != nil {
		return err
	}
	byName := map[string]VirtualDisk{}
	for _, d := range have {
		byName[d.Name] = d
	}
	for _, d := range disks {
		old, ok := byName[d.Name]
		delete(byName, d.Name)
		switch {
		case !ok:
			if err := s.client.Create(ctx, pathDisks, map[string]any{"virtual_machine": vmID, "name": d.Name, "size": d.SizeMiB,
				"changelog_message": msg}, nil); err != nil {
				return fmt.Errorf("create the disk %s: %w", d.Name, err)
			}
			*changes = append(*changes, "disk "+d.Name+" created")
		case old.Size != d.SizeMiB:
			if err := s.client.Update(ctx, pathDisks, old.ID, map[string]any{"size": d.SizeMiB, "changelog_message": msg}, nil); err != nil {
				return fmt.Errorf("update the disk %s: %w", d.Name, err)
			}
			*changes = append(*changes, "disk "+d.Name+" updated")
		}
	}
	for _, d := range have {
		if _, extra := byName[d.Name]; !extra {
			continue
		}
		if err := s.client.Delete(ctx, pathDisks, d.ID, msg); err != nil {
			return fmt.Errorf("delete the disk %s: %w", d.Name, err)
		}
		*changes = append(*changes, "disk "+d.Name+" deleted")
	}
	return nil
}

// vlanID returns the NetBox ID of a VLAN tag, 0 if NetBox doesn't know it.
func (s *Service) vlanID(ctx context.Context, vid int) (int64, error) {
	if vid == 0 {
		return 0, nil
	}
	q, err := s.vlanQuery(ctx, vid)
	if err != nil {
		return 0, err
	}
	vlans, err := s.client.VLANs(ctx, q)
	if err != nil || len(vlans) == 0 {
		return 0, err
	}
	return vlans[0].ID, nil
}

func (s *Service) syncInterfaces(ctx context.Context, msg string, vm VirtualMachine, nets []GuestNetwork, tag int64, changes *[]string) error {
	have, err := s.client.Interfaces(ctx, url.Values{"virtual_machine_id": {strconv.FormatInt(vm.ID, 10)}})
	if err != nil {
		return err
	}
	byName := map[string]Interface{}
	for _, i := range have {
		byName[i.Name] = i
	}
	keep := map[string]bool{}
	var first4, first6 int64
	for _, n := range nets {
		keep[n.Name] = true
		vlan, err := s.vlanID(ctx, n.VLAN)
		if err != nil {
			return err
		}
		ifc, ok := byName[n.Name]
		want := map[string]any{"mode": nil, "untagged_vlan": nil}
		if vlan != 0 {
			want = map[string]any{"mode": "access", "untagged_vlan": vlan}
		}
		if !ok {
			want["virtual_machine"], want["name"], want["changelog_message"] = vm.ID, n.Name, msg
			if err := s.client.Create(ctx, pathInterfaces, want, &ifc); err != nil {
				return fmt.Errorf("create the interface %s: %w", n.Name, err)
			}
			*changes = append(*changes, "interface "+n.Name+" created")
		} else if curVLAN(ifc) != vlan {
			want["changelog_message"] = msg
			if err := s.client.Update(ctx, pathInterfaces, ifc.ID, want, &ifc); err != nil {
				return fmt.Errorf("update the interface %s: %w", n.Name, err)
			}
			*changes = append(*changes, "interface "+n.Name+" updated")
		}
		if err := s.syncMAC(ctx, msg, ifc, n.MAC, changes); err != nil {
			return err
		}
		for _, a := range n.Addresses {
			id, err := s.assignAddress(ctx, msg, ifc, a, tag, changes)
			if err != nil {
				return err
			}
			p, _ := netip.ParsePrefix(a)
			if p.Addr().Is4() && first4 == 0 {
				first4 = id
			} else if p.Addr().Is6() && first6 == 0 {
				first6 = id
			}
		}
	}
	for _, i := range have {
		if keep[i.Name] {
			continue
		}
		if err := s.client.Delete(ctx, pathInterfaces, i.ID, msg); err != nil {
			return fmt.Errorf("delete the interface %s: %w", i.Name, err)
		}
		*changes = append(*changes, "interface "+i.Name+" deleted")
	}
	// The first static addresses become the primary addresses unless the VM
	// has some.
	body := map[string]any{}
	if first4 != 0 && vm.PrimaryIP4 == nil {
		body["primary_ip4"] = first4
	}
	if first6 != 0 && vm.PrimaryIP6 == nil {
		body["primary_ip6"] = first6
	}
	if len(body) > 0 {
		body["changelog_message"] = msg
		if err := s.client.Update(ctx, pathVMs, vm.ID, body, nil); err != nil {
			return fmt.Errorf("set the primary addresses: %w", err)
		}
	}
	return nil
}

func curVLAN(i Interface) int64 {
	if i.UntaggedVLAN == nil {
		return 0
	}
	return i.UntaggedVLAN.ID
}

// syncMAC makes mac the primary MAC address of an interface. A previous
// primary MAC address is deleted.
func (s *Service) syncMAC(ctx context.Context, msg string, ifc Interface, mac string, changes *[]string) error {
	if mac == "" || (ifc.PrimaryMAC != nil && strings.EqualFold(ifc.PrimaryMAC.MACAddress, mac)) {
		return nil
	}
	macs, err := s.client.MACAddresses(ctx, url.Values{"vminterface_id": {strconv.FormatInt(ifc.ID, 10)}})
	if err != nil {
		return err
	}
	var id int64
	for _, m := range macs {
		if strings.EqualFold(m.MACAddress, mac) {
			id = m.ID
		}
	}
	if id == 0 {
		var m MACAddress
		if err := s.client.Create(ctx, pathMACs, map[string]any{"mac_address": mac, "assigned_object_type": TypeVMInterface,
			"assigned_object_id": ifc.ID, "changelog_message": msg}, &m); err != nil {
			return fmt.Errorf("create the MAC address %s: %w", mac, err)
		}
		id = m.ID
	}
	if err := s.client.Update(ctx, pathInterfaces, ifc.ID, map[string]any{"primary_mac_address": id, "changelog_message": msg}, nil); err != nil {
		return fmt.Errorf("set the MAC address of %s: %w", ifc.Name, err)
	}
	if ifc.PrimaryMAC != nil {
		if err := s.client.Delete(ctx, pathMACs, ifc.PrimaryMAC.ID, msg); err != nil && !errors.Is(err, ErrNotFound) {
			return fmt.Errorf("delete the MAC address %s: %w", ifc.PrimaryMAC.MACAddress, err)
		}
	}
	*changes = append(*changes, "interface "+ifc.Name+" MAC address "+mac)
	return nil
}

// findAddress returns the IP address object of an address (without prefix
// length) in the configured VRF, or ErrNotFound.
func (s *Service) findAddress(ctx context.Context, addr netip.Addr) (IPAddress, error) {
	vrf, err := s.vrfFilter(ctx)
	if err != nil {
		return IPAddress{}, err
	}
	l, err := s.client.IPAddresses(ctx, url.Values{"address": {addr.String()}, "vrf_id": {vrf}})
	if err != nil {
		return IPAddress{}, err
	}
	if len(l) == 0 {
		return IPAddress{}, fmt.Errorf("NetBox: address %s: %w", addr, ErrNotFound)
	}
	return l[0], nil
}

// newAddressBody returns the fields of a new address.
func (s *Service) newAddressBody(ctx context.Context, address string, tag int64, msg string) (map[string]any, error) {
	body := map[string]any{"address": address, "status": "active", "tags": []any{tag}, "changelog_message": msg}
	if s.cfg.VRF != "" {
		vrf, err := s.vrfID(ctx)
		if err != nil {
			return nil, err
		}
		body["vrf"] = vrf
	}
	return body, nil
}

// assignAddress assigns an address (with prefix length) to an interface,
// creating it if needed, and returns its ID. An address assigned to another
// interface is an error.
func (s *Service) assignAddress(ctx context.Context, msg string, ifc Interface, address string, tag int64, changes *[]string) (int64, error) {
	p, err := netip.ParsePrefix(address)
	if err != nil {
		return 0, fmt.Errorf("address %q: %w", address, err)
	}
	ip, err := s.findAddress(ctx, p.Addr())
	switch {
	case errors.Is(err, ErrNotFound):
		body, err := s.newAddressBody(ctx, address, tag, msg)
		if err != nil {
			return 0, err
		}
		body["assigned_object_type"], body["assigned_object_id"] = TypeVMInterface, ifc.ID
		if err := s.client.Create(ctx, pathIPAddresses, body, &ip); err != nil {
			return 0, fmt.Errorf("create the address %s: %w", address, err)
		}
		*changes = append(*changes, "address "+address+" created")
		return ip.ID, nil
	case err != nil:
		return 0, err
	}
	if ip.AssignedObjectID != nil && *ip.AssignedObjectID == ifc.ID && ip.AssignedObjectType == TypeVMInterface {
		return ip.ID, nil
	}
	if ip.AssignedObjectID != nil {
		return 0, fmt.Errorf("NetBox: the address %s is assigned to another interface", address)
	}
	if err := s.client.Update(ctx, pathIPAddresses, ip.ID, map[string]any{"assigned_object_type": TypeVMInterface,
		"assigned_object_id": ifc.ID, "changelog_message": msg}, nil); err != nil {
		return 0, fmt.Errorf("assign the address %s: %w", address, err)
	}
	*changes = append(*changes, "address "+address+" assigned")
	return ip.ID, nil
}

// DeleteGuest handles a deleted guest: the NetBox VM is deleted or set to
// "decommissioning" (netbox.on_delete). A VM that isn't in NetBox is no
// error.
func (s *Service) DeleteGuest(ctx context.Context, base audit.Entry, cluster string, container bool, vmid int, name string) *Result {
	if !s.active(cluster) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, syncTimeout)
	defer cancel()
	details := map[string]any{"name": name}
	err := func() error {
		clusterID, _, err := s.clusterID(ctx, cluster)
		if err != nil {
			return err
		}
		vm, err := s.findVM(ctx, clusterID, vmid, name)
		if errors.Is(err, ErrNotFound) {
			details["changes"] = []string{}
			return nil
		}
		if err != nil {
			return err
		}
		msg := message(base, "delete "+name)
		if s.cfg.OnDelete == config.NetBoxDeleteAlways || (s.cfg.OnDelete == config.NetBoxDeleteTagged && vm.HasTag(s.cfg.Tag)) {
			details["changes"] = []string{"vm deleted"}
			return s.client.Delete(ctx, pathVMs, vm.ID, msg)
		}
		details["changes"] = []string{"vm decommissioning"}
		return s.client.Update(ctx, pathVMs, vm.ID, map[string]any{"status": "decommissioning", "changelog_message": msg}, nil)
	}()
	return s.finish(ctx, base, guestTarget(cluster, container, vmid), details, err)
}

// Base is the audit entry of a write-back caused by a request: actor, client
// address and request ID; the time is set when it is recorded.
func Base(r *http.Request) audit.Entry {
	base := audit.FromRequest(r, "", "", "")
	base.Time = time.Time{}
	return base
}
