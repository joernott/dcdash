# Implementation Plan — dcdash v0.1.0

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

## Overview

| Sprint | Branch | Goal | Status |
|---|---|---|---|
| 0 | `sprint/00-planning` | Concept review, plan, conventions | in progress |
| 1 | `sprint/01-foundation` | Repo skeleton, config, HTTP server, frontend scaffold, CI | open |
| 2 | `sprint/02-tls` | Certificates: static files, ACME (Let's Encrypt, step-ca) | open |
| 3 | `sprint/03-auth` | State store, LDAP login, sessions | open |
| 4 | `sprint/04-mfa-rbac` | TOTP, roles and permissions, audit log | open |
| 5 | `sprint/05-ui-shell` | Frontend shell: login, enrollment, layout, admin views | open |
| 6 | `sprint/06-proxmox` | Proxmox multi-cluster management | open |
| 7 | `sprint/07-dns` | DNS: Cloudflare and bind9 | open |
| 8 | `sprint/08-dhcp` | DHCP: Kea v3 DHCPv4 and DHCPv6 | open |
| 9 | `sprint/09-release` | Hardening, documentation, packaging, v0.1.0 | open |

---

## Sprint 0 — Planning

- [x] **0.1** Create the prompt log `docs/zz_prompts.md`
- [x] **0.2** Review the concept (`docs/concept-review.md`)
- [x] **0.3** Create the implementation plan (this document)
- [ ] **0.4** Add `CLAUDE.md` with the project conventions and workflow rules
- [ ] **0.5** Resolve the open questions Q1–Q8 from the concept review and amend the plan
  if needed

## Sprint 1 — Foundation

- [ ] **1.1** Repository layout: Go module, `cmd/dcdash`, `internal/...`, `web/`, `docs/`,
  `LICENSE` (GPLv3), SPDX headers, `.editorconfig`, `.gitignore`
- [ ] **1.2** Build tooling: Makefile or Taskfile (build, test, lint, dev), pinned Go and
  Node versions
- [ ] **1.3** CLI skeleton: `serve`, `version`, `config validate`
- [ ] **1.4** Configuration: YAML schema and loader with defaults and validation, secret
  indirection (`*_file`, `env:`), permission warning for world-readable files, example
  `config.example.yaml`
- [ ] **1.5** Logging with `log/slog`: JSON or text output, configurable level
- [ ] **1.6** HTTP server: router, middleware (request ID, logging, panic recovery, security
  headers), graceful shutdown, `/healthz`, `/readyz`, `/api/v1/version`
- [ ] **1.7** Frontend scaffold: Vue 3, TypeScript, Vite, Tailwind CSS v4, Pinia, Vue
  Router, ESLint and Prettier, Vitest
- [ ] **1.8** Embed the frontend build with `go:embed`, SPA fallback routing, dev proxy setup
  (Vite → Go backend)
- [ ] **1.9** OpenAPI 3 spec skeleton (`api/openapi.yaml`) and a TypeScript client
  generated from it
- [ ] **1.10** CI with GitHub Actions: Go lint and test, frontend lint, test and build,
  combined binary build
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
- [ ] **3.10** Optional break-glass local account (depends on Q1)

## Sprint 4 — TOTP, Authorization and Audit

- [ ] **4.1** Two-step TOTP login: password first, then TOTP, with a session that only
  becomes fully authenticated after the second step
- [ ] **4.2** TOTP enrollment: generate the secret and QR code, confirm with one code, store
  it encrypted
- [ ] **4.3** Recovery codes: generate them, store them hashed, single use
- [ ] **4.4** TOTP replay protection and lockout after repeated failures
- [ ] **4.5** Role model: built-in `root` (group `dashroot`), roles from the config with
  group mappings and permission strings
- [ ] **4.6** Permission engine: the `module:instance:resource:action` grammar, wildcards,
  action hierarchy, unit tests
- [ ] **4.7** Authorization middleware and helpers for API handlers, permissions exposed in
  `/auth/me`
- [ ] **4.8** Audit log: data model, writer, logging of logins and every state change,
  query API with filters, optional output to syslog or a file
- [ ] **4.9** Admin API: list and terminate sessions, reset a user's TOTP (root only), plus
  the `dcdash totp reset` CLI command

## Sprint 5 — Frontend Shell

- [ ] **5.1** Design basics: Tailwind theme, light and dark mode, base components (button,
  table, form, modal, toast)
- [ ] **5.2** API client layer: error handling, CSRF header, session-expiry handling
- [ ] **5.3** Login page with the TOTP step
- [ ] **5.4** TOTP enrollment page: QR code, confirmation, recovery code display
- [ ] **5.5** App layout: navigation that only shows modules the user may use, user menu,
  logout
- [ ] **5.6** Route guards based on permissions
- [ ] **5.7** Home dashboard: per-backend health tiles
- [ ] **5.8** Admin views: audit log (filter, paginate), sessions, TOTP reset
- [ ] **5.9** Frontend tests (Vitest) and an end-to-end smoke test (Playwright) for login

## Sprint 6 — Proxmox

- [ ] **6.1** Configuration: several clusters, each with several endpoints, API token, CA
  or fingerprint
- [ ] **6.2** Client wrapper around `go-proxmox`: endpoint failover, timeouts, health check
- [ ] **6.3** Read API: clusters, nodes with resource usage, VMs and containers with status
  and details
- [ ] **6.4** Operate API: start, shutdown, stop, reboot, suspend, resume, all checked for
  permission and audited
- [ ] **6.5** Task tracking: task status and log, polling the result of an action
- [ ] **6.6** Frontend: cluster overview, node list, VM and container list with filter and
  search, detail view, action buttons with confirmation
- [ ] **6.7** Tests: HTTP mocks of the Proxmox API; manual test against a real cluster
  documented

## Sprint 7 — DNS (Cloudflare and bind9)

- [ ] **7.1** `dns.Provider` interface and a common record model (A, AAAA, CNAME, MX, TXT,
  SRV, PTR, NS, CAA) with validation
- [ ] **7.2** Cloudflare provider (`cloudflare-go`): list zones, record CRUD, `proxied`,
  TTL, comments
- [ ] **7.3** bind9 provider, read: AXFR with TSIG
- [ ] **7.4** bind9 provider, write: RFC 2136 updates with TSIG, prerequisite checks to
  avoid lost updates
- [ ] **7.5** bind9 zone operations through `rndc` (status, reload), using a configured key
  file and binary path
- [ ] **7.6** DNS API (per-instance permissions, audited)
- [ ] **7.7** Frontend: zone list, record table with filter, create, edit and delete
  dialogs, provider-specific fields
- [ ] **7.8** Tests: bind9 container for integration tests, HTTP mocks for Cloudflare
- [ ] **7.9** Documentation: required `named.conf` settings (TSIG key, `update-policy`,
  `allow-transfer`, rndc)

## Sprint 8 — DHCP (Kea v3)

- [ ] **8.1** Kea 3.x API research: control socket configuration, authentication, hook
  availability and licensing; amend this sprint if needed
- [ ] **8.2** Kea client: HTTP and HTTPS, basic auth, client certificates, command and
  response model, error mapping
- [ ] **8.3** Capability detection with `list-commands`, `status-get` and `version-get`
- [ ] **8.4** Read API: subnets and pools with utilisation (`statistic-get-all`), leases
  for v4 and v6 (search by IP, MAC or DUID, hostname)
- [ ] **8.5** Write API: delete a lease, host reservation CRUD (`host_cmds`), all checked
  for permission and audited
- [ ] **8.6** HA pair awareness in the config: grouping and choosing the write target
- [ ] **8.7** Frontend: server overview, subnet utilisation, lease search, reservation
  management, features hidden when the server doesn't support them
- [ ] **8.8** Tests: Kea 3.x container (DHCPv4 and DHCPv6, with hooks) for integration
  tests

## Sprint 9 — Hardening and Release v0.1.0

- [ ] **9.1** Security review: authentication flows, session handling, CSRF, headers,
  input validation, secret handling, dependency audit (`govulncheck`, `npm audit`)
- [ ] **9.2** Optional Prometheus `/metrics` endpoint
- [ ] **9.3** End-to-end test suite against the full docker compose environment
- [ ] **9.4** Documentation: installation, full configuration reference, admin guide, user
  guide, backend prerequisites (Proxmox token, bind, Kea, LDAP)
- [ ] **9.5** Packaging: goreleaser, Linux amd64 and arm64 binaries, `.deb`/`.rpm` with a
  systemd unit, container image
- [ ] **9.6** Release workflow in CI: build artifacts and checksums when a tag is pushed
- [ ] **9.7** `CHANGELOG.md`, update the README, tag `v0.1.0`
