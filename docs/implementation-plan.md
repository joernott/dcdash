# Implementation Plan — dcdash

Based on [concept.md](concept.md) and the decisions in [concept-review.md](concept-review.md).

## Workflow

- **Sprints and branches.** Each sprint is developed on its own branch
  `sprint/NN-<topic>`, forked from `main`. When the sprint is complete, the branch is
  **squash-merged** into `main`, and the next sprint branch is forked from the updated
  `main`. Sprint branches are kept as documentation and are never deleted.
- **Commits.** Each task gets one or more commits. Commit messages start with the task
  number, for example `2.3: Add ACME renewal loop`.
- **Numbering.** Tasks are numbered `<sprint>.<task>`. If a task turns out to need extra
  work, the plan gets a subtask `<sprint>.<task>.<n>` (for example `1.1.1`) and is amended in
  the same branch.
- **Progress.** Completed tasks are checked (`[x]`). A sprint is complete when all its tasks
  and subtasks are checked.
- **Prompts.** Every prompt is recorded in [zz_prompts.md](zz_prompts.md).

## Definition of done

These apply to every task:

- Go code is linted and covered by tests. API changes are reflected in `api/openapi.yaml`.
- Every user-visible text is translated into **English and German**. Translation keys are
  complete, and CI checks this.
- Every UI element works and is readable in **light and dark mode**.
- State-changing actions are checked for permission and written to the audit log.

## Overview

