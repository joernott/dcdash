// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"strings"
	"testing"
	"time"
)

const netboxProxmoxYAML = `
server:
  tls: {mode: none}
proxmox:
  clusters:
    - name: lab
      endpoints: [{url: "https://pve1.example.com:8006"}]
      token_id: dcdash@pve!dash
      token_secret: "s"
`

func TestNetBoxConfig(t *testing.T) {
	cfg, err := Parse(strings.NewReader(testKeyYAML + netboxProxmoxYAML))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NetBox.Enabled() || cfg.NetBox.Name != "netbox" || !cfg.NetBox.WriteBack || cfg.NetBox.Tag != "dcdash" ||
		cfg.NetBox.OnDelete != NetBoxDeleteTagged {
		t.Errorf("defaults: %+v", cfg.NetBox)
	}

	cfg, err = Parse(strings.NewReader(testKeyYAML + netboxProxmoxYAML + `
netbox:
  url: https://netbox.example.com
  tls: {ca_file: /etc/dcdash/netbox-ca.pem}
  token: "nbt_abc.def"
  timeout: 5s
  write_back: false
  on_delete: delete
  vmid_field: proxmox_vmid
  clusters: {lab: "Lab cluster"}
  vlan_group: dc1-vlans
  vrf: internal
`))
	if err != nil {
		t.Fatal(err)
	}
	n := cfg.NetBox
	if !n.Enabled() || n.Token.Value() != "nbt_abc.def" || n.RequestTimeout() != 5*time.Second || n.WriteBack ||
		n.OnDelete != NetBoxDeleteAlways || n.Clusters["lab"] != "Lab cluster" || n.ClusterNames()[0] != "lab" {
		t.Errorf("netbox: %+v", n)
	}
	if (NetBox{}).RequestTimeout() != DefaultNetBoxTimeout {
		t.Error("default timeout")
	}
}

func TestNetBoxConfigErrors(t *testing.T) {
	_, err := Parse(strings.NewReader(testKeyYAML + netboxProxmoxYAML + `
netbox:
  name: "Net:Box"
  url: http://netbox.example.com
  tls: {fingerprint: "zz"}
  timeout: -1s
  tag: "bad tag"
  on_delete: never
  vmid_field: "Proxmox-ID"
  clusters: {prod: "Prod", lab: ""}
  site: "a site"
`))
	if err == nil {
		t.Fatal("no error")
	}
	for _, want := range []string{
		`netbox.name "Net:Box"`, "netbox.url", "clear text", "netbox.tls.fingerprint", "netbox.token: required",
		"netbox.timeout", `netbox.tag "bad tag"`, `netbox.on_delete "never"`, "netbox.vmid_field",
		`"prod" is not a cluster`, "netbox.clusters.lab", "netbox.site",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in:\n%v", want, err)
		}
	}

	_, err = Parse(strings.NewReader(testKeyYAML + netboxProxmoxYAML + `
netbox:
  url: ftp://netbox.example.com
  token: "x"
`))
	if err == nil || !strings.Contains(err.Error(), "must be https://") {
		t.Errorf("scheme: %v", err)
	}
	if _, err = Parse(strings.NewReader(testKeyYAML + netboxProxmoxYAML + `
netbox:
  url: http://127.0.0.1:8000
  insecure: true
  token: "x"
`)); err != nil {
		t.Errorf("insecure: %v", err)
	}
}

func TestProjectsConfig(t *testing.T) {
	cfg, err := Parse(strings.NewReader(testKeyYAML + netboxProxmoxYAML))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Projects.Dir != "/etc/dcdash/projects" || cfg.Projects.ReloadInterval.D() != time.Minute {
		t.Errorf("defaults: %+v", cfg.Projects)
	}
	if _, err := Parse(strings.NewReader(testKeyYAML + netboxProxmoxYAML + "projects: {reload_interval: -1s}\n")); err == nil ||
		!strings.Contains(err.Error(), "projects.reload_interval") {
		t.Errorf("negative interval: %v", err)
	}
}
