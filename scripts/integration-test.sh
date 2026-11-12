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
TEST_LOG="$WORK/go-test.log"
: > "$TEST_LOG"

# annotate prints text as a GitHub Actions error annotation (visible on the run
# page without access to the job log); outside of GitHub Actions it does nothing.
annotate() {
  [[ -n "${GITHUB_ACTIONS:-}" ]] || return 0
  local title=$1 text
  text=$(cat)
  text=${text//'%'/'%25'}
  text=${text//$'\r'/'%0D'}
  text=${text//$'\n'/'%0A'}
  echo "::error title=${title}::${text}"
}

cleanup() {
  local status=$?
  if [[ $status -ne 0 ]]; then
    echo "==> Integration tests failed, diagnostics:"
    "${COMPOSE[@]}" ps --all 2>&1 | tee /dev/stderr | annotate "docker compose ps" || true
    for svc in bind pebble step-ca; do
      "${COMPOSE[@]}" logs --no-color --tail 40 "$svc" 2>&1 | annotate "logs: $svc" || true
    done
    grep -E -- '--- FAIL|acme_test.go|"level":"error"|^(FAIL|panic)' "$TEST_LOG" | tail -40 | annotate "go test" || true
  fi
  if [[ "${KEEP:-0}" != 1 ]]; then
    "${COMPOSE[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || true
  fi
  exit $status
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
go test -tags integration -count=1 -v ./test/integration/... "$@" 2>&1 | tee "$TEST_LOG"