| Sprint | Branch | Goal | Release | Status |
|---|---|---|---|---|
| 0 | `sprint/00-planning` | Concept review, plan, conventions | – | done |
| 1 | `sprint/01-foundation` | Repo skeleton, config, HTTP server, frontend scaffold, CI | v0.1.0 | done |
| 2 | `sprint/02-tls` | Certificates: static files, ACME (Let's Encrypt, step-ca) | v0.1.0 | done |
| 3 | `sprint/03-auth` | State store, LDAP login, sessions, break-glass account | v0.1.0 | done |
| 4 | `sprint/04-mfa-rbac` | TOTP per role, roles and permissions, audit log, user preferences | v0.1.0 | done |
| 5 | `sprint/05-ui-shell` | Frontend shell: i18n (en/de), dark mode, login, layout, admin views | v0.1.0 | done |
| 6 | `sprint/06-proxmox` | Proxmox: inventory, power actions, console | v0.1.0 | done |
| 7 | `sprint/07-dns` | DNS: Cloudflare and bind9, zones and records | v0.1.0 | done |
| 8 | `sprint/08-dhcp` | DHCP: Kea v3 DHCPv4 and DHCPv6 | v0.1.0 | done |
| 9 | `sprint/09-release` | Hardening, documentation, packaging, **v0.1.0** | v0.1.0 | done |
| 10 | `sprint/10-proxmox-provisioning` | Creating and deleting VMs and containers, **v0.1.1** | v0.1.1 | done |
| 11 | `sprint/11-vm-config-and-ui` | LDAP failover, VM configuration, built-in documentation, layout fixes, **v0.1.2** | v0.1.2 | done |
| 12 | `sprint/12-netbox` | NetBox as common data source with write-back, projects | v0.2.0 | done |
| 13 | `sprint/13-aruba-cx` | Aruba CX 6x00 switches: VLANs, ports, MAC search | v0.2.0 | done |
| 14 | `sprint/14-opnsense` | OPNsense firewalls: health, VPN, aliases, rules, **v0.2.0** | v0.2.0 | done |
| 15 | `sprint/15-overview-links` | Overview: compact colored tiles with UI, SSH and RDP links; icons on buttons; permission reference | v0.2.1 | done |
| 16 | `sprint/16-active-directory` | Active Directory and Samba: users, groups, AD DNS, self-service password change, project groups | v0.3.0 | done |
| 17 | `sprint/17-config-directory` | Configuration directory with merged files; directories in the example configuration | v0.4.0 | done |
| 18 | `sprint/18-page-layout` | Common page layout: filters and refresh in the top line, all servers on one page, closable messages, colored tables, navigation order | – | in progress |

---

## Sprint 0 — Planning

- [x] **0.1** Create the prompt log `docs/zz_prompts.md`
- [x] **0.2** Review the concept (`docs/concept-review.md`)
- [x] **0.3** Create the implementation plan (this document)
- [x] **0.4** Add `CLAUDE.md` with the project conventions and workflow rules
- [x] **0.5** Resolve the open questions Q1–Q8 from the concept review and amend the plan
  if needed
  - [x] **0.5.1** Incorporate the review feedback: VM and container creation moved to
    v0.1.1 (new Sprint 10), zerolog for logging
  - [x] **0.5.2** Add the new requirements to the plan: German translation, dark mode,
    Proxmox console, DNS zone management, TOTP per role
  - [x] **0.5.3** Decide how bind9 zone files are provisioned: SFTP, because dcdash can't be
    assumed to run on the BIND primary

## Sprint 1 — Foundation

- [x] **1.1** Repository layout: Go module, `cmd/dcdash`, `internal/...`, `web/`, `docs/`,
  `LICENSE` (GPLv3), SPDX headers, `.editorconfig`, `.gitignore`
- [x] **1.2** Build tooling: Makefile or Taskfile (build, test, lint, dev), pinned Go and
  Node versions
  - [x] **1.2.1** Development VM: pinned toolchain versions (`.go-version`,
    `.node-version`, `.golangci-lint-version`) and the provisioning script
    `scripts/setup-dev-vm.sh` for Debian 13 (Go, Node.js, golangci-lint, Docker). Needed
    because the Windows workstation can't run the toolchain
- [x] **1.3** CLI skeleton: `serve`, `version`, `config validate`
- [x] **1.4** Configuration: YAML schema and loader with defaults and validation, secret
  references (`env:NAME`, `file:/path`), permission warning for world-readable files, example
  `config.example.yaml`
- [x] **1.5** Logging with `rs/zerolog`: JSON or console output, configurable level, request
  ID in log context
- [x] **1.6** HTTP server: router, middleware (request ID, logging, panic recovery, security
  headers), graceful shutdown, `/healthz`, `/readyz`, `/api/v1/version`
- [x] **1.7** Frontend scaffold: Vue 3, TypeScript, Vite, Tailwind CSS v4 (class-based dark
  variant), Pinia, Vue Router, vue-i18n (en and de), ESLint and Prettier, Vitest
- [x] **1.8** Embed the frontend build with `go:embed`, SPA fallback routing, dev proxy setup
  (Vite → Go backend)
- [x] **1.9** OpenAPI 3 spec skeleton (`api/openapi.yaml`) and a TypeScript client
  generated from it
- [x] **1.10** CI with GitHub Actions: Go lint and test, frontend lint, test and build,
  translation key completeness check, combined binary build
- [x] **1.11** Developer documentation: `docs/development.md`

## Sprint 2 — TLS and Certificates

- [x] **2.1** Certificate manager interface with hot reload through
  `tls.Config.GetCertificate`
- [x] **2.2** Provider for static files: load the PEM cert, chain and key, and reload when
  the files change
- [x] **2.3** ACME provider (lego): account registration and storage, configurable
  directory URL, additional trusted root CA (for step-ca)
- [x] **2.4** DNS-01 solvers: Cloudflare (API token) and RFC 2136 (bind9 with TSIG)
- [x] **2.5** Renewal loop: renew before expiry, retry with backoff, log events, expose
  certificate expiry in `/readyz` (the metric follows with the metrics endpoint in 9.2)
- [x] **2.6** HTTP→HTTPS redirect listener, TLS 1.2 minimum, secure cipher defaults, HSTS
- [x] **2.7** Integration tests against Pebble, and against step-ca where possible
- [x] **2.8** Documentation: certificate configuration for all three modes

## Sprint 3 — State Store and Authentication

- [x] **3.1** State store: SQLite (`modernc.org/sqlite`), migrations, data directory from
  the config
- [x] **3.2** Encryption of secrets at rest: AES-GCM, key from the config or a key file,
  key rotation concept documented
- [x] **3.3** LDAP client: LDAPS and StartTLS, custom CA, service bind, user search and
  bind, connection timeouts
- [x] **3.4** Group resolution: AD nested groups (matching rule in chain) and generic LDAP
  (`memberOf` or group search), groups given as CN or DN
- [x] **3.5** Sessions: server-side store, opaque cookie, absolute lifetime and idle
  timeout from the config, logout, cleanup job
- [x] **3.6** CSRF protection for state-changing requests
- [x] **3.7** Login rate limiting and temporary lockout per user and per source IP
  - [x] **3.7.1** Client IP behind reverse proxies: `server.trusted_proxies`; `X-Forwarded-For`
    is only used when the request comes from a trusted proxy (otherwise all clients would
    share the proxy address and the per-IP lockout would lock everyone out)
- [x] **3.8** API: `POST /auth/login`, `POST /auth/logout`, `GET /auth/me`
- [x] **3.9** LDAP integration tests:
  - AD mode against the existing Samba AD server in the network, with connection data and
    test accounts from environment variables. The tests are skipped when these aren't set
    (for example in GitHub Actions, which can't reach the internal network).
  - Generic LDAP mode against an OpenLDAP container, which also runs in CI.
- [x] **3.10** Break-glass local account: disabled by default, password hash in the config,
  always holds `root` and always requires TOTP, every use is audited and logged as a warning

## Sprint 4 — TOTP, Authorization, Audit and Preferences

- [x] **4.1** Two-step login: password first, then TOTP when required, with a session that
  only becomes fully authenticated after the second step
  - [x] **4.1.1** Sessions that wait for the TOTP step or the enrollment expire after
    10 minutes
- [x] **4.2** TOTP requirement per role (`require_totp`, default `true`): required if any of
  the user's roles requires it; always required for `root` and the break-glass account;
  enforced once a user has enrolled voluntarily
  - [x] **4.2.1** Integration test against the Samba AD: a member of `dashroot` enrolls TOTP,
    gets the root role and logs in again with the TOTP step
- [x] **4.3** TOTP enrollment: generate the secret and QR code, confirm with one code, store
  it encrypted
  - [x] **4.3.1** `dcdash totp enroll`: the break-glass account enrolls on the server only, so
    that its password alone never grants access
  - [x] **4.3.2** `dcdash reencrypt`: re-encrypt stored secrets after a key rotation (promised in
    `docs/security.md`)
- [x] **4.4** Recovery codes: generate them, store them hashed, single use
- [x] **4.5** TOTP replay protection and lockout after repeated failures
- [x] **4.6** Role model: built-in `root` (group `dashroot`), roles from the config with
  group mappings, `require_totp` and permission strings
- [x] **4.7** Permission engine: the `module:instance:resource:action` grammar, wildcards,
  action hierarchy (`read` < `operate` < `console` < `write` < `admin`), unit tests
- [x] **4.8** Authorization middleware and helpers for API handlers, permissions exposed in
  `/auth/me`
- [x] **4.9** Audit log: data model, writer, logging of logins and every state change,
  query API with filters, optional output to syslog or a file
  - [x] **4.9.1** Test that `configs/config.example.yaml` stays valid (it had no test yet)
- [x] **4.10** Admin API: list and terminate sessions, reset a user's TOTP (root only), plus
  the `dcdash totp reset` CLI command
- [x] **4.11** User preferences: storage and API (`GET`/`PUT /api/v1/me/preferences`) for
  language and theme

## Sprint 5 — Frontend Shell

- [x] **5.1** Design system: Tailwind theme tokens for light and dark, base components
  (button, table, form, modal, toast), each checked in both themes
- [x] **5.2** Dark mode: light, dark and system modes (`prefers-color-scheme`), toggle in the
  user menu, stored in the user preferences, inline bootstrap script so the page doesn't
  flash the wrong theme on load
- [x] **5.3** i18n: vue-i18n setup, English and German message files, browser language
  detection, language switcher stored in the user preferences, localized date and number
  formats
- [x] **5.4** API client layer: error handling (localized messages), CSRF header,
  session-expiry handling
  - [x] **5.4.1** Vite dev server over HTTPS with the dev certificate: the session cookie is
    always `Secure` (`__Host-` prefix), so it is not sent over plain HTTP (added in 3.5)
- [x] **5.5** Login page with the TOTP step
- [x] **5.6** TOTP enrollment page: QR code (rendered in the browser from the `otpauth://` URI
  returned by the API, see 4.3), confirmation, recovery code display; voluntary enrollment and
  new recovery codes from the user menu
- [x] **5.7** App layout: navigation that only shows modules the user may use, user menu
  (theme, language, logout)
- [x] **5.8** Route guards based on permissions
- [x] **5.9** Home dashboard: per-backend health tiles
  - [x] **5.9.1** `GET /api/v1/status` for logged-in users: database, certificate and directory
    (later the backends), checked concurrently; `/readyz` stays limited to what a load balancer
    needs, so a directory outage does not take dcdash and the break-glass access offline
- [x] **5.10** Admin views: audit log (filter, paginate), sessions, TOTP reset
- [x] **5.11** Frontend tests (Vitest) and an end-to-end smoke test (Playwright) for login,
  run in both themes and both languages

## Sprint 6 — Proxmox

- [x] **6.1** Configuration: several clusters, each with several endpoints, API token, CA
  or fingerprint, and optional console credentials (see 6.6)
- [x] **6.2** Client wrapper around `go-proxmox`: endpoint failover, timeouts, health check.
  Changed: dcdash uses its own small client instead of go-proxmox, see
  `docs/adr/0002-proxmox-client.md`
- [x] **6.3** Read API: clusters, nodes with resource usage, VMs and containers with status
  and details
- [x] **6.4** Operate API: start, shutdown, stop, reboot, suspend, resume, all checked for
  permission and audited
- [x] **6.5** Task tracking: task status and log, polling the result of an action
- [x] **6.6** Console spike: find out whether the Proxmox `vncwebsocket` and `termproxy`
  accept API-token authentication; otherwise use a dedicated Proxmox user with tickets.
  Record the decision in `docs/adr/`
- [x] **6.7** Console backend: websocket proxy for `vncproxy` (VMs) and `termproxy`
  (containers, node shell), short-lived one-time console tickets, `console` permission,
  audit of console session start and end
  - [x] **6.7.1** Terminals (containers, node shells) use the console user, because Proxmox
    rejects the termproxy login with API tokens (ADR 0001); VNC uses the API token. The
    cluster list reports `terminals`, the API answers 409 `terminal_unavailable` without a
    console user
- [x] **6.8** Frontend inventory: cluster overview, node list, VM and container list with
  filter and search, detail view, action buttons with confirmation
- [x] **6.9** Frontend console: noVNC for VMs, xterm.js for containers and node shells,
  full-screen mode and theming for dark mode
  - [x] **6.9.1** Fix: the noVNC canvas had no height. The Proxmox fake speaks a minimal RFB
    server, so the end-to-end test checks the VNC console
  - [x] **6.9.2** The guest page shows the live status (`status/current`), because the
    cluster resource list lags about 10 seconds behind
- [x] **6.10** Tests: HTTP and websocket mocks of the Proxmox API; manual test against a
  real cluster documented

## Sprint 7 — DNS (Cloudflare and bind9)

- [x] **7.1** `dns.Provider` interface (zones and records) and a common record model (A,
  AAAA, CNAME, MX, TXT, SRV, PTR, NS, CAA) with validation
- [x] **7.2** Cloudflare provider (`cloudflare-go`): list, create and delete zones (account
  ID), record CRUD, `proxied`, TTL, comments
- [x] **7.3** bind9 provider, read: AXFR with TSIG
- [x] **7.4** bind9 provider, write: RFC 2136 updates with TSIG, prerequisite checks to
  avoid lost updates
- [x] **7.5** bind9 server operations through `rndc` (status, reload, `addzone`,
  `delzone`), using a configured key file and binary path
- [x] **7.6** bind9 zone file provisioning over SFTP (`x/crypto/ssh`, `pkg/sftp`):
  - Per-primary configuration: host, port, user, private key file, pinned host key or
    `known_hosts`, remote zone directory
  - Atomic upload (temporary file, then rename), and removal of the zone file and `.jnl`
  - Zone file rendering from the SOA/NS template in the config
  - Connection health check
- [x] **7.7** bind9 zone creation and deletion: create the zone file, `rndc addzone`,
  catalog zone update; and the reverse for deletion, with a safety confirmation
- [x] **7.8** DNS API for zones and records (per-instance permissions, audited)
- [x] **7.9** Frontend: zone list with create and delete, record table with filter, create,
  edit and delete dialogs, provider-specific fields
- [x] **7.10** Tests: bind9 containers (primary with an SFTP server, and a secondary with a
  catalog zone) for integration tests, HTTP mocks for Cloudflare
- [x] **7.11** Documentation:
  - Required `named.conf` settings: TSIG key, `update-policy`, `allow-transfer`,
    `allow-new-zones`, catalog zone, rndc
  - SFTP user setup on the primary: chrooted, SFTP only, dedicated key, directory
    permissions shared with `named`
  - Cloudflare token permissions

## Sprint 8 — DHCP (Kea v3)

- [x] **8.1** Kea 3.x API research: control socket configuration, authentication, hooks
  needed (`lease_cmds`, `host_cmds` with a host database, `stat_cmds`, and `subnet_cmds` if
  useful); amend this sprint if needed
  (`docs/adr/0003-kea-api.md`)
- [x] **8.2** Kea client: HTTP and HTTPS, basic auth, client certificates, command and
  response model, error mapping
- [x] **8.3** Capability detection with `list-commands`, `status-get` and `version-get`
- [x] **8.4** Read API: subnets and pools with utilisation, leases for v4 and v6 (search by
  IP, MAC or DUID, hostname)
  - [x] **8.4.1** Reservations from the configuration file and the host database
    (`operation-target`), the configuration ones read-only
- [x] **8.5** Write API: delete a lease, host reservation CRUD (`host_cmds`), all checked
  for permission and audited
- [x] **8.6** HA pair awareness in the config: grouping and choosing the write target
  - [x] **8.6.1** Check with an HA pair in containers whether lease changes through
    `lease_cmds` reach the partner; otherwise delete leases on both partners
- [x] **8.7** Frontend: server overview, subnet utilisation, lease search, reservation
  management, features hidden when the server doesn't support them
- [x] **8.8** Tests: Kea 3.x container (DHCPv4 and DHCPv6, with hooks and a host database)
  for integration tests
  - [x] **8.8.1** PostgreSQL image with the Kea schema from the source archive, pinned by
    SHA-256
- [x] **8.9** Documentation: Kea prerequisites (control sockets, hooks, host database)

## Sprint 9 — Hardening and Release v0.1.0

- [x] **9.1** Security review: authentication flows, session handling, CSRF, headers,
  websocket proxy, input validation, secret handling, dependency audit (`govulncheck`,
  `npm audit`)
  - [x] **9.1.1** Strict DNS names: zone and record names allow only letters, digits, `_` and
    `-` per label (`*` as leftmost record label). miekg/dns accepted tabs, `;`, braces and `#`, which
    a zone admin could use to inject options into `rndc addzone` or other record types into updates
  - [x] **9.1.2** Login and TOTP lockout count attempts in progress (parallel requests got past
    the limit), IPv6 clients per /64, a hard cap on tracked keys; user names over 256 characters
    or with control characters are rejected before logging
  - [x] **9.1.3** Sessions and consoles: an open console no longer extends the idle timeout, at
    most 3 consoles in preparation per session, a TOTP reset ends the user's sessions; read and
    idle timeouts for the HTTP server
  - [x] **9.1.4** Warning for `file:` secrets readable by other users; DNS updates record the
    previous values in the audit log; review results and known limitations in `security.md`
- [x] **9.2** Optional Prometheus `/metrics` endpoint, including certificate expiry
- [x] **9.3** End-to-end test suite against the full docker compose environment
- [x] **9.4** UI review: German translation reviewed by a native speaker, dark and light mode
  checked for WCAG AA contrast on all views
  - [x] **9.4.1** Automated WCAG 2.1 AA check (axe-core, including color contrast) of every view
    in the end-to-end tests, in light and dark mode: no violations
- [x] **9.5** Documentation: installation, full configuration reference, admin guide, user
  guide, backend prerequisites (Proxmox token, bind, Kea, LDAP)
- [x] **9.6** Packaging: goreleaser, Linux amd64 and arm64 binaries, `.deb`/`.rpm` with a
  systemd unit, container image
- [x] **9.7** Release workflow in CI: build artifacts and checksums when a tag is pushed
- [x] **9.8** `CHANGELOG.md`, update the README, tag `v0.1.0`
- [x] **9.9** Translatable status details: `/api/v1/status` returns a detail code with parameters
  (for example the certificate expiry) instead of English text, so that the overview shows
  them in German as well (found in the 5.11 screenshots)

## Sprint 10 — Proxmox Provisioning and Release v0.1.1

- [x] **10.1** API research: `qemu` create and clone, `lxc` create, cloud-init parameters,
  storage and network options; amend this sprint if needed (results and decisions:
  [ADR 0004](adr/0004-proxmox-provisioning.md))
- [x] **10.2** Configuration: provisioning defaults and limits per cluster (allowed
  storages, bridges and VLANs, templates, maximum CPU, memory and disk)
- [x] **10.3** Create VMs: clone from a template (full or linked), and create from an ISO
- [x] **10.4** Cloud-init for VMs: user, SSH keys, IP configuration, DNS settings
- [x] **10.5** Create containers from templates: resources, network, root password or SSH
  keys
- [x] **10.6** Delete VMs and containers: the user types the name to confirm, the action is
  audited, and an optional purge removes them from backup jobs and HA
- [x] **10.7** Free VM and container ID allocation, and progress tracking with the task API
  - [x] **10.7.1** Provisioning jobs: the backend runs the steps of a creation (clone, wait,
    configure, grow the disk, start) and the browser polls the job
- [x] **10.8** Frontend: creation wizard for VMs and containers, and the delete dialog
- [x] **10.9** Tests: mocked API, manual test against a real cluster documented
  - [x] **10.9.1** Fixes from the test on a real cluster: the guest name is read from the
    configuration (the inventory lags behind after a creation), all storages of a node are
    listed, the token commands in `proxmox.md` work in bash
- [x] **10.10** Documentation, `CHANGELOG.md`, tag `v0.1.1`

## Sprint 11 — LDAP Failover, VM Configuration, UI Fixes and Release v0.1.2

- [x] **11.1** LDAP failover: `auth.ldap` accepts several URLs, which are tried in order
  until one answers (connection or TLS errors move on to the next one, wrong credentials do
  not). TLS settings, including a fingerprint per URL, the status check and the
  documentation are adapted
- [x] **11.2** Built-in documentation: a "Documentation" menu in the top bar shows the
  documentation from `docs/` (embedded in the binary, in the language of the user where
  available; currently English only), with working links between the pages
- [x] **11.3** Wider layout: the content uses more of wide screens (for example 1920 pixels)
  instead of a narrow centered column
- [x] **11.4** Proxmox guest list: the "Actions" column gets about 20% more width, so that
  all buttons fit in one line in English and German; the German label "Arbeitsspeicher"
  in the "Usage" column becomes "RAM"
- [x] **11.5** DNS zone view: the "Edit" and "Delete" buttons fit into the "Actions" column
  in English and German
- [x] **11.6** DHCP view: remove the status box of the DHCP server and move the server
  selection below the "DHCP" heading
- [x] **11.7** Sessions: find out why users are logged out after a few minutes of
  inactivity, fix it, and keep sessions alive for 8 hours (adjust the default of
  `auth.session.idle_timeout` if needed)
- [x] **11.8** API research for the VM configuration: changing options, disks and network
  devices of VMs (`PUT/POST .../config`, `resize`, `unlink`, pending changes, hot-plug,
  required privileges); amend this sprint if needed (results and decisions:
  [ADR 0005](adr/0005-proxmox-vm-configuration.md))
- [x] **11.9** Change VM options on the guest page, for example `agent`, `boot`, `onboot`,
  CPU and memory, with pending changes (they apply after a restart) shown as such
- [x] **11.10** VM disks: create, attach, detach, resize and delete disks, limited to the
  storages and limits of the provisioning configuration
- [x] **11.11** VM network devices: add, edit and remove, limited to the allowed bridges and
  VLANs
- [x] **11.12** Tests (mocked API, manual test against a real cluster) and documentation
  - [x] **11.12.1** Hardware changes against a real cluster; fix: a stale digest is reported
    by Proxmox as "checksum mismatch"
- [x] **11.13** Creation wizard: several network devices on the "Network and login" step (VMs
  and containers), each limited to the allowed bridges and VLANs, with its own IP
  configuration
- [x] **11.14** Creation wizard: steps after "Network and login" for DHCP reservations (IPv4
  and IPv6, per network device) and DNS records (A, AAAA and optionally PTR). The MAC
  addresses are fixed before the creation so that reservations can be made. The steps are
  shown only with the matching DHCP and DNS permissions; the creation job adds the entries
  and reports each one
- [x] **11.15** `CHANGELOG.md`, tag `v0.1.2`

## Sprint 12 — NetBox

- [x] **12.1** API research: NetBox REST API (devices, virtual machines, interfaces, IP
  addresses, prefixes, VLANs, MAC addresses), token permissions, and the mapping between
  dcdash objects and NetBox objects; results in an ADR, amend this sprint if needed
  ([ADR 0006](adr/0006-netbox.md))
- [x] **12.2** Configuration and client; NetBox as a common data source, for example VLAN
  names, prefixes and free IP addresses in the creation wizard
  - [x] **12.2.1** `netbox` configuration section, client (tokens v1 and v2, TLS, errors),
    status on the overview page
  - [x] **12.2.2** Fake NetBox (`test/nbfake`) with the calls of ADR 0006
  - [x] **12.2.3** VLAN names and free addresses in the creation wizard
- [x] **12.3** Write-back: changes in dcdash update NetBox (creating, changing and deleting
  guests, disks and network devices; DHCP reservations; DNS names). A failed NetBox update
  does not undo the change itself, but it is shown to the user and written to the audit log
  - [x] **12.3.1** Creating and deleting guests (job step)
  - [x] **12.3.2** Changing options, disks and network devices of VMs
  - [x] **12.3.3** DHCP reservations and DNS `A`/`AAAA` records
  - [x] **12.3.4** Web interface: NetBox results and errors after changes
- [x] **12.4** Comparison view: differences between NetBox and the actual state of the
  connected systems
- [x] **12.5** Permissions and audit actions, navigation entry
- [x] **12.6** Tests: fake NetBox server, manual test against a real NetBox documented
- [x] **12.7** Documentation
- [x] **12.8** Projects: tags `project-<name>` group the objects of a project. Projects are
  defined in YAML files in `/etc/dcdash/projects` (one file per project: tag name, color;
  more metadata later); dcdash synchronizes their tags to NetBox
  - [x] **12.8.1** Design: project files, tag synchronization, which objects carry project
    tags in NetBox and Proxmox, permissions ([ADR 0007](adr/0007-projects.md))
  - [x] **12.8.2** `projects` configuration, loading and reloading the project files, tag
    synchronization to NetBox, status on the overview page
  - [x] **12.8.3** Assigning projects: creation wizard and guest page (NetBox VM, its
    addresses and Proxmox tags), DHCP reservations and DNS records (their NetBox addresses);
    VLANs, prefixes, IP addresses and ranges, and switch ports (device interfaces) in NetBox
  - [x] **12.8.4** Project pages: a list of all projects and a page per project with all
    related objects and links to them in dcdash
  - [x] **12.8.5** Permissions and audit, tests, OpenAPI, documentation

## Sprint 13 — Aruba CX 6x00 Switches

- [x] **13.1** API research: AOS-CX REST API (sessions, VLANs, interfaces, MAC address
  table, LLDP, saving the configuration); results in an ADR, amend this sprint if needed
- [x] **13.2** Configuration and client for several switches, inventory and health status
- [x] **13.3** Port overview across all switches: status, speed, VLANs, PoE, LLDP neighbor
  - [x] **13.3.1** Compact overview of all switches like their front panels (odd ports above,
    even ports below, SFP ports at the end), with https and ssh links
- [x] **13.4** Find devices by MAC address across all switches, telling access ports from
  uplinks
- [x] **13.5** Manage VLANs: create, rename and delete, on one or several switches
- [x] **13.6** Configure ports: enable and disable, description, access VLAN, trunk VLANs;
  the change is saved to the startup configuration
- [x] **13.7** NetBox write-back for switch VLANs and ports (see 12.3); projects for switch ports on
  the switch pages (see 12.8)
  - [x] **13.7.1** Test with the NetBox copy: device DCDASH-SW33; shared NetBox VLANs are only
    renamed or deleted if dcdash created them
- [x] **13.8** Permissions and audit actions, navigation entry
- [x] **13.9** Tests: fake AOS-CX server, manual test against a real switch documented
- [x] **13.10** Documentation
- [x] **13.11** NetBox test with a copy of the production database (see 12.3): keep interfaces
  maintained by hand, matched by name or MAC address, and delete only interfaces dcdash
  created

## Sprint 14 — OPNsense Firewalls and Release v0.2.0

- [x] **14.1** API research: OPNsense API (system status, gateways, services, WireGuard,
  IPsec, aliases, filter rules with savepoint and rollback); results in an ADR, amend this
  sprint if needed
- [x] **14.2** Configuration and client, health check (system, gateways, services) on the
  overview page
- [x] **14.3** VPN status: WireGuard peers (last handshake, traffic) and IPsec tunnels
  (phase 1 and phase 2)
- [x] **14.4** Enable and disable WireGuard peers, restart IPsec tunnels
- [x] **14.5** Manage aliases
- [x] **14.6** Manage filter rules, applied with a savepoint so that a rule that cuts off
  access is rolled back automatically
- [x] **14.7** Permissions and audit actions, navigation entry
- [x] **14.8** Tests: fake OPNsense server, manual test against a real firewall documented
  - [x] **14.8.1** Test against a firewall with OPNsense 26.7 (ADR 0009, "Verification"):
    list IPsec connections without a session, map the interface names of automatic rules,
    detect missing savepoints and show the rules read-only
  - [x] **14.8.2** Rule changes on OPNsense 26.7, to which the firewalls are updated: lockout
    guard instead of the savepoint rollback, tested against the test firewall (replaces the
    planned test of savepoints on 26.1 or Business Edition 26.4)
- [x] **14.9** Documentation, `CHANGELOG.md`, tag `v0.2.0`

## Sprint 15 — Overview, Colors, Icons and Release v0.2.1

- [x] **15.1** Overview page: narrower tiles, six next to each other on wide screens instead
  of three (fewer on smaller screens); name, status and details must stay readable at that
  width
- [x] **15.2** Configuration: optional settings `ui`, `ssh` and `rdp` for everything that
  produces a tile on the overview page: Proxmox clusters, DNS servers, DHCP servers, NetBox,
  switches, firewalls and the LDAP servers (user directory). `ui` is an `https://` (or
  `http://`) URL, `ssh` and `rdp` a host with an optional port (and user for `ssh`);
  validation and documentation
- [x] **15.3** API: the status checks return the configured links; OpenAPI
- [x] **15.4** Tiles: a small button for each set (non-empty) link, left of the status
  element, like the https and ssh links of the switch overview; `ssh://` and an RDP link (the
  form that Windows and the common RDP clients open is decided in this task); en/de labels,
  light and dark mode, accessibility
- [x] **15.5** Tests and documentation for 15.1 to 15.4 (`configuration.md`, user guide)
- [x] **15.6** Colors per system on the overview page instead of the group headings:
  name and border in the system's color, background a transparent tint of it (10 % in light,
  15 % in dark mode); dcdash's own tiles keep their colors. Base colors Kea DHCP `#008037`,
  bind9 `#0066CC`, Cloudflare `#F48120`, NetBox `#00857D`, Proxmox `#D302D4`, switches
  `#90E5E6`, OPNsense `#E34A21`. As text they need 4.5:1 on the tinted background, so they
  are adjusted per mode (agreed 2026-11-23, light / dark): Kea `#007B35` / `#00A446`, bind9 `#0066CC` /
  `#148AFF`, Cloudflare `#AB5308` / `#F48120`, NetBox `#00766F` / `#009F95`, Proxmox `#B502B6` /
  `#F102F2`, switches `#1C7D7E` / `#90E5E6`, OPNsense `#BE3B18` / `#E6603C`. Color is not the only cue
  (the system type stays readable on the tile and as accessible name); a test checks the
  contrast of all colors in both modes
- [x] **15.7** DNS zones: rows highlighted in the Cloudflare or bind9 color
- [x] **15.8** All resources embedded in the binary: the UI loads nothing from elsewhere
  (fonts, icons, scripts, styles); a build check fails on external URLs in the built UI
- [x] **15.9** Icons instead of text on buttons, from Font Awesome Free, bundled as SVG
  (license CC BY 4.0 for the icons, MIT for the code; named in the third-party notices). An
  icon button with the former text as accessible name and tooltip (en/de), light and dark mode:
  - Proxmox overview and VM detail page: start `power-off` in green, shut down `power-off` in
    red, reboot `arrow-rotate-left`, suspend `moon` (regular), stop `bolt-lightning`, console
    `display`
  - Everywhere: delete `trash-can` (regular), refresh `arrows-rotate`, search
    `magnifying-glass`
  - Console page: Ctrl+Alt+Del as an own icon like Proxmox's (two small squares centered in
    the lower half, one in the upper half slightly to the right, like the keys on a keyboard)
