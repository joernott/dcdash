# DHCP: Kea

dcdash shows the subnets, leases and reservations of Kea DHCPv4 and DHCPv6 servers. Users
can delete leases and manage reservations. What each user sees and may change depends on
their permissions per subnet (see [security.md](security.md#dhcp-permissions)). Every
change is recorded in the audit log.

dcdash needs **Kea 3.0 or later**. It talks to each `kea-dhcp4` and `kea-dhcp6` daemon over
the daemon's own HTTP(S) control socket. The Kea Control Agent isn't used: it's deprecated
in Kea 3.0 and removed in 3.2. The design and the behavior it's based on are described in
[ADR 0003](adr/0003-kea-api.md).

```yaml
dhcp:
  servers:
    - name: dhcp4-a                    # used in the UI and in permissions (dhcp:dhcp4-a:...)
      family: v4                       # v4 (kea-dhcp4) or v6 (kea-dhcp6)
      url: https://kea1.example.com:8000
      tls:
        ca_file: /etc/dcdash/kea-ca.pem
        # Client certificate, if Kea's control socket has cert-required: true.
        cert_file: /etc/dcdash/kea-client.pem
        key_file: /etc/dcdash/kea-client.key
      username: dcdash
      password: "file:/etc/dcdash/kea-dcdash.password"
      ha_group: site1                  # optional, see "HA pairs"
    - name: dhcp4-b
      family: v4
      url: https://kea2.example.com:8000
      tls: {ca_file: /etc/dcdash/kea-ca.pem}
      username: dcdash
      password: "file:/etc/dcdash/kea-dcdash.password"
      ha_group: site1
```

Instead of `ca_file`, `tls.fingerprint` pins the server certificate (SHA-256), as for LDAP and
Proxmox. Plain `http://` URLs are refused unless `insecure: true` is set, because the
password would cross the network in clear text. Use that only for tests or for a socket
bound to `127.0.0.1` on the dcdash host.

## Prerequisites on the Kea servers

### Control socket

Add an HTTPS control socket with basic authentication to each daemon's configuration
(`kea-dhcp4.conf` or `kea-dhcp6.conf`):

```json
"control-sockets": [
  {
    "socket-type": "https",
    "socket-address": "0.0.0.0",
    "socket-port": 8000,
    "trust-anchor": "/etc/kea/ca.pem",
    "cert-file": "/etc/kea/kea.pem",
    "key-file": "/etc/kea/kea.key",
    "cert-required": false,
    "authentication": {
      "type": "basic",
      "realm": "kea",
      "directory": "/etc/kea",
      "clients": [ { "user": "dcdash", "password-file": "dcdash.password" } ]
    }
  }
]
```

- Each daemon needs its own address or port. A DHCPv4 and a DHCPv6 daemon on one host can
  use ports 8000 and 8001, for example.
- With `"cert-required": true`, Kea accepts only clients with a certificate signed by the
  `trust-anchor`. Configure that certificate in dcdash as `tls.cert_file` and `tls.key_file`.
- Keep the password in a file (`password-file`), not in the configuration: Kea's
  `config-get` returns the whole configuration, including passwords written in it. dcdash
  uses `config-get` only internally and never shows it.
- Allow only the dcdash host to reach the port (firewall). The Kea user can run every
  command, because role-based access control (`libdhcp_rbac.so`) is only available to ISC
  subscribers. dcdash's own permissions decide what each dcdash user can do.

### Hook libraries

dcdash shows a feature only if the server has the hook library it needs, and tells the user
which one is missing. Since Kea 3.0, all of these are open source and part of ISC's
packages:

| Hook library | dcdash feature |
|---|---|
| `libdhcp_lease_cmds.so` | Show, search and delete leases |
| `libdhcp_host_cmds.so` | Show reservations; with a host database, add, change and delete them |
| `libdhcp_stat_cmds.so` | Utilization of each subnet |
| `libdhcp_subnet_cmds.so` | Pools of each subnet (without it, dcdash lists the subnets without pools) |
| `libdhcp_pgsql.so` or `libdhcp_mysql.so` | The host database (see below) |
| `libdhcp_ha.so` | HA state on the server overview |

```json
"hooks-libraries": [
  { "library": "libdhcp_pgsql.so" },
  { "library": "libdhcp_lease_cmds.so" },
  { "library": "libdhcp_host_cmds.so" },
  { "library": "libdhcp_stat_cmds.so" },
  { "library": "libdhcp_subnet_cmds.so" }
]
```

Since Kea 2.7.9, libraries are loaded only from Kea's hooks directory (`kea-dhcp4 -W` shows
it), so the file name is enough.

### Host database

Reservations in the Kea configuration file can't be changed through the API. dcdash shows
them as read-only. To add, change and delete reservations, Kea needs a host database in
PostgreSQL or MySQL:

```json
"hosts-database": {
  "type": "postgresql",
  "host": "db.example.com",
  "name": "kea",
  "user": "kea",
  "password": "...",
  "on-fail": "serve-retry-continue"
}
```

Create the schema with `kea-admin db-init pgsql` (or `mysql`). The DHCPv4 and DHCPv6
daemons can share one database. Reservations from the configuration file and from the
database work side by side: dcdash shows both and marks where each one is stored.

dcdash doesn't write reservations into the configuration file (`config-write`). Those changes
would rewrite the operator's file without its comments.

## Permissions

The permission instance is the server name, the resource is the subnet prefix:

- `dhcp:dhcp4-a:*:read` shows everything on `dhcp4-a`.
- `dhcp:dhcp4-a:192.0.2.0/24:operate` also allows deleting leases in that subnet.
- `dhcp:*:10.1.*:write` allows changing reservations in all subnets starting with `10.1.` on
  all servers.
- Global reservations (outside any subnet) use the resource `global`.

See [security.md](security.md#dhcp-permissions) for the details.

## HA pairs

Put both servers of a Kea HA pair into the same `ha_group`. dcdash then handles them
together:

- **Leases:** Kea's HA hook doesn't pass changes made through the API on to the partner. So
  when a user deletes a lease, dcdash deletes it on every server of the group. If a partner
  can't be reached, the user sees a warning, and the audit entry lists the result per
  partner.
- **Reservations:** the partners must share the host database. dcdash writes a change
  once: to the selected server, or to a partner if the selected server has no host database.
- **State:** the server overview shows each server's HA state and whether it's in touch with
  its partner. The status page reports an error when a partner is out of touch.

## Leases

The lease list of a subnet shows at most 5000 leases. For larger subnets, search by IP
address, MAC address, client ID (DHCPv4), DUID (DHCPv6) or host name.

Deleting a lease frees the address in Kea. A client that still uses the address keeps using
it until it renews, and then gets a new lease.

## Troubleshooting

| Message in dcdash | Cause |
|---|---|
| "Unavailable: server not reachable" | Network, firewall, wrong URL or port, or a TLS problem (wrong `ca_file`, fingerprint or client certificate). dcdash logs the details. |
| "Kea rejected dcdash's credentials" | Wrong user or password, or the password file on the Kea server is not readable for Kea. |
| "Leases are not available …" (or another feature) | The named hook library isn't loaded on that server. |
| "Reservations are read-only …" | The server has no `hosts-database`, and no partner in its HA group has one. |
| "This reservation is in the Kea configuration file …" | It's defined in `kea-dhcp4.conf` or `kea-dhcp6.conf`. Change it there, or move it to the host database. |
| "The server did not accept the change: …" | Kea's own checks failed, for example a reservation for an address or identifier that's already reserved. The message is Kea's. |
| Utilization is missing, but the hook is loaded | Kea counts only addresses in pools. A subnet without pools has no utilization. |
