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

# Fake Proxmox API (internal/proxmox/pvefake) with the same certificate, pinned
# by its fingerprint. Playwright stops the whole process group at the end.
PVE_LISTEN="${DCDASH_E2E_PVE_LISTEN:-127.0.0.1:18006}"
go build -o "$DIR/pvefake" ./test/pvefake
"$DIR/pvefake" -listen "$PVE_LISTEN" -cert "$DIR/cert.pem" -key "$DIR/key.pem" 2>"$DIR/pvefake.log" &
PVE_FP=$(openssl x509 -in "$DIR/cert.pem" -noout -fingerprint -sha256 | cut -d= -f2)

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
  # bob: no TOTP required, power actions and consoles (no node shell);
  # alice is in dashroot (root, TOTP required).
  - name: vm-operator
    groups: [dcdash-vm]
    require_totp: false
    permissions: ["proxmox:*:*:console"]
proxmox:
  clusters:
    - name: lab
      endpoints: [{url: "https://$PVE_LISTEN", fingerprint: "$PVE_FP"}]
      token_id: "dcdash@pve!e2e"
      token_secret: "e2e-test-only"
      console: {username: "console@pve", password: "console-e2e"}
security:
  encryption_key: "file:$DIR/encryption.key"
log: {level: warn, format: console}
data_dir: $DIR/data
EOF
chmod 600 "$DIR/config.yaml"
exec "$BIN" --config "$DIR/config.yaml" serve
