// SPDX-License-Identifier: GPL-3.0-or-later

package netbox

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"slices"
	"strings"

	"github.com/joernott/dcdash/internal/audit"
)

// Write-back of DHCP reservations and DNS records (task 12.3.3): a reserved
// address and the address of an A or AAAA record are IP addresses in NetBox;
// the record's name is the address's DNS name.

// Reservation is a DHCP reservation.
type Reservation struct {
	// Server is the name of the DHCP server in dcdash.
	Server string
	// Addresses are the reserved addresses (without prefix length).
	Addresses []string
	// MAC is the hardware address; empty for reservations by other
	// identifiers.
	MAC string
	// Projects (names) replace the projects of the addresses; nil keeps them.
	Projects *[]string
}

func (r Reservation) description() string { return "DHCP reservation (" + r.Server + ")" }

// writing reports whether changes are written to NetBox.
func (s *Service) writing() bool { return s != nil && s.cfg.WriteBack }

// Writes reports whether DHCP and DNS changes are written to NetBox.
func (s *Service) Writes() bool { return s.writing() }

// prefixLength returns the address with the prefix length of the longest
// NetBox prefix that contains it (in the configured VRF), or /32 (/128).
func (s *Service) prefixLength(ctx context.Context, a netip.Addr) (string, error) {
	vrf, err := s.vrfFilter(ctx)
	if err != nil {
		return "", err
	}
	l, err := s.client.Prefixes(ctx, url.Values{"contains": {a.String()}, "vrf_id": {vrf}})
	if err != nil {
		return "", err
	}
	bits := a.BitLen()
	best := -1
	for _, p := range l {
		if pp, err := netip.ParsePrefix(p.Prefix); err == nil && pp.Contains(a) && pp.Bits() > best && pp.Bits() < bits {
			best = pp.Bits()
		}
	}
	if best < 0 {
		best = bits
	}
	return netip.PrefixFrom(a, best).String(), nil
}

// ensureAddress returns the IP address object of an address, creating it with
// the tag (and the fields of extra) if it doesn't exist.
func (s *Service) ensureAddress(ctx context.Context, msg string, a netip.Addr, extra map[string]any, changes *[]string) (IPAddress, bool, error) {
	ip, err := s.findAddress(ctx, a)
	if err == nil || !errors.Is(err, ErrNotFound) {
		return ip, false, err
	}
	tag, err := s.tagID(ctx)
	if err != nil {
		return ip, false, err
	}
	address, err := s.prefixLength(ctx, a)
	if err != nil {
		return ip, false, err
	}
	body, err := s.newAddressBody(ctx, address, tag, msg)
	if err != nil {
		return ip, false, err
	}
	for k, v := range extra {
		body[k] = v
	}
	if err := s.client.Create(ctx, pathIPAddresses, body, &ip); err != nil {
		return ip, false, fmt.Errorf("create the address %s: %w", address, err)
	}
	*changes = append(*changes, "address "+address+" created")
	return ip, true, nil
}

// interfaceByMAC returns the ID of the VM interface with that MAC address, 0
// if there is none or more than one.
func (s *Service) interfaceByMAC(ctx context.Context, mac string) (int64, error) {
	if mac == "" {
		return 0, nil
	}
	l, err := s.client.Interfaces(ctx, url.Values{"mac_address": {mac}})
	if err != nil || len(l) != 1 {
		return 0, err
	}
	return l[0].ID, nil
}

// SyncReservation writes a new or changed reservation: its addresses exist in
// NetBox, described as DHCP reservation, and are assigned to the VM interface
// with the reservation's MAC address if they aren't assigned yet. previous is
// the reservation before a change (nil for a new one); its addresses that
// are no longer reserved are handled like a deleted reservation. It returns
// nil if changes aren't written to NetBox.
func (s *Service) SyncReservation(ctx context.Context, base audit.Entry, target string, res Reservation, previous *Reservation) *Result {
	if !s.writing() {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, syncTimeout)
	defer cancel()
	var changes []string
	err := func() error {
		msg := message(base, "DHCP reservation "+strings.Join(res.Addresses, ", "))
		if previous != nil {
			gone := slices.DeleteFunc(slices.Clone(previous.Addresses), func(a string) bool { return slices.Contains(res.Addresses, a) })
			if err := s.releaseAddresses(ctx, msg, *previous, gone, &changes); err != nil {
				return err
			}
		}
		ifc, err := s.interfaceByMAC(ctx, res.MAC)
		if err != nil {
			return err
		}
		for _, addr := range res.Addresses {
			a, err := netip.ParseAddr(addr)
			if err != nil {
				return fmt.Errorf("address %q: %w", addr, err)
			}
			extra := map[string]any{"description": res.description()}
			if ifc != 0 {
				extra["assigned_object_type"], extra["assigned_object_id"] = TypeVMInterface, ifc
			}
			ip, created, err := s.ensureAddress(ctx, msg, a, extra, &changes)
			if err != nil || created {
				if err != nil {
					return err
				}
				continue
			}
			body := map[string]any{}
			if ip.Description == "" {
				body["description"] = res.description()
			}
			if ifc != 0 && ip.AssignedObjectID == nil {
				body["assigned_object_type"], body["assigned_object_id"] = TypeVMInterface, ifc
			}
			if len(body) == 0 {
				continue
			}
			body["changelog_message"] = msg
			if err := s.client.Update(ctx, pathIPAddresses, ip.ID, body, nil); err != nil {
				return fmt.Errorf("update the address %s: %w", ip.Address, err)
			}
			changes = append(changes, "address "+ip.Address+" updated")
		}
		return s.setAddressProjects(ctx, msg, res.Addresses, res.Projects, &changes)
	}()
	return s.finish(ctx, base, target, map[string]any{"addresses": res.Addresses, "changes": nonNil(changes)}, err)
}

