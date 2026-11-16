// SPDX-License-Identifier: GPL-3.0-or-later

package kea

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
)

// Status is the answer of status-get.
type Status struct {
	PID            int   `json:"pid"`
	Uptime         int64 `json:"uptime"`
	Reload         int64 `json:"reload"`
	MultiThreading bool  `json:"multi-threading-enabled"`
	// HA is present only with the HA hook.
	HA []HAStatus `json:"high-availability,omitempty"`
}

// HAStatus is the state of one HA relationship as seen by this server.
type HAStatus struct {
	Mode    string `json:"ha-mode"`
	Servers struct {
		Local struct {
			Role   string   `json:"role"`
			State  string   `json:"state"`
			Name   string   `json:"server-name"`
			Scopes []string `json:"scopes"`
		} `json:"local"`
		Remote struct {
			Role      string `json:"role"`
			LastState string `json:"last-state"`
			Name      string `json:"server-name"`
			InTouch   bool   `json:"in-touch"`
			Age       int64  `json:"age"`
			Interrupt bool   `json:"communication-interrupted"`
		} `json:"remote"`
	} `json:"ha-servers"`
}

// Version returns the short version, e.g. "3.0.4".
func (c *Client) Version(ctx context.Context) (string, error) {
	r, err := c.Do(ctx, "version-get", nil, nil)
	return r.Text, err
}

// Status returns the server status.
func (c *Client) Status(ctx context.Context) (Status, error) {
	var s Status
	_, err := c.Do(ctx, "status-get", nil, &s)
	return s, err
}

// Commands returns the commands the server supports (list-commands).
func (c *Client) Commands(ctx context.Context) ([]string, error) {
	var cmds []string
	_, err := c.Do(ctx, "list-commands", nil, &cmds)
	slices.Sort(cmds)
	return cmds, err
}

// HostDatabase reports whether the server has a host database, which
// reservation changes need. It reads config-get and keeps only that fact:
// the configuration can contain passwords and is not passed on.
func (c *Client) HostDatabase(ctx context.Context) (bool, error) {
	var cfg map[string]json.RawMessage
	if _, err := c.Do(ctx, "config-get", nil, &cfg); err != nil {
		return false, err
	}
	var srv map[string]json.RawMessage
	if raw, ok := cfg["Dhcp"+c.v()]; !ok || json.Unmarshal(raw, &srv) != nil {
		return false, errors.New("kea: config-get: no Dhcp" + c.v() + " configuration; wrong family?")
	}
	for _, key := range []string{"hosts-database", "hosts-databases"} {
		if v, ok := srv[key]; ok && !isEmptyJSON(v) {
			return true, nil
		}
	}
	return false, nil
}

func isEmptyJSON(v json.RawMessage) bool {
	s := strings.TrimSpace(string(v))
	return s == "" || s == "null" || s == "[]" || s == "{}"
}

// Capabilities are the features a server offers to dcdash.
type Capabilities struct {
	// Leases: list and search leases (lease_cmds).
	Leases bool `json:"leases"`
	// LeaseDelete: delete a lease (lease_cmds).
	LeaseDelete bool `json:"leaseDelete"`
	// Reservations: list reservations (host_cmds).
	Reservations bool `json:"reservations"`
	// ReservationWrite: add, change and delete reservations (host_cmds and a
	// host database).
	ReservationWrite bool `json:"reservationWrite"`
	// Subnets: list subnets and their pools (subnet_cmds).
	Subnets bool `json:"subnets"`
	// Statistics: per-subnet utilization (stat_cmds).
	Statistics bool `json:"statistics"`
	// HA: the HA hook is loaded.
	HA bool `json:"ha"`
}

// Info is what dcdash knows about a server after a probe.
type Info struct {
	Version      string       `json:"version"`
	Status       Status       `json:"status"`
	Commands     []string     `json:"-"`
	HostDatabase bool         `json:"hostDatabase"`
	Capabilities Capabilities `json:"capabilities"`
}

// Probe reads version, status, the supported commands and whether a host
// database is configured, and derives the capabilities.
func (c *Client) Probe(ctx context.Context) (Info, error) {
	var (
		info Info
		err  error
	)
	if info.Version, err = c.Version(ctx); err != nil {
		return info, err
	}
	if info.Status, err = c.Status(ctx); err != nil {
		return info, err
	}
	if info.Commands, err = c.Commands(ctx); err != nil {
		return info, err
	}
	has := func(names ...string) bool {
		for _, n := range names {
			if _, ok := slices.BinarySearch(info.Commands, strings.ReplaceAll(n, "#", c.v())); !ok {
				return false
			}
		}
		return true
	}
	if has("reservation-add") {
		if info.HostDatabase, err = c.HostDatabase(ctx); err != nil {
			return info, err
		}
	}
	info.Capabilities = Capabilities{
		Leases:           has("lease#-get-page", "lease#-get"),
		LeaseDelete:      has("lease#-del"),
		Reservations:     has("reservation-get-page"),
		ReservationWrite: info.HostDatabase && has("reservation-add", "reservation-update", "reservation-del"),
		Subnets:          has("subnet#-list", "subnet#-get"),
		Statistics:       has("stat-lease#-get"),
		HA:               has("ha-heartbeat"),
	}
	return info, nil
}
