// SPDX-License-Identifier: GPL-3.0-or-later

package proxmox

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/netbox"
)

// Write-back to NetBox (task 12.3, docs/adr/0006-netbox.md): after a guest
// was created, changed or deleted, its configuration is written to NetBox.

// SetNetBox connects the NetBox module; nil if not configured.
func (s *Service) SetNetBox(nb *netbox.Service) { s.netbox = nb }

var ctDiskPattern = regexp.MustCompile(`^(rootfs|mp\d+)$`)

// netboxGuest builds the NetBox state of a guest from its configuration.
func netboxGuest(cluster, typ string, vmid int, cfg map[string]any) netbox.Guest {
	g := netbox.Guest{Cluster: cluster, VMID: vmid, Name: configName(cfg, strconv.Itoa(vmid)), Container: typ == TypeLXC}
	num := func(k string, def float64) float64 {
		if v, err := strconv.ParseFloat(fmt.Sprint(cfg[k]), 64); err == nil && cfg[k] != nil {
			return v
		}
		return def
	}
	g.MemoryMiB = int64(num("memory", 512))
	if g.Container {
		g.VCPUs = num("cores", 0)
	} else {
		g.VCPUs = num("sockets", 1) * num("cores", 1)
	}
	type indexed struct {
		i int
		n netbox.GuestNetwork
	}
	var nets []indexed
	for k, v := range cfg {
		val := fmt.Sprint(v)
		switch {
		case !g.Container && diskKeyPattern.MatchString(k):
			if d, ok := parseDisk(k, val); ok {
				g.Disks = append(g.Disks, netbox.GuestDisk{Name: k, SizeMiB: d.SizeBytes >> 20})
			}
		case g.Container && ctDiskPattern.MatchString(k):
			size, _ := getProp(parseProps(val), "size")
			if sz, err := config.ParseSize(size); err == nil {
				g.Disks = append(g.Disks, netbox.GuestDisk{Name: k, SizeMiB: int64(sz) >> 20})
			}
		case netKeyPattern.MatchString(k):
			i, _ := strconv.Atoi(netKeyPattern.FindStringSubmatch(k)[1])
			nets = append(nets, indexed{i, netboxNetwork(k, val, cfg, g.Container)})
		}
	}
	slices.SortFunc(g.Disks, func(a, b netbox.GuestDisk) int { return strings.Compare(a.Name, b.Name) })
	slices.SortFunc(nets, func(a, b indexed) int { return cmp.Compare(a.i, b.i) })
	for _, n := range nets {
		g.Networks = append(g.Networks, n.n)
	}
	return g
}

// netboxNetwork returns a network device: netN of a VM with the static
// addresses of ipconfigN, or a container's device with its own name and
// addresses.
func netboxNetwork(key, value string, cfg map[string]any, container bool) netbox.GuestNetwork {
	ps := parseProps(value)
	n := netbox.GuestNetwork{Name: key}
	addrs := ps
	if container {
		if name, ok := getProp(ps, "name"); ok {
			n.Name = name
		}
		n.MAC, _ = getProp(ps, "hwaddr")
	} else {
		n.MAC = parseNet(key, value).MAC
		addrs = parseProps(fmt.Sprint(cfg["ipconfig"+strings.TrimPrefix(key, "net")]))
	}
	n.MAC = strings.ToUpper(n.MAC)
	if tag, ok := getProp(ps, "tag"); ok {
		n.VLAN, _ = strconv.Atoi(tag)
	}
	for _, k := range []string{"ip", "ip6"} {
		if a, ok := getProp(addrs, k); ok {
			if p, err := netip.ParsePrefix(a); err == nil {
				n.Addresses = append(n.Addresses, p.String())
			}
		}
	}
	return n
}

// syncNetBox writes the current configuration of a guest to NetBox. oldName
// is the name before the change. It returns nil if the cluster isn't written
// to NetBox.
func (s *Service) syncNetBox(ctx context.Context, base audit.Entry, c *Cluster, node, typ string, vmid int, oldName string) *netbox.Result {
	if !s.netbox.Applies(c.Name()) {
		return nil
	}
	cfg, err := c.GuestConfig(ctx, node, typ, vmid)
	if err != nil {
		return s.netbox.Failed(ctx, base, c.Name()+"/"+guestResource(typ, vmid), fmt.Errorf("read the configuration: %w", err))
	}
	g := netboxGuest(c.Name(), typ, vmid, cfg)
	g.OldName = oldName
	return s.netbox.SyncGuest(ctx, base, g)
}

// netboxStep is a step of a creation job: it writes the new guest to
// NetBox. Its failure doesn't fail the job.
func (s *Service) netboxStep(base audit.Entry, c *Cluster, node, typ string, vmid int) step {
	return step{name: "netbox", optional: true, run: func(ctx context.Context, _ func(string)) error {
		res := s.syncNetBox(ctx, base, c, node, typ, vmid, "")
		if res != nil && res.Status == netbox.StatusFailed {
			return errors.New(res.Error)
		}
		return nil
	}}
}

// NetBoxGuests implements netbox.GuestSource: the guests of a cluster
// (without templates) with their configuration. The configurations are read
// with limited concurrency.
func (s *Service) NetBoxGuests(ctx context.Context, cluster string) ([]netbox.Guest, error) {
	c, ok := s.clusters[cluster]
	if !ok {
		return nil, fmt.Errorf("unknown cluster %q", cluster)
	}
	rs, err := c.Resources(ctx)
	if err != nil {
		return nil, err
	}
	rs = slices.DeleteFunc(rs, func(r Resource) bool { return (r.Type != TypeQEMU && r.Type != TypeLXC) || r.Template != 0 })
	out := make([]netbox.Guest, len(rs))
	errs := make([]error, len(rs))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i, r := range rs {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			cfg, err := c.GuestConfig(ctx, r.Node, r.Type, r.VMID)
			if err != nil {
				errs[i] = fmt.Errorf("%s/%d: %w", r.Type, r.VMID, err)
				return
			}
			out[i] = netboxGuest(cluster, r.Type, r.VMID, cfg)
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return out, nil
}

// NetBoxGuest implements netbox.GuestSource.
func (s *Service) NetBoxGuest(ctx context.Context, cluster string, container bool, vmid int) (netbox.Guest, error) {
	c, ok := s.clusters[cluster]
	if !ok {
		return netbox.Guest{}, fmt.Errorf("unknown cluster %q", cluster)
	}
	typ := TypeQEMU
	if container {
		typ = TypeLXC
	}
	rs, err := c.Resources(ctx)
	if err != nil {
		return netbox.Guest{}, err
	}
	i := slices.IndexFunc(rs, func(r Resource) bool { return r.Type == typ && r.VMID == vmid })
	if i < 0 {
		return netbox.Guest{}, fmt.Errorf("%w: guest %s/%d", netbox.ErrNotFound, typ, vmid)
	}
	cfg, err := c.GuestConfig(ctx, rs[i].Node, typ, vmid)
	if err != nil {
		return netbox.Guest{}, err
	}
	return netboxGuest(cluster, typ, vmid, cfg), nil
}
