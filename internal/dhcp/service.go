// SPDX-License-Identifier: GPL-3.0-or-later

// Package dhcp is dcdash's DHCP module: Kea 3.x servers (package kea), their
// subnets, leases and reservations, with permission checks and audit.
package dhcp

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/dhcp/kea"
	"github.com/joernott/dcdash/internal/status"
)

// Module is the permission module of DHCP. The permission instance is the
// server name, the resource the subnet prefix ("global" for global
// reservations): "dhcp:<server>:<prefix>:<action>". read shows subnets,
// leases and reservations, operate deletes leases, write changes
// reservations.
const Module = "dhcp"

// GlobalResource is the permission resource of global reservations
// (subnet ID 0).
const GlobalResource = "global"

// Cache lifetimes of a server probe and of its subnet list.
const (
	probeTTL      = 30 * time.Second
	probeErrorTTL = 5 * time.Second
)

// Backend is what the service needs of a Kea server; *kea.Client implements it.
type Backend interface {
	Name() string
	Family() string
	SearchKeys() []string
	Probe(ctx context.Context) (kea.Info, error)
	Subnets(ctx context.Context) ([]kea.SubnetRef, error)
	SubnetsFromConfig(ctx context.Context) ([]kea.SubnetRef, error)
	Subnet(ctx context.Context, id uint32) (kea.Subnet, error)
	LeaseStats(ctx context.Context) ([]kea.SubnetStats, error)
	LeasesBy(ctx context.Context, key, value string) ([]kea.Lease, error)
	Lease(ctx context.Context, ip, typ string) (kea.Lease, bool, error)
	SubnetLeases(ctx context.Context, subnetID uint32) ([]kea.Lease, error)
	DeleteLease(ctx context.Context, ip, typ string) (bool, error)
	Reservations(ctx context.Context, subnetID int64) ([]kea.Reservation, error)
	Reservation(ctx context.Context, subnetID uint32, idType, id, source string) (kea.Reservation, bool, error)
	AddReservation(ctx context.Context, res kea.Reservation) error
	UpdateReservation(ctx context.Context, res kea.Reservation) error
	DeleteReservation(ctx context.Context, subnetID uint32, idType, id string) (bool, error)
}

type server struct {
	backend Backend
	group   string

	mu       sync.Mutex
	info     kea.Info
	subnets  []kea.SubnetRef
	err      error
	probedAt time.Time
}

// Service is the DHCP API of dcdash.
type Service struct {
	servers map[string]*server
	order   []string
	groups  map[string][]string // HA group -> server names
	audit   *audit.Log
	log     zerolog.Logger
	now     func() time.Time
}

// NewService creates the service for the configured servers.
func NewService(cfgs []config.DHCPServer, auditLog *audit.Log, log zerolog.Logger) (*Service, error) {
	backends := make([]Backend, 0, len(cfgs))
	groups := map[string]string{}
	for _, c := range cfgs {
		b, err := kea.New(c, log)
		if err != nil {
			return nil, err
		}
		backends = append(backends, b)
		groups[c.Name] = c.HAGroup
	}
	return newService(backends, groups, auditLog, log), nil
}

func newService(backends []Backend, groups map[string]string, auditLog *audit.Log, log zerolog.Logger) *Service {
	s := &Service{servers: map[string]*server{}, groups: map[string][]string{}, audit: auditLog,
		log: log.With().Str("component", "dhcp").Logger(), now: time.Now}
	for _, b := range backends {
		g := groups[b.Name()]
		s.servers[b.Name()] = &server{backend: b, group: g}
		s.order = append(s.order, b.Name())
		if g != "" {
			s.groups[g] = append(s.groups[g], b.Name())
		}
	}
	return s
}

// partners returns the other servers of a server's HA group.
func (s *Service) partners(name string) []*server {
	var out []*server
	for _, n := range s.groups[s.servers[name].group] {
		if n != name {
			out = append(out, s.servers[n])
		}
	}
	return out
}

// state returns the cached probe of a server, refreshing it when it is
// older than its lifetime (or always with fresh).
func (s *Service) state(ctx context.Context, srv *server, fresh bool) (kea.Info, []kea.SubnetRef, error) {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	ttl := probeTTL
	if srv.err != nil {
		ttl = probeErrorTTL
	}
	if !fresh && !srv.probedAt.IsZero() && s.now().Sub(srv.probedAt) < ttl {
		return srv.info, srv.subnets, srv.err
	}
	info, err := srv.backend.Probe(ctx)
	var subnets []kea.SubnetRef
	if err == nil {
		if info.Capabilities.Subnets {
			subnets, err = srv.backend.Subnets(ctx)
		} else {
			subnets, err = srv.backend.SubnetsFromConfig(ctx)
		}
	}
	if errors.Is(err, context.Canceled) {
		return info, subnets, err // do not cache a canceled request
	}
	srv.info, srv.subnets, srv.err, srv.probedAt = info, subnets, err, s.now()
	return info, subnets, err
}

// StatusChecks returns a dashboard check per server.
func (s *Service) StatusChecks() []status.Check {
	out := make([]status.Check, 0, len(s.order))
	for _, n := range s.order {
		srv := s.servers[n]
		out = append(out, status.Check{Name: n, Kind: Module, Detail: func(ctx context.Context) (status.Detail, error) {
			info, _, err := s.state(ctx, srv, true)
			if err != nil {
				return status.Detail{}, err
			}
			d := status.Detail{Text: "Kea " + info.Version, Code: "dhcp.kea", Params: map[string]string{"version": info.Version}}
			var ha []string
			for _, h := range info.Status.HA {
				// Mode and state are Kea's terms, e.g. "hot-standby: hot-standby".
				ha = append(ha, fmt.Sprintf("%s: %s", h.Mode, h.Servers.Local.State))
				if !h.Servers.Remote.InTouch {
					partner := h.Servers.Remote.Name
					return d, &status.Error{Code: "dhcp.partnerOutOfTouch", Params: map[string]string{"partner": partner},
						Err: fmt.Errorf("HA partner %s not in touch", partner)}
				}
			}
			if len(ha) > 0 {
				d.Text += ", HA " + strings.Join(ha, ", ")
				d.Code, d.Params["ha"] = "dhcp.keaHA", strings.Join(ha, ", ")
			}
			return d, nil
		}})
	}
	return out
}

// resource returns the permission resource of a subnet ID.
func resource(subnets []kea.SubnetRef, id uint32) (string, bool) {
	if id == 0 {
		return GlobalResource, true
	}
	for _, sn := range subnets {
		if sn.ID == id {
			return sn.Prefix, true
		}
	}
	return "", false
}

// subnetName is how a subnet appears in audit targets.
func subnetName(subnets []kea.SubnetRef, id uint32) string {
	if r, ok := resource(subnets, id); ok {
		return r
	}
	return "subnet " + strconv.FormatUint(uint64(id), 10)
}

// target is the audit target of an object of a server.
func target(server string, parts ...string) string {
	return strings.Join(append([]string{server}, parts...), "/")
}
