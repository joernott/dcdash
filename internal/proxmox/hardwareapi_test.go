// SPDX-License-Identifier: GPL-3.0-or-later

package proxmox

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
)

func init() {
	provUsers["configurer"] = []string{"proxmox:lab:qemu/5*:write"}
	provUsers["remover"] = []string{"proxmox:lab:qemu/5*:admin"}
}

const hw5100 = "/lab/guests/qemu/5100/hardware"

func (e *provEnv) hardware(t *testing.T) Hardware {
	t.Helper()
	var hw Hardware
	if st, msg := e.do(t, "viewer", http.MethodGet, hw5100, nil, &hw); st != http.StatusOK {
		t.Fatalf("hardware: %d %s", st, msg)
	}
	return hw
}

func option(hw Hardware, key string) OptionSetting {
	for _, o := range hw.Options {
		if o.Key == key {
			return o
		}
	}
	return OptionSetting{}
}

func disk(hw Hardware, key string) (HardwareDisk, bool) {
	for _, d := range hw.Disks {
		if d.Key == key {
			return d, true
		}
	}
	return HardwareDisk{}, false
}

func netDev(hw Hardware, key string) (NetworkDevice, bool) {
	for _, n := range hw.Networks {
		if n.Key == key {
			return n, true
		}
	}
	return NetworkDevice{}, false
}

func TestHardwareView(t *testing.T) {
	e := newProvEnv(t)
	hw := e.hardware(t)
	if hw.Digest == "" || hw.Running || !hw.Configurable || len(hw.Storages) != 2 || hw.Limits.Cores != 8 {
		t.Errorf("hardware: %+v", hw)
	}
	if o := option(hw, "cores"); o.Value != "2" || !o.Set {
		t.Errorf("cores: %+v", o)
	}
	if o := option(hw, "agent"); o.Set {
		t.Errorf("agent should not be set: %+v", o)
	}
	if n, ok := netDev(hw, "net0"); !ok || n.Model != "virtio" || n.MAC != "BC:24:11:00:00:01" || n.Bridge != "vmbr0" {
		t.Errorf("net0: %+v", n)
	}

	// Without provisioning the hardware is shown but can't be changed.
	var off Hardware
	if st, _ := e.do(t, "other", http.MethodGet, "/off/guests/qemu/5100/hardware", nil, &off); st != http.StatusOK || off.Configurable || off.Storages == nil {
		t.Errorf("off: %d %+v", st, off)
	}
	if st, _ := e.do(t, "other", http.MethodPost, "/off/guests/qemu/5100/hardware/options", OptionsRequest{Set: map[string]string{"onboot": "1"}}, nil); st != http.StatusConflict {
		t.Errorf("change without provisioning: %d", st)
	}
	if st, _ := e.do(t, "viewer", http.MethodGet, "/lab/guests/lxc/5100/hardware", nil, nil); st != http.StatusBadRequest && st != http.StatusNotFound {
		t.Errorf("container: %d", st)
	}
}

