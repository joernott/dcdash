# NetBox

dcdash uses NetBox as a common data source:

- **Creation wizard:** the VLAN field shows the name of the VLAN in NetBox, and the address
  fields offer free addresses from the prefixes of the VLAN.
- **Write-back:** changes made in dcdash update NetBox: creating, changing and deleting guests
  (virtual machine, disks, network devices, static addresses), DHCP reservations and DNS
  `A`/`AAAA` records.
- **Comparison:** the NetBox page shows where NetBox and the connected systems differ, and
  updates NetBox on request.

dcdash works with NetBox 4.2 and later (MAC addresses as objects); it is tested with NetBox
4.7. The design is described in [ADR 0006](adr/0006-netbox.md).

## Prerequisites in NetBox

1. **A user and an API token for dcdash.** Prefer a v2 token (`nbt_<key>.<token>`, NetBox
   4.6 and later). Give the user object permissions (Admin → Permissions), see
   [Token permissions](#token-permissions) below.
2. **A tag** for the objects dcdash creates, `dcdash` by default (`netbox.tag`). dcdash
   doesn't create it.
3. **A cluster for each Proxmox cluster** whose guests are written to NetBox, mapped in
   `netbox.clusters`. Guests of other clusters are not written.
4. **Optional, recommended: an integer custom field** for virtual machines that holds the
   Proxmox VMID (`netbox.vmid_field`, for example `proxmox_vmid`). Without it, dcdash finds
   virtual machines by name; a guest renamed outside dcdash is then no longer found.
5. **VLANs with prefixes:** for the wizard, assign the prefixes of a VLAN to it in NetBox. If
   VLAN IDs are used more than once, restrict the lookup to a VLAN group or site
   (`netbox.vlan_group`, `netbox.site`).

### Token permissions

The object permissions of the dcdash user in NetBox. With `write_back: false`, only the view
action is needed for every row.

| Object types | Actions | Used for |
|---|---|---|
| Virtualization: Cluster | view | The mapped clusters, the comparison |
| DCIM: Site; IPAM: VLAN group, VRF | view | `netbox.site`, `netbox.vlan_group` and `netbox.vrf`, if set |
| IPAM: Prefix | view, change | Free addresses in the wizard and prefix lengths; change: project tags on prefixes |
| IPAM: IP range | view, change | The project page; change: project tags on IP ranges |
| Virtualization: Virtual machine, VM interface, Virtual disk; DCIM: MAC address; IPAM: IP address | view, add, change, delete | Writing guests, DHCP reservations and DNS names back; project tags on virtual machines and addresses |
| IPAM: VLAN | view, add, change, delete | VLAN names in the wizard; VLANs created and deleted on switches; project tags on VLANs |
| DCIM: Device | view, change | Switches (`switches[].netbox_device`) and the comparison of nodes and switches; change: "Update NetBox" sets the cluster, primary IP and serial number |
| DCIM: Interface | view, change | Switch ports and the primary IP of nodes; change: switch port changes, project tags on ports |
| Extras: Tag | view, add, change | The `dcdash` tag (create it beforehand); add and change: the project tags `project-<name>` |

dcdash deletes virtual machines and VLANs only if they have the `dcdash` tag. To enforce that
in NetBox, give the delete action for these two types in a separate permission with the
constraint `{"tags__slug": "dcdash"}`. Don't constrain the other types or the change action: dcdash also updates objects maintained by
hand, such as devices, existing virtual machines and interfaces.

Without switches, the add and delete actions on VLANs and change on interfaces aren't needed;
without projects, change on prefixes and IP ranges and add/change on tags aren't needed.

## Configuration

```yaml
netbox:
  url: https://netbox.example.com
  tls:
    ca_file: /etc/dcdash/netbox-ca.pem
  token: "file:/etc/dcdash/netbox.token"
  vmid_field: proxmox_vmid
  clusters:
    pve-lab: "Lab"
  vlan_group: dc1
```

All options are described in [configuration.md](configuration.md#netbox). The overview page
shows NetBox as a backend: reachable, version, and whether the tag, the VRF and the mapped
clusters exist.

## What dcdash writes

| Change in dcdash | NetBox |
|---|---|
| A guest is created | A virtual machine in the mapped cluster with the tag, status `active`, vCPUs (sockets × cores), memory, the VMID custom field; a virtual disk per disk; an interface per network device (`netN`, for containers the name inside, for example `eth0`) with the MAC address as primary MAC address and the VLAN as untagged VLAN; the static addresses on the interfaces, the first ones as primary addresses |
| Options, disks or network devices of a VM are changed | The same objects are updated: renamed, resized, created and deleted to match the guest. Existing interfaces are found by name or by their primary MAC address and keep their name (for example `ens18` maintained by hand). New interfaces get the tag; an interface without the tag is never deleted, so interfaces and addresses maintained by hand stay |
| A guest is deleted | The virtual machine is deleted if it has the tag, otherwise set to `decommissioning` (`netbox.on_delete`). NetBox deletes its interfaces, disks and addresses with it |
| A DHCP reservation is added or changed | An IP address with the description `DHCP reservation (<server>)`, assigned to the VM interface with the reservation's MAC address if NetBox has one |
| A DHCP reservation is deleted | Its address is deleted if dcdash created it (tag, not assigned, no DNS name); otherwise only the description is cleared |
| A switch port or VLAN is changed | The device interface or the VLAN, see [switches.md](switches.md#netbox) |
| An `A` or `AAAA` record is added or changed | The DNS name of the IP address; the address is created if needed |
| An `A` or `AAAA` record is deleted | The DNS name is cleared; the address stays |

Addresses get the prefix length of the longest NetBox prefix that contains them (in the
configured VRF), otherwise `/32` or `/128`. Every change carries a change log message that
names the dcdash user, for example `dcdash (alice): update app1`.

The power state is not written: a stopped guest stays `active` in NetBox.

When a guest is created, NetBox is updated after the guest is configured and before the DHCP
reservations and DNS records are added, so that they find its interfaces.

## When NetBox can't be updated

The change in dcdash stays. The web interface shows an error message, and the audit log
records a `netbox.sync` entry with the result `failure` and the error. In the creation job,
the step "Update NetBox" fails, but the job does not. dcdash doesn't retry by itself: open the
NetBox page and update the guest there.

A change that takes longer than a minute in Proxmox (the task is still running) is not
written; the comparison shows it afterwards. A guest whose deletion takes longer is removed
from NetBox when the task ends.

## Comparison

The NetBox page (menu "NetBox") compares, per mapped cluster, the guests with the virtual
machines in NetBox: name, vCPUs, memory, disks, interfaces with MAC address and VLAN, and the
static addresses. Interfaces are matched like the write-back does; interfaces without the tag that
match no network device are not compared. Each guest is:

- **Matches**: no differences.
- **Differs**: the differences are listed, Proxmox ("Actual") and NetBox side by side.
- **Missing in NetBox**: NetBox has no virtual machine for the guest.
- **Only in NetBox**: a virtual machine of the cluster without a guest.

"Update NetBox" writes the current state of a guest to NetBox. "Delete from NetBox" removes a
virtual machine that exists only in NetBox; this is offered only for virtual machines with the
dcdash tag. Both are audited as `netbox.apply`.

**Physical machines and switches.** Below the guests of a cluster, its Proxmox nodes are
compared with the NetBox device of the same name; the switches have a table of their own and
are compared with their device (`switches[].netbox_device`, or the switch name):

| | Compared | "Update NetBox" sets |
|---|---|---|
| Node | Cluster (the mapped NetBox cluster), primary IP (the address in `pvecm status`) | The cluster; the primary IP if the address is assigned to an interface of the device in NetBox |
| Switch | Model (by part number if both have one, otherwise the name), serial number of the first member | The serial number |

dcdash doesn't create devices (it doesn't know their type, role and site) and doesn't change
the device type: a different model shows "Change this in NetBox". Updates are audited as
`netbox.apply` with the target `node:<cluster>/<node>` or `switch:<name>`. A switch that
can't be reached is shown as "Not reachable".

"Compare DNS and DHCP" at the top right lists DHCP reservations whose address is
missing in NetBox, and `A`/`AAAA` records whose address is missing in NetBox or has another
DNS name there. It reads all reservations and all records, which can take a while.

## Permissions

| Permission | Allows |
|---|---|
| `netbox:<name>:ipam:read` | VLAN names and free addresses in the creation wizard |
| `netbox:<name>:sync:read` | The comparison view |
| `netbox:<name>:sync:write` | Updating NetBox from the comparison view |

`<name>` is `netbox.name`, `netbox` by default. The comparison lists only guests and nodes the user may
read in Proxmox, and only switches the user may read. Writing changes back needs no NetBox permission; see
[security.md](security.md#netbox-permissions).

## Testing against a real NetBox

`scripts/netbox-test.sh` starts NetBox 4.7 in a container and runs the integration test
against it; see [development.md](development.md).

## Troubleshooting

| Symptom | Cause |
|---|---|
| Overview: `Invalid v2 token` or `Invalid token` | Wrong token, or a v2 token on NetBox before 4.6 |
| Overview: `the tag "dcdash" doesn't exist` | Create the tag in NetBox, or set `netbox.tag` |
| Overview: `the cluster "..." doesn't exist` | The name in `netbox.clusters` doesn't match a NetBox cluster |
| Write-back: `Duplicate IP address found in global table` | The address exists in NetBox, assigned to something else; NetBox enforces unique addresses (`ENFORCE_GLOBAL_UNIQUE`) |
| Write-back: `the address ... is assigned to another interface` | A static address of the guest belongs to another device in NetBox |
| Write-back: `Virtual machine name must be unique per cluster and tenant` | Another virtual machine in the cluster has the name; with `vmid_field`, dcdash finds the guest's own virtual machine by its VMID |
| The wizard shows "This VLAN is not in NetBox." | No VLAN with that ID (in the configured VLAN group or site) |
