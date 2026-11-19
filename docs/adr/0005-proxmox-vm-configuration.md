# ADR 0005: Changing the configuration of virtual machines

- Status: accepted
- Date: 2026-11-18
- Task: 11.8

## Context

Users want to change existing VMs on the guest page: options such as `agent`, `boot` and
`onboot`, CPU and memory, disks (create, attach, detach, resize, delete) and network devices
(add, change, remove). Task 11.8 checks the API calls, how changes to running VMs behave, which
privileges the API token needs, and how dcdash limits the changes. Containers are not part of
this task.

The read calls were checked on the test cluster (Proxmox VE 9). The write calls follow the API
viewer of Proxmox VE 9; they are exercised against the fake API (`internal/proxmox/pvefake`) and
against stopped test VMs on a real cluster (task 11.12).

## API

| Purpose | Call | Notes |
|---|---|---|
| Configuration with pending changes | `GET /nodes/{node}/qemu/{vmid}/pending` | A list of `{key, value, pending, delete}`: `value` is the active value, `pending` a change waiting for a restart, `delete: 1` a pending removal (`2` if forced). Includes `digest`. |
| Change options, add or change disks and network devices | `POST /nodes/{node}/qemu/{vmid}/config` | Like `PUT`, but returns a task, which matters for disk allocation. `delete=key1,key2` removes keys, `revert=key1,key2` drops pending changes, `digest` rejects the change if the configuration changed in the meantime (synchronously, HTTP 500 "checksum mismatch (file change by other user?)"). |
| Grow a disk | `PUT /nodes/{node}/qemu/{vmid}/resize` | `disk=scsi1`, `size=+8G` or an absolute size; it can't shrink. Works on running VMs. Returns a task. |

Disks and network devices are configuration keys:

- **New disk:** `scsiN=<storage>:<GiB>[,discard=on][,ssd=1][,iothread=1][,backup=0]`. Proxmox
  allocates the volume and writes `scsiN=<storage>:vm-<vmid>-disk-<n>,size=<GiB>G,...`. Buses:
  `scsi` (0–30), `virtio` (0–15), `sata` (0–5), `ide` (0–3).
- **Detach:** `delete=scsiN` keeps the volume as `unusedN=<volid>`.
- **Attach** an unused disk: `scsiN=<volid>[,options]`. The `unusedN` key disappears.
- **Delete** a disk: `delete=unusedN` destroys the volume. An attached disk has to be
  detached first.
- **Network device:** `netN=<model>[=<MAC>],bridge=<bridge>[,tag=<VLAN>][,firewall=1][,link_down=1]`,
  models `virtio`, `e1000`, `e1000e`, `vmxnet3`, `rtl8139`. Without a MAC Proxmox generates one
  (prefix `BC:24:11`). Changing a device must repeat the MAC, or the guest gets a new one.

### Running VMs

What can be applied to a running VM depends on its `hotplug` option (default
`network,disk,usb`):

- Network devices and disks are hot-plugged: adding, detaching and changing bridge, VLAN,
  firewall and link state take effect at once. Changing the model of a network device is
  pending.
- CPU and memory changes are pending unless `hotplug` contains `cpu` or `memory` and `numa` is
  on.
- Options such as `boot`, `agent`, `ostype`, `bios` and `machine` are always pending.
- Pending changes take effect when the VM is started again: after a stop and start, or a
  reboot through Proxmox, but not after a reboot from inside the guest. `revert` drops them.

The fake API marks every change of a running VM as pending except network devices, disks and
the options that never need a restart (`name`, `description`, `tags`, `onboot`, `protection`).
dcdash shows what Proxmox reports and doesn't predict it.

## Privileges of the API token

`PVEVMAdmin` (as for task 10) contains the privileges `VM.Config.Options`, `VM.Config.CPU`,
`VM.Config.Memory`, `VM.Config.Disk`, `VM.Config.Network` and `VM.Config.HWType`.
`PVEDatastoreUser` on the storages covers the allocation of new disks (`Datastore.AllocateSpace`),
and `PVESDNUser` on the zones the use of bridges (`SDN.Use`).

## Decision

- **Permissions.** Changing options, disks and network devices needs `write` on `qemu/<vmid>`.
  Deleting a disk destroys data and needs `admin`, as deleting the guest does. Proxmox checks
  the privileges of the token in addition.
- **Limits.** Changes are only possible on clusters with a `provisioning` configuration: CPU
  and memory up to the limits, new disks only on allowed storages and up to the disk limit,
  network devices only on allowed bridges and VLANs.
- **Options.** Only a fixed list of options can be changed: `name`, `description`, `tags`,
  `onboot`, `protection`, `agent`, `boot`, `cores`, `sockets`, `memory`, `balloon`, `cpu`,
  `numa`, `ostype`, `hotplug`. Options that could run code on the host or need `root` (such as
  `args`, `hookscript`, `hostpci`) are not offered.
- **Concurrency.** Every change sends the `digest` of the configuration the user saw. If the
  configuration changed in the meantime, Proxmox rejects the change and dcdash asks the user
  to reload.
- **Tasks.** Changes use the asynchronous `POST .../config` and wait up to 60 seconds for the
  task. A change that takes longer is reported as still running; the task list shows its end.
- **Deleting a disk** of a stopped VM detaches it and destroys the volume in one action; the
  user types the disk name (for example `scsi1`) to confirm. A disk of a running VM has to be
  detached first.
- **MAC addresses** of new network devices are generated by dcdash (prefix `BC:24:11`, as
  Proxmox does), so that DHCP reservations can be made before the guest exists (task 11.14).
- **Audit.** `proxmox.guest.configure` (options, with the changed keys),
  `proxmox.disk.create`, `.resize`, `.attach`, `.detach`, `.delete` and
  `proxmox.network.create`, `.update`, `.delete`.
- **Not part of this task:** containers (mount points, network of containers), moving disks
  between storages, CD-ROM drives, USB and PCI devices.
