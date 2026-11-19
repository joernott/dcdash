// SPDX-License-Identifier: GPL-3.0-or-later

package netbox

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// NetBox objects, with the fields dcdash uses.

// Ref is a nested object in a response (brief representation).
type Ref struct {
	ID   int64  `json:"id"`
	Name string `json:"name,omitempty"`
	Slug string `json:"slug,omitempty"`
}

// Choice is a field with fixed values, such as a status.
type Choice struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Tag is a nested tag.
type Tag struct {
	ID   int64  `json:"id"`
	Slug string `json:"slug"`
}

// Cluster is a virtualization cluster.
type Cluster struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// VirtualMachine is a virtual machine.
type VirtualMachine struct {
	ID           int64          `json:"id"`
	Name         string         `json:"name"`
	Status       Choice         `json:"status"`
	Cluster      *Ref           `json:"cluster"`
	VCPUs        *float64       `json:"vcpus"`
	Memory       *int64         `json:"memory"`
	Disk         *int64         `json:"disk"`
	PrimaryIP4   *IPRef         `json:"primary_ip4"`
	PrimaryIP6   *IPRef         `json:"primary_ip6"`
	Description  string         `json:"description"`
	Tags         []Tag          `json:"tags"`
	CustomFields map[string]any `json:"custom_fields"`
}

// HasTag reports whether the VM has the tag (slug).
func (v VirtualMachine) HasTag(slug string) bool { return hasTag(v.Tags, slug) }

func hasTag(tags []Tag, slug string) bool {
	for _, t := range tags {
		if t.Slug == slug {
			return true
		}
	}
	return false
}

// IPRef is a nested IP address.
type IPRef struct {
	ID      int64  `json:"id"`
	Address string `json:"address"`
}

// VLANRef is a nested VLAN.
type VLANRef struct {
	ID   int64  `json:"id"`
	VID  int    `json:"vid"`
	Name string `json:"name"`
}

// MACRef is a nested MAC address.
type MACRef struct {
	ID         int64  `json:"id"`
	MACAddress string `json:"mac_address"`
}

// Interface is a VM interface.
type Interface struct {
	ID             int64    `json:"id"`
	VirtualMachine Ref      `json:"virtual_machine"`
	Name           string   `json:"name"`
	Enabled        bool     `json:"enabled"`
	Mode           *Choice  `json:"mode"`
	UntaggedVLAN   *VLANRef `json:"untagged_vlan"`
	PrimaryMAC     *MACRef  `json:"primary_mac_address"`
	Description    string   `json:"description"`
}

// MACAddress is a MAC address object (NetBox 4.2 and later).
type MACAddress struct {
	ID                 int64  `json:"id"`
	MACAddress         string `json:"mac_address"`
	AssignedObjectType string `json:"assigned_object_type"`
	AssignedObjectID   *int64 `json:"assigned_object_id"`
	AssignedObject     *struct {
		ID             int64  `json:"id"`
		Name           string `json:"name"`
		VirtualMachine *Ref   `json:"virtual_machine"`
	} `json:"assigned_object"`
}

// IPAddress is an IP address with prefix length.
type IPAddress struct {
	ID                 int64  `json:"id"`
	Address            string `json:"address"`
	VRF                *Ref   `json:"vrf"`
	Status             Choice `json:"status"`
	DNSName            string `json:"dns_name"`
	Description        string `json:"description"`
	AssignedObjectType string `json:"assigned_object_type"`
	AssignedObjectID   *int64 `json:"assigned_object_id"`
	Tags               []Tag  `json:"tags"`
}

// HasTag reports whether the address has the tag (slug).
func (a IPAddress) HasTag(slug string) bool { return hasTag(a.Tags, slug) }

// VirtualDisk is a disk of a VM; Size is in MB.
type VirtualDisk struct {
	ID             int64  `json:"id"`
	VirtualMachine Ref    `json:"virtual_machine"`
	Name           string `json:"name"`
	Size           int64  `json:"size"`
}

