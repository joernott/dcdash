// SPDX-License-Identifier: GPL-3.0-or-later

package kea

import (
	"context"
	"fmt"
)

// Lease is a DHCPv4 or DHCPv6 lease as Kea reports it.
type Lease struct {
	IPAddress string `json:"ip-address"`
	// HWAddress is the MAC address (v4; v6 only if known).
	HWAddress string `json:"hw-address,omitempty"`
	// ClientID (v4).
	ClientID string `json:"client-id,omitempty"`
	// DUID, IAID, Type ("IA_NA", "IA_PD") and PrefixLen (v6).
	DUID      string `json:"duid,omitempty"`
	IAID      uint32 `json:"iaid,omitempty"`
	Type      string `json:"type,omitempty"`
	PrefixLen int    `json:"prefix-len,omitempty"`
	Hostname  string `json:"hostname,omitempty"`
	SubnetID  uint32 `json:"subnet-id"`
	// ValidLifetime and PreferredLifetime (v6) in seconds.
	ValidLifetime     uint32 `json:"valid-lft"`
	PreferredLifetime uint32 `json:"preferred-lft,omitempty"`
	// CLTT is the client's last transmission time (Unix seconds); the lease
	// expires at CLTT + ValidLifetime.
	CLTT int64 `json:"cltt"`
	// State: 0 default, 1 declined, 2 expired-reclaimed, 3 released.
	State int `json:"state"`
}

// Lease states.
const (
	LeaseDefault  = 0
	LeaseDeclined = 1
	LeaseExpired  = 2
	LeaseReleased = 3
)

type leaseList struct {
	Leases []Lease `json:"leases"`
}

// LeasePage returns up to limit leases after the address from ("start" for
// the first page), ordered by address.
func (c *Client) LeasePage(ctx context.Context, from string, limit int) ([]Lease, error) {
	if from == "" {
		from = "start"
	}
	var l leaseList
	_, err := c.Do(ctx, "lease"+c.v()+"-get-page", map[string]any{"from": from, "limit": limit}, &l)
	return l.Leases, err
}

// Lease search keys.
const (
	ByIP        = "ip"
	ByHWAddress = "hw-address"
	ByClientID  = "client-id"
	ByDUID      = "duid"
	ByHostname  = "hostname"
)

// SearchKeys returns the lease search keys of the family.
func (c *Client) SearchKeys() []string {
	if c.family == "v6" {
		return []string{ByIP, ByDUID, ByHostname}
	}
	return []string{ByIP, ByHWAddress, ByClientID, ByHostname}
}

// LeasesBy finds leases by IP address, MAC address, client ID (v4), DUID
// (v6) or hostname. Nothing found is an empty list.
func (c *Client) LeasesBy(ctx context.Context, key, value string) ([]Lease, error) {
	v := c.v()
	switch key {
	case ByIP:
		var l Lease
		r, err := c.Do(ctx, "lease"+v+"-get", map[string]any{"ip-address": value}, &l)
		if err != nil || r.Empty() {
			return nil, err
		}
		return []Lease{l}, nil
	case ByHWAddress, ByClientID, ByDUID, ByHostname:
		if !c.searchable(key) {
			break
		}
		var l leaseList
		_, err := c.Do(ctx, "lease"+v+"-get-by-"+key, map[string]any{key: value}, &l)
		return l.Leases, err
	}
	return nil, fmt.Errorf("kea: DHCP%s leases cannot be searched by %s", v, key)
}

func (c *Client) searchable(key string) bool {
	for _, k := range c.SearchKeys() {
		if k == key {
			return true
		}
	}
	return false
}

// DeleteLease deletes the lease of an address. typ is "IA_NA" or "IA_PD"
// for v6 (empty: IA_NA) and ignored for v4. A missing lease is not an error
// (deleted reports false).
func (c *Client) DeleteLease(ctx context.Context, ip, typ string) (deleted bool, err error) {
	args := map[string]any{"ip-address": ip}
	if c.family == "v6" && typ != "" {
		args["type"] = typ
	}
	r, err := c.Do(ctx, "lease"+c.v()+"-del", args, nil)
	return err == nil && !r.Empty(), err
}

// Lease returns the lease of an address; typ is "IA_NA" or "IA_PD" for v6
// (empty: IA_NA) and ignored for v4.
func (c *Client) Lease(ctx context.Context, ip, typ string) (Lease, bool, error) {
	args := map[string]any{"ip-address": ip}
	if c.family == "v6" && typ != "" {
		args["type"] = typ
	}
	var l Lease
	r, err := c.Do(ctx, "lease"+c.v()+"-get", args, &l)
	if err != nil || r.Empty() {
		return Lease{}, false, err
	}
	return l, true, nil
}

// SubnetLeases returns all leases of a subnet (lease4-get-all or
// lease6-get-all with a subnet filter).
func (c *Client) SubnetLeases(ctx context.Context, subnetID uint32) ([]Lease, error) {
	var l leaseList
	_, err := c.Do(ctx, "lease"+c.v()+"-get-all", map[string]any{"subnets": []uint32{subnetID}}, &l)
	return l.Leases, err
}
