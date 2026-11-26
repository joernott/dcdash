# Changelog

All notable changes to dcdash are listed here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.5.0] - 2026-11-26

### Changed

- **Messages:** a framed close button; error messages stay until closed.
- **Top bar:** no "Overview" entry ("dcdash" leads there); the modules in the order Projects,
  Directories, NetBox, Proxmox, DNS, DHCP, Firewalls, Switches; Audit log and Sessions on the
  right next to the documentation.
- **DNS page:** UI, SSH and RDP buttons next to the name of each server
  (`GET /api/v1/dns/providers` lists the `links`); one color for all DNS servers, the one of the
  overview tiles.
- **Tables** have the color of their system, every other row in a lighter shade.
- **DHCP page:** like the DNS page, all servers with their subnets and UI/SSH/RDP buttons
  (`GET /api/v1/dhcp/servers` lists the `links`); the server field filters, the lease search
  sits next to it at the top right. The leases button of a subnet replaces the "Leases of a
  subnet" field; reservations open per server.
- **NetBox page:** all clusters (the cluster field filters), refresh as an icon and "Compare
  DNS and DHCP" at the top right. The Proxmox nodes of each cluster and the switches are compared
  with their NetBox devices (`GET /api/v1/netbox/compare/nodes/{cluster}`, `/compare/switches`);
  "Update NetBox" sets the cluster, primary IP and serial number. The NetBox token needs to
  change devices for that.
- **Firewalls and Directories pages:** like the Switches page, an overview of all firewalls or
  domains (the field at the top right filters, refresh next to it); the name opens the
  details with the tabs. `GET /api/v1/directories` lists the `links`.
- **Switches page:** the switch filter, the device search and refresh on the line of the
  heading.

## [0.4.0] - 2026-11-24

### Upgrade notes

