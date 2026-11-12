# Implementation Plan — dcdash v0.1.0 and v0.1.1

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
| 0 | `sprint/00-planning` | Concept review, plan, conventions | – | in progress |
| 1 | `sprint/01-foundation` | Repo skeleton, config, HTTP server, frontend scaffold, CI | v0.1.0 | open |
| 2 | `sprint/02-tls` | Certificates: static files, ACME (Let's Encrypt, step-ca) | v0.1.0 | open |
| 3 | `sprint/03-auth` | State store, LDAP login, sessions, break-glass account | v0.1.0 | open |
| 4 | `sprint/04-mfa-rbac` | TOTP per role, roles and permissions, audit log, user preferences | v0.1.0 | open |
| 5 | `sprint/05-ui-shell` | Frontend shell: i18n (en/de), dark mode, login, layout, admin views | v0.1.0 | open |
| 6 | `sprint/06-proxmox` | Proxmox: inventory, power actions, console | v0.1.0 | open |
| 7 | `sprint/07-dns` | DNS: Cloudflare and bind9, zones and records | v0.1.0 | open |
| 8 | `sprint/08-dhcp` | DHCP: Kea v3 DHCPv4 and DHCPv6 | v0.1.0 | open |
| 9 | `sprint/09-release` | Hardening, documentation, packaging, **v0.1.0** | v0.1.0 | open |
| 10 | `sprint/10-proxmox-provisioning` | Creating and deleting VMs and containers, **v0.1.1** | v0.1.1 | open |

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

## Sprint 1 — Foundation

- [ ] **1.1** Repository layout: Go module, `cmd/dcdash`, `internal/...`, `web/`, `docs/`,
  `LICENSE` (GPLv3), SPDX headers, `.editorconfig`, `.gitignore`
- [ ] **1.2** Build tooling: Makefile or Taskfile (build, test, lint, dev), pinned Go and
  Node versions
- [ ] **1.3** CLI skeleton: `serve`, `version`, `config validate`
- [ ] **1.4** Configuration: YAML schema and loader with defaults and validation, secret
  indirection (`*_file`, `env:`), permission warning for world-readable files, example
  `config.example.yaml`
- [ ] **1.5** Logging with `rs/zerolog`: JSON or console output, configurable level, request
  ID in log context
- [ ] **1.6** HTTP server: router, middleware (request ID, logging, panic recovery, security
  headers), graceful shutdown, `/healthz`, `/readyz`, `/api/v1/version`
- [ ] **1.7** Frontend scaffold: Vue 3, TypeScript, Vite, Tailwind CSS v4 (class-based dark
  variant), Pinia, Vue Router, vue-i18n (en and de), ESLint and Prettier, Vitest
- [ ] **1.8** Embed the frontend build with `go:embed`, SPA fallback routing, dev proxy setup
  (Vite → Go backend)
- [ ] **1.9** OpenAPI 3 spec skeleton (`api/openapi.yaml`) and a TypeScript client
  generated from it
- [ ] **1.10** CI with GitHub Actions: Go lint and test, frontend lint, test and build,
  translation key completeness check, combined binary build
- [ ] **1.11** Developer documentation: `docs/development.md`

## Sprint 2 — TLS and Certificates

- [ ] **2.1** Certificate manager interface with hot reload through
  `tls.Config.GetCertificate`
- [ ] **2.2** Provider for static files: load the PEM cert, chain and key, and reload when
  the files change
- [ ] **2.3** ACME provider (lego): account registration and storage, configurable
  directory URL, additional trusted root CA (for step-ca)
- [ ] **2.4** DNS-01 solvers: Cloudflare (API token) and RFC 2136 (bind9 with TSIG)
- [ ] **2.5** Renewal loop: renew before expiry, retry with backoff, log events, expose
  certificate expiry in `/readyz` and the metrics
- [ ] **2.6** HTTP→HTTPS redirect listener, TLS 1.2 minimum, secure cipher defaults, HSTS
- [ ] **2.7** Integration tests against Pebble, and against step-ca where possible
- [ ] **2.8** Documentation: certificate configuration for all three modes

## Sprint 3 — State Store and Authentication

- [ ] **3.1** State store: SQLite (`modernc.org/sqlite`), migrations, data directory from
  the config
- [ ] **3.2** Encryption of secrets at rest: AES-GCM, key from the config or a key file,
  key rotation concept documented
- [ ] **3.3** LDAP client: LDAPS and StartTLS, custom CA, service bind, user search and
  bind, connection timeouts
- [ ] **3.4** Group resolution: AD nested groups (matching rule in chain) and generic LDAP
  (`memberOf` or group search), groups given as CN or DN
- [ ] **3.5** Sessions: server-side store, opaque cookie, absolute lifetime and idle
  timeout from the config, logout, cleanup job
- [ ] **3.6** CSRF protection for state-changing requests
- [ ] **3.7** Login rate limiting and temporary lockout per user and per source IP
- [ ] **3.8** API: `POST /auth/login`, `POST /auth/logout`, `GET /auth/me`
- [ ] **3.9** Development environment: docker compose with a Samba AD DC, seeded users and
  groups; integration tests
- [ ] **3.10** Break-glass local account: disabled by default, password hash in the config,
  always holds `root` and always requires TOTP, every use is audited and logged as a warning

## Sprint 4 — TOTP, Authorization, Audit and Preferences

- [ ] **4.1** Two-step login: password first, then TOTP when required, with a session that
  only becomes fully authenticated after the second step
- [ ] **4.2** TOTP requirement per role (`require_totp`, default `true`): required if any of
  the user's roles requires it; always required for `root` and the break-glass account;
  enforced once a user has enrolled voluntarily
- [ ] **4.3** TOTP enrollment: generate the secret and QR code, confirm with one code, store
  it encrypted
- [ ] **4.4** Recovery codes: generate them, store them hashed, single use
- [ ] **4.5** TOTP replay protection and lockout after repeated failures
- [ ] **4.6** Role model: built-in `root` (group `dashroot`), roles from the config with
  group mappings, `require_totp` and permission strings
- [ ] **4.7** Permission engine: the `module:instance:resource:action` grammar, wildcards,
  action hierarchy (`read` < `operate` < `console` < `write` < `admin`), unit tests
- [ ] **4.8** Authorization middleware and helpers for API handlers, permissions exposed in
  `/auth/me`
- [ ] **4.9** Audit log: data model, writer, logging of logins and every state change,
  query API with filters, optional output to syslog or a file
- [ ] **4.10** Admin API: list and terminate sessions, reset a user's TOTP (root only), plus
  the `dcdash totp reset` CLI command
- [ ] **4.11** User preferences: storage and API (`GET`/`PUT /api/v1/me/preferences`) for
  language and theme

## Sprint 5 — Frontend Shell

- [ ] **5.1** Design system: Tailwind theme tokens for light and dark, base components
  (button, table, form, modal, toast), each checked in both themes
- [ ] **5.2** Dark mode: light, dark and system modes (`prefers-color-scheme`), toggle in the
  user menu, stored in the user preferences, inline bootstrap script so the page doesn't
  flash the wrong theme on load
- [ ] **5.3** i18n: vue-i18n setup, English and German message files, browser language
  detection, language switcher stored in the user preferences, localized date and number
  formats
- [ ] **5.4** API client layer: error handling (localized messages), CSRF header,
  session-expiry handling
- [ ] **5.5** Login page with the TOTP step
- [ ] **5.6** TOTP enrollment page: QR code, confirmation, recovery code display
- [ ] **5.7** App layout: navigation that only shows modules the user may use, user menu
  (theme, language, logout)
- [ ] **5.8** Route guards based on permissions
- [ ] **5.9** Home dashboard: per-backend health tiles
- [ ] **5.10** Admin views: audit log (filter, paginate), sessions, TOTP reset
- [ ] **5.11** Frontend tests (Vitest) and an end-to-end smoke test (Playwright) for login,
  run in both themes and both languages

## Sprint 6 — Proxmox

- [ ] **6.1** Configuration: several clusters, each with several endpoints, API token, CA
  or fingerprint, and optional console credentials (see 6.6)
- [ ] **6.2** Client wrapper around `go-proxmox`: endpoint failover, timeouts, health check
- [ ] **6.3** Read API: clusters, nodes with resource usage, VMs and containers with status
  and details
- [ ] **6.4** Operate API: start, shutdown, stop, reboot, suspend, resume, all checked for
  permission and audited
- [ ] **6.5** Task tracking: task status and log, polling the result of an action
- [ ] **6.6** Console spike: find out whether the Proxmox `vncwebsocket` and `termproxy`
  accept API-token authentication; otherwise use a dedicated Proxmox user with tickets.
  Record the decision in `docs/adr/`
- [ ] **6.7** Console backend: websocket proxy for `vncproxy` (VMs) and `termproxy`
  (containers, node shell), short-lived one-time console tickets, `console` permission,
  audit of console session start and end
- [ ] **6.8** Frontend inventory: cluster overview, node list, VM and container list with
  filter and search, detail view, action buttons with confirmation
- [ ] **6.9** Frontend console: noVNC for VMs, xterm.js for containers and node shells,
  full-screen mode and theming for dark mode
- [ ] **6.10** Tests: HTTP and websocket mocks of the Proxmox API; manual test against a
  real cluster documented

## Sprint 7 — DNS (Cloudflare and bind9)

- [ ] **7.1** `dns.Provider` interface (zones and records) and a common record model (A,
  AAAA, CNAME, MX, TXT, SRV, PTR, NS, CAA) with validation
- [ ] **7.2** Cloudflare provider (`cloudflare-go`): list, create and delete zones (account
  ID), record CRUD, `proxied`, TTL, comments
- [ ] **7.3** bind9 provider, read: AXFR with TSIG
- [ ] **7.4** bind9 provider, write: RFC 2136 updates with TSIG, prerequisite checks to
  avoid lost updates
- [ ] **7.5** bind9 server operations through `rndc` (status, reload, `addzone`,
  `delzone`), using a configured key file and binary path
- [ ] **7.6** bind9 zone provisioning decision (`docs/adr/`): how zone files get onto the
  primary (local or shared directory, or SFTP), the SOA/NS zone template in the config, and
  catalog zones (RFC 9432) for the secondaries
- [ ] **7.7** bind9 zone creation and deletion: create the zone file, `rndc addzone`,
  catalog zone update; and the reverse for deletion, with a safety confirmation
- [ ] **7.8** DNS API for zones and records (per-instance permissions, audited)
- [ ] **7.9** Frontend: zone list with create and delete, record table with filter, create,
  edit and delete dialogs, provider-specific fields
- [ ] **7.10** Tests: bind9 containers (primary and secondary with a catalog zone) for
  integration tests, HTTP mocks for Cloudflare
- [ ] **7.11** Documentation: required `named.conf` settings (TSIG key, `update-policy`,
  `allow-transfer`, `allow-new-zones`, catalog zone, rndc) and Cloudflare token permissions

## Sprint 8 — DHCP (Kea v3)

- [ ] **8.1** Kea 3.x API research: control socket configuration, authentication, hooks
  needed (`lease_cmds`, `host_cmds` with a host database, `stat_cmds`, and `subnet_cmds` if
  useful); amend this sprint if needed
- [ ] **8.2** Kea client: HTTP and HTTPS, basic auth, client certificates, command and
  response model, error mapping
- [ ] **8.3** Capability detection with `list-commands`, `status-get` and `version-get`
- [ ] **8.4** Read API: subnets and pools with utilisation, leases for v4 and v6 (search by
  IP, MAC or DUID, hostname)
- [ ] **8.5** Write API: delete a lease, host reservation CRUD (`host_cmds`), all checked
  for permission and audited
- [ ] **8.6** HA pair awareness in the config: grouping and choosing the write target
- [ ] **8.7** Frontend: server overview, subnet utilisation, lease search, reservation
  management, features hidden when the server doesn't support them
- [ ] **8.8** Tests: Kea 3.x container (DHCPv4 and DHCPv6, with hooks and a host database)
  for integration tests
- [ ] **8.9** Documentation: Kea prerequisites (control sockets, hooks, host database)

## Sprint 9 — Hardening and Release v0.1.0

- [ ] **9.1** Security review: authentication flows, session handling, CSRF, headers,
  websocket proxy, input validation, secret handling, dependency audit (`govulncheck`,
  `npm audit`)
- [ ] **9.2** Optional Prometheus `/metrics` endpoint
- [ ] **9.3** End-to-end test suite against the full docker compose environment
- [ ] **9.4** UI review: German translation reviewed by a native speaker, dark and light mode
  checked for WCAG AA contrast on all views
- [ ] **9.5** Documentation: installation, full configuration reference, admin guide, user
  guide, backend prerequisites (Proxmox token, bind, Kea, LDAP)
- [ ] **9.6** Packaging: goreleaser, Linux amd64 and arm64 binaries, `.deb`/`.rpm` with a
  systemd unit, container image
- [ ] **9.7** Release workflow in CI: build artifacts and checksums when a tag is pushed
- [ ] **9.8** `CHANGELOG.md`, update the README, tag `v0.1.0`

## Sprint 10 — Proxmox Provisioning and Release v0.1.1

- [ ] **10.1** API research: `qemu` create and clone, `lxc` create, cloud-init parameters,
  storage and network options; amend this sprint if needed
- [ ] **10.2** Configuration: provisioning defaults and limits per cluster (allowed
  storages, bridges and VLANs, templates, maximum CPU, memory and disk)
- [ ] **10.3** Create VMs: clone from a template (full or linked), and create from an ISO
- [ ] **10.4** Cloud-init for VMs: user, SSH keys, IP configuration, DNS settings
- [ ] **10.5** Create containers from templates: resources, network, root password or SSH
  keys
- [ ] **10.6** Delete VMs and containers: the user types the name to confirm, the action is
  audited, and an optional purge removes them from backup jobs and HA
- [ ] **10.7** Free VM and container ID allocation, and progress tracking with the task API
- [ ] **10.8** Frontend: creation wizard for VMs and containers, and the delete dialog
- [ ] **10.9** Tests: mocked API, manual test against a real cluster documented
- [ ] **10.10** Documentation, `CHANGELOG.md`, tag `v0.1.1`
