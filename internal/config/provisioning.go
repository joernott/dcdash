// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Size is a size in bytes, written with a binary unit in YAML: "512M", "32G",
// "1T" (also "512MiB", "32GiB").
type Size int64

// Size units.
const (
	MiB Size = 1 << 20
	GiB Size = 1 << 30
)

var sizePattern = regexp.MustCompile(`^([0-9]+)\s*([KMGT])(?:I?B)?$`)

// ParseSize parses a size like "32G".
func ParseSize(s string) (Size, error) {
	m := sizePattern.FindStringSubmatch(strings.ToUpper(strings.TrimSpace(s)))
	if m == nil {
		return 0, fmt.Errorf("invalid size %q: use a number with K, M, G or T, e.g. 32G", s)
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	shift := map[string]uint{"K": 10, "M": 20, "G": 30, "T": 40}[m[2]]
	if err != nil || n > (1<<62)>>shift {
		return 0, fmt.Errorf("invalid size %q: too large", s)
	}
	return Size(n << shift), nil
}

// UnmarshalYAML implements yaml.Unmarshaler.
func (s *Size) UnmarshalYAML(n *yaml.Node) error {
	var v string
	if err := n.Decode(&v); err != nil {
		return err
	}
	sz, err := ParseSize(v)
	if err != nil {
		return fmt.Errorf("line %d: %w", n.Line, err)
	}
	*s = sz
	return nil
}

// MarshalYAML implements yaml.Marshaler.
func (s Size) MarshalYAML() (any, error) { return s.String(), nil }

func (s Size) String() string {
	for _, u := range []struct {
		n string
		v Size
	}{{"T", 1 << 40}, {"G", GiB}, {"M", MiB}, {"K", 1 << 10}} {
		if s != 0 && s%u.v == 0 {
			return strconv.FormatInt(int64(s/u.v), 10) + u.n
		}
	}
	return strconv.FormatInt(int64(s), 10) + "B"
}

// MiB returns the size in MiB, rounded down.
func (s Size) MiB() int64 { return int64(s / MiB) }

// GiB returns the size in GiB, rounded down.
func (s Size) GiB() int64 { return int64(s / GiB) }

// IDRange is a range of numbers, both ends included.
type IDRange struct{ Min, Max int }

// IDRanges is a list of numbers and ranges, written as ["100-199", 300] in YAML.
type IDRanges []IDRange

// UnmarshalYAML implements yaml.Unmarshaler.
func (r *IDRanges) UnmarshalYAML(n *yaml.Node) error {
	var items []string
	if err := n.Decode(&items); err != nil {
		return fmt.Errorf("line %d: a list of numbers and ranges like \"100-199\" expected", n.Line)
	}
	out := IDRanges{}
	for _, it := range items {
		lo, hi, isRange := strings.Cut(strings.TrimSpace(it), "-")
		a, err1 := strconv.Atoi(strings.TrimSpace(lo))
		b, err2 := a, error(nil)
		if isRange {
			b, err2 = strconv.Atoi(strings.TrimSpace(hi))
		}
		if err1 != nil || err2 != nil || a > b {
			return fmt.Errorf("line %d: invalid range %q", n.Line, it)
		}
		out = append(out, IDRange{a, b})
	}
	*r = out
	return nil
}

// MarshalYAML implements yaml.Marshaler.
func (r IDRanges) MarshalYAML() (any, error) { return r.Strings(), nil }

// Strings returns the ranges in their textual form.
func (r IDRanges) Strings() []string {
	out := make([]string, len(r))
	for i, x := range r {
		out[i] = strconv.Itoa(x.Min)
		if x.Max != x.Min {
			out[i] += "-" + strconv.Itoa(x.Max)
		}
	}
	return out
}

// Contains reports whether n is in one of the ranges.
func (r IDRanges) Contains(n int) bool {
	return slices.ContainsFunc(r, func(x IDRange) bool { return n >= x.Min && n <= x.Max })
}

// ProxmoxProvisioning allows creating and deleting guests in a cluster and
// limits what users may create (see docs/adr/0004-proxmox-provisioning.md).
// Without storages, provisioning is off.
type ProxmoxProvisioning struct {
	// VMIDs is the range of IDs for new guests. Empty: any ID, by default the
	// next free one of Proxmox.
	VMIDs IDRanges `yaml:"vmids"`
	// Storages for VM disks and container root file systems.
	Storages []string `yaml:"storages"`
	// ImageStorages hold ISO images and container templates. Empty: the
	// storages above.
	ImageStorages []string `yaml:"image_storages"`
	// Bridges (or SDN VNets) for network devices.
	Bridges []string `yaml:"bridges"`
	// VLANs are the allowed VLAN tags. Empty: untagged only.
	VLANs IDRanges `yaml:"vlans"`
	// Templates are the VM IDs of templates that may be cloned. Empty: all.
	Templates IDRanges `yaml:"templates"`
	// Pool is the Proxmox pool new guests are added to (optional).
	Pool     string               `yaml:"pool"`
	Limits   ProvisioningLimits   `yaml:"limits"`
	Defaults ProvisioningDefaults `yaml:"defaults"`
}

// ProvisioningLimits are the maximum resources of a new guest.
type ProvisioningLimits struct {
	Cores  int  `yaml:"cores"`
	Memory Size `yaml:"memory"`
	Disk   Size `yaml:"disk"`
}

// ProvisioningDefaults are the values the creation wizard starts with.
type ProvisioningDefaults struct {
	Cores   int    `yaml:"cores"`
	Memory  Size   `yaml:"memory"`
	Disk    Size   `yaml:"disk"`
	Storage string `yaml:"storage"`
	Bridge  string `yaml:"bridge"`
}

// Default limits and wizard values.
const (
	DefaultMaxCores  = 16
	DefaultMaxMemory = 64 * GiB
	DefaultMaxDisk   = 1024 * GiB
	DefaultCores     = 2
	DefaultMemory    = 2 * GiB
	DefaultDisk      = 32 * GiB
)

// Enabled reports whether guests may be created and deleted.
func (p ProxmoxProvisioning) Enabled() bool { return len(p.Storages) > 0 }

// WithDefaults returns p with the defaults applied.
func (p ProxmoxProvisioning) WithDefaults() ProxmoxProvisioning {
	if len(p.ImageStorages) == 0 {
		p.ImageStorages = p.Storages
	}
	l, d := &p.Limits, &p.Defaults
	if l.Cores == 0 {
		l.Cores = DefaultMaxCores
	}
	if l.Memory == 0 {
		l.Memory = DefaultMaxMemory
	}
	if l.Disk == 0 {
		l.Disk = DefaultMaxDisk
	}
	if d.Cores == 0 {
		d.Cores = min(DefaultCores, l.Cores)
	}
	if d.Memory == 0 {
		d.Memory = min(DefaultMemory, l.Memory)
	}
	if d.Disk == 0 {
		d.Disk = min(DefaultDisk, l.Disk)
	}
	if d.Storage == "" && len(p.Storages) > 0 {
		d.Storage = p.Storages[0]
	}
	if d.Bridge == "" && len(p.Bridges) > 0 {
		d.Bridge = p.Bridges[0]
	}
	return p
}

var (
	pveStorageID  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]{0,99}$`)
	pveBridgeName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]{0,14}$`)
	pvePoolName   = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
)

