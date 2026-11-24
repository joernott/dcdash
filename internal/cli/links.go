// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/status"
)

// withLinks attaches the configured ui, ssh and rdp links (and the provider type of DNS
// providers) to the status checks of the systems they belong to, found by kind and name.
func withLinks(cfg *config.Config, checks []status.Check) []status.Check {
	byKind := map[string]map[string]config.Links{
		"proxmox":   {},
		"dns":       {},
		"dhcp":      {},
		"netbox":    {},
		"switch":    {},
		"firewall":  {},
		"directory": {},
		"system":    {"directory": cfg.Auth.LDAP.Links},
	}
	for _, c := range cfg.Proxmox.Clusters {
		byKind["proxmox"][c.Name] = c.Links
	}
	dnsTypes := map[string]string{}
	for _, p := range cfg.DNS.Providers {
		byKind["dns"][p.Name] = p.Links
		dnsTypes[p.Name] = p.Type
	}
	for _, s := range cfg.DHCP.Servers {
		byKind["dhcp"][s.Name] = s.Links
	}
	byKind["netbox"][cfg.NetBox.Name] = cfg.NetBox.Links
	for _, s := range cfg.Switches {
		byKind["switch"][s.Name] = s.Links
	}
	for _, d := range cfg.Directories {
		byKind["directory"][d.Name] = d.Links
	}
	for _, f := range cfg.Firewalls {
		byKind["firewall"][f.Name] = f.Links
	}
	out := make([]status.Check, len(checks))
	for i, c := range checks {
		out[i] = c
		if c.Kind == "dns" {
			out[i].Type = dnsTypes[c.Name]
		}
		if l, ok := byKind[c.Kind][c.Name]; ok && (l.UI != "" || l.SSH != "" || l.RDP != "") {
			out[i].Links = &status.Links{UI: l.UI, SSH: l.SSHURL(), RDP: l.RDP}
		}
	}
	return out
}
