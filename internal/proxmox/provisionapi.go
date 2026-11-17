// SPDX-License-Identifier: GPL-3.0-or-later

package proxmox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/auth"
	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/httpx"
	"github.com/joernott/dcdash/internal/rbac"
)

// Creating and deleting guests (tasks 10.2 to 10.7, ADR 0004).

// ProvisioningLimits are the maximum resources of a new guest.
type ProvisioningLimits struct {
	Cores  int   `json:"cores"`
	Memory int64 `json:"memory"` // MiB
	Disk   int64 `json:"disk"`   // GiB
}

// ProvisioningDefaults are the start values of the creation wizard.
type ProvisioningDefaults struct {
	Cores   int    `json:"cores"`
	Memory  int64  `json:"memory"` // MiB
	Disk    int64  `json:"disk"`   // GiB
	Storage string `json:"storage"`
	Bridge  string `json:"bridge"`
}

// StorageOption is a storage for disks on the selected node.
type StorageOption struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Shared bool   `json:"shared"`
	Avail  int64  `json:"avail"`
	Total  int64  `json:"total"`
	// VM and Container: the storage holds VM disks (images) or container root
	// file systems (rootdir).
	VM        bool `json:"vm"`
	Container bool `json:"container"`
}

// TemplateOption is a VM template that may be cloned.
type TemplateOption struct {
	VMID int    `json:"vmid"`
	Name string `json:"name"`
	Node string `json:"node"`
}

// ProvisioningOptions is the response of GET .../provisioning: what the
// user may choose in the creation wizard on a node.
type ProvisioningOptions struct {
	Node               string               `json:"node"`
	Nodes              []string             `json:"nodes"`
	Storages           []StorageOption      `json:"storages"`
	ISOs               []Volume             `json:"isos"`
	ContainerTemplates []Volume             `json:"containerTemplates"`
	Bridges            []Bridge             `json:"bridges"`
	VLANs              []string             `json:"vlans"`
	VMIDs              []string             `json:"vmids"`
	Templates          []TemplateOption     `json:"templates"`
	Pool               string               `json:"pool"`
	Limits             ProvisioningLimits   `json:"limits"`
	Defaults           ProvisioningDefaults `json:"defaults"`
}

// provisioning returns the cluster of the request if provisioning is enabled
// and the user may create or delete at least some guests.
func (s *Service) provisioning(w http.ResponseWriter, r *http.Request) (*Cluster, *rbac.Subject, config.ProxmoxProvisioning, bool) {
	c, subj, ok := s.cluster(w, r, rbac.ActionAdmin)
	if !ok {
		return nil, nil, config.ProxmoxProvisioning{}, false
	}
	p, on := c.Provisioning()
	if !on {
		httpx.WriteError(w, http.StatusConflict, "provisioning_disabled", "creating and deleting guests is not enabled for this cluster")
		return nil, nil, p, false
	}
	return c, subj, p, true
}

