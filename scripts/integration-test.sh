#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Starts the integration test environment (test/integration/compose.yaml), runs
# the Go integration tests against it and tears it down again.
#
# Usage: scripts/integration-test.sh [go test flags, e.g. -run Pebble]
# Set KEEP=1 to keep the containers running afterwards.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."
COMPOSE=(docker compose -f test/integration/compose.yaml)
WORK=.dev/integration
mkdir -p "$WORK"

cleanup() {
  if [[ "${KEEP:-0}" != 1 ]]; then
    "${COMPOSE[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

echo "==> Starting test environment"
"${COMPOSE[@]}" up --detach --wait --wait-timeout 180

# Pebble has no health check (distroless image): wait for its directory.
for _ in $(seq 1 60); do
  curl -fsk https://localhost:14000/dir >/dev/null && break
  sleep 1
done

echo "==> Fetching CA certificates"
docker cp "$("${COMPOSE[@]}" ps -q pebble):/test/certs/pebble.minica.pem" "$WORK/pebble-ca.pem"
docker cp "$("${COMPOSE[@]}" ps -q step-ca):/home/step/certs/root_ca.crt" "$WORK/step-ca-root.pem"

export DCDASH_IT_DNS=10.77.0.53:53
export DCDASH_IT_PEBBLE_DIRECTORY=https://localhost:14000/dir
export DCDASH_IT_PEBBLE_CA="$PWD/$WORK/pebble-ca.pem"
export DCDASH_IT_STEPCA_DIRECTORY=https://localhost:19000/acme/acme/directory
export DCDASH_IT_STEPCA_CA="$PWD/$WORK/step-ca-root.pem"

echo "==> Running integration tests"
go test -tags integration -count=1 -v ./test/integration/... "$@"
