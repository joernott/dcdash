# Development Guide

This guide covers how to build, run and test dcdash locally. The project workflow (sprints,
branches, task numbering) is described in the [implementation plan](implementation-plan.md).

## Prerequisites

dcdash is developed and built on Linux. The toolchain versions are pinned in the repository
root and used by CI as well:

| Tool | Version file |
|---|---|
| Go | `.go-version` |
| Node.js | `.node-version` |
| golangci-lint | `.golangci-lint-version` |

On Debian 13 the script `scripts/setup-dev-vm.sh` installs exactly these versions, plus
Docker (needed for integration tests from Sprint 3 on):

```sh
sudo scripts/setup-dev-vm.sh "$USER"   # then log out and in again for the docker group
make tools-check                       # verifies the installed versions
```

## Repository layout

| Path | Content |
|---|---|
| `cmd/dcdash/` | Entry point of the binary |
| `internal/certs/` | TLS certificates: files with hot reload, ACME (lego) with DNS-01 and renewal |
| `internal/cli/` | Command line interface (cobra): `serve`, `version`, `config validate` |
| `internal/config/` | YAML configuration: schema, defaults, validation, secrets |
| `internal/logging/` | zerolog setup |
| `internal/metrics/` | Optional Prometheus `/metrics`: HTTP, audit, certificate and backend metrics |
| `internal/store/` | SQLite state database and schema migrations |
| `internal/vault/` | AES-256-GCM encryption of secrets at rest, key ring for rotation |
| `internal/server/` | HTTP(S) server, middleware, health endpoints |
| `internal/ui/` | Embedded frontend build (`dist/`) and SPA handler |
| `internal/version/` | Build information (set via `-ldflags`) |
| `internal/proxmox/` | Proxmox API client (failover, pinning), inventory, power actions, tasks, console proxy; `pvefake/` is an in-memory fake of the Proxmox API for tests |
| `internal/dns/` | DNS model and API; `bind/` (AXFR, RFC 2136, rndc, SFTP, catalog zone) and `cloudflare/` (SDK, `cffake/` fake of the API for tests) |
| `internal/dhcp/` | DHCP API (permissions per subnet, HA groups); `kea/` is the Kea 3.x client, `kea/keafake/` an in-memory fake of a Kea control socket for tests |
| `internal/testutil/` | Helpers shared by tests |
| `test/integration/` | Integration tests (build tag `integration`) and their container environment; `dns/` holds the bind9 primary (with SFTP) and secondary, `kea/` the Kea configurations and the PostgreSQL image with the Kea schema |
| `test/pvefake/` | The Proxmox fake as a program, used by the end-to-end tests |
| `test/cffake/`, `test/keafake/` | The Cloudflare and Kea fakes as programs, used by the end-to-end tests |
| `api/openapi.yaml` | API specification; source of the TypeScript client types |
| `web/` | Frontend: Vue 3, TypeScript, Vite, Tailwind CSS v4, Pinia, vue-i18n |
| `configs/` | Example and development configuration |
| `scripts/` | Helper scripts |
| `docs/` | Concept, plan, prompt log, documentation |

`web/go.mod` is intentional: it excludes the frontend tree, including `node_modules` (where
some npm packages ship `.go` files), from the Go module.

## Make targets

Run `make` to list all targets. The most important ones:

