// SPDX-License-Identifier: GPL-3.0-or-later

package aoscx

import (
	"context"
	"sort"
	"strings"
	"time"
)

// Member is a chassis of a switch: the switch itself or a member of a stack (VSF).
type Member struct {
	// Name is the subsystem name, e.g. "1".
	Name       string `json:"name"`
	Product    string `json:"product"`
	Serial     string `json:"serial"`
	PartNumber string `json:"partNumber"`
}

// Inventory describes a switch.
type Inventory struct {
	Hostname string
	// Platform is the platform name, e.g. "6000".
	Platform string
	// Firmware is the software version, e.g. "PL.10.08.1010".
	Firmware string
	// Booted is when the switch started; zero if unknown.
	Booted  time.Time
	Members []Member
}

type system struct {
	Hostname        string `json:"hostname"`
	PlatformName    string `json:"platform_name"`
	SoftwareVersion string `json:"software_version"`
	BootTime        int64  `json:"boot_time"`
}

type subsystem struct {
	ProductInfo struct {
		ProductName  string `json:"product_name"`
		SerialNumber string `json:"serial_number"`
		PartNumber   string `json:"part_number"`
	} `json:"product_info"`
}

// Inventory reads the system information and the chassis.
func (c *Client) Inventory(ctx context.Context) (Inventory, error) {
	var s system
	if err := c.Get(ctx, "/system?attributes=hostname,platform_name,software_version,boot_time", &s); err != nil {
		return Inventory{}, err
	}
	inv := Inventory{Hostname: s.Hostname, Platform: s.PlatformName, Firmware: s.SoftwareVersion}
	if s.BootTime > 0 {
		inv.Booted = time.Unix(s.BootTime, 0).UTC()
	}
	// Keys are "<type>,<name>": "chassis,1", "line_card,1/1", "management_module,1/1".
	var subs map[string]subsystem
	if err := c.Get(ctx, "/system/subsystems?depth=2&attributes=product_info", &subs); err != nil {
		return Inventory{}, err
	}
	for key, sub := range subs {
		typ, name, ok := strings.Cut(key, ",")
		if !ok || typ != "chassis" {
			continue
		}
		p := sub.ProductInfo
		inv.Members = append(inv.Members, Member{Name: name, Product: p.ProductName, Serial: p.SerialNumber, PartNumber: p.PartNumber})
	}
	sort.Slice(inv.Members, func(i, j int) bool { return inv.Members[i].Name < inv.Members[j].Name })
	return inv, nil
}
