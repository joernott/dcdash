// SPDX-License-Identifier: GPL-3.0-or-later

package proxmox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Guest types.
const (
	TypeQEMU = "qemu" // virtual machine
	TypeLXC  = "lxc"  // container
)

// IsGuestType reports whether t is "qemu" or "lxc".
func IsGuestType(t string) bool { return t == TypeQEMU || t == TypeLXC }

// Resource is an entry of GET /cluster/resources. Usage values are only set
// when the token may see them (Sys.Audit for nodes, VM.Audit for guests).
type Resource struct {
	ID       string  `json:"id"`   // e.g. "qemu/101", "node/pve1", "storage/pve1/local"
	Type     string  `json:"type"` // qemu, lxc, node, storage, ...
	Node     string  `json:"node"`
	VMID     int     `json:"vmid"`
	Name     string  `json:"name"`
	Status   string  `json:"status"`
	Template flexInt `json:"template"`
	Tags     string  `json:"tags"`
	Pool     string  `json:"pool"`
	Lock     string  `json:"lock"`
	HAState  string  `json:"hastate"`
	Storage  string  `json:"storage"`
	CPU      float64 `json:"cpu"`
	MaxCPU   float64 `json:"maxcpu"`
	Mem      int64   `json:"mem"`
	MaxMem   int64   `json:"maxmem"`
	Disk     int64   `json:"disk"`
	MaxDisk  int64   `json:"maxdisk"`
	Uptime   int64   `json:"uptime"`
}

// flexInt decodes a number that Proxmox sends as number or string.
type flexInt int

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return err
	}
	*f = flexInt(n)
	return nil
}

// Resources returns all resources of the cluster the token may see.
func (c *Cluster) Resources(ctx context.Context) ([]Resource, error) {
	var rs []Resource
	if err := c.do(ctx, http.MethodGet, "/cluster/resources", nil, &rs); err != nil {
		return nil, err
	}
	return rs, nil
}

// ErrNotFound means that a guest or node does not exist (or is not visible to the token).
var ErrNotFound = errors.New("proxmox: not found")

// FindGuest returns the resource of a guest, which contains its current node.
func (c *Cluster) FindGuest(ctx context.Context, typ string, vmid int) (Resource, error) {
	rs, err := c.Resources(ctx)
	if err != nil {
		return Resource{}, err
	}
	for _, r := range rs {
		if r.Type == typ && r.VMID == vmid {
			return r, nil
		}
	}
	return Resource{}, ErrNotFound
}

var nodeNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]{0,62}$`)

// guestPath returns /nodes/{node}/{type}/{vmid}.
func guestPath(node, typ string, vmid int) (string, error) {
	if !nodeNamePattern.MatchString(node) || !IsGuestType(typ) || vmid <= 0 {
		return "", fmt.Errorf("proxmox: invalid guest %s/%s/%d", node, typ, vmid)
	}
	return "/nodes/" + node + "/" + typ + "/" + strconv.Itoa(vmid), nil
}

// GuestStatus returns GET .../status/current (state, usage, HA, agent).
func (c *Cluster) GuestStatus(ctx context.Context, node, typ string, vmid int) (map[string]any, error) {
	p, err := guestPath(node, typ, vmid)
	if err != nil {
		return nil, err
	}
	var v map[string]any
	if err := c.do(ctx, http.MethodGet, p+"/status/current", nil, &v); err != nil {
		return nil, err
	}
	return v, nil
}

// hiddenConfigKeys are not returned by GuestConfig.
var hiddenConfigKeys = []string{"cipassword", "digest"}

// GuestConfig returns the configuration of a guest without secrets.
func (c *Cluster) GuestConfig(ctx context.Context, node, typ string, vmid int) (map[string]any, error) {
	p, err := guestPath(node, typ, vmid)
	if err != nil {
		return nil, err
	}
	var v map[string]any
	if err := c.do(ctx, http.MethodGet, p+"/config", nil, &v); err != nil {
		return nil, err
	}
	for _, k := range hiddenConfigKeys {
		delete(v, k)
	}
	return v, nil
}

// Actions are the power actions of guests.
var Actions = []string{"start", "shutdown", "stop", "reboot", "suspend", "resume"}

// IsAction reports whether a is a power action.
func IsAction(a string) bool { return slices.Contains(Actions, a) }

// Action runs a power action and returns the UPID of the Proxmox task.
func (c *Cluster) Action(ctx context.Context, node, typ string, vmid int, action string) (string, error) {
	if !IsAction(action) {
		return "", fmt.Errorf("proxmox: unknown action %q", action)
	}
	p, err := guestPath(node, typ, vmid)
	if err != nil {
		return "", err
	}
	var upid string
	if err := c.do(ctx, http.MethodPost, p+"/status/"+action, nil, &upid); err != nil {
		return "", err
	}
	return upid, nil
}

// UPID is a parsed Proxmox task ID:
// UPID:{node}:{pid}:{pstart}:{starttime}:{type}:{id}:{user}:
type UPID struct {
	Raw  string
	Node string
	Type string // e.g. qmstart, vzstop
	ID   string // usually the VMID
	User string
}

// ParseUPID parses a task ID.
func ParseUPID(s string) (UPID, error) {
	parts := strings.Split(s, ":")
	if len(parts) < 9 || parts[0] != "UPID" || !nodeNamePattern.MatchString(parts[1]) {
		return UPID{}, fmt.Errorf("proxmox: invalid task ID %q", s)
	}
	// The user may contain no colons; the token name follows "!".
	return UPID{Raw: s, Node: parts[1], Type: parts[5], ID: parts[6], User: parts[7]}, nil
}

// VMID returns the guest ID of the task, or 0.
func (u UPID) VMID() int {
	n, err := strconv.Atoi(u.ID)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// TaskStatus is the state of a task.
type TaskStatus struct {
	UPID      string `json:"upid"`
	Node      string `json:"node"`
	Type      string `json:"type"`
	ID        string `json:"id"`
	User      string `json:"user"`
	Status    string `json:"status"`     // running or stopped
	ExitState string `json:"exitstatus"` // "OK" or an error, when stopped
	StartTime int64  `json:"starttime"`
	EndTime   int64  `json:"endtime"`
}

// LogLine is a line of a task log.
type LogLine struct {
	N int    `json:"n"`
	T string `json:"t"`
}

// Task returns the status of a task.
func (c *Cluster) Task(ctx context.Context, u UPID) (TaskStatus, error) {
	var s TaskStatus
	err := c.do(ctx, http.MethodGet, "/nodes/"+u.Node+"/tasks/"+url.PathEscape(u.Raw)+"/status", nil, &s)
	return s, err
}

// TaskLog returns up to limit lines of a task log, starting at line start.
func (c *Cluster) TaskLog(ctx context.Context, u UPID, start, limit int) ([]LogLine, error) {
	var l []LogLine
	form := url.Values{"start": {strconv.Itoa(start)}, "limit": {strconv.Itoa(limit)}}
	err := c.do(ctx, http.MethodGet, "/nodes/"+u.Node+"/tasks/"+url.PathEscape(u.Raw)+"/log", form, &l)
	return l, err
}
