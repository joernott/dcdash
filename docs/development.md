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
| `internal/store/` | SQLite state database and schema migrations |
| `internal/vault/` | AES-256-GCM encryption of secrets at rest, key ring for rotation |
| `internal/server/` | HTTP(S) server, middleware, health endpoints |
| `internal/ui/` | Embedded frontend build (`dist/`) and SPA handler |
| `internal/version/` | Build information (set via `-ldflags`) |
| `internal/testutil/` | Helpers shared by tests |
| `test/integration/` | Integration tests (build tag `integration`) and their container environment |
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
| `make test-integration` | Integration tests against containers (bind9, Pebble, step-ca); needs Docker. `KEEP=1` keeps the containers running |
| `make lint` | golangci-lint, oxlint, ESLint, Prettier check, translation check, type check |
| `make fmt` | Format and auto-fix Go and frontend sources |
| `make gen-api` | Regenerate `web/src/api/schema.d.ts` from `api/openapi.yaml` |
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
