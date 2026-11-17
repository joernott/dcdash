// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"strings"
	"testing"
)

func TestParseSize(t *testing.T) {
	for in, want := range map[string]Size{"512M": 512 * MiB, "32G": 32 * GiB, "1T": 1024 * GiB, "2GiB": 2 * GiB, "64k": 64 << 10, "8 GB": 8 * GiB} {
		if got, err := ParseSize(in); err != nil || got != want {
			t.Errorf("ParseSize(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "32", "1.5G", "-1G", "1P", "99999999999T"} {
		if _, err := ParseSize(in); err == nil {
			t.Errorf("ParseSize(%q): no error", in)
		}
	}
	if s := (1536 * MiB).String(); s != "1536M" {
		t.Errorf("String: %s", s)
	}
	if (32*GiB).GiB() != 32 || (2*GiB).MiB() != 2048 {
		t.Error("GiB/MiB")
	}
}

const provisioningBase = testKeyYAML + `
server:
  tls: {mode: none}
proxmox:
  clusters:
    - name: lab
      endpoints: [{url: "https://pve1.example.com:8006"}]
      token_id: "dcdash@pve!dcdash"
      token_secret: "x"
`

func TestProvisioningConfig(t *testing.T) {
	cfg, err := Parse(strings.NewReader(provisioningBase + `
      provisioning:
        vmids: ["5000-5999", 7000]
        storages: [local-lvm, ceph]
        bridges: [vmbr0]
        vlans: ["100-199"]
        templates: [9000]
        pool: dev
        limits: {cores: 8, memory: 16G, disk: 200G}
        defaults: {disk: 20G, bridge: vmbr0}
    - name: off
      endpoints: [{url: "https://pve9.example.com:8006"}]
      token_id: "dcdash@pve!dcdash"
      token_secret: "x"
`))
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Proxmox.Clusters[0].Provisioning
	if !p.Enabled() || cfg.Proxmox.Clusters[1].Provisioning.Enabled() {
		t.Error("Enabled")
	}
	if !p.VMIDs.Contains(5000) || !p.VMIDs.Contains(7000) || p.VMIDs.Contains(6000) || !p.VLANs.Contains(150) ||
		p.VLANs.Contains(200) || !p.Templates.Contains(9000) {
		t.Errorf("ranges: %+v", p)
	}
	e := p.WithDefaults()
	if strings.Join(e.ImageStorages, ",") != "local-lvm,ceph" || e.Defaults.Storage != "local-lvm" || e.Defaults.Cores != 2 ||
		e.Defaults.Memory != 2*GiB || e.Defaults.Disk != 20*GiB || e.Limits.Memory != 16*GiB {
		t.Errorf("defaults: %+v", e)
	}
	if got := p.VMIDs.Strings(); strings.Join(got, ",") != "5000-5999,7000" {
		t.Errorf("Strings: %v", got)
	}
}

func TestProvisioningConfigErrors(t *testing.T) {
	_, err := Parse(strings.NewReader(provisioningBase + `
      provisioning:
        vmids: ["50-60"]
        storages: ["bad storage"]
        vlans: ["0-5000"]
        pool: "a b"
        limits: {cores: 4}
        defaults: {cores: 8, storage: other}
`))
	for _, want := range []string{
		`provisioning.bridges: at least one`,
		`provisioning: invalid storage "bad storage"`,
		`provisioning.vmids: IDs must be between`,
		`provisioning.vlans: VLAN tags`,
		`provisioning.pool "a b"`,
		`provisioning.defaults.cores`,
		`provisioning.defaults.storage "other"`,
	} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
	for _, bad := range []string{`vmids: ["9-1"]`, `limits: {memory: 12}`, `vlans: 5`} {
		if _, err := Parse(strings.NewReader(provisioningBase + "      provisioning:\n        storages: [a]\n        bridges: [b]\n        " + bad + "\n")); err == nil {
			t.Errorf("%s: no error", bad)
		}
	}
}