- [x] **15.10** Icons on the DNS zone page (refresh, add record `plus`, edit `pencil`, delete)
  and the DHCP page (show leases `bookmark`)
- [x] **15.11** Icons on the firewall page: restart like the VMs' reboot, enable and disable
  with the power icon, create rules and aliases with `plus`, edit `pencil`, delete
  `trash-can`. Text buttons stay for "Update NetBox" and "Compare" on the NetBox page and for
  "Apply" and "Reset" in the audit log
- [x] **15.12** Documentation: complete list of the permissions that roles can contain (all
  modules, resources and actions, with examples); a test checks it against the permissions
  the code checks
- [x] **15.14** `.github/dependabot.yml`: updates for the Go modules, the npm packages in
  `web/`, the GitHub Actions and the base images of `packaging/Dockerfile`
- [x] **15.13** `CHANGELOG.md`
- [x] **15.15** Overview: a check that fails with an HTTP 4xx response (Kea control agents
  answer 408 now and then) is repeated once after `status.retry_delay` (default 1 s) before
  the tile shows the result

## Sprint 16 — Active Directory and Samba

Two scenarios: a Windows domain with a Windows Server 2019 domain controller in the airgapped
network, and two current Samba AD domain controllers in the test labs.

- [x] **16.1** API research, results in an ADR (amend this sprint if needed). Compare per
  feature and scenario:
  - **Agentless:** LDAPS for users, groups and passwords (`unicodePwd`, which needs an
    encrypted connection), DNS records with RFC 2136 updates signed with GSS-TSIG
    (Kerberos), DNS zones as `dnsZone` objects in the `DomainDnsZones` partition, or the
    DNS RPC protocol (MS-DNSP) that `samba-tool dns` uses; kpasswd for expired passwords.
    Works the same for Windows and Samba and needs nothing on the domain controllers.
  - **Agent on the Samba DC:** a small dcdash agent (Go, mTLS) that wraps `samba-tool` for
    what the agentless way can't do well; not available for the Windows DC unless an
    equivalent (PowerShell) agent runs there, which the airgapped network may not allow.
  - Verify the decision against a lab Samba DC; note what can't be checked on Windows.
  - Result: [ADR 0010](adr/0010-active-directory.md), agentless for every feature, verified
    on the lab Samba DC; zone management on Windows is unverified and behind a setting.