func TestChangeOptions(t *testing.T) {
	e := newProvEnv(t)
	hw := e.hardware(t)
	req := OptionsRequest{Digest: hw.Digest, Set: map[string]string{
		"cores": "4", "onboot": "1", "agent": "enabled=1,fstrim_cloned_disks=1", "boot": "order=scsi0;net0", "description": "line 1\nline 2",
	}}
	var res ConfigResult
	if st, msg := e.do(t, "configurer", http.MethodPost, hw5100+"/options", req, &res); st != http.StatusOK || !res.Done || res.UPID == "" {
		t.Fatalf("options: %d %s %+v", st, msg, res)
	}
	f := e.lastCall(t, http.MethodPost, "/qemu/5100/config")
	if f.Get("cores") != "4" || f.Get("agent") != "enabled=1,fstrim_cloned_disks=1" || f.Get("digest") != hw.Digest {
		t.Errorf("form: %v", f)
	}
	hw = e.hardware(t)
	if option(hw, "cores").Value != "4" || option(hw, "onboot").Value != "1" {
		t.Errorf("after change: %+v", hw.Options)
	}
	if l := e.entries(t, "proxmox.guest.configure"); len(l) != 1 || l[0].Result != "success" || l[0].Target != "lab/qemu/5100" {
		t.Errorf("audit: %+v", l)
	}

	// Removing an option.
	if st, msg := e.do(t, "configurer", http.MethodPost, hw5100+"/options", OptionsRequest{Digest: hw.Digest, Delete: []string{"onboot"}}, nil); st != http.StatusOK {
		t.Fatalf("delete: %d %s", st, msg)
	}
	if o := option(e.hardware(t), "onboot"); o.Set {
		t.Errorf("onboot still set: %+v", o)
	}

	for name, tc := range map[string]struct {
		user string
		req  OptionsRequest
		want int
	}{
		"read only":       {"viewer", OptionsRequest{Set: map[string]string{"onboot": "1"}}, http.StatusForbidden},
		"other vm":        {"configurer", OptionsRequest{Set: map[string]string{"onboot": "1"}}, http.StatusForbidden},
		"cores limit":     {"configurer", OptionsRequest{Set: map[string]string{"cores": "9"}}, http.StatusBadRequest},
		"cores × sockets": {"configurer", OptionsRequest{Set: map[string]string{"cores": "4", "sockets": "4"}}, http.StatusBadRequest},
		"balloon":         {"configurer", OptionsRequest{Set: map[string]string{"balloon": "4096"}}, http.StatusBadRequest},
		"not offered":     {"configurer", OptionsRequest{Set: map[string]string{"args": "-x"}}, http.StatusBadRequest},
		"bad boot":        {"configurer", OptionsRequest{Set: map[string]string{"boot": "order=scsi0;/dev/sda"}}, http.StatusBadRequest},
		"bad ostype":      {"configurer", OptionsRequest{Set: map[string]string{"ostype": "dos"}}, http.StatusBadRequest},
		"remove name":     {"configurer", OptionsRequest{Delete: []string{"name"}}, http.StatusBadRequest},
		"set and remove":  {"configurer", OptionsRequest{Set: map[string]string{"onboot": "1"}, Delete: []string{"onboot"}}, http.StatusBadRequest},
		"nothing":         {"configurer", OptionsRequest{}, http.StatusBadRequest},
		"changed":         {"configurer", OptionsRequest{Digest: "0000", Set: map[string]string{"onboot": "1"}}, http.StatusConflict},
	} {
		path := hw5100
		if name == "other vm" {
			path = "/lab/guests/qemu/101/hardware"
		}
		if st, msg := e.do(t, tc.user, http.MethodPost, path+"/options", tc.req, nil); st != tc.want {
			t.Errorf("%s: %d %s, want %d", name, st, msg, tc.want)
		}
	}
	if st, msg := e.do(t, "configurer", http.MethodPost, hw5100+"/options", OptionsRequest{Digest: "0000", Set: map[string]string{"onboot": "1"}}, nil); !strings.Contains(msg, "config_changed") {
		t.Errorf("digest mismatch: %d %s", st, msg)
	}
}

// Changes of a running VM that need a restart are pending and can be
// reverted.
func TestPendingOptions(t *testing.T) {
	e := newProvEnv(t)
	if st, msg := e.do(t, "configurer", http.MethodPost, "/lab/guests/qemu/5100/actions/start", nil, nil); st >= 300 {
		t.Fatalf("start: %d %s", st, msg)
	}
	hw := e.hardware(t)
	if !hw.Running {
		t.Fatal("not running")
	}
	if st, msg := e.do(t, "configurer", http.MethodPost, hw5100+"/options", OptionsRequest{Digest: hw.Digest, Set: map[string]string{"memory": "4096", "onboot": "1"}}, nil); st != http.StatusOK {
		t.Fatalf("options: %d %s", st, msg)
	}
	hw = e.hardware(t)
	if o := option(hw, "memory"); o.Value != "2048" || o.Pending == nil || *o.Pending != "4096" {
		t.Errorf("memory: %+v", o)
	}
	if o := option(hw, "onboot"); o.Value != "1" || o.Pending != nil {
		t.Errorf("onboot applies at once: %+v", o)
	}
	if st, msg := e.do(t, "configurer", http.MethodPost, hw5100+"/options", OptionsRequest{Digest: hw.Digest, Revert: []string{"memory"}}, nil); st != http.StatusOK {
		t.Fatalf("revert: %d %s", st, msg)
	}
	if o := option(e.hardware(t), "memory"); o.Pending != nil {
		t.Errorf("memory still pending: %+v", o)
	}
}

