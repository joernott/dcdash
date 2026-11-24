// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Proxmox configures the Proxmox VE clusters managed by dcdash.
type Proxmox struct {
	Clusters []ProxmoxCluster `yaml:"clusters"`
}

// ProxmoxCluster is one Proxmox VE cluster (or a single node).
type ProxmoxCluster struct {
	// Name identifies the cluster in the UI and in permissions
	// (proxmox:<name>:<resource>:<action>).
	Name string `yaml:"name"`
	// Endpoints are API URLs of cluster nodes, tried in order (failover).
	Endpoints []ProxmoxEndpoint `yaml:"endpoints"`
	// TLS applies to all endpoints, e.g. ca_file with the cluster CA
	// (/etc/pve/pve-root-ca.pem). An endpoint fingerprint overrides it.
	TLS TLSClient `yaml:"tls"`
	// TokenID is the API token, "user@realm!tokenname".
	TokenID     string `yaml:"token_id"`
	TokenSecret Secret `yaml:"token_secret"`
	// Timeout of a single API request.
	Timeout Duration `yaml:"timeout"`
	// Console is an optional Proxmox user for consoles that do not accept
	// API tokens (see docs/adr/0001-proxmox-console-authentication.md).
	Console ProxmoxConsole `yaml:"console"`
	// Provisioning allows creating and deleting guests (optional).
	Provisioning ProxmoxProvisioning `yaml:"provisioning"`
	// Links: web interface, SSH and RDP of the system, shown on its overview tile.
	Links `yaml:",inline"`
	// NodeLinks are the web interface and SSH of each node, shown on the node tiles of the
	// Proxmox page; "{node}" is replaced by the node name. Default: the endpoint whose host
	// is the node name or starts with it ("pve1" matches pve1.example.com).
	NodeLinks Links `yaml:"node_links"`
}

// ProxmoxEndpoint is the API URL of one node.
type ProxmoxEndpoint struct {
	// URL, e.g. https://pve1.example.com:8006
	URL string `yaml:"url"`
	// Fingerprint pins this node's certificate (SHA-256), e.g. for
	// self-signed certificates. Overrides tls of the cluster.
	Fingerprint string `yaml:"fingerprint"`
}

// ProxmoxConsole is a Proxmox user that opens console sessions with a ticket.
type ProxmoxConsole struct {
	// Username, "user@realm". Empty: consoles use the API token.
	Username string `yaml:"username"`
	Password Secret `yaml:"password"`
}

// TLSFor returns the TLS settings of an endpoint.
func (c ProxmoxCluster) TLSFor(e ProxmoxEndpoint) TLSClient {
	t := c.TLS
	if e.Fingerprint != "" {
		t.Fingerprint = e.Fingerprint
	}
	return t
}

// DefaultProxmoxTimeout is used when a cluster sets no timeout.
const DefaultProxmoxTimeout = 15 * time.Second

// RequestTimeout returns timeout with its default applied.
func (c ProxmoxCluster) RequestTimeout() time.Duration {
	if c.Timeout <= 0 {
		return DefaultProxmoxTimeout
	}
	return c.Timeout.D()
}

var (
	// Instance names are a permission segment: no colons, no wildcards.
	instanceNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}$`)
	pveTokenIDPattern   = regexp.MustCompile(`^[^@!:\s]+@[^@!:\s]+![A-Za-z][A-Za-z0-9._-]*$`)
	pveUserPattern      = regexp.MustCompile(`^[^@!:\s]+@[^@!:\s]+$`)
)

func (p Proxmox) validate(add func(string, ...any)) {
	seen := map[string]bool{}
	for i, c := range p.Clusters {
		pre := "proxmox.clusters[" + strconv.Itoa(i) + "]"
		validateLinks(c.Links, pre+".", add)
		validateLinks(c.NodeLinks.ForNode("node"), pre+".node_links.", add)
		switch {
		case !instanceNamePattern.MatchString(c.Name):
			add("%s.name %q: lower-case letters, digits, '.', '-' and '_' (max. 64)", pre, c.Name)
		case seen[c.Name]:
			add("%s.name %q: duplicate", pre, c.Name)
		}
		seen[c.Name] = true
		if len(c.Endpoints) == 0 {
			add("%s.endpoints: at least one endpoint required", pre)
		}
		for j, e := range c.Endpoints {
			ep := pre + ".endpoints[" + strconv.Itoa(j) + "]"
			u, err := url.Parse(e.URL)
			if err != nil || u.Scheme != "https" || u.Host == "" || (u.Path != "" && u.Path != "/") {
				add("%s.url %q: must be https://host[:port]", ep, e.URL)
			}
			c.TLSFor(e).validate(ep+".", add)
		}
		c.TLS.validate(pre+".tls.", add)
		if !pveTokenIDPattern.MatchString(c.TokenID) {
			add("%s.token_id %q: must be user@realm!tokenname", pre, c.TokenID)
		}
		if !c.TokenSecret.IsSet() {
			add("%s.token_secret: required", pre)
		}
		if c.Timeout < 0 {
			add("%s.timeout: must not be negative", pre)
		}
		if c.Console.Username != "" {
			if !pveUserPattern.MatchString(c.Console.Username) {
				add("%s.console.username %q: must be user@realm", pre, c.Console.Username)
			}
			if !c.Console.Password.IsSet() {
				add("%s.console.password: required with console.username", pre)
			}
		}
		c.Provisioning.validate(pre+".provisioning", add)
	}
}

// ForNode returns the links with "{node}" replaced by node.
func (l Links) ForNode(node string) Links {
	r := strings.NewReplacer("{node}", node)
	return Links{UI: r.Replace(l.UI), SSH: r.Replace(l.SSH), RDP: r.Replace(l.RDP)}
}

// LinksForNode returns the web interface and SSH links of a node: node_links if set,
// otherwise those of the endpoint whose host is the node name or starts with "<node>.".
func (c ProxmoxCluster) LinksForNode(node string) Links {
	if c.NodeLinks != (Links{}) {
		return c.NodeLinks.ForNode(node)
	}
	for _, e := range c.Endpoints {
		u, err := url.Parse(e.URL)
		if err != nil {
			continue
		}
		host := strings.ToLower(u.Hostname())
		n := strings.ToLower(node)
		if host == n || strings.HasPrefix(host, n+".") {
			return Links{UI: u.Scheme + "://" + u.Host, SSH: "root@" + u.Hostname()}
		}
	}
	return Links{}
}