- [x] **16.2** Configuration: a `directories` section for several AD domains (name, type
  `windows` or `samba`, LDAPS URLs with failover like `auth.ldap`, base DN, service account,
  TLS, Kerberos realm and KDCs, the OUs dcdash may manage, protected
  accounts and groups, self-service); completely independent of the login directory
  (`auth.ldap`), which may point to the same domain (moved to 16.3: overview tile
  and status check)
- [x] **16.3** Client for users and groups (search, read, create, change, delete) and a test
  setup: a fake for unit tests and a Samba AD DC container for integration and e2e tests;
  overview tile and status check per directory (its color: 16.10)
- [x] **16.4** Users: create (OU, sAMAccountName, UPN, names, initial password, must change
  at next logon), lock and unlock, disable and enable, delete, reset the password (optionally
  "must change"); built-in and protected accounts (Administrator, krbtgt, members of
  privileged groups) only with `admin`; API and permissions here, the page in 16.10. AD has
  no administrative lock: "lock" is disable, "unlock" clears a lockout by failed logins
- [x] **16.5** Groups: create, change (name, description, scope and type) and delete groups,
  add and remove members; built-in groups protected; API here, the page in 16.10
- [x] **16.6** DNS: AD-integrated zones and records as DNS provider type `ad` on the existing
  DNS page (Windows DNS and Samba's internal DNS), referencing a directory by name with the
  DNS server's host name; zones created and deleted as decided in 16.1 (`ad.manage_zones`).
  Samba doesn't increment the SOA serial and refuses update prerequisites, so the version
  of a zone comes from the directory (`uSNChanged`)
- [ ] **16.7** ~~The agent for the Samba DC, if 16.1 chooses it~~ Dropped: ADR 0010 chose
  the agentless way for every feature. An agent with the `DnsServer` PowerShell module is the
  fallback for zone management on Windows if the manual test there fails (later sprint).
- [x] **16.8** Self-service password change, like LTB Self Service Password: a page that
  every user of a configured directory can use without a dcdash role (directory, user name,
  current password, new password twice). The change uses the user's own credentials (LDAP or
  kpasswd, so that expired passwords and "must change" work); the domain's password policy
  is shown and its errors explained; rate limits and the domain lockout are respected, user
  names can't be enumerated, the audit log records the change without passwords; en/de
