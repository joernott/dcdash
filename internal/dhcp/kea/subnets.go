// SPDX-License-Identifier: GPL-3.0-or-later

package kea

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
)

// SubnetRef is an entry of subnet4-list or subnet6-list.
type SubnetRef struct {
	ID            uint32  `json:"id"`
	Prefix        string  `json:"subnet"`
	SharedNetwork *string `json:"shared-network-name"`
}

// Subnets lists the subnets (subnet_cmds).
func (c *Client) Subnets(ctx context.Context) ([]SubnetRef, error) {
	var l struct {
		Subnets []SubnetRef `json:"subnets"`
	}
	_, err := c.Do(ctx, "subnet"+c.v()+"-list", nil, &l)
	return l.Subnets, err
}

// Pool is an address pool of a subnet.
type Pool struct {
	// Pool is "first - last" or a prefix.
	Pool string `json:"pool"`
	// PoolID groups pools for statistics (0 if not configured).
	PoolID int `json:"pool-id,omitempty"`
}

// PDPool is a DHCPv6 prefix delegation pool.
type PDPool struct {
	Prefix       string `json:"prefix"`
	PrefixLen    int    `json:"prefix-len"`
	DelegatedLen int    `json:"delegated-len"`
}

// Option is an option value (option-data).
type Option struct {
	Name  string `json:"name,omitempty"`
	Code  int    `json:"code,omitempty"`
	Space string `json:"space,omitempty"`
	Data  string `json:"data,omitempty"`
}

// Subnet is a subnet with its pools (subnet4-get or subnet6-get).
type Subnet struct {
	ID            uint32   `json:"id"`
	Prefix        string   `json:"subnet"`
	SharedNetwork string   `json:"shared-network-name,omitempty"`
	Pools         []Pool   `json:"pools"`
	PDPools       []PDPool `json:"pd-pools,omitempty"`
	Options       []Option `json:"option-data,omitempty"`
	ValidLifetime uint32   `json:"valid-lifetime,omitempty"`
}

// Subnet returns a subnet with its pools.
func (c *Client) Subnet(ctx context.Context, id uint32) (Subnet, error) {
	var out map[string][]Subnet
	key := "subnet" + c.v()
	r, err := c.Do(ctx, key+"-get", map[string]any{"id": id}, &out)
	if err != nil {
		return Subnet{}, err
	}
	if r.Empty() || len(out[key]) == 0 {
		return Subnet{}, &Error{Command: key + "-get", Result: ResultEmpty, Text: fmt.Sprintf("subnet %d not found", id)}
	}
	return out[key][0], nil
}

// SubnetStats is the utilization of a subnet (stat_cmds). For v6, the
// address counts are IA_NA addresses and the PD counts delegated prefixes.
// Counts are float64: a /64 has more addresses than an int64 holds.
type SubnetStats struct {
	SubnetID   uint32  `json:"subnetId"`
	Total      float64 `json:"total"`
	Assigned   float64 `json:"assigned"`
	Declined   float64 `json:"declined"`
	TotalPD    float64 `json:"totalPd,omitempty"`
	AssignedPD float64 `json:"assignedPd,omitempty"`
}

// LeaseStats returns the utilization of all subnets (stat-lease4-get or
// stat-lease6-get).
func (c *Client) LeaseStats(ctx context.Context) ([]SubnetStats, error) {
	var out struct {
		ResultSet struct {
			Columns []string        `json:"columns"`
			Rows    [][]json.Number `json:"rows"`
		} `json:"result-set"`
	}
	r, err := c.Do(ctx, "stat-lease"+c.v()+"-get", nil, &out)
	if err != nil || r.Empty() {
		return nil, err
	}
	var stats []SubnetStats
	for _, row := range out.ResultSet.Rows {
		var s SubnetStats
		for i, col := range out.ResultSet.Columns {
			if i >= len(row) {
				break
			}
			f, err := row[i].Float64()
			if err != nil {
				return nil, fmt.Errorf("kea: stat-lease%s-get: %s: %w", c.v(), col, err)
			}
			switch col {
			case "subnet-id":
				s.SubnetID = uint32(f)
			case "total-addresses", "total-nas":
				s.Total = f
			case "assigned-addresses", "assigned-nas":
				s.Assigned = f
			case "declined-addresses", "declined-nas":
				s.Declined = f
			case "total-pds":
				s.TotalPD = f
			case "assigned-pds":
				s.AssignedPD = f
			}
		}
		stats = append(stats, s)
	}
	return stats, nil
}