| Target | Purpose |
|---|---|
| `make build` | Build the frontend, then `bin/dcdash` with the frontend embedded |
| `make build-go` | Build only the binary, with the frontend currently in `internal/ui/dist` |
| `make test` | Go tests (with race detector) and frontend unit tests |
| `make test-integration` | Integration tests against containers (bind9 for ACME, a bind9 primary with SFTP and a secondary for the DNS module, Kea DHCPv4/DHCPv6 with a PostgreSQL host database and a Kea HA pair, OpenLDAP, Pebble, step-ca); needs Docker and `rndc` (`bind9-utils`; `DCDASH_IT_RNDC` may point to another binary). `KEEP=1` keeps the containers running |
| `make test-e2e` | End-to-end tests (Playwright) of the built binary against the OpenLDAP test container and the Proxmox fake; needs Docker and `npx playwright install --with-deps chromium` once. Results and screenshots (light/dark, English/German) go to `.e2e/` |
| `make test-e2e-full` | The end-to-end tests against the full docker compose environment as well: dcdash then also uses the real bind9 primary (zone creation with rndc, SFTP and the catalog zone), Kea with the PostgreSQL host database and the Kea HA pair (`web/e2e/full.spec.ts`). Needs Docker and `rndc`; this is what the CI runs |
| `make lint` | golangci-lint, oxlint, ESLint, Prettier check, translation check, type check |
| `make fmt` | Format and auto-fix Go and frontend sources |
| `make gen-api` | Regenerate `web/src/api/schema.d.ts` from `api/openapi.yaml` |
| `make snapshot` | Build a local release into `dist/` with GoReleaser (see [Releases](#releases)) |
| `make clean` | Remove build output |

## Running locally

Use two terminals:

```sh
make dev-backend   # creates a self-signed cert in .dev/ and serves https://localhost:8443
make dev-web       # Vite dev server on https://localhost:5173 with hot reload
```

The dev server uses the same self-signed certificate (`make dev-cert`, run automatically). It
must use HTTPS because the session cookie is always `Secure`; over plain HTTP the browser
would not send it back and every login would seem to fail. Your browser warns about the
self-signed certificate once.

The Vite dev server proxies `/api`, `/healthz` and `/readyz` to the backend. Set
`DCDASH_BACKEND` to use another backend URL. If you develop on a remote machine or VM,
expose the dev server with `make dev-web DEV_HOST=0.0.0.0`.

`make dev-backend` uses `configs/dev.yaml`, which keeps all state in `.dev/` (git-ignored).
Because of the repository's file permissions it logs a warning that the config file is
readable by other users; in production the config file should be `chmod 0640`.

The production binary serves the embedded frontend itself, so after `make build` this is
enough:

```sh
./bin/dcdash --config configs/dev.yaml serve
```

## Tests against a real Proxmox cluster

The Proxmox integration tests in `test/integration/proxmox_test.go` run with `make
test-integration` when the `DCDASH_IT_PVE_*` variables are set (see
`scripts/integration-test.sh`). Keep them out of the repository, for example in a file with
mode 0600 that you load with `set -a; . file; set +a`. Use guests that exist only for tests:
`DCDASH_IT_PVE_POWER_GUEST` guests are started, suspended, resumed and stopped.

Last manual test: 2026-11-13, Proxmox VE 9.0.11, single node, API token with privilege
separation (`PVEAuditor` on `/`, `PVEVMUser` on two test guests) and a console user.

| Check | Result |
|---|---|
| Inventory (`TestProxmoxRead`): node, 35 VMs, 3 containers, storage | passed |
| Power actions on a VM: start, suspend, resume, stop, with task status and log | passed |
| Power actions on a container: start, stop | passed |
| VNC console with the API token and with the console user | passed |
| Container terminal with the console user | passed |
| Container terminal with the API token | rejected by Proxmox, as described in ADR 0001 |
| Browser (Chromium): login, inventory, start and stop from the guest page, VNC console with the guest's screen, container terminal with login prompt | passed |

The browser test used a separate dcdash configuration whose role allowed `console` only on
the two test guests.

### Creating and deleting guests

`TestProxmoxProvisioning` in `test/integration/proxmox_provision_test.go` creates guests
through dcdash's API and deletes them again (also when it fails). It runs when
`DCDASH_IT_PVE_PROVISION_VMIDS` is set, for example:

```sh
DCDASH_IT_PVE_PROVISION_VMIDS=1001-1099 \
DCDASH_IT_PVE_PROVISION_TEMPLATE=999 \
DCDASH_IT_PVE_PROVISION_CT_TEMPLATE=vm_data:vztmpl/debian-13-standard_13.1-2_amd64.tar.zst \
DCDASH_IT_PVE_PROVISION_STORAGE=vm_data DCDASH_IT_PVE_PROVISION_BRIDGE=kunden0 \
DCDASH_IT_PVE_PROVISION_VLAN=172 DCDASH_IT_PVE_PROVISION_ISO=vm_data:iso/debian-13.7.0-amd64-netinst.iso \
go test -tags integration -run TestProxmoxProvisioning -v ./test/integration/
```

Use an ID range that holds no other guests. The guests are not started.

Last manual test: 2026-11-18 (first on 2026-11-17), Proxmox VE 9, single node, API token with privilege
separation and `PVEVMAdmin`, `PVEDatastoreUser` and `PVESDNUser` (see
[proxmox.md](proxmox.md#privileges-of-the-api-token)).

| Check | Result |
|---|---|
| Options: storages, bridges, templates and container templates of the node | passed |
| Linked clone of a VM template with cloud-init (user, SSH key, static IPv4, DNS), VLAN tag, disk grown from 64 to 65 GiB, a cloud-init drive added | passed |
| Container from a template: unprivileged, VLAN tag, SSH key | passed |
| VM from an ISO image | passed |
| Delete with purge, typing the name, right after the creation | passed after a fix: `/cluster/resources` lags behind with the name, dcdash now reads it from the configuration |
| SSH keys with `+` and `/` (encoding of `sshkeys`) | passed |
| Two network devices (2026-11-18): the clone keeps a given MAC on `net0` and gets `net1` with a generated MAC and `ipconfig1=ip6=auto`; the container gets `eth1` with `hwaddr` | passed |

### Changing VMs

`TestProxmoxHardware` in `test/integration/proxmox_hardware_test.go` creates an empty VM with
the Proxmox API in the same ID range, changes it through dcdash's API and deletes it. It needs
`DCDASH_IT_PVE_PROVISION_VMIDS`, `_STORAGE` and `_BRIDGE` (optionally `_VLAN`), no template:

```sh
DCDASH_IT_PVE_PROVISION_VMIDS=1001-1099 DCDASH_IT_PVE_PROVISION_STORAGE=vm_data \
DCDASH_IT_PVE_PROVISION_BRIDGE=kunden0 DCDASH_IT_PVE_PROVISION_VLAN=44 \
go test -tags integration -run TestProxmoxHardware -v ./test/integration/
```

Last manual test: 2026-11-18, Proxmox VE 9, same token. The VM is never started, so pending
changes of running VMs are only covered by the fake API.

| Check | Result |
|---|---|
| Change and remove options (cores, memory, agent, boot order, description) | passed |
| A change with an outdated digest is refused | passed after a fix: Proxmox answers synchronously with "checksum mismatch (file change by other user?)", which dcdash now reports as `config_changed` |
| Disk: create, grow from 1 to 2 GiB, detach, attach to another bus, delete | passed |
| Network device: add with VLAN and firewall, change model and link state (the MAC stays), remove | passed |

## Tests against a real NetBox

The unit and end-to-end tests use the NetBox fake (`internal/netbox/nbfake`). `TestNetBox` in
`test/integration/netbox_test.go` runs against a real NetBox when `DCDASH_IT_NETBOX_URL` and
`DCDASH_IT_NETBOX_TOKEN` are set. `scripts/netbox-test.sh` starts NetBox 4.7 in a container
(`test/integration/netbox/compose.yaml`), provisions a token and runs the test. The first
start runs the database migrations and takes about 15 minutes on a small VM, which is why the
test is not part of the CI; the database volume is kept for later runs (`DOWN=1` removes it,
`KEEP=1` keeps NetBox running).

The test creates its own tag, cluster, VLAN group, VLAN and prefix and deletes them at the
end. Last run: 2026-11-19, NetBox 4.7.5.

| Check | Result |
|---|---|
| Status check (token, tag, cluster), VLAN with prefix and free addresses | passed |
| Guest written: VM, virtual disk, interfaces with MAC address and VLAN, static address as primary address | passed |
| Guest changed: renamed, more memory, disk grown and added, interface removed, new MAC address (the old one is deleted) | passed |
| DHCP reservation assigned to the interface by MAC address; DNS name set and cleared | passed |
| Comparison finds a changed value | passed |
| Guest deleted: VM with interfaces and addresses removed | passed |
| Projects: tag created, prefix found by search, tagged, listed by tag and untagged | passed |

### Copy of a production NetBox

On 2026-11-19 dcdash was tested against a copy of a production NetBox 4.7.5 (`pg_dump`,
restored into a separate container on the dev VM, deleted afterwards) and the real test
Proxmox cluster. `TestNetBox` passed on the copy and left no objects behind. The comparison of
37 guests with 26 virtual machines of the mapped cluster found 23 pairs, 14 guests only in
Proxmox and 3 VMs only in NetBox.

The real data had interfaces named like inside the guest (`ens18`, `eth0`, `Ethernet`,
`kunden_vm0`), often without MAC address, with the addresses assigned to them. The write-back
would have deleted these interfaces and, with them, their addresses. Since then interfaces
are matched by name or primary MAC address, and only interfaces with the dcdash tag are
deleted (task 13.11). "Update NetBox" on two real VMs then kept all interfaces and addresses
maintained by hand and showed no differences afterwards.
## Configuration

See `configs/config.example.yaml` for all options. Check a configuration with:

```sh
dcdash --config /etc/dcdash/config.yaml config validate
```

Secrets can be given as `env:NAME` or `file:/path` instead of literal values. Unknown keys
are rejected, so typos are caught early.

## Conventions

### Backend

- Each new source file starts with `// SPDX-License-Identifier: GPL-3.0-or-later`.
- Logging uses zerolog. In HTTP handlers, get the request logger (with request ID) with
  `hlog.FromRequest(r)`.
- API errors are JSON (`{"error": {"code": "...", "message": "..."}}`). `code` is stable and
  translated by the frontend, `message` is an English fallback.
- Every API change updates `api/openapi.yaml`, followed by `make gen-api`. CI fails if the
  generated types are out of date.
- **Permission checks:** every API handler checks the caller's permission (see
  [security.md](security.md#roles-and-permissions)). Wrap handlers that need a fixed
  permission with `auth.Require(rbac.MustParse("system:core:audit:read"), h)`, or root-only
  handlers with `auth.RequireRoot(h)`. When the permission depends on the request, call
  `auth.Check(w, r, rbac.P(module, instance, resource, action))` in the handler; it writes the
  401 or 403 response itself. Never rely on the frontend hiding something.

### Frontend

- **Translations:** every user-visible text goes into `web/src/locales/en.json` **and**
  `de.json`. `npm run check` (and CI) fails on missing, extra or empty keys.
- **Dark mode:** every component is styled for light and dark mode with Tailwind's `dark:`
  variant. Dark mode is activated by the `dark` class on `<html>`.
- `npm run lint` and `npm run format` fix issues; `npm run check` only reports them, as in
  CI.

## Continuous integration

`.github/workflows/ci.yml` runs on pushes to `main` and `sprint/**` and on pull requests:

1. **Go:** golangci-lint and `go test -race ./...`
2. **Integration:** `scripts/integration-test.sh` with bind9, Pebble and step-ca containers
3. **Frontend:** lint, format and translation check, type check, unit tests, check that the
   generated API types are current, build
4. **Build:** builds the binary with the embedded frontend, runs a smoke test and uploads
   the binary as an artifact
5. **End-to-end:** the Playwright tests against the built binary, with the fakes and the full
   docker compose environment (`scripts/e2e-full.sh`). Every view is also checked for
   WCAG 2.1 AA violations (axe-core, light and dark mode)
6. **Dependency audit:** `govulncheck` (Go, reachable code only) and `npm audit` (all
   frontend dependencies, including build tools)

## Releases

Releases are built by [GoReleaser](https://goreleaser.com/) (`.goreleaser.yaml`) when a tag
`v*` is pushed (`.github/workflows/release.yml`):

- Archives with the binary, the documentation, the example configuration and the systemd
  unit for Linux amd64 and arm64.
- `.deb` and `.rpm` packages (`packaging/`): the user `dcdash`, `/etc/dcdash/config.yaml` from
  the example, an encryption key generated at the first installation, and the hardened
  systemd unit `packaging/systemd/dcdash.service`. The service is not started automatically.
- `SHA256SUMS` for all files.
- The container image `ghcr.io/joernott/dcdash` (`packaging/Dockerfile`, distroless, user
  `nonroot`) for both architectures, tagged with the version, `<major>.<minor>` and `latest`.
  Pre-releases (for example `v0.2.0-rc.1`) get only the version tag.
- The release notes are the version's section in `CHANGELOG.md`
  (`scripts/release-notes.sh`); the workflow fails if it is missing.

To release:

1. Move the entries under `[Unreleased]` in `CHANGELOG.md` to a new version section and add
   its link at the bottom. Merge to `main`.
2. Tag the merge commit and push the tag: `git tag -a v0.1.0 -m v0.1.0 && git push origin v0.1.0`.

`make snapshot` builds the same files locally into `dist/` without publishing anything; it
needs GoReleaser 2 and Docker with buildx.
