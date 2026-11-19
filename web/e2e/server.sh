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
# A project (task 12.8); its tag is created in the NetBox fake at startup.
mkdir -p "$DIR/projects"
cat > "$DIR/projects/webshop.yaml" <<'PROJECT'
title: Webshop
color: 2196f3
description: The new web shop
PROJECT

# Fake Proxmox API (internal/proxmox/pvefake) with the same certificate, pinned
# by its fingerprint. Playwright stops the whole process group at the end.
PVE_LISTEN="${DCDASH_E2E_PVE_LISTEN:-127.0.0.1:18006}"
go build -o "$DIR/pvefake" ./test/pvefake
"$DIR/pvefake" -listen "$PVE_LISTEN" -cert "$DIR/cert.pem" -key "$DIR/key.pem" 2>"$DIR/pvefake.log" &
PVE_FP=$(openssl x509 -in "$DIR/cert.pem" -noout -fingerprint -sha256 | cut -d= -f2)

# Fake Cloudflare API (internal/dns/cloudflare/cffake), plain HTTP on loopback.
CF_LISTEN="${DCDASH_E2E_CF_LISTEN:-127.0.0.1:18007}"
go build -o "$DIR/cffake" ./test/cffake
"$DIR/cffake" -listen "$CF_LISTEN" 2>"$DIR/cffake.log" &

# Fake Kea DHCPv4 and DHCPv6 servers (internal/dhcp/kea/keafake), plain HTTP on loopback.
KEA4_LISTEN="${DCDASH_E2E_KEA4_LISTEN:-127.0.0.1:18010}"
KEA6_LISTEN="${DCDASH_E2E_KEA6_LISTEN:-127.0.0.1:18011}"
go build -o "$DIR/keafake" ./test/keafake
"$DIR/keafake" -listen4 "$KEA4_LISTEN" -listen6 "$KEA6_LISTEN" 2>"$DIR/keafake.log" &

# Fake NetBox (internal/netbox/nbfake), plain HTTP on loopback.
NB_LISTEN="${DCDASH_E2E_NB_LISTEN:-127.0.0.1:18020}"
go build -o "$DIR/nbfake" ./test/nbfake
"$DIR/nbfake" -listen "$NB_LISTEN" 2>"$DIR/nbfake.log" &

# Fake Aruba CX switch (internal/switches/aoscx/cxfake) with the same certificate, pinned by
# its fingerprint.
CX_LISTEN="${DCDASH_E2E_CX_LISTEN:-127.0.0.1:18030}"
go build -o "$DIR/cxfake" ./test/cxfake
"$DIR/cxfake" -listen "$CX_LISTEN" -cert "$DIR/cert.pem" -key "$DIR/key.pem" 2>"$DIR/cxfake.log" &