func (p ProxmoxProvisioning) validate(pre string, add func(string, ...any)) {
	set := len(p.Storages) > 0 || len(p.ImageStorages) > 0 || len(p.Bridges) > 0 || len(p.VMIDs) > 0 ||
		len(p.VLANs) > 0 || len(p.Templates) > 0 || p.Pool != "" || p.Limits != (ProvisioningLimits{}) ||
		p.Defaults != (ProvisioningDefaults{})
	if !set {
		return
	}
	if len(p.Storages) == 0 {
		add("%s.storages: at least one storage required", pre)
	}
	if len(p.Bridges) == 0 {
		add("%s.bridges: at least one bridge required", pre)
	}
	for _, s := range slices.Concat(p.Storages, p.ImageStorages) {
		if !pveStorageID.MatchString(s) {
			add("%s: invalid storage %q", pre, s)
		}
	}
	for _, b := range p.Bridges {
		if !pveBridgeName.MatchString(b) {
			add("%s.bridges: invalid bridge %q", pre, b)
		}
	}
	for _, r := range p.VMIDs {
		if r.Min < 100 || r.Max > 999999999 {
			add("%s.vmids: IDs must be between 100 and 999999999", pre)
		}
	}
	for _, r := range p.VLANs {
		if r.Min < 1 || r.Max > 4094 {
			add("%s.vlans: VLAN tags must be between 1 and 4094", pre)
		}
	}
	if p.Pool != "" && !pvePoolName.MatchString(p.Pool) {
		add("%s.pool %q: letters, digits, '.', '-' and '_'", pre, p.Pool)
	}
	l := p.Limits
	if l.Cores < 0 || l.Cores > 512 || l.Memory < 0 || l.Disk < 0 {
		add("%s.limits: cores between 1 and 512, sizes not negative", pre)
	}
	e := p.WithDefaults()
	d := e.Defaults
	switch {
	case d.Cores < 1 || d.Cores > e.Limits.Cores:
		add("%s.defaults.cores: between 1 and limits.cores (%d)", pre, e.Limits.Cores)
	case d.Memory < 16*MiB || d.Memory > e.Limits.Memory:
		add("%s.defaults.memory: between 16M and limits.memory (%s)", pre, e.Limits.Memory)
	case d.Disk < GiB || d.Disk > e.Limits.Disk:
		add("%s.defaults.disk: between 1G and limits.disk (%s)", pre, e.Limits.Disk)
	}
	if len(p.Storages) > 0 && !slices.Contains(p.Storages, d.Storage) {
		add("%s.defaults.storage %q: not in storages", pre, d.Storage)
	}
	if len(p.Bridges) > 0 && !slices.Contains(p.Bridges, d.Bridge) {
		add("%s.defaults.bridge %q: not in bridges", pre, d.Bridge)
	}
}
