// SPDX-License-Identifier: GPL-3.0-or-later

package proxmox

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/joernott/dcdash/internal/config"
)

// The configuration of virtual machines: options, disks and network devices
// (see docs/adr/0005-proxmox-vm-configuration.md).

// PendingEntry is one key of GET .../qemu/{vmid}/pending.
type PendingEntry struct {
	Key     string `json:"key"`
	Value   any    `json:"value"`
	Pending any    `json:"pending"`
	// Delete is 1 for a pending removal, 2 for a forced one.
	Delete int `json:"delete"`
}

// Pending returns the configuration of a VM with its pending changes.
func (c *Cluster) Pending(ctx context.Context, node string, vmid int) ([]PendingEntry, error) {
	p, err := guestPath(node, TypeQEMU, vmid)
	if err != nil {
		return nil, err
	}
	var out []PendingEntry
	if err := c.do(ctx, http.MethodGet, p+"/pending", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ConfigureVM changes the configuration of a VM asynchronously and returns
// the task ID.
func (c *Cluster) ConfigureVM(ctx context.Context, node string, vmid int, form url.Values) (string, error) {
	p, err := guestPath(node, TypeQEMU, vmid)
	if err != nil {
		return "", err
	}
	var upid optString
	err = c.do(ctx, http.MethodPost, p+"/config", form, &upid)
	return string(upid), err
}

// ResizeVMDisk grows a disk to size (e.g. "40G"), rejecting the change if
// the configuration no longer has digest. It returns the task ID, or "" if
// Proxmox resized synchronously.
func (c *Cluster) ResizeVMDisk(ctx context.Context, node string, vmid int, disk, size, digest string) (string, error) {
	p, err := guestPath(node, TypeQEMU, vmid)
	if err != nil {
		return "", err
	}
	form := url.Values{"disk": {disk}, "size": {size}}
	if digest != "" {
		form.Set("digest", digest)
	}
	var upid optString
	err = c.do(ctx, http.MethodPut, p+"/resize", form, &upid)
	return string(upid), err
}

// OptionSetting is a changeable option of a VM.
type OptionSetting struct {
	Key string `json:"key"`
	// Value is the active value; Set is false if the option is not set.
	Value string `json:"value"`
	Set   bool   `json:"set"`
	// Pending is a change that takes effect at the next start of the VM.
	Pending *string `json:"pending,omitempty"`
	// Delete is true if the removal of the option is pending.
	Delete bool `json:"delete,omitempty"`
}

// HardwareDisk is a disk of a VM, attached (Bus scsi, virtio, sata, ide) or
// unused (Bus "unused").
type HardwareDisk struct {
	Key     string `json:"key"`
	Bus     string `json:"bus"`
	Volume  string `json:"volume"`
	Storage string `json:"storage"`
	// Size as configured, e.g. "32G"; SizeBytes 0 if unknown.
	Size      string `json:"size"`
	SizeBytes int64  `json:"sizeBytes"`
	Discard   bool   `json:"discard"`
	SSD       bool   `json:"ssd"`
	IOThread  bool   `json:"iothread"`
	// Backup is false if the disk is excluded from backups.
	Backup bool `json:"backup"`
	// Active is false for a disk whose addition is pending.
	Active  bool    `json:"active"`
	Pending *string `json:"pending,omitempty"`
	Delete  bool    `json:"delete,omitempty"`
}

// NetworkDevice is a network device of a VM.
type NetworkDevice struct {
	Key      string  `json:"key"`
	Model    string  `json:"model"`
	MAC      string  `json:"mac"`
	Bridge   string  `json:"bridge"`
	VLAN     int     `json:"vlan,omitempty"`
	Firewall bool    `json:"firewall"`
	LinkDown bool    `json:"linkDown"`
	Active   bool    `json:"active"`
	Pending  *string `json:"pending,omitempty"`
	Delete   bool    `json:"delete,omitempty"`
}

// Hardware is the response of GET .../guests/qemu/{vmid}/hardware.
type Hardware struct {
	// Digest identifies the configuration; changes must send it back.
	Digest   string          `json:"digest"`
	Running  bool            `json:"running"`
	Options  []OptionSetting `json:"options"`
	Disks    []HardwareDisk  `json:"disks"`
	Networks []NetworkDevice `json:"networks"`
	// Configurable is true if the cluster allows changes (provisioning is
	// configured); the user also needs write on the VM.
	Configurable bool `json:"configurable"`
	// Allowed values for changes; empty unless Configurable.
	Storages []string           `json:"storages"`
	Bridges  []string           `json:"bridges"`
	VLANs    []string           `json:"vlans"`
	Limits   ProvisioningLimits `json:"limits"`
}

// Options lists the options that can be changed, in display order.
var Options = []string{
	"name", "description", "tags", "onboot", "protection", "agent", "boot",
	"cores", "sockets", "memory", "balloon", "cpu", "numa", "ostype", "hotplug",
}

// DiskBuses are the buses of VM disks with their highest index.
var DiskBuses = map[string]int{"scsi": 30, "virtio": 15, "sata": 5, "ide": 3}

// NetModels are the models of network devices.
var NetModels = []string{"virtio", "e1000", "e1000e", "vmxnet3", "rtl8139"}

const maxNetDevices = 32

var (
	diskKeyPattern   = regexp.MustCompile(`^(scsi|virtio|sata|ide)([0-9]{1,2})$`)
	unusedKeyPattern = regexp.MustCompile(`^unused([0-9]{1,3})$`)
	netKeyPattern    = regexp.MustCompile(`^net([0-9]{1,2})$`)
	macPattern       = regexp.MustCompile(`^[0-9A-F]{2}(:[0-9A-F]{2}){5}$`)
	configKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
)

// isDiskKey reports whether key is an attached disk (or CD-ROM) slot.
func isDiskKey(key string) bool {
	m := diskKeyPattern.FindStringSubmatch(key)
	if m == nil {
		return false
	}
	n, _ := strconv.Atoi(m[2])
	return n <= DiskBuses[m[1]]
}

// valueString converts a configuration value of the API to text.
func valueString(v any) (string, bool) {
	switch t := v.(type) {
	case nil:
		return "", false
	case string:
		return t, true
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), true
	case bool:
		if t {
			return "1", true
		}
		return "0", true
	default:
		return fmt.Sprint(t), true
	}
}

// prop is one "key=value" part of a property string; Key is "" for a part
// without "=".
type prop struct{ Key, Value string }

// parseProps splits a property string like "local:vm-1-disk-0,size=32G".
func parseProps(s string) []prop {
	var out []prop
	for part := range strings.SplitSeq(s, ",") {
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			out = append(out, prop{Value: part})
			continue
		}
		out = append(out, prop{Key: k, Value: v})
	}
	return out
}

