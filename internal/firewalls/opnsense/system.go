// SPDX-License-Identifier: GPL-3.0-or-later

package opnsense

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// Search answers: {"total": n, "rowCount": n, "current": 1, "rows": [...]}.
type searchResult[T any] struct {
	Total int `json:"total"`
	Rows  []T `json:"rows"`
}

// searchAll reads all rows of a search command.
func searchAll[T any](ctx context.Context, c *Client, path string) ([]T, error) {
	var r searchResult[T]
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	if err := c.Get(ctx, path+sep+"current=1&rowCount=-1", &r); err != nil {
		return nil, err
	}
	if r.Rows == nil {
		r.Rows = []T{}
	}
	return r.Rows, nil
}

// flexString reads a JSON string, number or boolean as text (search rows are
// not consistent about it).
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*f = flexString(s)
		return nil
	}
	if string(b) == "null" {
		*f = ""
		return nil
	}
	*f = flexString(strings.Trim(string(b), `"`))
	return nil
}

func (f flexString) bool() bool {
	switch strings.ToLower(string(f)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func (f flexString) int64() int64 {
	n, _ := strconv.ParseInt(string(f), 10, 64)
	return n
}

// System is the state of a firewall.
type System struct {
	// Product is "OPNsense" (or the Business Edition's name), Version e.g. "26.1.6".
	Product string `json:"product"`
	Version string `json:"version"`
	// Uptime as OPNsense formats it, e.g. "12 days, 03:04:05".
	Uptime string `json:"uptime,omitempty"`
	// MemoryUsed and MemoryTotal in bytes; 0 if unknown.
	MemoryUsed  int64 `json:"memoryUsed,omitempty"`
	MemoryTotal int64 `json:"memoryTotal,omitempty"`
}

// System reads version, uptime and memory. Uptime and memory are optional:
// without the privilege "Lobby: Dashboard" they stay empty.
func (c *Client) System(ctx context.Context) (System, error) {
	var fw struct {
		Product struct {
			Version string `json:"product_version"`
			Name    string `json:"product_name"`
		} `json:"product"`
	}
	if err := c.Get(ctx, "/core/firmware/status", &fw); err != nil {
		return System{}, err
	}
	s := System{Product: fw.Product.Name, Version: fw.Product.Version}
	var t struct {
		Uptime string `json:"uptime"`
	}
	if err := c.Get(ctx, "/diagnostics/system/system_time", &t); err == nil {
		s.Uptime = t.Uptime
	}
	var r struct {
		Memory struct {
			Total flexString `json:"total"`
			Used  flexString `json:"used"`
		} `json:"memory"`
	}
	if err := c.Get(ctx, "/diagnostics/system/system_resources", &r); err == nil {
		s.MemoryTotal, s.MemoryUsed = r.Memory.Total.int64(), r.Memory.Used.int64()
	}
	return s, nil
}

// Gateway states.
const (
	GatewayOnline = "online"
	GatewayDown   = "down"
	// GatewayDegraded: delay or packet loss above the thresholds.
	GatewayDegraded = "degraded"
)

// Gateway is the state of a gateway.
type Gateway struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	// Status is "online", "down" or "degraded"; Detail is OPNsense's state
	// ("none", "down", "force_down", "delay", "loss", "delay+loss").
	Status string `json:"status"`
	Detail string `json:"detail"`
	// Delay, StdDev and Loss as OPNsense formats them ("1.2 ms", "0.0 %"), "" if not monitored.
	Delay  string `json:"delay,omitempty"`
	StdDev string `json:"stddev,omitempty"`
	Loss   string `json:"loss,omitempty"`
}

// Gateways reads the gateway states.
func (c *Client) Gateways(ctx context.Context) ([]Gateway, error) {
	var r struct {
		Items []struct {
			Name    string `json:"name"`
			Address string `json:"address"`
			Status  string `json:"status"`
			Delay   string `json:"delay"`
			StdDev  string `json:"stddev"`
			Loss    string `json:"loss"`
		} `json:"items"`
	}
	if err := c.Get(ctx, "/routes/gateway/status", &r); err != nil {
		return nil, err
	}
	out := make([]Gateway, 0, len(r.Items))
	for _, g := range r.Items {
		st := GatewayDegraded
		switch g.Status {
		case "none":
			st = GatewayOnline
		case "down", "force_down":
			st = GatewayDown
		}
		out = append(out, Gateway{Name: g.Name, Address: g.Address, Status: st, Detail: g.Status,
			Delay: unmonitored(g.Delay), StdDev: unmonitored(g.StdDev), Loss: unmonitored(g.Loss)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func unmonitored(s string) string {
	if s == "~" {
		return ""
	}
	return s
}

// Service is a service of the firewall.
type Service struct {
	// ID is the service name, or "name/id" for services with several instances.
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Running     bool   `json:"running"`
	// Locked services cannot be stopped (OPNsense needs them).
	Locked bool `json:"locked"`
}

// Services reads the services.
func (c *Client) Services(ctx context.Context) ([]Service, error) {
	rows, err := searchAll[struct {
		ID          flexString `json:"id"`
		Name        flexString `json:"name"`
		Description flexString `json:"description"`
		Running     flexString `json:"running"`
		Locked      flexString `json:"locked"`
	}](ctx, c, "/core/service/search")
	if err != nil {
		return nil, err
	}
	out := make([]Service, 0, len(rows))
	for _, r := range rows {
		out = append(out, Service{ID: string(r.ID), Name: string(r.Name), Description: string(r.Description),
			Running: r.Running.bool(), Locked: r.Locked.bool()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// RestartService restarts a service by its ID.
func (c *Client) RestartService(ctx context.Context, id string) error {
	return c.Post(ctx, "/core/service/restart/"+pathID(id), nil, nil)
}

// pathID escapes each segment of an ID like "name/id".
func pathID(id string) string {
	parts := strings.Split(id, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}
