# Concept Review — dcdash v0.1.0

Review of [concept.md](concept.md) as of 2026-11-12. It covers gaps, technical corrections,
proposed decisions and open questions. Decisions marked **(proposed)** are what the
[implementation plan](implementation-plan.md) assumes. If you change one, the affected sprint
gets amended.

## 1. Summary

The concept is a solid scope for a first release: a single Go binary that serves a Vue
frontend over HTTPS, authenticates against LDAP with TOTP, and wraps three service families
(Proxmox, DNS, DHCP). The main issues:

1. **Persistent state is missing.** A YAML config file alone can't hold TOTP secrets,
   sessions or an audit trail. dcdash needs a small state store.
2. **bind9 can't manage records through rndc.** rndc controls zones and the server.
   Records need dynamic updates (RFC 2136 with TSIG).
3. **"Permissions" is undefined.** Roles map to LDAP groups, but nothing says what a
   permission is or how it is scoped (for example per cluster or per zone).
4. **Kea v3 changes the control API.** Kea 3.0 deprecates the Control Agent, so the daemons
   expose HTTP(S) control sockets themselves. Leases and reservations depend on hook
   libraries being loaded.
5. **There is no audit logging.** A tool that can power off VMs and change DNS needs a
   record of who did what.

## 2. Findings in detail

### 2.1 Persistence (gap, high)

| Data | Where it can live |
|---|---|
| Static configuration (listeners, LDAP, roles, backends) | YAML file ✔ (as in the concept) |
| TOTP secrets, recovery codes | Needs a writable store, encrypted at rest |
| Sessions | Needs a store, or sessions are lost on restart |
| Audit log | Needs a queryable store |
| ACME account and certificates | File storage directory |

**(proposed)** Embedded SQLite using `modernc.org/sqlite` (pure Go, so no CGO and simple
cross-compilation), with schema migrations. The YAML file sets the data directory and the
encryption key, or a path to a file holding the key. Secrets such as TOTP seeds are encrypted
with AES-GCM.

### 2.2 TLS and certificates (clarification, medium)

- Let's Encrypt with Cloudflare DNS-01 fits well. DNS-01 also works for hosts that are only
  reachable internally, as long as the name is in a Cloudflare-hosted zone.
- **step-ca also speaks ACME.** One ACME client with a configurable directory URL and an
  extra trusted root CA covers both Let's Encrypt and step-ca. **(proposed)** Use
  `go-acme/lego` (MIT, supports Cloudflare and RFC 2136 DNS providers). With RFC 2136,
  step-ca setups that only use bind9 can solve DNS-01 without Cloudflare.
- Provided certificates: load a PEM cert and key from files, and reload them when the files
  change.
- Also needed: an HTTP→HTTPS redirect listener, hot reload of the certificate
  (`tls.Config.GetCertificate`) and automatic renewal.
- Use **separate Cloudflare tokens** for ACME and for DNS management (least privilege). The
  config should allow both.

### 2.3 Authentication: LDAP (clarification, medium)

These points need defining:

- Bind method: **(proposed)** a service account searches for the user's DN, then dcdash binds
  as the user to verify the password.
- Transport: LDAPS or StartTLS, with a custom CA option. Plain LDAP only behind an explicit
  `insecure` flag.
- Group resolution: AD groups are often nested. **(proposed)** AD mode uses
  `LDAP_MATCHING_RULE_IN_CHAIN` (OID 1.2.840.113556.1.4.1941). Generic LDAP mode uses
  `memberOf` or a group search (`member`/`uniqueMember`).
- Group identification: the config should name groups by **CN** (simple) or by full DN
  (unambiguous). **(proposed)** Accept both. A value containing `=` is treated as a DN.
- `dashroot`: the concept hard-wires it. **(proposed)** Keep it as the default, and let the
  config override the group name but never remove the root role.
- **Break-glass access:** if LDAP is down, nobody can log in. **(decided, Q1)** A local
  emergency account, disabled by default, configured with a password hash in YAML. It holds
  the `root` role and always requires TOTP.

### 2.4 TOTP 2FA (gap, medium)

- **(decided, Q2)** TOTP is configurable per role with `require_totp` (default `true`).
  A user must use TOTP if **any** of their roles requires it, so the strictest role wins.
  `root` and the break-glass account always require TOTP and can't be configured otherwise.
  Users who don't need TOTP may still enroll voluntarily, and then it's enforced for them.
- Enrollment flow: on first successful LDAP login without TOTP, show a QR code
  (`otpauth://` URI), confirm one code, and then issue recovery codes.
- Reset: root can reset a user's TOTP. The reset is audited.
- Replay protection: reject a code that was already used within its time window.
- Rate limiting and temporary lockout on failed password or TOTP attempts.
- **(proposed)** Library: `pquerna/otp` (Apache-2.0).

### 2.5 Sessions (clarification, low)

- The concept defines the session duration. **(proposed)** Add an idle timeout next to the
  absolute lifetime.