// formatProps joins parts again.
func formatProps(ps []prop) string {
	parts := make([]string, 0, len(ps))
	for _, p := range ps {
		if p.Key == "" {
			parts = append(parts, p.Value)
		} else {
			parts = append(parts, p.Key+"="+p.Value)
		}
	}
	return strings.Join(parts, ",")
}

// getProp returns the value of key.
func getProp(ps []prop, key string) (string, bool) {
	for _, p := range ps {
		if p.Key == key {
			return p.Value, true
		}
	}
	return "", false
}

// setProp sets or, with value "", removes key; new keys are appended.
func setProp(ps []prop, key, value string) []prop {
	for i, p := range ps {
		if p.Key == key {
			if value == "" {
				return slices.Delete(ps, i, i+1)
			}
			ps[i].Value = value
			return ps
		}
	}
	if value == "" {
		return ps
	}
	return append(ps, prop{Key: key, Value: value})
}

func flag(ps []prop, key string) bool {
	v, _ := getProp(ps, key)
	return v == "1" || v == "on"
}

// parseDisk parses a disk value; ok is false for CD-ROM drives and
// pass-through devices, which dcdash doesn't manage.
func parseDisk(key, value string) (HardwareDisk, bool) {
	ps := parseProps(value)
	d := HardwareDisk{Key: key, Backup: true}
	if m := diskKeyPattern.FindStringSubmatch(key); m != nil {
		d.Bus = m[1]
	} else {
		d.Bus = "unused"
	}
	if len(ps) == 0 || ps[0].Key != "" {
		return d, false
	}
	d.Volume = ps[0].Value
	if media, _ := getProp(ps, "media"); media == "cdrom" || d.Volume == "none" || strings.HasPrefix(d.Volume, "/dev/") {
		return d, false
	}
	d.Storage = volumeStorage(d.Volume)
	d.Size, _ = getProp(ps, "size")
	if d.Size != "" {
		if sz, err := config.ParseSize(d.Size); err == nil {
			d.SizeBytes = int64(sz)
		}
	}
	d.Discard = flag(ps, "discard")
	d.SSD = flag(ps, "ssd")
	d.IOThread = flag(ps, "iothread")
	if v, ok := getProp(ps, "backup"); ok && v == "0" {
		d.Backup = false
	}
	return d, true
}

