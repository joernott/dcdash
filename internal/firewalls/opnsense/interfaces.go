// SPDX-License-Identifier: GPL-3.0-or-later

package opnsense

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"strings"
)

// Interface is an assigned interface of the firewall.
type Interface struct {
	// Key is the identifier used in rules: lan, wan, opt1, ...
	Key         string
	Description string
	// Device is the operating system's name: igb0, vlan01, ...
	Device string
	// Prefixes are the interface addresses with their networks.
	Prefixes []netip.Prefix
}

// Interfaces reads the assigned interfaces with their addresses (privilege
// "Status: Interfaces").
func (c *Client) Interfaces(ctx context.Context) ([]Interface, error) {
	type addr struct {
		IPAddr string `json:"ipaddr"`
	}
	rows, err := searchAll[struct {
		Identifier  flexString `json:"identifier"`
		Description flexString `json:"description"`
		Device      flexString `json:"device"`
		IPv4        []addr     `json:"ipv4"`
		IPv6        []addr     `json:"ipv6"`
	}](ctx, c, "/interfaces/overview/interfaces_info")
	if err != nil {
		return nil, err
	}
	out := make([]Interface, 0, len(rows))
	for _, r := range rows {
		if r.Identifier == "" {
			continue
		}
		i := Interface{Key: string(r.Identifier), Description: string(r.Description), Device: string(r.Device)}
		for _, a := range append(r.IPv4, r.IPv6...) {
			// Link-local addresses carry a zone (fe80::1%igb0/64), which doesn't parse.
			if p, err := netip.ParsePrefix(a.IPAddr); err == nil {
				i.Prefixes = append(i.Prefixes, p)
			}
		}
		out = append(out, i)
	}
	return out, nil
}

// Route is an entry of the firewall's routing table.
type Route struct {
	// Destination is 0.0.0.0/0 or ::/0 for the default routes.
	Destination netip.Prefix
	// Device is the interface the route goes through (igb0, ...).
	Device string
}

// Routes reads the routing table (privilege "Diagnostics: Routing tables").
func (c *Client) Routes(ctx context.Context) ([]Route, error) {
	var rows []struct {
		Proto       string `json:"proto"`
		Destination string `json:"destination"`
		NetIf       string `json:"netif"`
	}
	if err := c.Get(ctx, "/diagnostics/interface/get_routes", &rows); err != nil {
		return nil, err
	}
	out := make([]Route, 0, len(rows))
	for _, r := range rows {
		dst := r.Destination
		if dst == "default" {
			dst = "0.0.0.0/0"
			if r.Proto == "ipv6" {
				dst = "::/0"
			}
		}
		p, err := netip.ParsePrefix(dst)
		if err != nil {
			a, aerr := netip.ParseAddr(dst)
			if aerr != nil {
				continue
			}
			p = netip.PrefixFrom(a, a.BitLen())
		}
		out = append(out, Route{Destination: p, Device: r.NetIf})
	}
	return out, nil
}

// ErrProxied means that dcdash reaches the firewall through an HTTP proxy, so
// the firewall doesn't see dcdash's own address.
var ErrProxied = errors.New("opnsense: the firewall is reached through a proxy")

// Path opens a new connection to the firewall and returns dcdash's address
// on it and the firewall's address it connects to.
func (c *Client) Path(ctx context.Context) (local, remote netip.Addr, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base, nil)
	if err != nil {
		return local, remote, err
	}
	if tr, ok := c.fresh.Transport.(*http.Transport); ok && tr.Proxy != nil {
		if p, err := tr.Proxy(req); err != nil || p != nil {
			return local, remote, ErrProxied
		}
	}
	trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
		local, remote = addrOf(info.Conn.LocalAddr()), addrOf(info.Conn.RemoteAddr())
	}}
	if err := c.do(httptrace.WithClientTrace(ctx, trace), c.fresh, http.MethodGet, "/core/firmware/status", nil, nil); err != nil {
		return local, remote, err
	}
	return local, remote, nil
}

func addrOf(a net.Addr) netip.Addr {
	if ap, err := netip.ParseAddrPort(a.String()); err == nil {
		return ap.Addr().Unmap()
	}
	h, _, _ := strings.Cut(a.String(), ":")
	addr, _ := netip.ParseAddr(h)
	return addr
}
