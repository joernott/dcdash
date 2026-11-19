// SPDX-License-Identifier: GPL-3.0-or-later

package opnsense

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"time"
)

// Peer is a WireGuard peer ("client" in OPNsense) with its runtime state.
type Peer struct {
	UUID       string `json:"uuid"`
	Name       string `json:"name"`
	Enabled    bool   `json:"enabled"`
	PublicKey  string `json:"publicKey"`
	Endpoint   string `json:"endpoint,omitempty"`
	AllowedIPs string `json:"allowedIps,omitempty"`
	// Interface is the WireGuard instance (wg0, ...) the peer is active on.
	Interface string `json:"interface,omitempty"`
	// LatestHandshake is zero if there was none.
	LatestHandshake time.Time `json:"latestHandshake"`
	RxBytes         int64     `json:"rxBytes"`
	TxBytes         int64     `json:"txBytes"`
	// Status is "online" (handshake in the last 5 minutes), "stale", "offline"
	// or "" for disabled peers without runtime state.
	Status string `json:"status"`
}

// Peers reads the configured peers and merges their runtime state (matched by
// public key).
func (c *Client) Peers(ctx context.Context) ([]Peer, error) {
	conf, err := searchAll[struct {
		UUID          flexString `json:"uuid"`
		Name          flexString `json:"name"`
		Enabled       flexString `json:"enabled"`
		PubKey        flexString `json:"pubkey"`
		Endpoint      flexString `json:"endpoint"`
		TunnelAddress flexString `json:"tunneladdress"`
	}](ctx, c, "/wireguard/client/search_client")
	if err != nil {
		return nil, err
	}
	state, err := searchAll[struct {
		Type      flexString `json:"type"`
		IfName    flexString `json:"ifname"`
		PublicKey flexString `json:"public-key"`
		Endpoint  flexString `json:"endpoint"`
		Handshake flexString `json:"latest-handshake"`
		Rx        flexString `json:"transfer-rx"`
		Tx        flexString `json:"transfer-tx"`
		Status    flexString `json:"peer-status"`
	}](ctx, c, "/wireguard/service/show")
	if err != nil {
		return nil, err
	}
	out := make([]Peer, 0, len(conf))
	for _, p := range conf {
		peer := Peer{UUID: string(p.UUID), Name: string(p.Name), Enabled: p.Enabled.bool(), PublicKey: string(p.PubKey),
			Endpoint: string(p.Endpoint), AllowedIPs: string(p.TunnelAddress)}
		for _, s := range state {
			if s.Type != "peer" || string(s.PublicKey) != peer.PublicKey {
				continue
			}
			peer.Interface, peer.Status = string(s.IfName), string(s.Status)
			peer.RxBytes, peer.TxBytes = s.Rx.int64(), s.Tx.int64()
			if s.Endpoint != "" && s.Endpoint != "(none)" {
				peer.Endpoint = string(s.Endpoint)
			}
			if hs := s.Handshake.int64(); hs > 0 {
				peer.LatestHandshake = time.Unix(hs, 0).UTC()
			}
		}
		out = append(out, peer)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// SetPeerEnabled enables or disables a peer and applies the WireGuard
// configuration. toggle_client has no explicit state, so the current state
// is read first.
func (c *Client) SetPeerEnabled(ctx context.Context, uuid string, enabled bool) (changed bool, err error) {
	var cur struct {
		Client struct {
			Enabled flexString `json:"enabled"`
		} `json:"client"`
	}
	if err := c.Get(ctx, "/wireguard/client/get_client/"+url.PathEscape(uuid), &cur); err != nil {
		return false, err
	}
	if cur.Client.Enabled.bool() == enabled {
		return false, nil
	}
	var res struct {
		Result string `json:"result"`
	}
	if err := c.Post(ctx, "/wireguard/client/toggle_client/"+url.PathEscape(uuid), nil, &res); err != nil {
		return false, err
	}
	if (res.Result == "Enabled") != enabled {
		return false, fmt.Errorf("opnsense: toggle peer %s: unexpected result %q", uuid, res.Result)
	}
	return true, c.Post(ctx, "/wireguard/service/reconfigure", nil, nil)
}

// Tunnel is an IPsec connection (phase 1) with its child SAs (phase 2).
type Tunnel struct {
	// Name is the connection ID of swanctl, used to connect and disconnect.
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Connected   bool      `json:"connected"`
	LocalAddrs  string    `json:"localAddrs,omitempty"`
	RemoteAddrs string    `json:"remoteAddrs,omitempty"`
	BytesIn     int64     `json:"bytesIn"`
	BytesOut    int64     `json:"bytesOut"`
	Children    []ChildSA `json:"children"`
}

// ChildSA is a phase 2 entry.
type ChildSA struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	State       string `json:"state,omitempty"`
	LocalTS     string `json:"localTs,omitempty"`
	RemoteTS    string `json:"remoteTs,omitempty"`
	BytesIn     int64  `json:"bytesIn"`
	BytesOut    int64  `json:"bytesOut"`
}

// Tunnels reads the IPsec connections with their phase 2 entries.
func (c *Client) Tunnels(ctx context.Context) ([]Tunnel, error) {
	p1, err := searchAll[struct {
		Name      flexString `json:"name"`
		Desc      flexString `json:"phase1desc"`
		Connected flexString `json:"connected"`
		Local     flexString `json:"local-addrs"`
		Remote    flexString `json:"remote-addrs"`
		BytesIn   flexString `json:"bytes-in"`
		BytesOut  flexString `json:"bytes-out"`
	}](ctx, c, "/ipsec/sessions/search_phase1")
	if err != nil {
		return nil, err
	}
	out := make([]Tunnel, 0, len(p1))
	for _, p := range p1 {
		t := Tunnel{Name: string(p.Name), Description: string(p.Desc), Connected: p.Connected.bool(),
			LocalAddrs: string(p.Local), RemoteAddrs: string(p.Remote), BytesIn: p.BytesIn.int64(), BytesOut: p.BytesOut.int64(),
			Children: []ChildSA{}}
		var p2 searchResult[struct {
			Name     flexString `json:"name"`
			Desc     flexString `json:"phase2desc"`
			State    flexString `json:"state"`
			LocalTS  flexString `json:"local-ts"`
			RemoteTS flexString `json:"remote-ts"`
			BytesIn  flexString `json:"bytes-in"`
			BytesOut flexString `json:"bytes-out"`
		}]
		if err := c.Post(ctx, "/ipsec/sessions/search_phase2", map[string]any{"id": t.Name, "current": 1, "rowCount": -1}, &p2); err != nil {
			return nil, err
		}
		for _, ch := range p2.Rows {
			t.Children = append(t.Children, ChildSA{Name: string(ch.Name), Description: string(ch.Desc), State: string(ch.State),
				LocalTS: string(ch.LocalTS), RemoteTS: string(ch.RemoteTS), BytesIn: ch.BytesIn.int64(), BytesOut: ch.BytesOut.int64()})
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// RestartTunnel disconnects and connects an IPsec connection. Connecting is
// asynchronous: the state follows within seconds.
func (c *Client) RestartTunnel(ctx context.Context, name string) error {
	if err := c.Post(ctx, "/ipsec/sessions/disconnect/"+url.PathEscape(name), nil, nil); err != nil {
		return err
	}
	return c.Post(ctx, "/ipsec/sessions/connect/"+url.PathEscape(name), nil, nil)
}
