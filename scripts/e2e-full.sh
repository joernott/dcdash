#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Runs the end-to-end tests (Playwright) against the full docker compose test
# environment: besides the fakes (Proxmox, Cloudflare, Kea), dcdash then also uses
# the real bind9 primary and secondary, Kea DHCPv4/DHCPv6 with PostgreSQL and the Kea
# HA pair, and web/e2e/full.spec.ts runs.
#
# Needs Docker, rndc (bind9-utils), a built binary (make build) and the Playwright
# browser (npx playwright install --with-deps chromium).
# Usage: scripts/e2e-full.sh [playwright flags]; KEEP=1 keeps the containers running.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."
COMPOSE=(docker compose -f test/integration/compose.yaml)

cleanup() {
  local status=$?
  if [[ "${KEEP:-0}" != 1 ]]; then
    "${COMPOSE[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || true
    rm -rf .dev/integration/dns-keys .dev/integration/env.sh
  fi
  exit $status
}
trap cleanup EXIT

# Starts the containers and writes their settings to .dev/integration/env.sh; runs no
# Go test (-run '^$').
KEEP=1 DCDASH_IT_ENV_FILE=/dev/null scripts/integration-test.sh -run '^$'
# shellcheck disable=SC1091
. .dev/integration/env.sh

echo "==> Running end-to-end tests against the full environment"
cd web
DCDASH_E2E_FULL=1 npx playwright test "$@"