func TestDisks(t *testing.T) {
	e := newProvEnv(t)
	hw := e.hardware(t)
	var res ConfigResult
	req := DiskRequest{Digest: hw.Digest, Bus: "scsi", Storage: "local-lvm", SizeGiB: 8, Discard: true, SSD: true}
	if st, msg := e.do(t, "configurer", http.MethodPost, hw5100+"/disks", req, &res); st != http.StatusOK || res.Key != "scsi0" {
		t.Fatalf("create: %d %s %+v", st, msg, res)
	}
	if f := e.lastCall(t, http.MethodPost, "/qemu/5100/config"); f.Get("scsi0") != "local-lvm:8,discard=on,ssd=1" {
		t.Errorf("form: %v", f)
	}
	hw = e.hardware(t)
	d, ok := disk(hw, "scsi0")
	if !ok || d.Storage != "local-lvm" || d.Size != "8G" || d.SizeBytes != 8<<30 || !d.Discard || !d.SSD || d.IOThread || !d.Backup {
		t.Fatalf("scsi0: %+v", d)
	}

	for name, r := range map[string]DiskRequest{
		"storage":     {Bus: "scsi", Storage: "nfs", SizeGiB: 1},
		"size":        {Bus: "scsi", Storage: "ceph", SizeGiB: 101},
		"bus":         {Bus: "nvme", Storage: "ceph", SizeGiB: 1},
		"virtio ssd":  {Bus: "virtio", Storage: "ceph", SizeGiB: 1, SSD: true},
		"ide thread":  {Bus: "ide", Storage: "ceph", SizeGiB: 1, IOThread: true},
		"zero size":   {Bus: "scsi", Storage: "ceph"},
		"read only":   {Bus: "scsi", Storage: "ceph", SizeGiB: 1},
		"other guest": {Bus: "scsi", Storage: "ceph", SizeGiB: 1},
	} {
		user, path := "configurer", hw5100
		want := http.StatusBadRequest
		switch name {
		case "read only":
			user, want = "viewer", http.StatusForbidden
		case "other guest":
			path, want = "/lab/guests/qemu/101/hardware", http.StatusForbidden
		}
		if st, msg := e.do(t, user, http.MethodPost, path+"/disks", r, nil); st != want {
			t.Errorf("%s: %d %s, want %d", name, st, msg, want)
		}
	}

	// Grow and change options; shrinking is refused.
	if st, _ := e.do(t, "configurer", http.MethodPut, hw5100+"/disks/scsi0", DiskUpdate{SizeGiB: 4}, nil); st != http.StatusBadRequest {
		t.Errorf("shrink: %d", st)
	}
	on := true
	if st, msg := e.do(t, "configurer", http.MethodPut, hw5100+"/disks/scsi0", DiskUpdate{Digest: hw.Digest, SizeGiB: 16, IOThread: &on}, nil); st != http.StatusOK {
		t.Fatalf("update: %d %s", st, msg)
	}
	if f := e.lastCall(t, http.MethodPut, "/qemu/5100/resize"); f.Get("disk") != "scsi0" || f.Get("size") != "16G" || f.Get("digest") != "" {
		t.Errorf("resize form: %v", f)
	}
	hw = e.hardware(t)
	if d, _ := disk(hw, "scsi0"); d.Size != "16G" || !d.IOThread || !d.Discard {
		t.Errorf("after update: %+v", d)
	}
	if l := e.entries(t, "proxmox.disk.update"); len(l) != 1 {
		t.Errorf("audit update: %+v", l)
	}

	// Detach, attach to another bus.
	if st, msg := e.do(t, "configurer", http.MethodPost, hw5100+"/disks/scsi0/detach", DiskAction{Digest: hw.Digest}, nil); st != http.StatusOK {
		t.Fatalf("detach: %d %s", st, msg)
	}
	hw = e.hardware(t)
	u, ok := disk(hw, "unused0")
	if _, still := disk(hw, "scsi0"); still || !ok || u.Bus != "unused" || u.Storage != "local-lvm" {
		t.Fatalf("after detach: %+v", hw.Disks)
	}
	if st, msg := e.do(t, "configurer", http.MethodPost, hw5100+"/disks/unused0/attach", DiskAction{Digest: hw.Digest, Bus: "virtio"}, &res); st != http.StatusOK || res.Key != "virtio0" {
		t.Fatalf("attach: %d %s %+v", st, msg, res)
	}
	hw = e.hardware(t)
	if d, ok := disk(hw, "virtio0"); !ok || d.Volume != u.Volume {
		t.Errorf("after attach: %+v", hw.Disks)
	}
	if _, ok := disk(hw, "unused0"); ok {
		t.Error("unused0 still there")
	}

	// Deleting needs admin and the disk name.
	if st, _ := e.do(t, "configurer", http.MethodDelete, hw5100+"/disks/virtio0", DiskAction{Confirm: "virtio0"}, nil); st != http.StatusForbidden {
		t.Errorf("delete as writer: %d", st)
	}
	if st, msg := e.do(t, "remover", http.MethodDelete, hw5100+"/disks/virtio0", DiskAction{Confirm: "scsi0"}, nil); !strings.Contains(msg, "confirmation_mismatch") {
		t.Errorf("wrong confirmation: %d %s", st, msg)
	}
	if st, msg := e.do(t, "remover", http.MethodDelete, hw5100+"/disks/virtio0", DiskAction{Digest: hw.Digest, Confirm: "virtio0"}, nil); st != http.StatusOK {
		t.Fatalf("delete: %d %s", st, msg)
	}
	if hw = e.hardware(t); len(hw.Disks) != 0 {
		t.Errorf("disks after delete: %+v", hw.Disks)
	}
	if l := e.entries(t, "proxmox.disk.delete"); len(l) != 1 || l[0].Details["volume"] != u.Volume {
		t.Errorf("audit delete: %+v", l)
	}
	if st, _ := e.do(t, "remover", http.MethodDelete, hw5100+"/disks/scsi5", DiskAction{Confirm: "scsi5"}, nil); st != http.StatusNotFound {
		t.Errorf("unknown disk: %d", st)
	}
}