# With DCDASH_E2E_FULL=1 (scripts/e2e-full.sh), dcdash also uses the real services of
# the docker compose environment: the bind9 primary (records, rndc, SFTP, catalog zone),
# Kea DHCPv4/DHCPv6 with the PostgreSQL host database, and the Kea HA pair.
FULL_ROLES="" FULL_DNS="" FULL_DHCP=""
if [[ "${DCDASH_E2E_FULL:-}" == 1 ]]; then
  FULL_ROLES='
  - name: full-e2e
    groups: [dcdash-vm]
    require_totp: false
    permissions: ["dns:bind:static.dcdash.test:write", "dns:bind:*.e2e.dcdash.test:admin", "dhcp:kea4-db:*:write", "dhcp:kea-ha*:*:operate"]'
  FULL_DNS="
    - name: bind
      type: bind
      timeout: 10s
      bind:
        nameserver: $DCDASH_IT_BIND_PRIMARY
        tsig_key: dcdash.
        tsig_secret: \"$DCDASH_IT_BIND_TSIG_SECRET\"
        tsig_algorithm: hmac-sha256
        zones: [static.dcdash.test]
        catalog_zone: catalog.dcdash.test
        rndc: {binary: \"$DCDASH_IT_RNDC\", server: $DCDASH_IT_BIND_PRIMARY, port: 953, key_file: \"$DCDASH_IT_BIND_RNDC_KEY\"}
        sftp:
          address: \"$DCDASH_IT_BIND_PRIMARY:22\"
          user: dcdash
          private_key_file: \"$DCDASH_IT_BIND_SFTP_KEY\"
          host_key: \"$DCDASH_IT_BIND_SFTP_HOSTKEY\"
          zone_dir: /zones
        new_zones:
          file_dir: /srv/sftp/zones
          options: 'update-policy { grant dcdash. zonesub ANY; }; allow-transfer { key dcdash.; 10.77.0.55; }; also-notify { 10.77.0.55; };'
          ttl: 1h
          soa: {mname: ns1.dcdash.test, rname: hostmaster.dcdash.test}
          nameservers: [ns1.dcdash.test]"
  FULL_DHCP="
    - {name: kea4-db, family: v4, url: \"$DCDASH_IT_KEA4_URL\", insecure: true, username: dcdash, password: \"$DCDASH_IT_KEA_PASSWORD\"}
    - {name: kea-ha1, family: v4, url: \"$DCDASH_IT_KEA_HA1_URL\", insecure: true, username: dcdash, password: \"$DCDASH_IT_KEA_PASSWORD\", ha_group: ha}
    - {name: kea-ha2, family: v4, url: \"$DCDASH_IT_KEA_HA2_URL\", insecure: true, username: dcdash, password: \"$DCDASH_IT_KEA_PASSWORD\", ha_group: ha}"
fi

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
  # bob: no TOTP required, power actions and consoles (no node shell), creating and
  # deleting guests with IDs 5000-5999;
  # alice is in dashroot (root, TOTP required).
  - name: vm-operator
    groups: [dcdash-vm]
    require_totp: false
    permissions: ["proxmox:*:*:console"]
  - name: vm-creator
    groups: [dcdash-vm]
    require_totp: false
    permissions: ["proxmox:lab:qemu/5*:admin", "proxmox:lab:lxc/5*:admin"]
  - name: dns-editor
    groups: [dcdash-vm]
    require_totp: false
    permissions: ["dns:cf:*:write"]
  - name: dhcp-operator
    groups: [dcdash-vm]
    require_totp: false
    permissions: ["dhcp:kea4:192.0.2.0/24:write", "dhcp:kea4:*:read", "dhcp:kea6:*:read"]
  - name: netbox-user
    groups: [dcdash-vm]
    require_totp: false
    permissions: ["netbox:netbox:*:write", "project:*:objects:write", "proxmox:lab:lxc/200:write"]
  - name: switch-operator
    groups: [dcdash-vm]
    require_totp: false
    permissions: ["switch:sw1:*:read", "switch:sw1:port/1/1/5:write", "switch:sw1:vlan/4*:write"]
$FULL_ROLES
proxmox:
  clusters:
    - name: lab
      endpoints: [{url: "https://$PVE_LISTEN", fingerprint: "$PVE_FP"}]
      token_id: "dcdash@pve!e2e"
      token_secret: "e2e-test-only"
      console: {username: "console@pve", password: "console-e2e"}
      provisioning:
        vmids: ["5000-5999"]
        storages: [local-lvm, ceph]
        image_storages: [local]
        bridges: [vmbr0]
        vlans: ["100-199"]
        limits: {cores: 8, memory: 16G, disk: 200G}
dns:
  providers:
    - name: cf
      type: cloudflare
      cloudflare:
        api_token: "cf-e2e-test-only"
        base_url: "http://$CF_LISTEN/client/v4/"
$FULL_DNS
dhcp:
  servers:
    - {name: kea4, family: v4, url: "http://$KEA4_LISTEN", insecure: true, username: dcdash, password: "kea-e2e-test-only"}
    - {name: kea6, family: v6, url: "http://$KEA6_LISTEN", insecure: true, username: dcdash, password: "kea-e2e-test-only"}
$FULL_DHCP
netbox:
  url: "http://$NB_LISTEN"
  insecure: true
  token: "nbt_e2e.netbox-e2e-test-only"
  clusters: {lab: "Lab"}
switches:
  - name: sw1
    url: "https://$CX_LISTEN"
    tls: {fingerprint: "$PVE_FP"}
    username: dcdash
    password: "cx-e2e-test-only"
    netbox_device: sw-e2e
projects:
  dir: "$DIR/projects"
metrics: {enabled: true, token: "metrics-e2e-test-only"}
security:
  encryption_key: "file:$DIR/encryption.key"
log: {level: warn, format: console}
data_dir: $DIR/data
EOF
chmod 600 "$DIR/config.yaml"
exec "$BIN" --config "$DIR/config.yaml" serve
