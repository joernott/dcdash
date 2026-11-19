#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Runs the NetBox integration test (test/integration/netbox_test.go) against a NetBox 4.7
# container (test/integration/netbox/compose.yaml). The first start runs the database
# migrations and takes several minutes; the database volume is kept, so later starts are
# fast. A new API token is provisioned for each run.
#
# Usage: scripts/netbox-test.sh [go test flags]
# Set KEEP=1 to keep NetBox running afterwards (http://127.0.0.1:18080, admin /
# netbox-test-only), DOWN=1 to remove the containers and the database volume afterwards.
#
# To test against another NetBox instead, set DCDASH_IT_NETBOX_URL and DCDASH_IT_NETBOX_TOKEN
# and run: go test -tags integration -count=1 -run NetBox ./test/integration/
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."
COMPOSE=(docker compose -f test/integration/netbox/compose.yaml)
URL=http://127.0.0.1:18080

cleanup() {
  local status=$?
  if [[ "${DOWN:-0}" == 1 ]]; then
    "${COMPOSE[@]}" down --volumes >/dev/null 2>&1 || true
  elif [[ "${KEEP:-0}" != 1 ]]; then
    "${COMPOSE[@]}" stop >/dev/null 2>&1 || true
  fi
  exit $status
}
trap cleanup EXIT

echo "==> Starting NetBox"
"${COMPOSE[@]}" up --detach
for i in $(seq 1 180); do
  # /api/status/ needs a token: 403 means NetBox is up.
  code=$(curl -s -o /dev/null -w '%{http_code}' "$URL/api/status/" || true)
  [[ "$code" == 403 || "$code" == 200 ]] && break
  if [[ $i == 180 ]]; then
    echo "NetBox did not start" >&2
    "${COMPOSE[@]}" logs --tail 40 netbox >&2
    exit 1
  fi
  sleep 5
done

echo "==> Provisioning an API token"
resp=$(curl -fsS -X POST -H 'Content-Type: application/json' -H 'Accept: application/json' \
  -d '{"username":"admin","password":"netbox-test-only"}' "$URL/api/users/tokens/provision/")
key=$(jq -r .key <<<"$resp")
secret=$(jq -r .token <<<"$resp")
export DCDASH_IT_NETBOX_URL=$URL
export DCDASH_IT_NETBOX_TOKEN="nbt_$key.$secret"

echo "==> Running the NetBox integration test"
go test -tags integration -count=1 -v -run NetBox ./test/integration/ "$@"
