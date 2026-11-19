// SPDX-License-Identifier: GPL-3.0-or-later

package netbox

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"sync"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/status"
)

// Service is the NetBox module.
type Service struct {
	cfg    config.NetBox
	client *Client
	audit  *audit.Log
	log    zerolog.Logger

	mu  sync.Mutex
	ids map[string]int64 // resolved IDs, see lookup

	// Sources of the comparison view; nil if the module isn't configured.
	guests       GuestSource
	reservations ReservationSource
	records      RecordSource
}

// NewService creates the module.
func NewService(cfg config.NetBox, auditLog *audit.Log, log zerolog.Logger) (*Service, error) {
	c, err := NewClient(cfg)
	if err != nil {
		return nil, err
	}
	return &Service{
		cfg:    cfg,
		client: c,
		audit:  auditLog,
		log:    log.With().Str("component", "netbox").Logger(),
		ids:    map[string]int64{},
	}, nil
}

// Name returns the name of the instance (permission segment).
func (s *Service) Name() string { return s.cfg.Name }

// WriteBack reports whether changes are written to NetBox.
func (s *Service) WriteBack() bool { return s.cfg.WriteBack }

// Client returns the API client.
func (s *Service) Client() *Client { return s.client }

// lookup returns a cached ID, resolving it with f on the first call. Errors
// are not cached, so that an object created in NetBox later is found.
func (s *Service) lookup(ctx context.Context, key string, f func(context.Context) (int64, error)) (int64, error) {
	s.mu.Lock()
	id, ok := s.ids[key]
	s.mu.Unlock()
	if ok {
		return id, nil
	}
	id, err := f(ctx)
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	s.ids[key] = id
	s.mu.Unlock()
	return id, nil
}

// tagID returns the ID of the configured tag.
func (s *Service) tagID(ctx context.Context) (int64, error) {
	id, err := s.lookup(ctx, "tag", func(ctx context.Context) (int64, error) { return s.client.TagID(ctx, s.cfg.Tag) })
	if errors.Is(err, ErrNotFound) {
		return 0, fmt.Errorf("NetBox: the tag %q doesn't exist (netbox.tag)", s.cfg.Tag)
	}
	return id, err
}

// vrfFilter returns the filter for addresses and prefixes in the configured
// VRF ("null" is the global table).
func (s *Service) vrfFilter(ctx context.Context) (string, error) {
	if s.cfg.VRF == "" {
		return "null", nil
	}
	id, err := s.vrfID(ctx)
	return strconv.FormatInt(id, 10), err
}

// vrfID returns the ID of the configured VRF, 0 for the global table.
func (s *Service) vrfID(ctx context.Context) (int64, error) {
	if s.cfg.VRF == "" {
		return 0, nil
	}
	id, err := s.lookup(ctx, "vrf", func(ctx context.Context) (int64, error) { return s.client.VRFID(ctx, s.cfg.VRF) })
	if errors.Is(err, ErrNotFound) {
		return 0, fmt.Errorf("NetBox: the VRF %q doesn't exist (netbox.vrf)", s.cfg.VRF)
	}
	return id, err
}

// clusterID returns the ID of the NetBox cluster mapped to a Proxmox cluster;
// ok is false if the cluster isn't mapped.
func (s *Service) clusterID(ctx context.Context, pveCluster string) (id int64, ok bool, err error) {
	name, ok := s.cfg.Clusters[pveCluster]
	if !ok {
		return 0, false, nil
	}
	id, err = s.lookup(ctx, "cluster:"+pveCluster, func(ctx context.Context) (int64, error) {
		c, err := s.client.ClusterByName(ctx, name)
		return c.ID, err
	})
	if errors.Is(err, ErrNotFound) {
		return 0, true, fmt.Errorf("NetBox: the cluster %q doesn't exist (netbox.clusters.%s)", name, pveCluster)
	}
	return id, true, err
}

// vlanQuery returns the filter for VLANs with a VLAN ID in the configured
// VLAN group or site.
func (s *Service) vlanQuery(ctx context.Context, vid int) (url.Values, error) {
	q := url.Values{"vid": {strconv.Itoa(vid)}}
	if s.cfg.VLANGroup != "" {
		id, err := s.lookup(ctx, "vlangroup", func(ctx context.Context) (int64, error) {
			return s.client.VLANGroupID(ctx, s.cfg.VLANGroup)
		})
		if errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("NetBox: the VLAN group %q doesn't exist (netbox.vlan_group)", s.cfg.VLANGroup)
		}
		if err != nil {
			return nil, err
		}
		q.Set("group_id", strconv.FormatInt(id, 10))
	}
	if s.cfg.Site != "" {
		id, err := s.lookup(ctx, "site", func(ctx context.Context) (int64, error) { return s.client.SiteID(ctx, s.cfg.Site) })
		if errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("NetBox: the site %q doesn't exist (netbox.site)", s.cfg.Site)
		}
		if err != nil {
			return nil, err
		}
		q.Set("site_id", strconv.FormatInt(id, 10))
	}
	return q, nil
}

// StatusChecks returns the check for the overview page: NetBox is reachable,
// the token is valid, and the configured tag, VRF and clusters exist.
func (s *Service) StatusChecks() []status.Check {
	return []status.Check{{Name: s.cfg.Name, Kind: "netbox", Detail: s.check}}
}

func (s *Service) check(ctx context.Context) (status.Detail, error) {
	v, err := s.client.Version(ctx)
	if err != nil {
		return status.Detail{}, err
	}
	if s.cfg.WriteBack {
		if _, err := s.tagID(ctx); err != nil {
			return status.Detail{}, err
		}
	}
	if _, err := s.vrfID(ctx); err != nil {
		return status.Detail{}, err
	}
	for _, c := range s.cfg.ClusterNames() {
		if _, _, err := s.clusterID(ctx, c); err != nil {
			return status.Detail{}, err
		}
	}
	return status.Detail{Text: "NetBox " + v, Code: "netbox.connected", Params: map[string]string{"version": v}}, nil
}

// ProxmoxCluster returns the Proxmox cluster that is mapped to a NetBox
// cluster, or "".
func (s *Service) ProxmoxCluster(netboxCluster string) string {
	for k, v := range s.cfg.Clusters {
		if v == netboxCluster {
			return k
		}
	}
	return ""
}
