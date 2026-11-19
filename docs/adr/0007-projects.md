# ADR 0007: Projects

- Status: accepted
- Date: 2026-11-19
- Task: 12.8.1

## Context

The objects that belong to the same project (guests, addresses, DHCP reservations, DNS
records, VLANs, prefixes, IP ranges, switch ports) should be grouped by a tag `project-<name>`.
Projects are defined by administrators in files, one per project, so that more metadata can
be added later. dcdash shows a list of the projects and a page per project with all its
objects, and lets users assign objects to projects.

## Decision

### Project files

- One YAML file per project in `projects.dir` (default `/etc/dcdash/projects`), named
  `<name>.yaml`. The name is the file name without `.yaml`: lower-case letters, digits and
  `-`, at most 55 characters, so that the tag `project-<name>` is a valid Proxmox tag and
  NetBox slug.
- Fields: `title` (display name and name of the NetBox tag; default `project-<name>`),
  `color` (6 hex digits, the NetBox tag color; default `9e9e9e`), `description`. Unknown
  fields are rejected, so that typos are noticed; new metadata gets new fields.
- dcdash reads the directory at startup and every `projects.reload_interval` (default one
  minute). An invalid file is skipped and reported in the log and on the overview page; the
  other projects stay usable. A missing directory means no projects.
- The files are the only place where projects are created, changed and deleted. dcdash runs
  with a read-only `/etc` (systemd hardening) and doesn't write them.

### Tags

- **NetBox:** for every project, dcdash creates or updates the tag with slug
  `project-<name>`, name `title`, color and description, after each load. Tags with the
  `project-` prefix that have no file are reported as orphaned and not deleted: they may
  still be on objects. With `netbox.write_back: false`, tags are only read.
- **Proxmox:** a guest's projects are Proxmox tags `project-<name>` on the guest. They are the
  source of truth for guests and are written to the NetBox VM and its addresses with every
  write-back of the guest.
- An object can belong to several projects.

### Which objects carry the tag

| Object | Where the tag is | Assigned in dcdash |
|---|---|---|
| Guest | Proxmox tags; NetBox VM and the addresses on its interfaces | Creation wizard, guest page, project page |
| DHCP reservation | NetBox IP address of the reservation | Reservation dialog |
| DNS `A`/`AAAA` record | NetBox IP address of the record | Record dialog |
| IP address, IP range, prefix, VLAN | NetBox object | Project page |
| Switch port | NetBox device interface | Project page (from sprint 13 also the switch pages) |

Kea and the DNS providers have no place for tags, so reservations and records are grouped by
their NetBox addresses. Without NetBox, projects group only guests.

### Project pages

- **List:** all projects the user may see, with title, color, description and the number of
  objects; orphaned tags are listed for administrators.
- **Project page:** guests (from Proxmox, linked to the guest page), and from NetBox the
  virtual machines without a Proxmox guest, IP addresses (with DNS name and DHCP description),
  IP ranges, prefixes, VLANs and device interfaces. Objects can be added and removed.

### Permissions and audit

- `project:<name>:objects:read` shows a project; `project:<name>:objects:write` assigns
  objects to it and removes them. `*` as name covers all projects.
- Assigning a guest also needs `write` on the guest, a reservation `write` on its subnet, a
  record `write` on its zone. NetBox-only objects need only the project permission.
- Audit: `project.assign` and `project.unassign` (target `<project>`, details: object type,
  object, result), `project.tags` (tag synchronization to NetBox, with the created and
  changed tags).

## Consequences

- Assigning a project to a guest changes its Proxmox configuration (`tags`), which needs the
  same privilege as other option changes (`VM.Config.Options`).
- The project page queries NetBox by tag for each object type; large projects are paged by
  NetBox and loaded in full (limit 50,000 objects per type, as for other lists).
