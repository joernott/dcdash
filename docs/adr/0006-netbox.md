# ADR 0006: NetBox as a common data source

- Status: accepted
- Date: 2026-11-19
- Task: 12.1

## Context

Many data centers document their virtual machines, VLANs, prefixes and IP addresses in
NetBox. dcdash should use NetBox as a common data source and keep it up to date:

- **Read:** the creation wizard shows the names of VLANs and offers free IP addresses from the
  prefixes of a VLAN.
- **Write back:** changes made in dcdash (guests, disks, network devices, DHCP reservations,
  DNS names) update the matching NetBox objects.
- **Compare:** a view shows where NetBox and the connected systems differ, and lets the user
  update NetBox.

The calls below were checked against NetBox 4.7.5 (container `netboxcommunity/netbox:v4.7`,
PostgreSQL 17, Valkey 8) on the development VM.

## API

All calls go to `<url>/api/...` with JSON bodies. Lists are paginated (`limit`, `offset`;
the server limits `limit` to `MAX_PAGE_SIZE`, 1000 by default).

| Purpose | Call | Notes |
|---|---|---|
| Version, health | `GET /api/status/` | Needs a token (`403` without). Returns `netbox-version`. |
| Virtual machines | `/api/virtualization/virtual-machines/` | Filters `cluster_id`, `name`, `cf_<field>`. The name is unique per cluster and tenant. |
| Virtual disks | `/api/virtualization/virtual-disks/` | `virtual_machine`, `name`, `size` in MB. Once a VM has virtual disks, its `disk` field is their sum and can't be set. |
| VM interfaces | `/api/virtualization/interfaces/` | `virtual_machine`, `name`, `mode` (`access`, `tagged`), `untagged_vlan`, `primary_mac_address`. Filter `mac_address`. |
| MAC addresses | `/api/dcim/mac-addresses/` | Separate objects since NetBox 4.2, assigned with `assigned_object_type: virtualization.vminterface` and `assigned_object_id`; the interface's `primary_mac_address` points to one of them. Filter `mac_address` (case-insensitive). |
| IP addresses | `/api/ipam/ip-addresses/` | `address` with prefix length, `status`, `dns_name`, `assigned_object_*`, `vrf`. The VM's `primary_ip4`/`primary_ip6` point to an assigned address. Filter `address` (without length). |
| Prefixes | `/api/ipam/prefixes/` | Filter `vlan_id`. `GET .../{id}/available-ips/?limit=n` lists free addresses; `POST` there allocates one. |
| VLANs | `/api/ipam/vlans/` | Filters `vid`, `group_id`, `site_id`. |
| Clusters | `/api/virtualization/clusters/` | Filter `name`. |

Observed behaviour:

- **Tokens.** NetBox 4.7 creates v2 tokens; they are sent as
  `Authorization: Bearer nbt_<key>.<token>`. v1 tokens (`Authorization: Token <token>`) are
  deprecated since 4.6 and removed in 5.0. dcdash sends `Bearer` for tokens that start with
  `nbt_` and `Token` for all others.
- **Deleting cascades.** Deleting a VM interface deletes its IP addresses and MAC addresses;
  deleting a VM deletes its interfaces, disks and their addresses.
- **Unique addresses.** With the default `ENFORCE_GLOBAL_UNIQUE`, creating an address that
  exists in the same VRF fails: `Duplicate IP address found in global table: 192.0.2.10/24`.
- **Errors** are objects that map field names (or `__all__`) to messages, for example
  `{"__all__": ["Virtual machine name must be unique per cluster and tenant."]}`. Failed bulk
  writes return `{"detail": ..., "errors": [{"index": n, "errors": {...}}]}` and change
  nothing.
- **Change log.** A `changelog_message` field in the body is stored with the change of that
  object (not with the objects deleted along with it).

## Mapping

| dcdash | NetBox |
|---|---|
| Proxmox cluster | Cluster, mapped by name in the configuration. Guests of clusters that aren't mapped are not written to NetBox. |
| Virtual machine or container | Virtual machine in the mapped cluster. Found by the custom field with the VMID if one is configured (recommended), otherwise by name. `vcpus` = sockets × cores (VMs) or cores (containers), `memory` = memory in MiB. |
| Disk (`scsi0`, `virtio1`, ...; root disk `rootfs` and mount points `mpN` of containers) | Virtual disk with the same name, size in MiB. Detached disks are removed. |
| Network device `netN` | VM interface `netN` (for containers the name of the device inside, for example `eth0`), with the MAC address as primary MAC address and the VLAN as untagged VLAN (`mode: access`). |
| Static IP setting of a network device | IP address assigned to the interface; the first IPv4 and IPv6 address become the VM's primary addresses. |
| DHCP reservation | IP address with the reserved address, status `active`. If a VM interface in NetBox has the reservation's MAC address, the address is assigned to it. |
| DNS record `A` or `AAAA` | `dns_name` of the IP address with that content. Other record types aren't written. |
| VLAN in the wizard | VLAN with that VID, in the configured VLAN group or site if set. |

