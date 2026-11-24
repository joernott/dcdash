// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"testing"

	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/status"
)

func TestWithLinks(t *testing.T) {
	cfg := &config.Config{}
	cfg.Auth.LDAP.Links = config.Links{RDP: "dc1.example.com"}
	cfg.Proxmox.Clusters = []config.ProxmoxCluster{{Name: "pve", Links: config.Links{UI: "https://pve.example.com:8006", SSH: "root@pve.example.com"}}}
	cfg.Switches = config.Switches{{Name: "sw1"}}
	cfg.DNS.Providers = []config.DNSProvider{{Name: "pve", Type: config.DNSTypeBind}}
	cfg.Directories = config.Directories{{Name: "lab", Links: config.Links{RDP: "dc1.lab.example.com"}}}
	checks := withLinks(cfg, []status.Check{
		{Name: "database", Kind: "system"}, {Name: "directory", Kind: "system"},
		{Name: "pve", Kind: "proxmox"}, {Name: "sw1", Kind: "switch"}, {Name: "pve", Kind: "dns"},
		{Name: "lab", Kind: "directory"},
	})
	if l := checks[5].Links; l == nil || l.RDP != "dc1.lab.example.com" {
		t.Errorf("managed directory: %+v", l)
	}
	if checks[0].Links != nil || checks[3].Links != nil || checks[4].Links != nil {
		t.Errorf("links without configuration: %+v", checks)
	}
	if checks[4].Type != "bind" || checks[2].Type != "" {
		t.Errorf("types: %q %q", checks[4].Type, checks[2].Type)
	}
	if l := checks[1].Links; l == nil || l.RDP != "dc1.example.com" || l.UI != "" {
		t.Errorf("directory: %+v", l)
	}
	if l := checks[2].Links; l == nil || l.UI != "https://pve.example.com:8006" || l.SSH != "ssh://root@pve.example.com" {
		t.Errorf("proxmox: %+v", l)
	}
}