// DeleteReservation handles a deleted reservation: an address that dcdash
// created for it (with the tag, not assigned to an interface, without DNS
// name) is deleted; otherwise only the description is cleared.
func (s *Service) DeleteReservation(ctx context.Context, base audit.Entry, target string, res Reservation) *Result {
	if !s.writing() {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, syncTimeout)
	defer cancel()
	var changes []string
	err := s.releaseAddresses(ctx, message(base, "DHCP reservation deleted"), res, res.Addresses, &changes)
	return s.finish(ctx, base, target, map[string]any{"addresses": res.Addresses, "changes": nonNil(changes)}, err)
}

func (s *Service) releaseAddresses(ctx context.Context, msg string, res Reservation, addrs []string, changes *[]string) error {
	for _, addr := range addrs {
		a, err := netip.ParseAddr(addr)
		if err != nil {
			continue
		}
		ip, err := s.findAddress(ctx, a)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		switch {
		case ip.HasTag(s.cfg.Tag) && ip.AssignedObjectID == nil && ip.DNSName == "":
			if err := s.client.Delete(ctx, pathIPAddresses, ip.ID, msg); err != nil {
				return fmt.Errorf("delete the address %s: %w", ip.Address, err)
			}
			*changes = append(*changes, "address "+ip.Address+" deleted")
		case ip.Description == res.description():
			if err := s.client.Update(ctx, pathIPAddresses, ip.ID, map[string]any{"description": "", "changelog_message": msg}, nil); err != nil {
				return fmt.Errorf("update the address %s: %w", ip.Address, err)
			}
			*changes = append(*changes, "address "+ip.Address+" updated")
		}
	}
	return nil
}

// DNSRecord is an A or AAAA record.
type DNSRecord struct {
	// Name is the fully qualified name, with or without the final dot.
	Name    string
	Address string
	// Projects (names) replace the projects of the address; nil keeps them.
	Projects *[]string
}

func (r DNSRecord) name() string { return strings.TrimSuffix(strings.ToLower(r.Name), ".") }

// SyncDNSName writes a new or changed A or AAAA record: the address exists
// in NetBox with the record's name as DNS name. previous is the record before
// a change (nil for a new one); its name is cleared from its address.
func (s *Service) SyncDNSName(ctx context.Context, base audit.Entry, target string, rec DNSRecord, previous *DNSRecord) *Result {
	if !s.writing() {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, syncTimeout)
	defer cancel()
	var changes []string
	err := func() error {
		msg := message(base, "DNS "+rec.name()+" "+rec.Address)
		if previous != nil && (previous.Address != rec.Address || previous.name() != rec.name()) {
			if err := s.clearDNSName(ctx, msg, *previous, &changes); err != nil {
				return err
			}
		}
		a, err := netip.ParseAddr(rec.Address)
		if err != nil {
			return fmt.Errorf("address %q: %w", rec.Address, err)
		}
		ip, created, err := s.ensureAddress(ctx, msg, a, map[string]any{"dns_name": rec.name()}, &changes)
		if err != nil {
			return err
		}
		if !created && ip.DNSName != rec.name() {
			if err := s.client.Update(ctx, pathIPAddresses, ip.ID, map[string]any{"dns_name": rec.name(), "changelog_message": msg}, nil); err != nil {
				return fmt.Errorf("update the address %s: %w", ip.Address, err)
			}
			changes = append(changes, "address "+ip.Address+" DNS name "+rec.name())
		}
		return s.setAddressProjects(ctx, msg, []string{rec.Address}, rec.Projects, &changes)
	}()
	return s.finish(ctx, base, target, map[string]any{"name": rec.name(), "address": rec.Address, "changes": nonNil(changes)}, err)
}

// ClearDNSName handles a deleted A or AAAA record: the address keeps existing,
// but loses its DNS name if it is the record's name.
func (s *Service) ClearDNSName(ctx context.Context, base audit.Entry, target string, rec DNSRecord) *Result {
	if !s.writing() {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, syncTimeout)
	defer cancel()
	var changes []string
	err := s.clearDNSName(ctx, message(base, "DNS "+rec.name()+" deleted"), rec, &changes)
	return s.finish(ctx, base, target, map[string]any{"name": rec.name(), "address": rec.Address, "changes": nonNil(changes)}, err)
}

func (s *Service) clearDNSName(ctx context.Context, msg string, rec DNSRecord, changes *[]string) error {
	a, err := netip.ParseAddr(rec.Address)
	if err != nil {
		return nil
	}
	ip, err := s.findAddress(ctx, a)
	if errors.Is(err, ErrNotFound) || (err == nil && ip.DNSName != rec.name()) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := s.client.Update(ctx, pathIPAddresses, ip.ID, map[string]any{"dns_name": "", "changelog_message": msg}, nil); err != nil {
		return fmt.Errorf("update the address %s: %w", ip.Address, err)
	}
	*changes = append(*changes, "address "+ip.Address+" DNS name cleared")
	return nil
}

// setAddressProjects sets the projects of addresses; nil keeps them.
func (s *Service) setAddressProjects(ctx context.Context, msg string, addrs []string, projects *[]string, changes *[]string) error {
	if projects == nil {
		return nil
	}
	for _, a := range addrs {
		addr, err := netip.ParseAddr(a)
		if err != nil {
			continue
		}
		ip, err := s.findAddress(ctx, addr)
		if err != nil {
			return err
		}
		if err := s.tagProjects(ctx, msg, ip, *projects, changes); err != nil {
			return err
		}
	}
	return nil
}