// An attached disk of a running VM is not deleted in one step.
func TestDeleteDiskOfRunningVM(t *testing.T) {
	e := newProvEnv(t)
	hw := e.hardware(t)
	if st, msg := e.do(t, "configurer", http.MethodPost, hw5100+"/disks", DiskRequest{Digest: hw.Digest, Bus: "scsi", Storage: "ceph", SizeGiB: 2}, nil); st != http.StatusOK {
		t.Fatalf("create: %d %s", st, msg)
	}
	if st, msg := e.do(t, "configurer", http.MethodPost, "/lab/guests/qemu/5100/actions/start", nil, nil); st >= 300 {
		t.Fatalf("start: %d %s", st, msg)
	}
	if st, msg := e.do(t, "remover", http.MethodDelete, hw5100+"/disks/scsi0", DiskAction{Confirm: "scsi0"}, nil); !strings.Contains(msg, "guest_running") {
		t.Errorf("running: %d %s", st, msg)
	}
}

var macRe = regexp.MustCompile(`^BC:24:11(:[0-9A-F]{2}){3}$`)

func TestNetworks(t *testing.T) {
	e := newProvEnv(t)
	hw := e.hardware(t)
	var res ConfigResult
	req := NetworkRequest{Digest: hw.Digest, Model: "virtio", Bridge: "vmbr0", VLAN: 150, Firewall: true}
	if st, msg := e.do(t, "configurer", http.MethodPost, hw5100+"/networks", req, &res); st != http.StatusOK || res.Key != "net1" {
		t.Fatalf("create: %d %s %+v", st, msg, res)
	}
	hw = e.hardware(t)
	n, ok := netDev(hw, "net1")
	if !ok || !macRe.MatchString(n.MAC) || n.VLAN != 150 || !n.Firewall || n.LinkDown {
		t.Fatalf("net1: %+v", n)
	}

	// A given MAC is kept (upper case); multicast addresses are refused.
	if st, msg := e.do(t, "configurer", http.MethodPost, hw5100+"/networks", NetworkRequest{Model: "e1000", Bridge: "vmbr0", MAC: "bc:24:11:aa:bb:cc"}, &res); st != http.StatusOK || res.Key != "net2" {
		t.Fatalf("create with MAC: %d %s", st, msg)
	}
	if n2, _ := netDev(e.hardware(t), "net2"); n2.MAC != "BC:24:11:AA:BB:CC" || n2.Model != "e1000" {
		t.Errorf("net2: %+v", n2)
	}
	for name, r := range map[string]NetworkRequest{
		"bridge":    {Model: "virtio", Bridge: "vmbr9"},
		"vlan":      {Model: "virtio", Bridge: "vmbr0", VLAN: 300},
		"model":     {Model: "ne2k", Bridge: "vmbr0"},
		"multicast": {Model: "virtio", Bridge: "vmbr0", MAC: "01:00:5E:00:00:01"},
		"bad mac":   {Model: "virtio", Bridge: "vmbr0", MAC: "BC:24:11"},
	} {
		if st, msg := e.do(t, "configurer", http.MethodPost, hw5100+"/networks", r, nil); st != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, st, msg)
		}
	}

	// A change keeps the MAC.
	hw = e.hardware(t)
	if st, msg := e.do(t, "configurer", http.MethodPut, hw5100+"/networks/net1", NetworkRequest{Digest: hw.Digest, Model: "vmxnet3", Bridge: "vmbr0", LinkDown: true, MAC: "BC:24:11:00:00:99"}, nil); st != http.StatusOK {
		t.Fatalf("update: %d %s", st, msg)
	}
	hw = e.hardware(t)
	if u, _ := netDev(hw, "net1"); u.MAC != n.MAC || u.Model != "vmxnet3" || u.VLAN != 0 || u.Firewall || !u.LinkDown {
		t.Errorf("after update: %+v (was %+v)", u, n)
	}
	if st, msg := e.do(t, "configurer", http.MethodDelete, hw5100+"/networks/net1", NetworkAction{Digest: hw.Digest}, nil); st != http.StatusOK {
		t.Fatalf("delete: %d %s", st, msg)
	}
	if _, ok := netDev(e.hardware(t), "net1"); ok {
		t.Error("net1 still there")
	}
	if st, _ := e.do(t, "configurer", http.MethodDelete, hw5100+"/networks/net7", NetworkAction{}, nil); st != http.StatusNotFound {
		t.Errorf("unknown device: %d", st)
	}
	for _, a := range []string{"proxmox.network.create", "proxmox.network.update", "proxmox.network.delete"} {
		if len(e.entries(t, a)) == 0 {
			t.Errorf("no audit entry %s", a)
		}
	}
}