- Server-side sessions with an opaque ID in a `Secure; HttpOnly; SameSite=Strict` cookie,
  plus CSRF protection for state-changing requests.
- Revocation: logout, and root can terminate the sessions of other users.

### 2.6 Authorization model (gap, high)

The concept says roles have permissions but doesn't define them. **(proposed)**

```yaml
roles:
  - name: dns-operator
    groups: ["dcdash-dns"]            # LDAP groups (CN or DN)
    require_totp: true                # default; see 2.4
    permissions:
      - "dns:cloudflare-prod:*:write" # module:instance:resource:action
      - "dns:bind-internal:*:read"
  - name: vm-operator
    groups: ["dcdash-vm"]
    permissions:
      - "proxmox:cluster-a:*:operate"
      - "proxmox:*:*:read"
```

- Modules: `proxmox`, `dns`, `dhcp`, `system` (audit log, sessions, users' TOTP).
- Actions form a hierarchy: `read` < `operate` (for example power actions) < `console`
  (interactive console or shell access, see 2.7) < `write` (create, change, delete) <
  `admin`.
- Wildcards (`*`) are allowed per segment. Permissions only grant, there is no deny.
- `root` holds `*:*:*:admin` implicitly.
- The backend enforces every check. The frontend only hides what the user can't use.

### 2.7 Proxmox (clarification, medium)

- `luthermonson/go-proxmox` (Apache-2.0) is fine. Authentication should use **API tokens**
  per cluster, not user passwords.
- Each cluster should accept several API endpoints for failover, and a custom CA or
  certificate fingerprint pinning (Proxmox often runs with self-signed certificates).
- Scope **(decided)**:
  - **v0.1.0**
    - Read: cluster status, nodes (CPU, memory, storage), VMs and containers with their
      status.
    - Operate: start, shutdown, stop, reboot, suspend and resume, plus the task log and task
      status.
    - **Console (Q3):** noVNC for VMs, and xterm.js for containers and node shells. The
      backend proxies the Proxmox `vncproxy`/`termproxy` websocket, so Proxmox credentials
      never reach the browser. This needs the `console` permission. Risk: check early whether
      the Proxmox websocket accepts API-token authentication. If it doesn't, the console
      needs a dedicated Proxmox user with a ticket (task 6.6).
  - **v0.1.1:** creating VMs and containers (from templates or clones, ISO, cloud-init) and
    deleting them.
  - **Later:** migration, backups, snapshots.

### 2.8 DNS: Cloudflare and bind9 (correction, high)

- **rndc doesn't manage records.** It covers `reload`, `freeze`/`thaw`, `sync`, `status`,
  `addzone`/`delzone` and similar commands.
  **(proposed)**
  - Read records with **AXFR** (zone transfer, TSIG-signed).
  - Change records with **RFC 2136 dynamic updates** signed with TSIG, using
    `miekg/dns` (BSD-3).
  - Use **rndc** only for zone and server operations (status, reload, and later
    freeze/thaw/sync). Go has no maintained rndc library, so dcdash calls the `rndc` binary
    with a configured key file. Alternatively the rndc protocol can be implemented later.
  - This requires `allow-update`/`update-policy` and `allow-transfer` for the dcdash TSIG
    key in `named.conf`. It belongs in the admin documentation.
- Cloudflare: use the official `cloudflare/cloudflare-go` SDK (Apache-2.0). The scope is
  zones (list, create, delete) and record CRUD, including the `proxied` flag and TTL. Creating
  zones needs the account ID and a token with zone-edit permission.
- **Zone creation and deletion (decided, Q4)** for bind9:
  - `rndc addzone`/`delzone` requires `allow-new-zones yes;` on the primary.
  - The zone file must exist on the primary before `addzone`. **(decided)** dcdash copies
    zone files to the primary over **SFTP**, because dcdash can't be assumed to run on the
    BIND server. Details:
    - Libraries: `golang.org/x/crypto/ssh` and `pkg/sftp` (BSD).
    - Each primary gets its own key pair and is used by a dedicated, chrooted SFTP-only
      user. Personal keys are never used.
    - The host key is verified against a pinned key or `known_hosts` in the config, and
      unknown host keys are rejected.
    - Uploads are atomic: the file is written to a temporary name and then renamed. The
      zone directory must be writable for both the SFTP user and `named`, because `named`
      writes `.jnl` journal files.
    - Deletion: `rndc delzone`, then the zone file and its `.jnl` are removed over SFTP.
    - The initial zone file is rendered from an SOA/NS template in the config.
  - Secondaries learn about new zones through a **catalog zone** (RFC 9432). dcdash updates
    the catalog with RFC 2136 as well.
- **(proposed)** A common `dns.Provider` interface (list zones, list records, create, update,
  delete), so the frontend has one DNS UI for both backends. Provider-specific fields such as
  Cloudflare's `proxied` are optional attributes.

### 2.9 DHCP: Kea v3 (clarification, high)

- In Kea 3.x the `kea-dhcp4`/`kea-dhcp6` daemons can expose **HTTP/HTTPS control sockets
  directly**, and the Control Agent is deprecated. dcdash should talk to each daemon's
  socket, with basic auth and TLS (including client certificates).
- Feature availability depends on the hooks loaded on the server:
  - `lease_cmds`: list, add, update and delete leases.
  - `host_cmds`: manage reservations (needs a host database backend for persistence).
  - `subnet_cmds`: manage subnets.
  - Without hooks, only `status-get`, `config-get`, `statistic-get-all` and the
    `config-set`/`config-write` round trip are available.
  - Kea 3.0 also changed hook licensing. Task 8.1 verifies which hooks are available on
    the target Kea version.
  - **(decided, Q5)** `lease_cmds`, `host_cmds` with a host database, and `stat_cmds` are
    available, and more hooks can be installed if needed. The admin guide documents them as
    prerequisites.
- **(proposed)** v0.1.0 scope:
  - Read: server status and version, subnets and pools with utilisation, leases (search by
    IP, MAC or hostname).
  - Write: delete a lease, and add, change or delete host reservations (when `host_cmds` is
    present).
  - The UI detects missing hooks with `list-commands` and hides features that aren't
    available.
- Kea HA pairs: the config should know which servers belong together, so writes go to the
  right peer or both. Detecting HA pairs is in the plan, managing them isn't.

### 2.10 Cross-cutting gaps

| Topic | Proposal |
|---|---|
| Audit log | Every state-changing action and every login or failed login: who, when, what, target, result. Stored in SQLite, shown in the UI, optionally also written to syslog or a file. |
| Secrets in YAML | Every secret field also accepts `*_file` (path) or `env:VAR`, so the YAML can be committed without secrets. Warn on world-readable config. |
| API | REST/JSON under `/api/v1`, documented in an OpenAPI 3 spec that is kept in the repo. |
| Frontend stack | Vue 3 + TypeScript + Vite + Tailwind CSS v4 + Pinia + Vue Router + vue-i18n, embedded in the binary with `go:embed`. |
| Languages (Q7) | **(decided)** English and German. All UI strings are in translation files from the start; CI fails when keys are missing. The language comes from the browser, can be changed in the UI and is stored per user. Documentation stays in English. |
| Dark mode | **(decided)** Light, dark and "system" (follows `prefers-color-scheme`). Uses Tailwind's class-based dark variant, so every component, including the noVNC/xterm.js consoles and charts, is designed for both themes. The choice is stored per user on the server, with a local copy to avoid a flash of the wrong theme on load. |
| User preferences | Stored server-side per user (language, theme, later table settings). |
| Observability | **(decided)** Structured logging with `rs/zerolog` (MIT), JSON or console output; `/healthz` and `/readyz`; optional Prometheus `/metrics`. |
| Backend health | Show per-backend connectivity status on the dashboard home page. |
| CLI | `dcdash serve`, `dcdash config validate`, `dcdash version`, `dcdash totp reset <user>`. |
| Packaging | goreleaser: static Linux binaries, `.deb`/`.rpm` with a systemd unit, a container image. |
| CI | GitHub Actions: lint (golangci-lint, eslint), unit tests, integration tests, build. |
| Testing | Integration tests against containers: OpenLDAP (generic LDAP), bind9, Kea, Pebble (ACME test server). **(decided)** AD mode is tested against the existing Samba AD server in the network, from the dev VM only. Proxmox and Cloudflare use HTTP mocks. |
| License | Add the `LICENSE` file (GPLv3) and SPDX headers. All proposed dependencies (MIT, BSD, Apache-2.0, MPL-2.0) are GPLv3-compatible. |
| Concurrency | Several admins can edit the same DNS zone or Kea config. Use optimistic checks where the backend allows it (SOA serial, Cloudflare record IDs) and avoid read-modify-write of the whole Kea config. |
| HA of dcdash itself | Out of scope for v0.1.0: one instance, local SQLite. |

## 3. Questions and decisions

Answered on 2026-11-12.

| # | Question | Decision |
|---|---|---|
| Q1 | Is a local break-glass account wanted? | **Yes.** Disabled by default, always requires TOTP (2.3) |
| Q2 | Is TOTP mandatory for every user, or configurable per role? | **Configurable per role** (2.4) |
| Q3 | Proxmox scope: is a console (noVNC) needed in v0.1.0? | **Yes** (2.7) |
| Q4 | DNS scope: should zone creation and deletion be included? | **Yes** (2.8) |
| Q5 | Kea: is `host_cmds` with a host database available in your environment? | **Yes.** More hooks can be installed (2.9) |
| Q6 | Target platforms: Linux amd64/arm64 only? | **Yes** |
| Q7 | Are UI languages other than English needed? | **English and German** (2.10) |
| Q8 | Should the module order in the plan (Proxmox → DNS → DHCP) reflect your priorities? | **Yes** |

Other feedback:

- Creating VMs and containers is part of **v0.1.1** (2.7).
- Logging uses **zerolog** (2.10).
- **Dark mode** is a v0.1.0 feature (2.10).
