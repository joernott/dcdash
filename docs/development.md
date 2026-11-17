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