// VLAN is a VLAN.
type VLAN struct {
	ID          int64  `json:"id"`
	VID         int    `json:"vid"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Group       *Ref   `json:"group"`
	Site        *Ref   `json:"site"`
}

// Prefix is an IP prefix.
type Prefix struct {
	ID          int64    `json:"id"`
	Prefix      string   `json:"prefix"`
	VRF         *Ref     `json:"vrf"`
	VLAN        *VLANRef `json:"vlan"`
	Description string   `json:"description"`
}

// AvailableIP is a free address of a prefix.
type AvailableIP struct {
	Address string `json:"address"`
	VRF     *Ref   `json:"vrf"`
}

// Object types of assigned objects.
const (
	TypeVMInterface = "virtualization.vminterface"
)

// API paths.
const (
	pathClusters    = "/virtualization/clusters/"
	pathVMs         = "/virtualization/virtual-machines/"
	pathInterfaces  = "/virtualization/interfaces/"
	pathDisks       = "/virtualization/virtual-disks/"
	pathMACs        = "/dcim/mac-addresses/"
	pathSites       = "/dcim/sites/"
	pathIPAddresses = "/ipam/ip-addresses/"
	pathPrefixes    = "/ipam/prefixes/"
	pathVLANs       = "/ipam/vlans/"
	pathVLANGroups  = "/ipam/vlan-groups/"
	pathVRFs        = "/ipam/vrfs/"
	pathTags        = "/extras/tags/"
)

func id(i int64) string { return strconv.FormatInt(i, 10) }

// ClusterByName returns the cluster with that name.
func (c *Client) ClusterByName(ctx context.Context, name string) (Cluster, error) {
	return get[Cluster](ctx, c, pathClusters, url.Values{"name": {name}})
}

// lookupID returns the ID of the object of a list endpoint that matches q.
func (c *Client) lookupID(ctx context.Context, path string, q url.Values) (int64, error) {
	r, err := get[Ref](ctx, c, path, q)
	return r.ID, err
}

// TagID returns the ID of the tag with that slug.
func (c *Client) TagID(ctx context.Context, slug string) (int64, error) {
	return c.lookupID(ctx, pathTags, url.Values{"slug": {slug}})
}

// VRFID returns the ID of the VRF with that name.
func (c *Client) VRFID(ctx context.Context, name string) (int64, error) {
	return c.lookupID(ctx, pathVRFs, url.Values{"name": {name}})
}

// VLANGroupID returns the ID of the VLAN group with that slug.
func (c *Client) VLANGroupID(ctx context.Context, slug string) (int64, error) {
	return c.lookupID(ctx, pathVLANGroups, url.Values{"slug": {slug}})
}

// SiteID returns the ID of the site with that slug.
func (c *Client) SiteID(ctx context.Context, slug string) (int64, error) {
	return c.lookupID(ctx, pathSites, url.Values{"slug": {slug}})
}

// VLANs returns the VLANs that match q (for example vid, group_id, site_id).
func (c *Client) VLANs(ctx context.Context, q url.Values) ([]VLAN, error) {
	return list[VLAN](ctx, c, pathVLANs, q)
}

// Prefixes returns the prefixes that match q (for example vlan_id, vrf_id).
func (c *Client) Prefixes(ctx context.Context, q url.Values) ([]Prefix, error) {
	return list[Prefix](ctx, c, pathPrefixes, q)
}

// AvailableIPs returns up to limit free addresses of a prefix.
func (c *Client) AvailableIPs(ctx context.Context, prefixID int64, limit int) ([]AvailableIP, error) {
	var out []AvailableIP
	err := c.do(ctx, http.MethodGet, pathPrefixes+id(prefixID)+"/available-ips/",
		url.Values{"limit": {strconv.Itoa(limit)}}, nil, &out)
	return out, err
}

// IPAddresses returns the addresses that match q (for example address,
// vrf_id, virtual_machine_id).
func (c *Client) IPAddresses(ctx context.Context, q url.Values) ([]IPAddress, error) {
	return list[IPAddress](ctx, c, pathIPAddresses, q)
}

// VMs returns the virtual machines that match q (for example cluster_id).
func (c *Client) VMs(ctx context.Context, q url.Values) ([]VirtualMachine, error) {
	return list[VirtualMachine](ctx, c, pathVMs, q)
}

// FindVM returns the virtual machine that matches q, or ErrNotFound.
func (c *Client) FindVM(ctx context.Context, q url.Values) (VirtualMachine, error) {
	return get[VirtualMachine](ctx, c, pathVMs, q)
}

// Interfaces returns the VM interfaces that match q (for example
// virtual_machine_id, cluster_id).
func (c *Client) Interfaces(ctx context.Context, q url.Values) ([]Interface, error) {
	return list[Interface](ctx, c, pathInterfaces, q)
}

// Disks returns the virtual disks that match q (for example
// virtual_machine_id).
func (c *Client) Disks(ctx context.Context, q url.Values) ([]VirtualDisk, error) {
	return list[VirtualDisk](ctx, c, pathDisks, q)
}

// MACAddresses returns the MAC addresses that match q (for example
// mac_address).
func (c *Client) MACAddresses(ctx context.Context, q url.Values) ([]MACAddress, error) {
	return list[MACAddress](ctx, c, pathMACs, q)
}

// Create creates an object at a list path and decodes it into out.
func (c *Client) Create(ctx context.Context, path string, body map[string]any, out any) error {
	return c.do(ctx, http.MethodPost, path, nil, body, out)
}

// Update changes fields of the object with that ID and decodes it into out
// (may be nil).
func (c *Client) Update(ctx context.Context, path string, objID int64, body map[string]any, out any) error {
	return c.do(ctx, http.MethodPatch, path+id(objID)+"/", nil, body, out)
}

// Delete deletes the object with that ID. message is stored in NetBox's change
// log.
func (c *Client) Delete(ctx context.Context, path string, objID int64, message string) error {
	var body any
	if message != "" {
		body = map[string]any{"changelog_message": message}
	}
	err := c.do(ctx, http.MethodDelete, path+id(objID)+"/", nil, body, nil)
	if err != nil {
		return fmt.Errorf("delete %s%d: %w", path, objID, err)
	}
	return nil
}