- [x] **16.9** Projects: project files name AD groups per directory; the project page shows
  them with their members
- [x] **16.10** Permissions and audit actions (module `directory`, resources for users,
  groups and OUs), navigation entry, a "Directories" page with users and groups; color for
  the overview tile; en/de, light and dark mode
- [x] **16.11** Tests: unit tests with the fake, integration and e2e tests with the Samba
  container, a manual test against a lab Samba DC (and the Windows DC, as far as possible)
  documented
- [x] **16.12** Documentation (`directories.md`, configuration, security, permission
  reference, user guide), OpenAPI, `CHANGELOG.md`

## Sprint 17 — Configuration Directory

v0.3.0 added the `directories` section and the DNS provider type `ad` without changing
`configs/config.example.yaml`, so the package installed no example and asked nothing on the
upgrade. The configuration also grows long with many switches, firewalls or clusters: it is
split into a directory of files that are merged.

- [x] **17.1** A commented `directories` example (all keys of
  `docs/configuration.md#directories`) and a DNS provider of type `ad`, in the example
  directory of 17.3 (`20_directories.yaml`, `50_dns.yaml`) that replaces
  `configs/config.example.yaml`
- [x] **17.2** Configuration directory: `-c`/`--config` names a directory. dcdash reads every
  `*.yaml` file in it (not in subdirectories, no hidden files) in alphabetical order and merges
  them into one configuration before decoding:
  - mappings are merged key by key, recursively;
  - lists are concatenated in file order, so that a switch, firewall or cluster can be added
    or removed by adding or deleting a file;
  - a scalar value set in two files is an error naming both files and lines (no silent
    override); equal values and empty values (`key:`) don't conflict;
  - unknown keys and syntax errors are reported with file name and line; an empty directory
    is an error.
  The default becomes `/etc/dcdash/config.d`. A file given to `-c` is still read alone,
  with a warning, so that existing installations keep working.
