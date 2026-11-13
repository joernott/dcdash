#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Starts dcdash for the end-to-end tests (started by playwright.config.ts).
# Uses the built binary (make build) with a fresh database, a self-signed
# certificate and the OpenLDAP test container (make test-e2e starts it).
# All keys and passwords here are test-only.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/../.."
BIN="${DCDASH_E2E_BIN:-bin/dcdash}"
DIR="${DCDASH_E2E_DIR:-.e2e}"
LDAP_URL="${DCDASH_E2E_LDAP_URL:-ldap://127.0.0.1:1389}"
LISTEN="${DCDASH_E2E_LISTEN:-127.0.0.1:18443}"

rm -rf "$DIR"
mkdir -p "$DIR"
chmod 700 "$DIR"
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 1 \
  -subj "/CN=localhost" -addext "subjectAltName=DNS:localhost,IP:127.0.0.1" \
  -keyout "$DIR/key.pem" -out "$DIR/cert.pem" 2>/dev/null
"$BIN" gen-key > "$DIR/encryption.key"

cat > "$DIR/config.yaml" <<EOF
server:
  listen: "$LISTEN"
  tls: {mode: files, cert_file: $DIR/cert.pem, key_file: $DIR/key.pem}
auth:
  ldap:
    url: "$LDAP_URL"
    insecure: true
    mode: generic
    bind_dn: "cn=dcdash-svc,ou=services,dc=dcdash,dc=test"
    bind_password: "svc-test-only"
    base_dn: "ou=people,dc=dcdash,dc=test"
    group_base_dn: "ou=groups,dc=dcdash,dc=test"
roles:
  # bob: no TOTP required; alice is in dashroot (root, TOTP required).
  - name: vm-operator
    groups: [dcdash-vm]
    require_totp: false
    permissions: ["proxmox:*:*:operate"]
security:
  encryption_key: "file:$DIR/encryption.key"
log: {level: warn, format: console}
data_dir: $DIR/data
EOF
chmod 600 "$DIR/config.yaml"
exec "$BIN" --config "$DIR/config.yaml" serve
