// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Links are the addresses of a system's own interfaces. The overview page shows a button for
// each one that is set, on the system's tile. They are part of every configuration entry
// that produces a tile (Proxmox clusters, DNS providers, DHCP servers, NetBox, switches,
// firewalls and the LDAP directory).
type Links struct {
	// UI is the URL of the system's web interface (http or https).
	UI string `yaml:"ui"`
	// SSH is [user@]host[:port] for an ssh:// link.
	SSH string `yaml:"ssh"`
	// RDP is host[:port] for a Remote Desktop connection file.
	RDP string `yaml:"rdp"`
}

// hostPattern matches a host name (letters, digits, '-', '.', '_').
var (
	hostPattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._-]{0,252}[A-Za-z0-9])?$`)
	userPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
)

// validHostPort checks host[:port], with IPv6 addresses in brackets when a port follows.
func validHostPort(s string) bool {
	host, port := s, ""
	if h, p, err := net.SplitHostPort(s); err == nil {
		if p == "" {
			return false
		}
		host, port = h, p
	} else if strings.Count(s, ":") == 1 {
		return false // host: without a valid port
	}
	if port != "" {
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return false
		}
	}
	if net.ParseIP(strings.Trim(host, "[]")) != nil {
		return true
	}
	return hostPattern.MatchString(host)
}

func (l Links) validate(pre string, add func(string, ...any)) {
	if l.UI != "" {
		u, err := url.Parse(l.UI)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
			add("%sui %q: must be an http:// or https:// URL", pre, l.UI)
		}
	}
	if l.SSH != "" {
		user, host, found := strings.Cut(l.SSH, "@")
		if !found {
			user, host = "", l.SSH
		}
		if (found && !userPattern.MatchString(user)) || !validHostPort(host) {
			add("%sssh %q: must be [user@]host[:port]", pre, l.SSH)
		}
	}
	if l.RDP != "" && !validHostPort(l.RDP) {
		add("%srdp %q: must be host[:port]", pre, l.RDP)
	}
}

// SSHURL returns the ssh:// URL for SSH, or "" if it is not set.
func (l Links) SSHURL() string {
	if l.SSH == "" {
		return ""
	}
	return "ssh://" + l.SSH
}