- [x] **17.3** The example as a directory `configs/config.d/`: `00_server.yaml`,
  `01_auth.yaml`, `02_audit.yaml`, `03_security.yaml`, `04_roles.yaml`, `05_log.yaml`,
  `06_metrics.yaml`, `07_global.yaml` (`status`, `data_dir` and other global settings),
  `10_projects.yaml`, `20_directories.yaml`, `30_netbox.yaml`, `40_proxmox.yaml`,
  `50_dns.yaml`, `60_dhcp.yaml`, `70_firewalls.yaml`, `80_switches.yaml`. A test checks that
  the files together contain every section of the configuration and that they validate, as
  shipped and with the commented module examples switched on.
- [x] **17.4** Packaging and migration: the `.deb` and `.rpm` packages install the files to
  `/etc/dcdash/config.d` as configuration files (optional modules disabled), the systemd
  unit, the container image and the postinstall notes use the directory; an existing
  `/etc/dcdash/config.yaml` keeps working (it is read instead of the directory while it
  exists; the rpm restores it from `.rpmsave` in `posttrans`), and the documentation
  describes moving it into the directory
- [x] **17.5** `config validate` and the start log list the files read; errors name the file
  and line
- [x] **17.6** Tests: merging (order, nested mappings, lists, conflicts, unknown keys, empty
  directory, a single file), the CLI, and the e2e and integration setups with a directory