// PoolStats is the utilization of the pools with one pool ID in a subnet,
// from statistic-get-all. Kea counts per pool-id, which is 0 unless the
// configuration sets it, so pools without an ID are counted together.
type PoolStats struct {
	SubnetID uint32  `json:"subnetId"`
	PoolID   int     `json:"poolId"`
	Total    float64 `json:"total"`
	Assigned float64 `json:"assigned"`
}

var poolStat = regexp.MustCompile(`^subnet\[(\d+)\]\.pool\[(\d+)\]\.(total-addresses|assigned-addresses|total-nas|assigned-nas)$`)

// PoolStats returns the utilization per subnet and pool ID.
func (c *Client) PoolStats(ctx context.Context) ([]PoolStats, error) {
	// Each statistic is a list of [value, timestamp] samples, newest first.
	var all map[string][][]json.RawMessage
	if _, err := c.Do(ctx, "statistic-get-all", nil, &all); err != nil {
		return nil, err
	}
	type key struct {
		subnet uint32
		pool   int
	}
	byKey := map[key]*PoolStats{}
	var order []key
	for name, samples := range all {
		m := poolStat.FindStringSubmatch(name)
		if m == nil || len(samples) == 0 || len(samples[0]) == 0 {
			continue
		}
		var n json.Number
		if err := json.Unmarshal(samples[0][0], &n); err != nil {
			continue
		}
		v, err := n.Float64()
		if err != nil {
			continue
		}
		sid, _ := strconv.ParseUint(m[1], 10, 32)
		pool, _ := strconv.Atoi(m[2])
		k := key{uint32(sid), pool}
		p := byKey[k]
		if p == nil {
			p = &PoolStats{SubnetID: k.subnet, PoolID: k.pool}
			byKey[k] = p
			order = append(order, k)
		}
		if m[3] == "total-addresses" || m[3] == "total-nas" {
			p.Total = v
		} else {
			p.Assigned = v
		}
	}
	out := make([]PoolStats, 0, len(order))
	for _, k := range order {
		out = append(out, *byKey[k])
	}
	slices.SortFunc(out, func(a, b PoolStats) int {
		return cmp.Or(cmp.Compare(a.SubnetID, b.SubnetID), cmp.Compare(a.PoolID, b.PoolID))
	})
	return out, nil
}

// SubnetsFromConfig lists the subnets from config-get, including those of
// shared networks, for servers without subnet_cmds. Only IDs and prefixes are
// kept; the rest of the configuration (which can contain passwords) is
// dropped.
func (c *Client) SubnetsFromConfig(ctx context.Context) ([]SubnetRef, error) {
	key := "subnet" + c.v()
	type subnet struct {
		ID     uint32 `json:"id"`
		Prefix string `json:"subnet"`
	}
	var cfg map[string]json.RawMessage
	if _, err := c.Do(ctx, "config-get", nil, &cfg); err != nil {
		return nil, err
	}
	raw, ok := cfg["Dhcp"+c.v()]
	if !ok {
		return nil, fmt.Errorf("kea: config-get: no Dhcp%s configuration; wrong family?", c.v())
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("kea: config-get: %w", err)
	}
	var out []SubnetRef
	var top []subnet
	if err := json.Unmarshal(orEmptyList(fields[key]), &top); err != nil {
		return nil, fmt.Errorf("kea: config-get: %s: %w", key, err)
	}
	for _, s := range top {
		out = append(out, SubnetRef{ID: s.ID, Prefix: s.Prefix})
	}
	var shared []map[string]json.RawMessage
	if err := json.Unmarshal(orEmptyList(fields["shared-networks"]), &shared); err != nil {
		return nil, fmt.Errorf("kea: config-get: shared-networks: %w", err)
	}
	for _, n := range shared {
		var name string
		_ = json.Unmarshal(n["name"], &name)
		var subs []subnet
		if err := json.Unmarshal(orEmptyList(n[key]), &subs); err != nil {
			return nil, fmt.Errorf("kea: config-get: shared network %s: %w", name, err)
		}
		for _, s := range subs {
			out = append(out, SubnetRef{ID: s.ID, Prefix: s.Prefix, SharedNetwork: &name})
		}
	}
	slices.SortFunc(out, func(a, b SubnetRef) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

func orEmptyList(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("[]")
	}
	return raw
}
