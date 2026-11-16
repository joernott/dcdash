# ADR 0003: Access to Kea 3.0

- Status: accepted
- Date: 2026-11-16
- Task: 8.1

## Context

dcdash manages Kea DHCPv4 and DHCPv6 servers (concept review 2.9). Kea 3.0 is the first Kea
LTS release (three years of support). It changes how clients reach the API and which hook
libraries are free. Task 8.1 checks what dcdash can rely on.

## Spike

The spike ran against the official ISC image `docker.cloudsmith.io/isc/docker/kea-dhcp4:3.0.4`
on the dev VM: first with memfile leases only, then with a PostgreSQL 17 host database. The
image is Alpine based and runs `kea-dhcp4` directly, as root, without the Control Agent. A
`kea-dhcp6:3.0.4` image exists as well.

### API access

- The daemons take commands directly over HTTP or HTTPS. They're configured in the list
  `control-sockets` (the old single `control-socket` is gone):

  ```json
  "control-sockets": [{
    "socket-type": "https",
    "socket-address": "0.0.0.0",
    "socket-port": 8000,
    "trust-anchor": "/etc/kea/ca.pem",
    "cert-file": "/etc/kea/kea.pem",
    "key-file": "/etc/kea/kea.key",
    "cert-required": true,
    "authentication": {
      "type": "basic", "realm": "kea", "directory": "/etc/kea",
      "clients": [{"user": "dcdash", "password-file": "dcdash.password"}]
    }
  }]
  ```

- The Control Agent is deprecated in 3.0 and removed in 3.2. dcdash therefore talks to each
  daemon itself, not to the Control Agent.
- A request without valid credentials gets HTTP 401 with `{"result": 401, "text":
  "Unauthorized"}`.
- Answers are always a JSON **array** with one element per service, even on a daemon's own
  socket. A `service` parameter is accepted and has no effect there.
- Result codes: 0 success, 1 error, 2 command not supported, 3 empty (nothing found or
  changed), 4 conflict.
- `list-commands`, `version-get`, `status-get` and `config-hash-get` work without hooks.
  `status-get` has a `high-availability` part only when the HA hook is loaded.
- `config-get` returns the whole configuration **including passwords** that are written
  inline, such as the host database password. dcdash never passes it on.

### Hooks

Kea 3.0 made most hook libraries open source. Only `cb_cmds` (configuration backend) and
`rbac` remain for ISC subscribers. All hooks dcdash uses are in the official image and in
ISC's packages: `lease_cmds`, `host_cmds`, `stat_cmds`, `subnet_cmds`, `ha`, and the
`pgsql` and `mysql` backends.

`list-commands` lists the commands of the loaded hooks, so dcdash can detect what a server
offers.

### Leases and statistics

- `lease4-get-page` and `lease6-get-page` page through all leases (`from`, `limit`).
  Searches by MAC (`lease4-get-by-hw-address`), client ID, DUID (`lease6-get-by-duid`) and
  hostname exist. "Not found" is result 3 with an empty list.
- `lease4-del` and `lease6-del` delete a lease by address.
- `stat-lease4-get` and `stat-lease6-get` (`stat_cmds`) return a table per subnet with total,
  assigned and declined addresses (v6 also prefixes).
- `statistic-get-all` has the same numbers per pool ID (`subnet[1].pool[0].assigned-addresses`).
  The ID is the pool's `pool-id`. Pools without one count together as pool 0, so per-pool
  numbers exist only where the configuration sets pool IDs.
- `subnet4-list` and `subnet6-list` (`subnet_cmds`) return the ID, prefix and shared network.
  Pools and options come from `subnet4-get`.

### Reservations

- Reservations from the configuration file are read-only: `reservation-update` answers "Host
  not updated (not found)" for them.
- Without a host database, `reservation-add` fails ("Host database not available"). With
  `"operation-target": "memory"` the reservation is added, but it's lost on restart unless the
  configuration is written back with `config-write`. That command only writes into the
  configuration file's own directory. It also rewrites the operator's file, without comments.
- With a host database (`hosts-database`, PostgreSQL or MySQL, hook `libdhcp_pgsql.so` or
  `libdhcp_mysql.so`), `reservation-add`, `reservation-update` and `reservation-del` work and
  persist.
- `"operation-target"` selects the source when reading: `memory` (the configuration file),
  `database`, or `all`. So dcdash can tell the sources apart.

### Test setup

- Neither the image nor the packages contain `kea-admin` or the database schema. The schema
  `src/share/database/scripts/pgsql/dhcpdb_create.pgsql` is in the source archive
  `kea-3.0.4.tar.xz`. Its ISC signature checks out (key `0259A33B5F5A3A4466CF345C7A5E084CACA51884`)
  and its SHA-256 is `62d32494a7ac62d130149ae2f1c6ea5c3c28ff4caa15cc6e1b9812ffb4158282`.
- PostgreSQL loads the schema at its first start from `/docker-entrypoint-initdb.d`.
- Kea 3.0 checks file locations: hooks only from the hooks directory, lease files only from
  its data directory, and so on. The `-X` switch turns these checks off.

## Decision

1. **Target Kea 3.0 and later.** dcdash talks to each `kea-dhcp4` and `kea-dhcp6` daemon over
   its own HTTP(S) control socket. There's no Control Agent support.
2. **Server configuration.** Each server has:
   - a name, used as the permission instance;
   - its family (`v4` or `v6`);
   - the URL;
   - TLS settings: CA file or certificate fingerprint, and an optional client certificate
     and key;
   - a basic-auth user and password.

   Plain HTTP is allowed only with an explicit `insecure: true`, as for LDAP.
3. **Capabilities** come from `list-commands`. They're cached and refreshed with the status.
   A feature whose commands are missing is hidden in the UI and answered with 409 by the API.
4. **Reservations are written to the host database only.** Reservations from the
   configuration file are shown as read-only. dcdash doesn't use `operation-target: memory`
   or `config-write`, because changes would be lost on restart or would rewrite the
   operator's file.
5. **No `config-get` passthrough.** Subnets come from `subnet4-list`/`subnet6-list` and
   `subnet4-get`/`subnet6-get`; utilisation comes from `stat-lease4-get`/`stat-lease6-get`.
6. **Tests** use the official `kea-dhcp4` and `kea-dhcp6` 3.0.4 images and PostgreSQL 17 with
   the schema from the source archive. The PostgreSQL image is built with the archive pinned by
   its SHA-256, so the schema isn't copied into this repository.

## Open question for task 8.6

It's not documented whether a lease change made through `lease_cmds` on one HA partner is
replicated to the other one. Task 8.6.1 checks this with an HA pair in containers. If it
isn't replicated, dcdash sends lease deletions to both partners. Reservations live in the
shared host database, so they're written once.

## Consequences

- Kea 2.x servers, and servers that only offer the Control Agent, aren't supported.
- Changing reservations needs a host database. The admin guide (8.9) lists it as a
  prerequisite. Servers without one still show their leases and reservations, read-only.
- dcdash needs its own Kea API user per server. That user can run every command, because
  role-based access control (`rbac`) is a subscriber hook. dcdash's own permissions restrict
  what each dcdash user can do.