- [x] **17.7** Documentation (configuration, admin guide, development, README),
  `CHANGELOG.md` with upgrade notes
- [x] **17.8** Overview: the heading, the UI/SSH/RDP links and the status of a tile are on one
  line again (since 15.x the links and status were on a line of their own); they wrap
  below a long name
- [x] **17.9** DNS page: the AD zones seemed missing because `/dns` showed only the first
  provider; the others were behind the provider selection. The page now lists every
  provider with its zones and its "Create zone" button; the provider selection is an
  optional filter (`/dns/<provider>`), the zone search covers all shown providers
- [x] **17.10** AD DNS: don't show an empty zone list silently. If none of the zone
  containers below `base_dn` exists or can be read, the tile and the page show an error
  naming the containers
- [x] **17.11** Switches: the https and ssh links of every switch on the switch overview become
  the same UI and SSH buttons as on the overview page (a shared `LinkButtons` component)
- [x] **17.12** Proxmox: the "Shell" link on the node tiles becomes a button like these, next
  to UI and SSH buttons for the node (`node_links` with `{node}`, or the endpoint named after
  the node)
- [x] **17.13** Overview: every DNS tile has the color of bind9, whatever the provider; the
  bottom line still names it (bind9, Cloudflare, AD DNS)
- [x] **17.14** The UI buttons open the web interface in a new tab, everywhere (all use
  `LinkButtons`)