- **The configuration is a directory now:** `/etc/dcdash/config.d`, whose `*.yaml` files are
  merged in alphabetical order (sections key by key, lists joined, a value set differently in
  two files is an error). An upgraded installation keeps reading `/etc/dcdash/config.yaml`
  as long as it exists, with a warning; the package installs the example files into
  `/etc/dcdash/config.d` (the rpm restores a changed `config.yaml` that rpm saved as
  `.rpmsave`). Move your settings into the directory and delete `config.yaml`, see
  [docs/configuration.md](docs/configuration.md#upgrading-from-dcdash-03). `--config` and
  `DCDASH_CONFIG` accept a directory or, as before, a single file.

### Added

- **Configuration directory** with an example of one file per section, `00_server.yaml` to
  `80_switches.yaml`, including the `directories` section and the AD DNS provider that the
  0.3 example lacked. `dcdash config validate` and the start log list the files read; errors
  name the file and line.

### Changed

- **DNS page:** lists every provider with its zones and its own "Create zone" button instead
  of only the first one; the provider selection is an optional filter, and the zone search
  covers all shown providers.
- **Buttons:** the switch overview and the Proxmox node tiles have the same UI and SSH buttons
  as the overview page; the node tiles also show "Shell" as such a button. Node links come
  from the new `proxmox.clusters[].node_links` (with `{node}`) or from the endpoint named
  after the node. UI buttons open a new tab.
- **Overview:** every DNS tile has the color of bind9; the bottom line names the provider.
- **Switches page:** shows all switches instead of redirecting to the first one; the overview
  has no tabs, the selection only filters it, and the device search is on the same line.
  The switch name (larger now) opens the details page with the ports and VLANs. One refresh
  button at the top right replaces those of the panels.
- **Login page:** a top bar with the name, the version and the display settings; below it the
  login tile and, if a domain offers the self-service password change, a second tile with a
  button per domain that opens the password change with this domain preselected.

### Fixed

- **Security:** gorilla/websocket updated to the commit that fixes GO-2026-6278 (weak random
  mask keys of WebSocket client frames, used by the Proxmox console).
- **AD DNS:** if none of the DNS containers below `base_dn` exists or can be read, the tile and
  the DNS page show an error naming them instead of an empty zone list.
- **Directories:** `managed_ous` are compared as DNs: spaces after commas and a different case
  are allowed; entries that aren't DNs are rejected.
- **Overview:** the heading, the UI/SSH/RDP buttons and the status of a tile are on one line
  again.

## [0.3.0] - 2026-11-24

Active Directory and Samba: users, groups, AD-integrated DNS, a self-service password change
and the groups of projects.

### Added

- **Active Directory and Samba** (`directories`), without software on the domain controllers
  ([ADR 0010](docs/adr/0010-active-directory.md), [docs/directories.md](docs/directories.md)):
  - A Directories page:
    - users: create, change, disable and enable, unlock, reset passwords, delete;
    - groups: create, change, rename, delete, members.
  - Protection: built-in and privileged objects need `admin`; objects outside `managed_ous`
    are read-only.
  - Permissions `directory:<name>:user/<account>:...` and `group/<account>`.
  - An overview tile per domain.
- **Self-service password change** at `/password`, linked from the login page: users of a
  domain change their password without a dcdash login, also when it expired or must be
  changed. Unknown users and wrong passwords get the same answer; failures are rate-limited.
- **AD-integrated DNS:** DNS provider type `ad`.
  - Zones and records are read via LDAP; records are changed with GSS-TSIG signed updates.
  - Zones are created and deleted with `manage_zones`.
- **Project groups:** project files can name AD groups per directory; the project page shows
  them with their members.
- A Samba AD DC in the integration and end-to-end test environment.

### Fixed

- A test of certificate pinning failed for 1 in 256 random certificates.

### Changed

- Frontend dependencies updated (Dependabot): Vue 3.5.46, vue-i18n 11.4.23, vue-router
  5.5.1; for development and tests Playwright 1.65, vitest 5, jsdom 31, @types/node 26 (as
  Node 26 in `.node-version`), oxlint 1.94 and Prettier 3.10.

## [0.2.1] - 2026-11-23

A more compact, colored overview with links to the systems, icons on the buttons, and the
complete permission reference.

### Added

- **Links on the overview tiles:** optional settings `ui`, `ssh` and `rdp` for the LDAP
  directory, Proxmox clusters, DNS providers, DHCP servers, NetBox, switches and firewalls.
  The tile shows a button for each: the web interface, an `ssh://` link, and a Remote Desktop
  file (`.rdp`). The status API returns them as `links`.
- **Permission reference:** [docs/permissions.md](docs/permissions.md) lists every permission
  a role can contain, with example roles; a test keeps it in line with the code.
- **Dependabot** for the Go modules, the npm packages, the GitHub Actions and the container
  base image.
- A build check that the web interface loads nothing from other hosts.
- [NOTICE.md](NOTICE.md) with the third-party works that need attribution.
- **Overview checks:** a check that fails with an HTTP 4xx response, such as 408 from a Kea
  control agent, is repeated once after `status.retry_delay` (default 1 s) before the tile
  shows an error.

### Changed

- **Overview:** up to six tiles per row, in the colors of their systems (Proxmox, bind9,
  Cloudflare, Kea, NetBox, switches, OPNsense), instead of the group headings. The bottom line
  names the system. The status API returns the DNS provider type as `type`.
- **DNS:** the rows of zones and records have the color of the provider.
- **Icons instead of text** on many buttons (Font Awesome Free): power actions and console of
  VMs and containers, Ctrl+Alt+Del, refresh, search, add, edit, delete, show leases, and the
  firewall's restart, enable and disable. The text stays as tooltip and accessible name.
- The built-in documentation also shows the NetBox, project, switch, firewall and
  permission pages.

### Fixed

- Proxmox: `read` on `task/<node>/<id>` returned an empty response instead of the task.

## [0.2.0] - 2026-11-20

NetBox as the common data source, projects, and the network: Aruba CX switches and OPNsense
firewalls.

### Added

- **NetBox:** NetBox 4.2 or later as a common data source (`netbox` section). The creation
  wizard shows VLAN names and offers free addresses from the VLAN's prefixes. Creating,
  changing and deleting guests, DHCP reservations and DNS `A`/`AAAA` records are written back
  to NetBox; a failed write-back doesn't undo the change, it is shown and audited as
  `netbox.sync`. A NetBox page compares NetBox with Proxmox, Kea and DNS and updates NetBox
  on request (`netbox.apply`). Interfaces maintained by hand are found by name or MAC address and
  never deleted. Permissions `netbox:<name>:ipam:read` and
  `netbox:<name>:sync:read`/`write`.
- **Firewalls:** OPNsense firewalls in the `firewalls` section: version and gateways on the
  overview page; a Firewalls page with gateways and services (restart), WireGuard peers
  (enable, disable) and IPsec tunnels (restart), aliases and filter rules. Rule changes are
  applied with a savepoint and rolled back by OPNsense if they cut off dcdash. Permissions
  `firewall:<name>:system|wireguard/<peer>|ipsec/<connection>|alias/<name>|rule/<interface>:<action>`,
  audited as `firewall.*`.
  OPNsense 26.7 and later have no savepoints: there, a lockout guard keeps dcdash from
  changing the rules of the interface it reaches the firewall through, floating rules and rules
  on interface groups (`protected_interfaces` for more).
- **Switches:** Aruba CX switches (AOS-CX 10.x) in the `switches` section: inventory and
  health on the overview page; a Switches page with a compact overview of all switches like
  their front panels, the ports (state, VLANs, LLDP neighbors,
  PoE), port changes (enable, disable, description, access and trunk VLANs), VLAN management
  on one or several switches and a MAC address search across all switches. Every change is
  saved to the startup configuration and audited (`switch.port.update`, `switch.vlan.*`);
  with `netbox_device` it is written to NetBox, and ports show and change their projects.
  Permissions `switch:<name>:port/<port>:read|operate|write`, `switch:<name>:vlan/<id>:write`.
- **Projects:** projects are defined as YAML files in `/etc/dcdash/projects` (`projects`
  section) and mark their objects with the tag `project-<name>`, synchronized to NetBox.
  Guests (Proxmox tags, set in the wizard and on the guest page), DHCP reservations and DNS
  records (their NetBox addresses), VLANs, prefixes, IP addresses and ranges and switch ports
  are assigned to projects; a projects page and a page per project list all their objects.
  Permissions `project:<name>:objects:read`/`write`, audited as `project.assign`,
  `project.unassign` and `project.tags`.

### Changed

- **API:** deleting a DHCP reservation or a DNS record answers `200` with the NetBox result
  instead of `204` when the change is written to NetBox; responses of changes contain a
  `netbox` field.

## [0.1.2] - 2026-11-18

Polish of v0.1.0: changing virtual machines, LDAP failover, the built-in documentation and
fixes from the first deployment.

### Added

- **Changing VMs:** the page of a VM shows its options, disks and network devices. Users with
  `write` on the VM change options (for example memory, cores, boot order, guest agent), add,
  grow, detach, attach and delete disks, and add, change and remove network devices, within
  the limits of the cluster's `provisioning` section. Changes Proxmox can't apply to a running
  VM are shown as pending and can be discarded. Deleting a disk needs `admin` and the disk name.
  Audited as `proxmox.guest.configure`, `proxmox.disk.*` and `proxmox.network.*`.
- **Creation wizard:** up to 8 network devices per guest, each with bridge, VLAN, IP settings
  and a MAC address that is generated when the device is added. Optional steps add DHCPv4 and
  DHCPv6 reservations for the devices and A, AAAA and PTR records; the job adds them before
  the guest starts.
- **LDAP failover:** `auth.ldap.servers` lists several directory servers, each with an optional
  certificate fingerprint. dcdash fails over on network and TLS errors; the overview names
  servers that are not reachable.
- **Documentation menu:** the user documentation is built into the binary and shown in the
  web interface, with working links between the pages.

### Changed

- **Sessions** end after 8 hours without requests instead of 30 minutes
  (`auth.session.idle_timeout`). A tab in the background no longer logs the user out after half
  an hour. The end of a session is logged with its reason.
- **Layout:** the pages use more of wide screens, and action columns size to their buttons, so
  that they fit in one line in English and German (Proxmox guests, DNS zones and records, DHCP,
  sessions). The German "Usage" column says "RAM".
- **DHCP page:** the server selection is below the heading; the server status is shown on the
  overview page only.
- **API:** a create request takes `networks` (a list) instead of `network`; `network` still
  works.

## [0.1.1] - 2026-11-17

Creating and deleting virtual machines and containers in Proxmox VE.

### Added

- **Proxmox provisioning:** create virtual machines from templates (full or linked clones with
  cloud-init: user, password, SSH keys, IP settings, DNS) or from ISO images, and containers
  from container templates, in a wizard that shows the progress of each step. Delete stopped
  guests after typing their name, optionally removing them from backup jobs, replication and
  HA. A `provisioning` section per cluster limits storages, bridges, VLANs, templates, the ID
  range and the resources; creating and deleting need `admin` on the guest. Audited as
  `proxmox.guest.create` and `proxmox.guest.delete`.

## [0.1.0] - 2026-11-17

The first release: one web interface for Proxmox VE, DNS and DHCP, with directory login,
TOTP, roles and an audit log.

### Added

- **Server:** a single static binary with the web interface built in. HTTPS with
  certificates from files (reloaded when they change) or from an ACME CA such as Let's Encrypt
  or step-ca, an optional HTTP-to-HTTPS redirect, `/healthz` and `/readyz`, structured logging.
- **Login:** LDAP and Active Directory, including nested AD groups, with TLS verification or
  certificate pinning. A local break-glass account for emergencies. Sessions with idle and
  absolute timeouts, CSRF protection, lockout after failed attempts per user and per client
  address.
- **TOTP:** required per role (always for `root`), enrollment in the web interface, recovery
  codes, reset by an administrator or on the command line.
- **Roles and permissions:** roles mapped from directory groups, permissions of the form
  `module:instance:resource:action` with wildcards, an optional TOTP requirement per role.
- **Audit log:** every login and every change, searchable in the web interface, optionally
  also written to a file or to syslog.
- **Web interface:** English and German, light and dark mode, user preferences stored on the
  server, an overview with the state of all backends, administration of sessions and the
  audit log.
- **Proxmox VE:** several clusters with failover between endpoints, inventory of nodes,
  virtual machines and containers, power actions with task progress, the VNC console (noVNC),
  container terminals and node shells (xterm.js) through dcdash.
- **DNS:** Cloudflare and bind9 (dynamic updates with TSIG). Records of zones, creating,
  changing and deleting them with protection against concurrent changes. For bind9 also
  creating and deleting zones (rndc, SFTP, catalog zone).
- **DHCP:** Kea 3.0 DHCPv4 and DHCPv6 over the daemons' own control sockets: subnets with
  utilization, leases with search and deletion, reservations (changeable with a host
  database), HA pairs.
- **Monitoring:** optional Prometheus metrics (`/metrics`) with request, audit, certificate
  expiry and backend metrics.
- **Command line:** `serve`, `config validate`, `version`, `gen-key`, `hash-password`,
  `totp enroll`/`reset`, `reencrypt`.
- **Packages:** Linux binaries for amd64 and arm64, `.deb` and `.rpm` packages with a systemd
  unit, and a container image.

### Security

- A security review before the release, with fixes for DNS name validation, lockout under
  parallel requests, console and session handling. See
  [docs/security.md](https://github.com/joernott/dcdash/blob/v0.1.0/docs/security.md#security-review) for the results and the known
  limitations.

[Unreleased]: https://github.com/joernott/dcdash/compare/v0.5.0...HEAD
[0.5.0]: https://github.com/joernott/dcdash/releases/tag/v0.5.0
[0.4.0]: https://github.com/joernott/dcdash/releases/tag/v0.4.0
[0.3.0]: https://github.com/joernott/dcdash/releases/tag/v0.3.0
[0.2.1]: https://github.com/joernott/dcdash/releases/tag/v0.2.1
[0.2.0]: https://github.com/joernott/dcdash/releases/tag/v0.2.0
[0.1.2]: https://github.com/joernott/dcdash/releases/tag/v0.1.2
[0.1.1]: https://github.com/joernott/dcdash/releases/tag/v0.1.1
[0.1.0]: https://github.com/joernott/dcdash/releases/tag/v0.1.0
