# ADR 0004: Creating and deleting guests in Proxmox VE

- Status: accepted
- Date: 2026-11-17
- Task: 10.1

## Context

Release v0.1.1 lets users create virtual machines and containers and delete them (concept
review 2.7). Task 10.1 checks which Proxmox VE API calls dcdash needs, which privileges the API
token needs, and how dcdash limits what users may create.

The API was read on the test cluster (Proxmox VE 9.0.11, read-only token); the write calls
follow the API viewer of Proxmox VE 9 and are exercised against the fake API
(`internal/proxmox/pvefake`) and, manually, against a real cluster (task 10.9).

## API

| Purpose | Call | Notes |
|---|---|---|
| Free ID | `GET /cluster/nextid[?vmid=N]` | Returns the lowest free ID (as a string), or with `vmid` fails with 400 if `N` is taken. It also sees guests the token can't see. |
| Storages of a node | `GET /nodes/{node}/storage?enabled=1&content=images\|rootdir\|iso\|vztmpl` | `avail` and `total` in bytes |
| ISO images, container templates | `GET /nodes/{node}/storage/{storage}/content?content=iso\|vztmpl` | `volid` like `local:iso/debian-13.4.0-amd64-netinst.iso` |
| Bridges | `GET /nodes/{node}/network?type=any_bridge`, `GET /cluster/sdn/vnets` | `bridge_vlan_aware` |
| Clone a template | `POST /nodes/{node}/qemu/{template}/clone` | `newid`, `name`, `full`, `storage` (full clones only), `pool`; returns a task. Runs on the template's node. A linked clone keeps its disks on the template's storage. |
| Change the configuration | `PUT /nodes/{node}/qemu/{vmid}/config` | `cores`, `memory`, `net0`, `ciuser`, `cipassword`, `sshkeys`, `ipconfig0`, `nameserver`, `searchdomain`, `tags`, `description`; synchronous |
| Grow a disk | `PUT /nodes/{node}/qemu/{vmid}/resize` | `disk=scsi0`, `size=40G`; can't shrink. Returns a task (Proxmox VE 8 and later) |
| Create a VM | `POST /nodes/{node}/qemu` | `scsi0=<storage>:<GiB>`, `ide2=<volid>,media=cdrom`, `net0=virtio,bridge=..,tag=..`; returns a task |
| Create a container | `POST /nodes/{node}/lxc` | `ostemplate`, `rootfs=<storage>:<GiB>`, `net0=name=eth0,bridge=..,ip=..`, `password`, `ssh-public-keys`, `unprivileged`, `start`; returns a task |
| Start | `POST .../status/start` | as for power actions |
| Delete | `DELETE /nodes/{node}/{type}/{vmid}?purge=1&destroy-unreferenced-disks=1` | The guest must be stopped. `purge` also removes it from backup jobs, replication and HA. Fails if the guest is protected. |

Quirks:

- **`sshkeys`** of a VM must be URL-encoded inside the value (spaces as `%20`), and the form
  encodes it again. Containers take `ssh-public-keys` as plain text.
- **Cloud-init** settings only have an effect if the VM has a cloud-init drive
  (`ide2: local-lvm:cloudinit`). dcdash adds one to the clone if the template has none.
- A template's boot disk is the first disk of `boot: order=...`, or the first of `scsi0`,
  `virtio0`, `sata0`, `ide0` that isn't a CD-ROM.
- **Windows 11** needs UEFI, an EFI disk and a TPM: `bios=ovmf`, `machine=q35`,
  `efidisk0=<storage>:1,efitype=4m,pre-enrolled-keys=1`, `tpmstate0=<storage>:1,version=v2.0`.
- `/cluster/nextid` is a hint, not a reservation: two clients may get the same ID. Creating a
  guest with a taken ID fails.

## Privileges of the API token

In addition to the privileges for the inventory and power actions (see `proxmox.md`):

| Proxmox role | Path | For |
|---|---|---|
| `PVEVMAdmin` | `/vms` or a pool | `VM.Allocate` (create, delete), `VM.Clone`, `VM.Config.*` |
| `PVEDatastoreUser` | `/storage` or single storages | `Datastore.AllocateSpace` for disks, using ISO images and templates |
| `PVESDNUser` | `/sdn/zones` | `SDN.Use` to attach a network device to a bridge |

## Decision

- **Limits in the configuration.** Provisioning is off unless a cluster has a `provisioning`
  section. It lists the storages for disks, the storages for ISO images and container
  templates, the bridges, the allowed VLAN tags, the templates that may be cloned (by VM ID),
  the ID range for new guests, an optional pool, maximum CPU cores, memory and disk size, and
  the defaults of the creation wizard. dcdash checks every request against it; Proxmox checks
  the token's privileges in addition.
- **Permissions.** Creating and deleting a guest need `admin` on `qemu/<vmid>` or
  `lxc/<vmid>` (like zones in the DNS module). Cloning also needs `read` on the template.
  A role like `proxmox:lab:qemu/5*:admin` together with the ID range `5000-5999` gives a
  team its own range of VMs.
- **IDs.** Without a requested ID, dcdash takes the lowest ID of the range that is free in
  the inventory, free according to `/cluster/nextid?vmid=`, not reserved by another
  creation in progress, and allowed to the user. Without a range, it uses `/cluster/nextid`.
- **Jobs.** Creating a VM from a template takes several API calls (clone, wait for the
  task, configure, grow the disk, start). dcdash runs them in the backend as a **job**, so
  passwords never go back to the browser and a closed browser doesn't stop half-way. The
  browser polls the job. Jobs live in memory and are kept for an hour after they end; a
  restart of dcdash during a job stops it (the Proxmox task in progress finishes on its own).
  The audit entry `proxmox.guest.create` is written when the job ends, with its result.
- **Delete** is a single task: the guest must be stopped, the request carries the name
  typed by the user, and `purge` is the default.
- **Not in v0.1.1:** changing the configuration of existing guests, more than one disk or
  network device, SDN subnets and IPAM, migrating guests, cloud-init on VMs installed from an
  ISO image, privileged containers.