- [x] **17.15** Directories: users and groups outside `managed_ous` are read-only. Compare DNs
  as DNs (spaces after commas, case), reject `managed_ous` entries that aren't DNs below
  `base_dn`, and say on the page which containers are managed
- [x] **17.16** Security: gorilla/websocket used a weak random generator for the mask keys
  of client frames (GO-2026-6278, GHSA-w67g-5rqw-f597; the Proxmox console is a client).
  Updated to the fixing commit `d67f41855da4`, as no release contains it yet
- [x] **17.17** Login page: a top bar with the name, version and display settings (also on
  the enrollment and password pages); below it the login tile and a tile with a button per
  self-service domain, which preselects that domain on the password change page
- [x] **17.18** Switches page like the DNS page: `/switches` shows all switches (no redirect,
  no tabs), the dropdown only filters, the device search sits on the same line, refresh at the
  top right next to the heading, larger switch names. A name opens `/switches/<name>` with
  the info and the tabs Ports and VLANs

---

## Sprint 18 — Page Layout

The DNS and Switches pages now show all their servers on one page with a filter. The other
module pages follow, with one layout: the heading on the left and the filter, search and
refresh (as an icon) in the same line on the right, every server as a section with its
UI/SSH/RDP buttons next to its name. The tables take the colors of the overview tiles.

- [x] **18.1** Messages (toasts) can be closed: error messages stay until the user closes
  them, also across page changes; success messages still disappear on their own
- [x] **18.2** DNS page: the UI/SSH/RDP buttons next to the heading of each server
- [x] **18.3** DNS page: one color for all DNS servers, the one of the DNS tiles on the
  overview; remove the colors per DNS server type
- [x] **18.4** DHCP page like the DNS page: all DHCP servers with their subnets; the server
  dropdown becomes a filter at the top right, together with the lease search from the
  "Leases" tab. A button next to each subnet shows its leases, which replaces the "Leases of a
  subnet" dropdown
- [x] **18.5** NetBox page: refresh as an icon at the top right, together with the Proxmox
  cluster filter and the DNS/DHCP comparison button; the list also shows physical machines
  and switches
  - [x] **18.5.1** The layout: all clusters (the field filters), refresh as an icon, the
    cluster filter and "Compare DNS and DHCP" at the top right
  - [x] **18.5.2** Physical machines and switches: Proxmox nodes (cluster, primary IP from
    `/cluster/status`) and switches (model, serial number) compared with their NetBox devices;
    "Update NetBox" sets the cluster, the primary IP (if assigned to the device) and the serial
    number. Devices are not created
- [x] **18.6** Switches page: the switch filter and the device search in the top line, as on
  the DNS page
- [x] **18.7** Firewalls page in the same layout
- [x] **18.8** Directories page in the same layout
- [x] **18.9** Tables on all pages in the colors of the overview tiles of their system, with a
  second, more transparent shade for alternating rows (light and dark mode)
- [x] **18.10** Top bar: remove "Overview" (the name "dcdash" leads there); order Projects,
  Directories, NetBox, Proxmox, DNS, DHCP, Firewalls, Switches; Audit log and Sessions on the
  right next to Documentation