// parseNet parses a network device value like
// "virtio=BC:24:11:00:00:01,bridge=vmbr0,tag=10,firewall=1".
func parseNet(key, value string) NetworkDevice {
	n := NetworkDevice{Key: key}
	for _, p := range parseProps(value) {
		switch {
		case slices.Contains(NetModels, p.Key):
			n.Model, n.MAC = p.Key, strings.ToUpper(p.Value)
		case p.Key == "" && slices.Contains(NetModels, p.Value):
			n.Model = p.Value
		case p.Key == "macaddr":
			n.MAC = strings.ToUpper(p.Value)
		case p.Key == "bridge":
			n.Bridge = p.Value
		case p.Key == "tag":
			n.VLAN, _ = strconv.Atoi(p.Value)
		case p.Key == "firewall":
			n.Firewall = p.Value == "1"
		case p.Key == "link_down":
			n.LinkDown = p.Value == "1"
		}
	}
	return n
}

// netValue builds a network device value, keeping other properties of old
// (e.g. mtu, rate, queues).
func netValue(n NetworkDevice, old string) string {
	var keep []prop
	for _, p := range parseProps(old) {
		switch {
		case slices.Contains(NetModels, p.Key), p.Key == "" && slices.Contains(NetModels, p.Value),
			p.Key == "macaddr", p.Key == "bridge", p.Key == "tag", p.Key == "firewall", p.Key == "link_down":
		default:
			keep = append(keep, p)
		}
	}
	ps := []prop{{Key: n.Model, Value: n.MAC}, {Key: "bridge", Value: n.Bridge}}
	if n.VLAN != 0 {
		ps = append(ps, prop{Key: "tag", Value: strconv.Itoa(n.VLAN)})
	}
	if n.Firewall {
		ps = append(ps, prop{Key: "firewall", Value: "1"})
	}
	if n.LinkDown {
		ps = append(ps, prop{Key: "link_down", Value: "1"})
	}
	return formatProps(append(ps, keep...))
}

// newMAC returns a random unicast MAC address with the prefix Proxmox uses.
func newMAC() (string, error) {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("BC:24:11:%02X:%02X:%02X", b[0], b[1], b[2]), nil
}

// buildHardware turns the pending list into the hardware view.
func buildHardware(entries []PendingEntry) Hardware {
	hw := Hardware{Options: []OptionSetting{}, Disks: []HardwareDisk{}, Networks: []NetworkDevice{}}
	opts := map[string]*OptionSetting{}
	for _, e := range entries {
		if slices.Contains(hiddenConfigKeys, e.Key) {
			if e.Key == "digest" {
				hw.Digest, _ = valueString(e.Value)
			}
			continue
		}
		value, active := valueString(e.Value)
		pv, hasPending := valueString(e.Pending)
		var pending *string
		if hasPending {
			pending = &pv
		}
		del := e.Delete != 0
		switch {
		case slices.Contains(Options, e.Key):
			opts[e.Key] = &OptionSetting{Key: e.Key, Value: value, Set: active, Pending: pending, Delete: del}
		case isDiskKey(e.Key) || unusedKeyPattern.MatchString(e.Key):
			src := value
			if !active {
				src = pv
			}
			if d, ok := parseDisk(e.Key, src); ok {
				d.Active, d.Pending, d.Delete = active, pending, del
				hw.Disks = append(hw.Disks, d)
			}
		case netKeyPattern.MatchString(e.Key):
			src := value
			if !active {
				src = pv
			}
			n := parseNet(e.Key, src)
			n.Active, n.Pending, n.Delete = active, pending, del
			hw.Networks = append(hw.Networks, n)
		}
	}
	for _, k := range Options {
		if o, ok := opts[k]; ok {
			hw.Options = append(hw.Options, *o)
		} else {
			hw.Options = append(hw.Options, OptionSetting{Key: k})
		}
	}
	slices.SortFunc(hw.Disks, func(a, b HardwareDisk) int { return compareKeys(a.Key, b.Key) })
	slices.SortFunc(hw.Networks, func(a, b NetworkDevice) int { return compareKeys(a.Key, b.Key) })
	return hw
}

