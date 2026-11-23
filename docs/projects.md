# Projects

A project groups the objects that belong together: virtual machines and containers, IP
addresses, DHCP reservations, DNS names, VLANs, prefixes, IP ranges and switch ports. They are
marked with the tag `project-<name>` in Proxmox and NetBox. The design is described in
[ADR 0007](adr/0007-projects.md).

## Defining projects

Each project is a YAML file `<name>.yaml` in the project directory (`projects.dir`, default
`/etc/dcdash/projects`). The name is the file name: lower-case letters, digits and `-`, at
most 55 characters.

```yaml
# /etc/dcdash/projects/webshop.yaml
title: Webshop relaunch   # display name and name of the NetBox tag; default project-<name>
color: 2196f3             # color of the NetBox tag (6 hex digits); default 9e9e9e
description: The new web shop
groups:                   # Active Directory groups of the project, by directory
  lab: [shop-admins, shop-devs]   # directories[].name: [sAMAccountName, ...]
```

All fields are optional; unknown fields are refused, so that typos are noticed. Titles must
be unique, because NetBox tag names are. Later versions of dcdash will read more metadata
from these files.

dcdash reads the directory at startup and every `projects.reload_interval` (default one
minute), so new and changed files take effect without a restart. An invalid file is skipped;
the overview page and the projects page show the error. dcdash never writes the files.

Deleting a file removes the project from dcdash. Its tag stays in NetBox and on the objects;
the projects page lists it as orphaned. Delete it in NetBox when it is no longer needed.

### Active Directory groups

`groups` names the groups of the project per directory configured under
[`directories`](configuration.md#directories). The project page lists them with their
description and direct members. Members are only shown to users who may read the group
(`directory:<directory>:group/<name>:read`); others see the group name only. A group that
doesn't exist, or a directory that isn't configured, is reported on the page. The groups are
not changed by dcdash; manage their members on the Directories page.

## Tags in NetBox

For every project, dcdash creates the NetBox tag `project-<name>` with the title, color and
description of the file and keeps it up to date. This needs NetBox with `write_back: true`
and the permission to add and change tags (see [netbox.md](netbox.md)). The audit log records
the changes as `project.tags`.

## Assigning objects

| Object | Where | Stored as |
|---|---|---|
| Virtual machine or container | Creation wizard (last step), guest page ("Change projects") | Proxmox tag `project-<name>`; written to the NetBox VM and the addresses on its interfaces |
| DHCP reservation | Reservation dialog | Tag on the NetBox addresses of the reservation |
| DNS `A`/`AAAA` record | Record dialog | Tag on the NetBox address of the record |
| VLAN, prefix, IP address, IP range, switch port, NetBox VM without guest | Project page, "Add NetBox objects" | Tag on the NetBox object |
| Switch port | Switches page, "Change projects" in the port list (with `switches[].netbox_device`) | Tag on the NetBox interface of the device |

An object can belong to several projects. The project tag of a guest is a normal Proxmox tag
and appears in the Proxmox web interface; dcdash checks the project permission also when the
tags are changed in the options of a VM.

The projects of the addresses of a guest follow the guest: when the guest is written to
NetBox, its addresses get the guest's projects, also addresses of DHCP reservations assigned
to its interfaces.

Without NetBox, projects group only guests.

## Project pages

"Projects" in the menu lists the projects the user may see. Users with access to all projects
also see invalid project files, synchronization errors and orphaned tags.

The page of a project shows its guests (from all Proxmox clusters, linked to their pages;
guests the user may not see are listed without details) and its NetBox objects by type: VMs
without guest, IP addresses (with DNS name and DHCP description), IP ranges, prefixes, VLANs
and switch ports. Objects in NetBox link to NetBox. "Add NetBox objects" searches NetBox and
adds objects to the project; "Remove" removes them.

## Permissions

| Permission | Allows |
|---|---|
| `project:<name>:objects:read` | Seeing the project and its page |
| `project:<name>:objects:write` | Assigning objects to the project and removing them |

`project:*:objects:write` covers all projects. Assigning a guest also needs `write` on the
guest, a reservation `write` on its subnet, a record `write` on its zone. See
[security.md](security.md#project-permissions).

## Audit

| Action | Recorded when |
|---|---|
| `project.assign`, `project.unassign` | A guest or a NetBox object was added to or removed from a project; `target` is the project, `details` name the object |
| `project.tags` | dcdash created or changed project tags in NetBox |
| `netbox.sync` | The projects of reservation and record addresses are part of the write-back (`details.changes`) |
