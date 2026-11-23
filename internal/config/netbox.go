// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"net/url"
	"regexp"
	"slices"
	"sort"
	"time"
)

// What happens to the NetBox VM when dcdash deletes a guest (see
// docs/adr/0006-netbox.md).
const (
	// NetBoxDeleteTagged deletes VMs with the dcdash tag and sets the others
	// to "decommissioning".
	NetBoxDeleteTagged = "tagged"
	// NetBoxDeleteAlways deletes the VM.
	NetBoxDeleteAlways = "delete"
	// NetBoxDeleteDecommission sets the VM to "decommissioning".
	NetBoxDeleteDecommission = "decommission"
)

// NetBox configures the NetBox instance that dcdash reads from and writes to
// (see docs/adr/0006-netbox.md). It is disabled without a URL.
type NetBox struct {
	// Name identifies NetBox in the UI and in permissions
	// (netbox:<name>:<resource>:<action>). Default "netbox".
	Name string `yaml:"name"`
	// URL of NetBox without /api, e.g. https://netbox.example.com.
	URL string `yaml:"url"`
	// Insecure allows an http:// URL (token in clear text). Testing only.
	Insecure bool `yaml:"insecure"`
	// TLS: ca_file or fingerprint for the server certificate.
	TLS TLSClient `yaml:"tls"`
	// Token is a NetBox API token: a v2 token "nbt_<key>.<token>" or a v1
	// token (40 hex digits).
	Token Secret `yaml:"token"`
	// Timeout of a single API request.
	Timeout Duration `yaml:"timeout"`
	// WriteBack writes changes made in dcdash to NetBox. false uses NetBox
	// only for reading.
	WriteBack bool `yaml:"write_back"`
	// Tag (slug) that dcdash puts on the objects it creates. It must exist in
	// NetBox.
	Tag string `yaml:"tag"`
	// OnDelete: "tagged" (default), "delete" or "decommission".
	OnDelete string `yaml:"on_delete"`
	// VMIDField is an optional integer custom field of virtual machines that
	// holds the Proxmox VMID. Without it, VMs are found by name.
	VMIDField string `yaml:"vmid_field"`
	// Clusters maps dcdash's Proxmox cluster names to NetBox cluster names.
	// Guests of other clusters are not written to NetBox.
	Clusters map[string]string `yaml:"clusters"`
	// VLANGroup and Site (slugs) restrict the VLAN lookup by VLAN ID.
	VLANGroup string `yaml:"vlan_group"`
	Site      string `yaml:"site"`
	// VRF (name) of the IP addresses; empty is the global table.
	VRF string `yaml:"vrf"`
	// Links: web interface, SSH and RDP of the system, shown on its overview tile.
	Links `yaml:",inline"`
}

// Enabled reports whether NetBox is configured.
func (n NetBox) Enabled() bool { return n.URL != "" }

// ClusterNames returns the mapped dcdash cluster names, sorted.
func (n NetBox) ClusterNames() []string {
	out := make([]string, 0, len(n.Clusters))
	for k := range n.Clusters {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// DefaultNetBoxTimeout is used when no timeout is set.
const DefaultNetBoxTimeout = 15 * time.Second

// RequestTimeout returns timeout with its default applied.
func (n NetBox) RequestTimeout() time.Duration {
	if n.Timeout <= 0 {
		return DefaultNetBoxTimeout
	}
	return n.Timeout.D()
}

func defaultNetBox() NetBox {
	return NetBox{Name: "netbox", WriteBack: true, Tag: "dcdash", OnDelete: NetBoxDeleteTagged}
}

var (
	netboxSlugPattern  = regexp.MustCompile(`^[-a-zA-Z0-9_]{1,100}$`)
	netboxFieldPattern = regexp.MustCompile(`^[a-z0-9_]{1,50}$`)
)

func (n NetBox) validate(proxmox Proxmox, add func(string, ...any)) {
	if !n.Enabled() {
		return
	}
	n.Links.validate("netbox.", add)
	if !instanceNamePattern.MatchString(n.Name) {
		add("netbox.name %q: lower-case letters, digits, '.', '-' and '_' (max. 64)", n.Name)
	}
	u, err := url.Parse(n.URL)
	switch {
	case err != nil || u.Host == "" || u.RawQuery != "" || u.Fragment != "":
		add("netbox.url %q: must be https://host[:port][/path]", n.URL)
	case u.Scheme == "http" && !n.Insecure:
		add("netbox.url %q: http sends the token in clear text; use https or set netbox.insecure (testing only)", n.URL)
	case u.Scheme != "https" && u.Scheme != "http":
		add("netbox.url %q: must be https://host[:port][/path]", n.URL)
	}
	n.TLS.validate("netbox.tls.", add)
	if !n.Token.IsSet() {
		add("netbox.token: required")
	}
	if n.Timeout < 0 {
		add("netbox.timeout: must not be negative")
	}
	if !netboxSlugPattern.MatchString(n.Tag) {
		add("netbox.tag %q: must be the slug of a NetBox tag", n.Tag)
	}
	if !slices.Contains([]string{NetBoxDeleteTagged, NetBoxDeleteAlways, NetBoxDeleteDecommission}, n.OnDelete) {
		add("netbox.on_delete %q: must be %q, %q or %q", n.OnDelete, NetBoxDeleteTagged, NetBoxDeleteAlways, NetBoxDeleteDecommission)
	}
	if n.VMIDField != "" && !netboxFieldPattern.MatchString(n.VMIDField) {
		add("netbox.vmid_field %q: must be the name of a NetBox custom field", n.VMIDField)
	}
	for _, s := range []struct{ key, v string }{{"vlan_group", n.VLANGroup}, {"site", n.Site}} {
		if s.v != "" && !netboxSlugPattern.MatchString(s.v) {
			add("netbox.%s %q: must be a NetBox slug", s.key, s.v)
		}
	}
	for _, k := range n.ClusterNames() {
		if !slices.ContainsFunc(proxmox.Clusters, func(c ProxmoxCluster) bool { return c.Name == k }) {
			add("netbox.clusters: %q is not a cluster in proxmox.clusters", k)
		}
		if n.Clusters[k] == "" {
			add("netbox.clusters.%s: the name of a NetBox cluster is required", k)
		}
	}
}