func (s *Service) handleProvisioningOptions(w http.ResponseWriter, r *http.Request) {
	c, subj, p, ok := s.provisioning(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	rs, err := c.Resources(ctx)
	if err != nil {
		upstreamError(w, r, err)
		return
	}
	out := ProvisioningOptions{
		Nodes: []string{}, Storages: []StorageOption{}, ISOs: []Volume{}, ContainerTemplates: []Volume{},
		Bridges: []Bridge{}, VLANs: p.VLANs.Strings(), VMIDs: p.VMIDs.Strings(), Templates: []TemplateOption{}, Pool: p.Pool,
		Limits: ProvisioningLimits{Cores: p.Limits.Cores, Memory: p.Limits.Memory.MiB(), Disk: p.Limits.Disk.GiB()},
		Defaults: ProvisioningDefaults{Cores: p.Defaults.Cores, Memory: p.Defaults.Memory.MiB(), Disk: p.Defaults.Disk.GiB(),
			Storage: p.Defaults.Storage, Bridge: p.Defaults.Bridge},
	}
	read := func(res string) bool { return subj.Can(rbac.P(Module, c.Name(), res, rbac.ActionRead)) }
	for _, x := range rs {
		switch {
		case x.Type == "node" && x.Status == "online" && read(nodeResource(x.Node)):
			out.Nodes = append(out.Nodes, x.Node)
		case x.Type == TypeQEMU && x.Template != 0 && (len(p.Templates) == 0 || p.Templates.Contains(x.VMID)) &&
			read(guestResource(TypeQEMU, x.VMID)):
			out.Templates = append(out.Templates, TemplateOption{VMID: x.VMID, Name: x.Name, Node: x.Node})
		}
	}
	slices.Sort(out.Nodes)
	slices.SortFunc(out.Templates, func(a, b TemplateOption) int { return a.VMID - b.VMID })
	out.Node = r.URL.Query().Get("node")
	if out.Node == "" && len(out.Nodes) > 0 {
		out.Node = out.Nodes[0]
	}
	if out.Node == "" {
		httpx.WriteJSON(w, http.StatusOK, out)
		return
	}
	if !slices.Contains(out.Nodes, out.Node) {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "unknown or offline node")
		return
	}
	if err := s.nodeOptions(ctx, c, p, &out); err != nil {
		upstreamError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// nodeOptions adds the storages, images and bridges of out.Node.
func (s *Service) nodeOptions(ctx context.Context, c *Cluster, p config.ProxmoxProvisioning, out *ProvisioningOptions) error {
	all, err := c.NodeStorages(ctx, out.Node, "")
	if err != nil {
		return err
	}
	for _, st := range all {
		if st.Active == 0 {
			continue
		}
		content := strings.Split(st.Content, ",")
		if slices.Contains(p.Storages, st.Storage) && (slices.Contains(content, "images") || slices.Contains(content, "rootdir")) {
			out.Storages = append(out.Storages, StorageOption{Name: st.Storage, Type: st.Type, Shared: st.Shared != 0,
				Avail: st.Avail, Total: st.Total, VM: slices.Contains(content, "images"), Container: slices.Contains(content, "rootdir")})
		}
		if !slices.Contains(p.ImageStorages, st.Storage) {
			continue
		}
		for kind, dst := range map[string]*[]Volume{"iso": &out.ISOs, "vztmpl": &out.ContainerTemplates} {
			if !slices.Contains(content, kind) {
				continue
			}
			vols, err := c.StorageContent(ctx, out.Node, st.Storage, kind)
			if err != nil {
				return err
			}
			*dst = append(*dst, vols...)
		}
	}
	for _, v := range [][]Volume{out.ISOs, out.ContainerTemplates} {
		slices.SortFunc(v, func(a, b Volume) int { return strings.Compare(a.VolID, b.VolID) })
	}
	bridges, err := c.Bridges(ctx, out.Node)
	if err != nil {
		return err
	}
	for _, b := range bridges {
		if slices.Contains(p.Bridges, b.Name) {
			out.Bridges = append(out.Bridges, b)
		}
	}
	return nil
}

// Sources of a new VM.
const (
	SourceTemplate = "template"
	SourceISO      = "iso"
)

// OS types of a VM installed from an ISO image.
var osTypes = map[string]string{"linux": "l26", "windows": "win11", "other": "other"}

// CreateNetwork is the network device of a new guest. The IP settings apply
// to containers and, with cloud-init, to clones of templates.
type CreateNetwork struct {
	Bridge string `json:"bridge"`
	VLAN   int    `json:"vlan,omitempty"`
	// IPv4 is "", "dhcp" or an address with prefix length, e.g. 10.0.0.5/24.
	IPv4     string `json:"ipv4,omitempty"`
	Gateway4 string `json:"gateway4,omitempty"`
	// IPv6 is "", "auto" (SLAAC), "dhcp" or an address with prefix length.
	IPv6     string `json:"ipv6,omitempty"`
	Gateway6 string `json:"gateway6,omitempty"`
	// Nameservers are IP addresses, SearchDomain a domain.
	Nameservers  []string `json:"nameservers,omitempty"`
	SearchDomain string   `json:"searchDomain,omitempty"`
}

// CreateRequest is the body of POST .../guests.
type CreateRequest struct {
	Type string `json:"type"` // qemu or lxc
	// Node is ignored for clones; they are created on the template's node.
	Node string `json:"node"`
	// VMID 0 takes the next free ID.
	VMID int    `json:"vmid,omitempty"`
	Name string `json:"name"`
	// VMs: Source is "template" (Template, Full) or "iso" (ISO, OSType).
	Source   string `json:"source,omitempty"`
	Template int    `json:"template,omitempty"`
	Full     bool   `json:"full,omitempty"`
	ISO      string `json:"iso,omitempty"`
	OSType   string `json:"osType,omitempty"`
	// Containers: OSTemplate is the volume of the container template.
	OSTemplate string `json:"osTemplate,omitempty"`
	Cores      int    `json:"cores"`
	Memory     int64  `json:"memory"` // MiB
	// Disk in GiB; for clones 0 keeps the template's size.
	Disk int64 `json:"disk"`
	// Storage of the disk; ignored for linked clones.
	Storage string        `json:"storage,omitempty"`
	Network CreateNetwork `json:"network"`
	// User (cloud-init only), Password and SSHKeys (one per line) are the
	// login of a cloned VM (cloud-init) or of root in a container.
	User        string   `json:"user,omitempty"`
	Password    string   `json:"password,omitempty"`
	SSHKeys     string   `json:"sshKeys,omitempty"`
	Start       bool     `json:"start,omitempty"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

// requestError is a validation error of a create request.
type requestError struct {
	status  int
	code    string
	message string
}

func (e *requestError) Error() string { return e.message }

func badRequest(format string, args ...any) error {
	return &requestError{http.StatusBadRequest, "invalid_request", fmt.Sprintf(format, args...)}
}

var (
	dnsNamePattern  = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)
	unixUserPattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	tagPattern      = regexp.MustCompile(`^[a-z0-9_][a-z0-9_+.-]{0,63}$`)
	sshKeyPattern   = regexp.MustCompile(`^(ssh-|ecdsa-|sk-)[a-z0-9@.-]+ [A-Za-z0-9+/=]+( [^\x00-\x1f\x7f]*)?$`)
)

func hasControl(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

// validateCommon checks the fields shared by all kinds of guests.
func validateCommon(req *CreateRequest, p config.ProxmoxProvisioning) error {
	req.Name = strings.TrimSpace(req.Name)
	if len(req.Name) > 253 || !dnsNamePattern.MatchString(req.Name) {
		return badRequest("name: a host name (letters, digits, '-' and '.') is required")
	}
	if req.Cores < 1 || req.Cores > p.Limits.Cores {
		return badRequest("cores: between 1 and %d", p.Limits.Cores)
	}
	if req.Memory < 16 || req.Memory > p.Limits.Memory.MiB() {
		return badRequest("memory: between 16 and %d MiB", p.Limits.Memory.MiB())
	}
	if req.Disk < 0 || req.Disk > p.Limits.Disk.GiB() {
		return badRequest("disk: at most %d GiB", p.Limits.Disk.GiB())
	}
	n := &req.Network
	if !slices.Contains(p.Bridges, n.Bridge) {
		return badRequest("network.bridge %q: not allowed", n.Bridge)
	}
	if n.VLAN != 0 && !p.VLANs.Contains(n.VLAN) {
		return badRequest("network.vlan %d: not allowed", n.VLAN)
	}
	if err := validateIP(n); err != nil {
		return err
	}
	if len(req.Description) > 8192 {
		return badRequest("description: at most 8192 characters")
	}
	for _, t := range req.Tags {
		if !tagPattern.MatchString(t) {
			return badRequest("tags: %q is not a valid tag (lower-case letters, digits, '_', '-', '+' and '.')", t)
		}
	}
	if req.Password != "" && (len(req.Password) < 5 || len(req.Password) > 128 || hasControl(req.Password)) {
		return badRequest("password: 5 to 128 characters")
	}
	if len(req.SSHKeys) > 16384 {
		return badRequest("sshKeys: too long")
	}
	for l := range strings.SplitSeq(req.SSHKeys, "\n") {
		if l = strings.TrimSpace(l); l != "" && !sshKeyPattern.MatchString(l) {
			return badRequest("sshKeys: one public key per line in OpenSSH format")
		}
	}
	return nil
}

func validateIP(n *CreateNetwork) error {
	if n.IPv4 != "" && n.IPv4 != "dhcp" {
		if pf, err := netip.ParsePrefix(n.IPv4); err != nil || !pf.Addr().Is4() {
			return badRequest("network.ipv4: dhcp or an address with prefix length, e.g. 192.0.2.10/24")
		}
	}
	if n.Gateway4 != "" {
		if a, err := netip.ParseAddr(n.Gateway4); err != nil || !a.Is4() || n.IPv4 == "" || n.IPv4 == "dhcp" {
			return badRequest("network.gateway4: an IPv4 address, with a static ipv4 only")
		}
	}
	if n.IPv6 != "" && n.IPv6 != "dhcp" && n.IPv6 != "auto" {
		if pf, err := netip.ParsePrefix(n.IPv6); err != nil || !pf.Addr().Is6() || pf.Addr().Is4In6() {
			return badRequest("network.ipv6: auto, dhcp or an address with prefix length, e.g. 2001:db8::10/64")
		}
	}
	if n.Gateway6 != "" {
		if a, err := netip.ParseAddr(n.Gateway6); err != nil || !a.Is6() || !strings.Contains(n.IPv6, "/") {
			return badRequest("network.gateway6: an IPv6 address, with a static ipv6 only")
		}
	}
	if len(n.Nameservers) > 3 {
		return badRequest("network.nameservers: at most 3")
	}
	for _, ns := range n.Nameservers {
		if _, err := netip.ParseAddr(ns); err != nil {
			return badRequest("network.nameservers: %q is not an IP address", ns)
		}
	}
	if n.SearchDomain != "" && (len(n.SearchDomain) > 253 || !dnsNamePattern.MatchString(n.SearchDomain)) {
		return badRequest("network.searchDomain: a domain name")
	}
	return nil
}

func (n CreateNetwork) hasIP() bool {
	return n.IPv4 != "" || n.IPv6 != "" || len(n.Nameservers) > 0 || n.SearchDomain != ""
}

// netDevice returns the bridge and VLAN part of a net0 value.
func (n CreateNetwork) netDevice() string {
	v := "bridge=" + n.Bridge
	if n.VLAN != 0 {
		v += ",tag=" + strconv.Itoa(n.VLAN)
	}
	return v
}

// volumeStorage returns the storage of a volume ID "storage:path".
func volumeStorage(volid string) string {
	st, _, _ := strings.Cut(volid, ":")
	return st
}

// findVolume checks that volid is a volume of the given content on an image
// storage of the node.
func findVolume(ctx context.Context, c *Cluster, p config.ProxmoxProvisioning, node, volid, content string) error {
	st := volumeStorage(volid)
	if !slices.Contains(p.ImageStorages, st) || !storageIDPattern.MatchString(st) {
		return badRequest("%s %q: not on an allowed storage", content, volid)
	}
	vols, err := c.StorageContent(ctx, node, st, content)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(vols, func(v Volume) bool { return v.VolID == volid }) {
		return badRequest("%s %q: not found on node %s", content, volid, node)
	}
	return nil
}

// templateInfo is what a clone needs to know about its template.
type templateInfo struct {
	node      string
	bootDisk  string // e.g. scsi0
	diskGiB   int64
	diskStore string
	netModel  string
	cloudInit bool
	freeSlot  string // a free IDE/SATA slot for a cloud-init drive
}

var diskKey = regexp.MustCompile(`^(scsi|virtio|sata|ide)[0-9]+$`)

func parseTemplate(node string, cfg map[string]any) templateInfo {
	t := templateInfo{node: node, netModel: "virtio"}
	str := func(k string) string { s, _ := cfg[k].(string); return s }
	isDisk := func(k string) bool {
		v := str(k)
		return diskKey.MatchString(k) && v != "" && !strings.Contains(v, "media=cdrom") && !strings.Contains(v, "cloudinit")
	}
	if order, ok := strings.CutPrefix(str("boot"), "order="); ok {
		for k := range strings.SplitSeq(order, ";") {
			if isDisk(k) {
				t.bootDisk = k
				break
			}
		}
	}
	for _, k := range []string{"scsi0", "virtio0", "sata0", "ide0"} {
		if t.bootDisk == "" && isDisk(k) {
			t.bootDisk = k
		}
	}
	if d := str(t.bootDisk); d != "" {
		t.diskStore = volumeStorage(d)
		for opt := range strings.SplitSeq(d, ",") {
			if v, ok := strings.CutPrefix(opt, "size="); ok {
				if sz, err := config.ParseSize(v); err == nil {
					t.diskGiB = sz.GiB()
				}
			}
		}
	}
	if m, _, _ := strings.Cut(str("net0"), "="); m != "" && !strings.Contains(m, ",") {
		t.netModel = m
	}
	for k, v := range cfg {
		if s, ok := v.(string); ok && diskKey.MatchString(k) && strings.Contains(s, "cloudinit") {
			t.cloudInit = true
		}
	}
	for _, k := range []string{"ide2", "ide0", "ide1", "ide3", "sata1", "sata2", "sata3", "sata4", "sata5"} {
		if t.freeSlot == "" && str(k) == "" {
			t.freeSlot = k
		}
	}
	return t
}

// sshKeysParam encodes keys for the VM option sshkeys, which Proxmox expects
// URL-encoded (the form encodes the value once more).
func sshKeysParam(keys string) string {
	return strings.ReplaceAll(url.QueryEscape(strings.TrimSpace(keys)+"\n"), "+", "%20")
}

func ipConfig(n CreateNetwork) string {
	var parts []string
	if n.IPv4 != "" {
		parts = append(parts, "ip="+n.IPv4)
		if n.Gateway4 != "" {
			parts = append(parts, "gw="+n.Gateway4)
		}
	}
	if n.IPv6 != "" {
		parts = append(parts, "ip6="+n.IPv6)
		if n.Gateway6 != "" {
			parts = append(parts, "gw6="+n.Gateway6)
		}
	}
	return strings.Join(parts, ",")
}

// commonForm sets the options shared by all guests.
func commonForm(req *CreateRequest, f url.Values) {
	f.Set("cores", strconv.Itoa(req.Cores))
	f.Set("memory", strconv.FormatInt(req.Memory, 10))
	if len(req.Tags) > 0 {
		f.Set("tags", strings.Join(req.Tags, ";"))
	}
	if req.Description != "" {
		f.Set("description", req.Description)
	}
	if len(req.Network.Nameservers) > 0 {
		f.Set("nameserver", strings.Join(req.Network.Nameservers, " "))
	}
	if req.Network.SearchDomain != "" {
		f.Set("searchdomain", req.Network.SearchDomain)
	}
}

// plan is a validated create request: the steps of its job and the details
// for the audit log.
type plan struct {
	node    string
	steps   []step
	details map[string]any
}

// planClone validates the clone of a VM template and returns its steps.
func planClone(ctx context.Context, c *Cluster, subj *rbac.Subject, p config.ProxmoxProvisioning, rs []Resource, req *CreateRequest) (*plan, error) {
	idx := slices.IndexFunc(rs, func(x Resource) bool { return x.Type == TypeQEMU && x.VMID == req.Template && x.Template != 0 })
	if idx < 0 || (len(p.Templates) > 0 && !p.Templates.Contains(req.Template)) ||
		!subj.Can(rbac.P(Module, c.Name(), guestResource(TypeQEMU, req.Template), rbac.ActionRead)) {
		return nil, badRequest("template %d: not found or not allowed", req.Template)
	}
	if req.ISO != "" || req.OSType != "" || req.OSTemplate != "" {
		return nil, badRequest("iso, osType and osTemplate don't apply to clones")
	}
	node := rs[idx].Node
	cfg, err := c.GuestConfig(ctx, node, TypeQEMU, req.Template)
	if err != nil {
		return nil, err
	}
	t := parseTemplate(node, cfg)
	if req.Full {
		if !slices.Contains(p.Storages, req.Storage) {
			return nil, badRequest("storage %q: not allowed", req.Storage)
		}
	} else {
		req.Storage = ""
	}
	if req.Disk != 0 && (t.bootDisk == "" || req.Disk < t.diskGiB) {
		return nil, badRequest("disk: at least the template's %d GiB", t.diskGiB)
	}
	cloudInit := req.User != "" || req.Password != "" || req.SSHKeys != "" || req.Network.hasIP()
	if req.User != "" && !unixUserPattern.MatchString(req.User) {
		return nil, badRequest("user: a Unix user name")
	}
	if cloudInit && !t.cloudInit && t.freeSlot == "" {
		return nil, badRequest("the template has no cloud-init drive and no free slot for one")
	}
	clone := url.Values{"newid": {strconv.Itoa(req.VMID)}, "name": {req.Name}, "full": {"0"}}
	if req.Full {
		clone.Set("full", "1")
		clone.Set("storage", req.Storage)
	}
	if p.Pool != "" {
		clone.Set("pool", p.Pool)
	}
	conf := url.Values{"net0": {t.netModel + "," + req.Network.netDevice()}}
	commonForm(req, conf)
	if cloudInit {
		if !t.cloudInit {
			st := req.Storage
			if st == "" {
				st = t.diskStore
			}
			conf.Set(t.freeSlot, st+":cloudinit")
		}
		if req.User != "" {
			conf.Set("ciuser", req.User)
		}
		if req.Password != "" {
			conf.Set("cipassword", req.Password)
		}
		if req.SSHKeys != "" {
			conf.Set("sshkeys", sshKeysParam(req.SSHKeys))
		}
		if ip := ipConfig(req.Network); ip != "" {
			conf.Set("ipconfig0", ip)
		}
	}
	vmid := req.VMID
	steps := []step{
		taskStep(c, "clone", func(ctx context.Context) (string, error) { return c.CloneVM(ctx, node, req.Template, clone) }),
		{name: "configure", run: func(ctx context.Context, _ func(string)) error {
			return c.UpdateConfig(ctx, node, TypeQEMU, vmid, conf)
		}},
	}
	if req.Disk > t.diskGiB {
		size := strconv.FormatInt(req.Disk, 10) + "G"
		steps = append(steps, taskStep(c, "resize", func(ctx context.Context) (string, error) {
			return c.ResizeDisk(ctx, node, vmid, t.bootDisk, size)
		}))
	}
	return &plan{node: node, steps: steps, details: map[string]any{
		"template": req.Template, "full": req.Full, "cloudInit": cloudInit,
	}}, nil
}

// planISO validates a new VM installed from an ISO image.
func planISO(ctx context.Context, c *Cluster, p config.ProxmoxProvisioning, req *CreateRequest) (*plan, error) {
	ostype, ok := osTypes[req.OSType]
	if !ok {
		return nil, badRequest("osType: linux, windows or other")
	}
	if req.Template != 0 || req.OSTemplate != "" || req.User != "" || req.Password != "" || req.SSHKeys != "" || req.Network.hasIP() {
		return nil, badRequest("template, user, password, SSH keys and IP settings don't apply to VMs installed from an ISO image")
	}
	if !slices.Contains(p.Storages, req.Storage) {
		return nil, badRequest("storage %q: not allowed", req.Storage)
	}
	if req.Disk < 1 {
		return nil, badRequest("disk: at least 1 GiB")
	}
	if err := findVolume(ctx, c, p, req.Node, req.ISO, "iso"); err != nil {
		return nil, err
	}
	f := url.Values{
		"vmid": {strconv.Itoa(req.VMID)}, "name": {req.Name}, "sockets": {"1"}, "cpu": {"x86-64-v2-AES"},
		"ostype": {ostype}, "scsihw": {"virtio-scsi-single"},
		"scsi0": {req.Storage + ":" + strconv.FormatInt(req.Disk, 10) + ",iothread=1"},
		"ide2":  {req.ISO + ",media=cdrom"}, "net0": {"virtio," + req.Network.netDevice()},
		"boot": {"order=scsi0;ide2;net0"}, "agent": {"1"},
	}
	if req.OSType == "windows" {
		f.Set("bios", "ovmf")
		f.Set("machine", "q35")
		f.Set("efidisk0", req.Storage+":1,efitype=4m,pre-enrolled-keys=1")
		f.Set("tpmstate0", req.Storage+":1,version=v2.0")
	}
	if p.Pool != "" {
		f.Set("pool", p.Pool)
	}
	commonForm(req, f)
	node := req.Node
	return &plan{node: node, steps: []step{
		taskStep(c, "create", func(ctx context.Context) (string, error) { return c.CreateGuest(ctx, node, TypeQEMU, f) }),
	}, details: map[string]any{"iso": req.ISO, "osType": req.OSType}}, nil
}

// planContainer validates a new container.
func planContainer(ctx context.Context, c *Cluster, p config.ProxmoxProvisioning, req *CreateRequest) (*plan, error) {
	if req.Source != "" || req.Template != 0 || req.ISO != "" || req.OSType != "" || req.User != "" {
		return nil, badRequest("source, template, iso, osType and user don't apply to containers")
	}
	if !slices.Contains(p.Storages, req.Storage) {
		return nil, badRequest("storage %q: not allowed", req.Storage)
	}
	if req.Disk < 1 {
		return nil, badRequest("disk: at least 1 GiB")
	}
	if req.Password == "" && strings.TrimSpace(req.SSHKeys) == "" {
		return nil, badRequest("password or sshKeys: required for the root login")
	}
	if err := findVolume(ctx, c, p, req.Node, req.OSTemplate, "vztmpl"); err != nil {
		return nil, err
	}
	n := req.Network
	net0 := "name=eth0," + n.netDevice()
	if ip := ipConfig(n); ip != "" {
		net0 += "," + ip
	}
	f := url.Values{
		"vmid": {strconv.Itoa(req.VMID)}, "hostname": {req.Name}, "ostemplate": {req.OSTemplate},
		"rootfs": {req.Storage + ":" + strconv.FormatInt(req.Disk, 10)}, "swap": {"512"}, "net0": {net0},
		"unprivileged": {"1"}, "features": {"nesting=1"},
	}
	if req.Password != "" {
		f.Set("password", req.Password)
	}
	if k := strings.TrimSpace(req.SSHKeys); k != "" {
		f.Set("ssh-public-keys", k+"\n")
	}
	if p.Pool != "" {
		f.Set("pool", p.Pool)
	}
	commonForm(req, f)
	node := req.Node
	return &plan{node: node, steps: []step{
		taskStep(c, "create", func(ctx context.Context) (string, error) { return c.CreateGuest(ctx, node, TypeLXC, f) }),
	}, details: map[string]any{"osTemplate": req.OSTemplate}}, nil
}

// maxIDProbes limits the /cluster/nextid checks of an allocation.
const maxIDProbes = 20

// allocateID reserves the requested or the next free guest ID the user may
// create. It writes the error response itself.
func (s *Service) allocateID(w http.ResponseWriter, r *http.Request, c *Cluster, subj *rbac.Subject, p config.ProxmoxProvisioning, rs []Resource, typ string, want int) (int, bool) {
	ctx := r.Context()
	used := map[int]bool{}
	for _, x := range rs {
		if x.VMID > 0 {
			used[x.VMID] = true
		}
	}
	free := func(id int) (bool, error) {
		if used[id] || s.jobs.isReserved(c.Name(), id) {
			return false, nil
		}
		return c.IDFree(ctx, id)
	}
	conflict := func(code, msg string) (int, bool) {
		httpx.WriteError(w, http.StatusConflict, code, msg)
		return 0, false
	}
	if want != 0 {
		if want < 100 || want > 999999999 || (len(p.VMIDs) > 0 && !p.VMIDs.Contains(want)) {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "vmid: outside the allowed range")
			return 0, false
		}
		if !auth.Check(w, r, rbac.P(Module, c.Name(), guestResource(typ, want), rbac.ActionAdmin)) {
			return 0, false
		}
		ok, err := free(want)
		if err != nil {
			upstreamError(w, r, err)
			return 0, false
		}
		if !ok || !s.jobs.reserve(c.Name(), want) {
			return conflict("vmid_taken", "the ID is already in use")
		}
		return want, true
	}
	allowed := func(id int) bool { return subj.Can(rbac.P(Module, c.Name(), guestResource(typ, id), rbac.ActionAdmin)) }
	var candidates func(yield func(int) bool)
	if len(p.VMIDs) == 0 {
		next, err := c.NextID(ctx)
		if err != nil {
			upstreamError(w, r, err)
			return 0, false
		}
		candidates = func(yield func(int) bool) {
			for id := next; id <= 999999999; id++ {
				if !yield(id) {
					return
				}
			}
		}
	} else {
		candidates = func(yield func(int) bool) {
			for _, rg := range p.VMIDs {
				for id := rg.Min; id <= rg.Max; id++ {
					if !yield(id) {
						return
					}
				}
			}
		}
	}
	probes, scanned := 0, 0
	for id := range candidates {
		if scanned++; scanned > 1_000_000 || probes >= maxIDProbes {
			break
		}
		if used[id] || !allowed(id) {
			continue
		}
		probes++
		ok, err := free(id)
		if err != nil {
			upstreamError(w, r, err)
			return 0, false
		}
		if ok && s.jobs.reserve(c.Name(), id) {
			return id, true
		}
	}
	return conflict("no_free_vmid", "no free ID that you may use")
}

func (s *Service) handleCreate(w http.ResponseWriter, r *http.Request) {
	c, subj, p, ok := s.provisioning(w, r)
	if !ok {
		return
	}
	var req CreateRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	switch {
	case req.Type == TypeQEMU && (req.Source == SourceTemplate || req.Source == SourceISO):
	case req.Type == TypeLXC:
	default:
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "type: qemu (source template or iso) or lxc")
		return
	}
	if err := validateCommon(&req, p); err != nil {
		writeRequestError(w, r, err)
		return
	}
	ctx := r.Context()
	rs, err := c.Resources(ctx)
	if err != nil {
		upstreamError(w, r, err)
		return
	}
	if req.Source != SourceTemplate {
		i := slices.IndexFunc(rs, func(x Resource) bool { return x.Type == "node" && x.Node == req.Node })
		if i < 0 || rs[i].Status != "online" || !subj.Can(rbac.P(Module, c.Name(), nodeResource(req.Node), rbac.ActionRead)) {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "node: unknown or offline")
			return
		}
	}
	vmid, ok := s.allocateID(w, r, c, subj, p, rs, req.Type, req.VMID)
	if !ok {
		return
	}
	req.VMID = vmid
	var pl *plan
	switch {
	case req.Type == TypeLXC:
		pl, err = planContainer(ctx, c, p, &req)
	case req.Source == SourceTemplate:
		pl, err = planClone(ctx, c, subj, p, rs, &req)
	default:
		pl, err = planISO(ctx, c, p, &req)
	}
	if err != nil {
		s.jobs.release(c.Name(), vmid)
		writeRequestError(w, r, err)
		return
	}
	if req.Start {
		node, typ := pl.node, req.Type
		pl.steps = append(pl.steps, taskStep(c, "start", func(ctx context.Context) (string, error) {
			return c.Action(ctx, node, typ, vmid, "start")
		}))
	}
	entry := audit.FromRequest(r, "proxmox.guest.create", c.Name()+"/"+guestResource(req.Type, vmid), "")
	maps := pl.details
	maps["node"], maps["name"], maps["cores"], maps["memory"], maps["disk"] = pl.node, req.Name, req.Cores, req.Memory, req.Disk
	maps["storage"], maps["bridge"], maps["vlan"], maps["start"] = req.Storage, req.Network.Bridge, req.Network.VLAN, req.Start
	if req.Network.hasIP() {
		maps["ipv4"], maps["ipv6"] = req.Network.IPv4, req.Network.IPv6
	}
	if req.User != "" {
		maps["user"] = req.User
	}
	maps["passwordSet"] = req.Password != ""
	maps["sshKeys"] = len(slices.DeleteFunc(strings.Split(req.SSHKeys, "\n"), func(l string) bool { return strings.TrimSpace(l) == "" }))
	entry.Details = maps
	job, err := s.jobs.start(&Job{Cluster: c.Name(), Type: req.Type, VMID: vmid, Node: pl.node, Name: req.Name, User: subj.Username},
		pl.steps, s.audit, entry)
	if err != nil {
		s.jobs.release(c.Name(), vmid)
		httpx.WriteError(w, http.StatusTooManyRequests, "too_many_jobs", err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, job)
}

func writeRequestError(w http.ResponseWriter, r *http.Request, err error) {
	var re *requestError
	if errors.As(err, &re) {
		httpx.WriteError(w, re.status, re.code, re.message)
		return
	}
	upstreamError(w, r, err)
}

func (s *Service) handleJob(w http.ResponseWriter, r *http.Request) {
	c, subj, ok := s.cluster(w, r, rbac.ActionRead)
	if !ok {
		return
	}
	j, found := s.jobs.get(r.PathValue("id"))
	if !found || j.Cluster != c.Name() ||
		(j.User != subj.Username && !subj.Can(rbac.P(Module, c.Name(), guestResource(j.Type, j.VMID), rbac.ActionRead))) {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "unknown job")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, j)
}

// DeleteRequest is the body of DELETE .../guests/{type}/{vmid}.
type DeleteRequest struct {
	// Confirm is the guest's name (or its ID if it has none), typed by the user.
	Confirm string `json:"confirm"`
	// Purge also removes the guest from backup jobs, replication and HA.
	Purge bool `json:"purge"`
}

func (s *Service) handleDelete(w http.ResponseWriter, r *http.Request) {
	if _, _, _, ok := s.provisioning(w, r); !ok {
		return
	}
	var req DeleteRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	c, res, ok := s.guest(w, r, rbac.ActionAdmin)
	if !ok {
		return
	}
	want := res.Name
	if want == "" {
		want = strconv.Itoa(res.VMID)
	}
	if req.Confirm != want {
		httpx.WriteError(w, http.StatusBadRequest, "confirmation_mismatch", "the confirmation doesn't match the guest's name")
		return
	}
	st, err := c.GuestStatus(r.Context(), res.Node, res.Type, res.VMID)
	if err != nil {
		upstreamError(w, r, err)
		return
	}
	if live := liveStatus(st); live != "stopped" {
		httpx.WriteError(w, http.StatusConflict, "guest_running", "stop the guest before deleting it")
		return
	}
	target := c.Name() + "/" + guestResource(res.Type, res.VMID)
	details := map[string]any{"node": res.Node, "name": res.Name, "purge": req.Purge}
	upid, err := c.DeleteGuest(r.Context(), res.Node, res.Type, res.VMID, req.Purge)
	if err != nil {
		details["error"] = err.Error()
		s.audit.RecordRequest(r, "proxmox.guest.delete", target, audit.ResultFailure, details)
		upstreamError(w, r, err)
		return
	}
	details["upid"] = upid
	s.audit.RecordRequest(r, "proxmox.guest.delete", target, audit.ResultSuccess, details)
	httpx.WriteJSON(w, http.StatusAccepted, TaskRef{UPID: upid, Node: res.Node})
}
