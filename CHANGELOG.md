# Changelog

All notable changes to dcdash are listed here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/).

## [Unreleased]

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

[Unreleased]: https://github.com/joernott/dcdash/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/joernott/dcdash/releases/tag/v0.1.1
[0.1.0]: https://github.com/joernott/dcdash/releases/tag/v0.1.0
