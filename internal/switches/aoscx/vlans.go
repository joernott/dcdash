// SPDX-License-Identifier: GPL-3.0-or-later

package aoscx

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// DefaultVLAN is VLAN 1, which cannot be deleted.
const DefaultVLAN = 1

// VLAN is a VLAN of a switch.
type VLAN struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	// Type is "default" (VLAN 1), "static", ...
	Type        string `json:"type"`
	AdminUp     bool   `json:"adminUp"`
	OperUp      bool   `json:"operUp"`
	Description string `json:"description,omitempty"`
}

// VLANs reads the VLANs, sorted by ID.
func (c *Client) VLANs(ctx context.Context) ([]VLAN, error) {
	var raw map[string]struct {
		ID          int     `json:"id"`
		Name        string  `json:"name"`
		Type        string  `json:"type"`
		Admin       string  `json:"admin"`
		OperState   string  `json:"oper_state"`
		Description *string `json:"description"`
	}
	if err := c.Get(ctx, "/system/vlans?depth=2&attributes=id,name,type,admin,oper_state,description", &raw); err != nil {
		return nil, err
	}
	out := make([]VLAN, 0, len(raw))
	for _, r := range raw {
		v := VLAN{ID: r.ID, Name: r.Name, Type: r.Type, AdminUp: r.Admin == "up", OperUp: r.OperState == "up"}
		if r.Description != nil {
			v.Description = *r.Description
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// CreateVLAN creates a VLAN.
func (c *Client) CreateVLAN(ctx context.Context, id int, name string) error {
	return c.Do(ctx, http.MethodPost, "/system/vlans", map[string]any{"id": id, "name": name}, nil)
}

// RenameVLAN changes the name of a VLAN (read, change, PUT back).
func (c *Client) RenameVLAN(ctx context.Context, id int, name string) error {
	path := "/system/vlans/" + strconv.Itoa(id)
	var w map[string]any
	if err := c.Get(ctx, path+"?selector=writable", &w); err != nil {
		return err
	}
	w["name"] = name
	return c.Do(ctx, http.MethodPut, path, w, nil)
}

// DeleteVLAN deletes a VLAN.
func (c *Client) DeleteVLAN(ctx context.Context, id int) error {
	return c.Do(ctx, http.MethodDelete, "/system/vlans/"+strconv.Itoa(id), nil, nil)
}

// SaveConfig copies the running configuration to the startup configuration
// ("write memory").
func (c *Client) SaveConfig(ctx context.Context) error {
	c.mu.Lock()
	prefix := c.prefix
	c.mu.Unlock()
	return c.Do(ctx, http.MethodPut, "/fullconfigs/startup-config?from="+prefix+"/fullconfigs/running-config", nil, nil)
}

// MACEntry is an entry of the MAC address table.
type MACEntry struct {
	MAC  string `json:"mac"`
	VLAN int    `json:"vlan"`
	Port string `json:"port"`
	// From is how the entry was learned: "dynamic", "static", ...
	From string `json:"from"`
}

// MACs reads the MAC address table of all VLANs.
func (c *Client) MACs(ctx context.Context) ([]MACEntry, error) {
	// {"<vlan>": {"<from>,<mac>": {"mac_addr": ..., "from": ..., "port": {"1/1/1": "/rest/..."}}}}
	var raw map[string]map[string]struct {
		MACAddr string            `json:"mac_addr"`
		From    string            `json:"from"`
		Port    map[string]string `json:"port"`
	}
	if err := c.Get(ctx, "/system/vlans/*/macs?depth=2&attributes=mac_addr,from,port", &raw); err != nil {
		return nil, err
	}
	var out []MACEntry
	for vlan, entries := range raw {
		id, _ := strconv.Atoi(vlan)
		for _, e := range entries {
			for port := range e.Port {
				out = append(out, MACEntry{MAC: strings.ToLower(e.MACAddr), VLAN: id, Port: port, From: e.From})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].MAC != out[j].MAC {
			return out[i].MAC < out[j].MAC
		}
		return out[i].VLAN < out[j].VLAN
	})
	return out, nil
}
