// SPDX-License-Identifier: GPL-3.0-or-later

package proxmox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// API calls for creating and deleting guests (see docs/adr/0004-proxmox-provisioning.md).

// optionalData marks a response whose data may be null (e.g. a call that
// returns a task ID in newer Proxmox versions only).
type optionalData interface{ optional() }

// optString is a string that may be null in a response.
type optString string

func (*optString) optional() {}

// IDFree reports whether a guest ID is unused in the cluster, including guests
// the token can't see.
func (c *Cluster) IDFree(ctx context.Context, vmid int) (bool, error) {
	var v json.RawMessage
	err := c.do(ctx, http.MethodGet, "/cluster/nextid", url.Values{"vmid": {strconv.Itoa(vmid)}}, &v)
	if IsStatus(err, http.StatusBadRequest) {
		return false, nil
	}
	return err == nil, err
}

// NextID returns the lowest free guest ID of Proxmox.
func (c *Cluster) NextID(ctx context.Context) (int, error) {
	var v flexInt
	if err := c.do(ctx, http.MethodGet, "/cluster/nextid", nil, &v); err != nil {
		return 0, err
	}
	return int(v), nil
}

// NodeStorage is a storage available on a node.
type NodeStorage struct {
	Storage string  `json:"storage"`
	Type    string  `json:"type"`
	Content string  `json:"content"`
	Active  flexInt `json:"active"`
	Shared  flexInt `json:"shared"`
	Avail   int64   `json:"avail"`
	Total   int64   `json:"total"`
}

// NodeStorages returns the enabled storages of a node for a content type
// (images, rootdir, iso, vztmpl; "" for all).
func (c *Cluster) NodeStorages(ctx context.Context, node, content string) ([]NodeStorage, error) {
	if !nodeNamePattern.MatchString(node) {
		return nil, fmt.Errorf("proxmox: invalid node %q", node)
	}
	form := url.Values{"enabled": {"1"}}
	if content != "" {
		form.Set("content", content)
	}
	var v []NodeStorage
	err := c.do(ctx, http.MethodGet, "/nodes/"+node+"/storage", form, &v)
	return v, err
}

// Volume is a file on a storage, e.g. an ISO image or a container template.
type Volume struct {
	VolID  string `json:"volid"`
	Size   int64  `json:"size"`
	Format string `json:"format"`
}

var storageIDPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]*$`)

// StorageContent returns the volumes of a content type on a storage of a node.
func (c *Cluster) StorageContent(ctx context.Context, node, storage, content string) ([]Volume, error) {
	if !nodeNamePattern.MatchString(node) || !storageIDPattern.MatchString(storage) {
		return nil, fmt.Errorf("proxmox: invalid storage %s/%s", node, storage)
	}
	var v []Volume
	err := c.do(ctx, http.MethodGet, "/nodes/"+node+"/storage/"+storage+"/content", url.Values{"content": {content}}, &v)
	return v, err
}

// Bridge is a network bridge of a node or an SDN VNet.
type Bridge struct {
	Name      string `json:"name"`
	VLANAware bool   `json:"vlanAware"`
}

// Bridges returns the bridges of a node and the SDN VNets of the cluster.
// VNets are left out if the token may not list them.
func (c *Cluster) Bridges(ctx context.Context, node string) ([]Bridge, error) {
	if !nodeNamePattern.MatchString(node) {
		return nil, fmt.Errorf("proxmox: invalid node %q", node)
	}
	var ifs []struct {
		Iface     string  `json:"iface"`
		VLANAware flexInt `json:"bridge_vlan_aware"`
	}
	if err := c.do(ctx, http.MethodGet, "/nodes/"+node+"/network", url.Values{"type": {"any_bridge"}}, &ifs); err != nil {
		return nil, err
	}
	out := []Bridge{}
	for _, i := range ifs {
		out = append(out, Bridge{Name: i.Iface, VLANAware: i.VLANAware != 0})
	}
	var vnets []struct {
		VNet      string  `json:"vnet"`
		VLANAware flexInt `json:"vlanaware"`
	}
	if err := c.do(ctx, http.MethodGet, "/cluster/sdn/vnets", nil, &vnets); err == nil {
		for _, v := range vnets {
			out = append(out, Bridge{Name: v.VNet, VLANAware: v.VLANAware != 0})
		}
	} else if errors.Is(err, context.Canceled) {
		return nil, err
	}
	return out, nil
}

// postTask sends a request that starts a Proxmox task and returns its ID.
func (c *Cluster) postTask(ctx context.Context, method, path string, form url.Values) (string, error) {
	var upid string
	if err := c.do(ctx, method, path, form, &upid); err != nil {
		return "", err
	}
	return upid, nil
}

// CloneVM clones a VM template; it runs on the template's node.
func (c *Cluster) CloneVM(ctx context.Context, node string, template int, form url.Values) (string, error) {
	p, err := guestPath(node, TypeQEMU, template)
	if err != nil {
		return "", err
	}
	return c.postTask(ctx, http.MethodPost, p+"/clone", form)
}

// CreateGuest creates a VM or container on a node.
func (c *Cluster) CreateGuest(ctx context.Context, node, typ string, form url.Values) (string, error) {
	if !nodeNamePattern.MatchString(node) || !IsGuestType(typ) {
		return "", fmt.Errorf("proxmox: invalid node %q or type %q", node, typ)
	}
	return c.postTask(ctx, http.MethodPost, "/nodes/"+node+"/"+typ, form)
}

// UpdateConfig changes the configuration of a guest.
func (c *Cluster) UpdateConfig(ctx context.Context, node, typ string, vmid int, form url.Values) error {
	p, err := guestPath(node, typ, vmid)
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodPut, p+"/config", form, nil)
}

// ResizeDisk grows a VM disk to size (e.g. "40G"). It returns the task ID,
// or "" if Proxmox resized synchronously.
func (c *Cluster) ResizeDisk(ctx context.Context, node string, vmid int, disk, size string) (string, error) {
	p, err := guestPath(node, TypeQEMU, vmid)
	if err != nil {
		return "", err
	}
	var upid optString
	err = c.do(ctx, http.MethodPut, p+"/resize", url.Values{"disk": {disk}, "size": {size}}, &upid)
	return string(upid), err
}

// DeleteGuest deletes a stopped guest with its disks. purge also removes it
// from backup jobs, replication and HA.
func (c *Cluster) DeleteGuest(ctx context.Context, node, typ string, vmid int, purge bool) (string, error) {
	p, err := guestPath(node, typ, vmid)
	if err != nil {
		return "", err
	}
	form := url.Values{"destroy-unreferenced-disks": {"1"}}
	if purge {
		form.Set("purge", "1")
	}
	return c.postTask(ctx, http.MethodDelete, p, form)
}

// taskPoll is the interval for WaitTask.
var taskPoll = time.Second

// WaitTask waits until a task has ended and returns an error if it failed.
func (c *Cluster) WaitTask(ctx context.Context, upid string) error {
	u, err := ParseUPID(upid)
	if err != nil {
		return err
	}
	for {
		st, err := c.Task(ctx, u)
		if err != nil && !errors.Is(err, ErrUnavailable) {
			return err
		}
		if err == nil && st.Status != "running" {
			if st.ExitState != "OK" && !strings.HasPrefix(st.ExitState, "WARNINGS") {
				return &TaskError{UPID: upid, ExitStatus: st.ExitState}
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(taskPoll):
		}
	}
}

// TaskError is a Proxmox task that ended with an error.
type TaskError struct {
	UPID       string
	ExitStatus string
}

func (e *TaskError) Error() string { return "proxmox task failed: " + e.ExitStatus }