// compareKeys sorts "scsi2" before "scsi10" and unused disks last.
func compareKeys(a, b string) int {
	split := func(k string) (string, int) {
		i := strings.IndexFunc(k, func(r rune) bool { return r >= '0' && r <= '9' })
		if i < 0 {
			return k, 0
		}
		n, _ := strconv.Atoi(k[i:])
		return k[:i], n
	}
	pa, na := split(a)
	pb, nb := split(b)
	if (pa == "unused") != (pb == "unused") {
		if pa == "unused" {
			return 1
		}
		return -1
	}
	if c := strings.Compare(pa, pb); c != 0 {
		return c
	}
	return na - nb
}

// usedKeys returns the keys that are set or pending.
func usedKeys(entries []PendingEntry) map[string]bool {
	used := map[string]bool{}
	for _, e := range entries {
		if e.Value != nil || e.Pending != nil {
			used[e.Key] = true
		}
	}
	return used
}

// freeKey returns the first unused key prefix0..prefix<max>.
func freeKey(used map[string]bool, prefix string, maxIndex int) (string, bool) {
	for i := 0; i <= maxIndex; i++ {
		if k := prefix + strconv.Itoa(i); !used[k] {
			return k, true
		}
	}
	return "", false
}

// entry returns the pending entry of key.
func entry(entries []PendingEntry, key string) (PendingEntry, bool) {
	for _, e := range entries {
		if e.Key == key {
			return e, true
		}
	}
	return PendingEntry{}, false
}

// effective returns the value of key after pending changes.
func effective(entries []PendingEntry, key string) (string, bool) {
	e, ok := entry(entries, key)
	if !ok || e.Delete != 0 {
		return "", false
	}
	if v, ok := valueString(e.Pending); ok {
		return v, true
	}
	return valueString(e.Value)
}

var (
	ostypes        = []string{"l24", "l26", "win11", "win10", "win8", "win7", "w2k8", "w2k3", "w2k", "wvista", "wxp", "solaris", "other"}
	agentPattern   = regexp.MustCompile(`^([01]|enabled=[01](,(fstrim_cloned_disks=[01]|type=(virtio|isa)))*)$`)
	bootPattern    = regexp.MustCompile(`^order=(scsi|virtio|sata|ide|net)[0-9]{1,2}(;(scsi|virtio|sata|ide|net)[0-9]{1,2})*$`)
	cpuPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	hotplugPattern = regexp.MustCompile(`^([01]|(network|disk|cpu|memory|usb|cloudinit)(,(network|disk|cpu|memory|usb|cloudinit))*)$`)
)

// validateOption checks a new value of an option.
func validateOption(key, value string, p config.ProxmoxProvisioning) error {
	intIn := func(lo, hi int64) error {
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n < lo || n > hi {
			return badRequest("%s: between %d and %d", key, lo, hi)
		}
		return nil
	}
	switch key {
	case "name":
		if len(value) > 253 || !dnsNamePattern.MatchString(value) {
			return badRequest("name: a host name (letters, digits, '-' and '.')")
		}
	case "description":
		if len(value) > 8192 || strings.ContainsFunc(value, func(r rune) bool { return (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f }) {
			return badRequest("description: at most 8192 characters")
		}
	case "tags":
		for t := range strings.SplitSeq(value, ";") {
			if !tagPattern.MatchString(t) {
				return badRequest("tags: %q is not a valid tag (lower-case letters, digits, '_', '-', '+' and '.')", t)
			}
		}
	case "onboot", "protection", "numa":
		if value != "0" && value != "1" {
			return badRequest("%s: 0 or 1", key)
		}
	case "agent":
		if !agentPattern.MatchString(value) {
			return badRequest("agent: 0, 1 or enabled=1[,fstrim_cloned_disks=1][,type=virtio]")
		}
	case "boot":
		if !bootPattern.MatchString(value) {
			return badRequest("boot: order=<device>;<device>, e.g. order=scsi0;net0")
		}
	case "cores":
		return intIn(1, int64(p.Limits.Cores))
	case "sockets":
		return intIn(1, 4)
	case "memory":
		return intIn(16, p.Limits.Memory.MiB())
	case "balloon":
		return intIn(0, p.Limits.Memory.MiB())
	case "cpu":
		if !cpuPattern.MatchString(value) {
			return badRequest("cpu: a CPU type, e.g. host or x86-64-v2-AES")
		}
	case "ostype":
		if !slices.Contains(ostypes, value) {
			return badRequest("ostype: one of %s", strings.Join(ostypes, ", "))
		}
	case "hotplug":
		if !hotplugPattern.MatchString(value) {
			return badRequest("hotplug: 0, 1 or a list of network, disk, cpu, memory, usb, cloudinit")
		}
	default:
		return badRequest("%s: can't be changed", key)
	}
	return nil
}