func TestParseHardware(t *testing.T) {
	hw := buildHardware([]PendingEntry{
		{Key: "digest", Value: "d1"},
		{Key: "cipassword", Value: "**********"},
		{Key: "cores", Value: float64(2), Pending: float64(4)},
		{Key: "agent", Value: "1", Delete: 1},
		{Key: "scsi10", Value: "ceph:vm-1-disk-2,size=1T,backup=0"},
		{Key: "scsi2", Value: "ceph:vm-1-disk-1,size=512M,iothread=1"},
		{Key: "ide2", Value: "local:iso/debian.iso,media=cdrom"},
		{Key: "unused0", Value: "ceph:vm-1-disk-3"},
		{Key: "virtio0", Pending: "ceph:vm-1-disk-4,size=4G"},
		{Key: "net0", Value: "e1000,macaddr=bc:24:11:00:00:02,bridge=vmbr1,tag=5,link_down=1,mtu=9000"},
	})
	if hw.Digest != "d1" {
		t.Errorf("digest %q", hw.Digest)
	}
	if o := option(hw, "cores"); o.Value != "2" || o.Pending == nil || *o.Pending != "4" {
		t.Errorf("cores: %+v", o)
	}
	if o := option(hw, "agent"); !o.Delete {
		t.Errorf("agent: %+v", o)
	}
	keys := make([]string, 0, len(hw.Disks))
	for _, d := range hw.Disks {
		keys = append(keys, d.Key)
	}
	if strings.Join(keys, ",") != "scsi2,scsi10,virtio0,unused0" {
		t.Errorf("disk order (no CD-ROM): %v", keys)
	}
	if d, _ := disk(hw, "scsi10"); d.SizeBytes != 1<<40 || d.Backup {
		t.Errorf("scsi10: %+v", d)
	}
	if d, _ := disk(hw, "virtio0"); d.Active || d.Pending == nil || d.Size != "4G" {
		t.Errorf("pending disk: %+v", d)
	}
	n, _ := netDev(hw, "net0")
	if n.Model != "e1000" || n.MAC != "BC:24:11:00:00:02" || n.VLAN != 5 || !n.LinkDown {
		t.Errorf("net0: %+v", n)
	}
	// netValue keeps properties dcdash doesn't manage.
	if v := netValue(NetworkDevice{Model: "virtio", MAC: n.MAC, Bridge: "vmbr0"}, "e1000,macaddr=bc:24:11:00:00:02,bridge=vmbr1,tag=5,mtu=9000"); v != "virtio=BC:24:11:00:00:02,bridge=vmbr0,mtu=9000" {
		t.Errorf("netValue: %s", v)
	}
}