Objects that dcdash creates get a tag (`dcdash` by default), and every write carries a
`changelog_message` that names the dcdash user, so that NetBox's change log shows who caused
the change.

## Decision

- **One NetBox instance**, configured in a `netbox` section: URL, token (encrypted like other
  secrets), TLS (CA file or certificate fingerprint), the cluster mapping, optionally the
  custom field for the VMID, the VLAN group or site for VLAN lookups, the VRF and the tag.
  Write-back can be switched off (`write_back: false`) to use NetBox only for reading.
- **Write-back runs after the change succeeded**, in the same request or job, with its own
  timeout (15 seconds). If it fails, the change in dcdash stays, the response carries a
  `netbox` result with the error that the web interface shows, and the audit log records a
  `netbox.sync` entry with the result `failure`. In creation and deletion jobs it is a job
  step that can fail without failing the job. There is no retry queue: the comparison view
  shows what is missing and updates NetBox on request.
- **dcdash deletes only what it created.** Deleting a guest deletes the NetBox VM if it has
  the dcdash tag, otherwise it sets the status `decommissioning` (configurable: `on_delete:
  delete` deletes it in any case). Deleting a DHCP reservation deletes the address only if it
  has the tag, isn't assigned to an interface and has no DNS name. Deleting a DNS record
  clears the `dns_name` and keeps the address.
- **Power state isn't written.** Many installations keep stopped VMs `active` in NetBox; a
  new guest is created as `active`.
- **Wizard data.** The bridge and VLAN fields show the VLAN name from NetBox; the IP fields
  offer the next free address of the prefixes that belong to the VLAN. The address isn't
  reserved before the guest is created; if it was taken in the meantime, NetBox rejects it
  during the write-back and the user sees the error. dcdash checks for an existing address
  when the creation request arrives and warns about it.
- **Comparison view** per mapped cluster: guests that exist only in Proxmox or only in
  NetBox, and differences in name, vCPUs, memory, disks, interfaces (MAC address, VLAN) and
  addresses; DHCP reservations and DNS `A`/`AAAA` records whose address is missing in NetBox
  or has another DNS name. Users with permission can update NetBox from a row (create or
  update the VM, its disks and interfaces) and delete VMs that exist only in NetBox if they
  carry the dcdash tag.
- **Permissions.** `netbox:<name>:ipam:read` for VLAN names and free addresses in the wizard,
  `netbox:<name>:sync:read` for the comparison view and `netbox:<name>:sync:write` to update
  NetBox from it. The write-back itself needs no NetBox permission: it follows a change the
  user was allowed to make, and the audit log names the user.
- **Audit.** `netbox.sync` (write-back after a change, with the objects and the result) and
  `netbox.apply` (update from the comparison view).
- **Status.** The overview shows NetBox as a backend: reachable, version, token valid.
- **Tests.** A fake NetBox (`test/nbfake`) for unit and end-to-end tests that implements the
  calls above, including the cascading deletes and the duplicate check. Starting a real
  NetBox takes several minutes (database migrations), too long for every CI run: the
  integration tests against a real NetBox run when `DCDASH_IT_NETBOX_URL` is set, and
  `scripts/netbox-test.sh` starts a NetBox 4.7 container for them.

## Token permissions in NetBox

The token's user needs object permissions:

- **View:** `virtualization` clusters, virtual machines, interfaces, virtual disks; `ipam`
  IP addresses, prefixes, VLANs, VLAN groups, VRFs; `dcim` MAC addresses, sites; `extras`
  tags.
- **Add, change, delete:** `virtualization` virtual machines, interfaces, virtual disks;
  `ipam` IP addresses; `dcim` MAC addresses.

The tag is created by an administrator in NetBox beforehand. With `write_back: false` the
view permissions suffice.

## Not part of this sprint

Devices, cables and physical interfaces (switches follow in sprint 13), tenants, writing
prefixes or VLANs, importing NetBox objects into Proxmox, several NetBox instances, NetBox
webhooks (NetBox telling dcdash about changes).
