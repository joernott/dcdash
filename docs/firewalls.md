# Firewalls: OPNsense

dcdash manages OPNsense firewalls:

- **Health:** version, uptime and memory; gateways with delay and loss; services, which can be
  restarted. A gateway that is down is reported on the overview page.
- **VPN:** WireGuard peers with their last handshake and traffic, enabled and disabled; IPsec
  tunnels with their phase 2 entries, restarted.
- **Aliases:** created, changed and deleted, and loaded into the packet filter right away.
- **Filter rules:** created, changed and deleted with a savepoint. If a change cuts off
  dcdash, OPNsense restores the previous rules by itself within a minute.

Every change is recorded in the audit log. The design and the behavior it's based on are
described in [ADR 0009](adr/0009-opnsense.md). The API calls were taken from the source of
OPNsense 26.1; they are checked against a real firewall before the release.

```yaml
firewalls:
  - name: fw-core                       # used in the UI and in permissions (firewall:fw-core:...)
    url: https://fw-core.example.com
    tls:
      fingerprint: "AB:CD:...:EF"        # SHA-256 of the web interface certificate
    api_key: "w86XNZob/8Oq8aC5r0kbNarNtdpoQU781fyoeaOBQsBwkXUt"
    api_secret: "file:/etc/dcdash/fw-core.secret"
```

## Versions

OPNsense has two release series a year in the community edition (26.1, 26.7) and two in the
Business Edition (26.4, 26.10), which is based on the community release before it. Filter
rule changes need the savepoint calls of the filter API, which OPNsense 26.7 removed: on
such a firewall, dcdash shows the rules but refuses to change them ("This OPNsense version
has no savepoints").

## Prerequisites on the firewall

1. Create a user for dcdash (System > Access > Users), with a random password it never uses,
   and an **API key** for it. OPNsense shows the secret only once: store it in a file that
   only dcdash can read.
2. Give the user the privileges for what dcdash should do:

   | Privilege | For |
   |---|---|
   | System: Firmware | Version (required) |
   | Lobby: Dashboard | Uptime and memory |
   | System: Gateways | Gateways |
   | Status: Services | Services, restarting them |
   | VPN: WireGuard: Configuration, VPN: WireGuard: Status | WireGuard peers |
   | Status: IPsec | IPsec tunnels |
   | Firewall: Alias: Edit | Aliases |
   | Firewall: Rules [new] | Filter rules |

   Without a privilege, the part is shown as not available; the rest works.
3. Rules created on the old rules page are read-only in dcdash. To change them, migrate them
   to the new rules page in OPNsense (Firewall > Rules [new], migration assistant).

## Certificate

The default certificate of the web interface is self-signed and issued for the firewall's
host name with its domain, without IP addresses. Pin it with `tls.fingerprint`:

```sh
openssl s_client -connect fw-core.example.com:443 </dev/null 2>/dev/null | openssl x509 -noout -fingerprint -sha256
```

The fingerprint changes when OPNsense creates a new certificate; dcdash then reports the
firewall as unreachable and logs the new fingerprint. With a certificate of your CA, use
`tls.ca_file` and the host name the certificate is issued for.

## Health

The "Health" tab lists the gateways (online, degraded with delay or loss, down) and the
services. "Restart" restarts a service after a confirmation; locked services are those
OPNsense needs. The overview page shows each firewall with its version, or the gateways that
are down.

## VPN

The "VPN" tab shows the WireGuard peers: online (handshake in the last 5 minutes), stale,
offline or disabled, the last handshake, the traffic and the tunnel addresses. "Disable" and
"Enable" change the peer and apply the WireGuard configuration. The IPsec tunnels show whether
they are connected and their phase 2 entries; "Restart" disconnects and connects the tunnel,
which takes a few seconds.

## Aliases

The "Aliases" tab lists the aliases with their content. Aliases of the types host, network,
port, url, urltable, mac and networkgroup can be created, changed and deleted; other types are
shown read-only. The content has one entry per line. Every change is loaded into the packet
filter right away. OPNsense refuses to delete an alias that rules use.

## Filter rules

The "Rules" tab lists the rules in their order, optionally of one interface. Legacy and
automatic rules are marked read-only. For every change of a rule, dcdash:

1. creates a savepoint of the filter configuration,
2. makes the change and applies it with the savepoint: OPNsense starts a 60-second timer,
3. waits two seconds and checks over a **new** connection that it still reaches the API
   (existing connections survive rule changes, so a reused one would prove nothing),
4. if so, cancels the timer: the change stays. If not, it leaves the timer running, and
   OPNsense restores the previous rules within a minute. The page says so, and the audit log
   records the change as failed with `rolledBack`.

Changes on the same firewall are made one after the other.

## Permissions

Permissions have the form `firewall:<firewall>:<resource>:<action>`:

| Permission | Allows |
|---|---|
| `firewall:<firewall>:*:read` | Seeing everything on the firewall |
| `firewall:<firewall>:system:read` | Health: gateways and services |
| `firewall:<firewall>:system:operate` | Restarting services |
| `firewall:<firewall>:wireguard/<peer>:read`, `:operate` | Seeing a WireGuard peer, enabling and disabling it |
| `firewall:<firewall>:ipsec/<connection>:read`, `:operate` | Seeing an IPsec tunnel, restarting it |
| `firewall:<firewall>:alias/<name>:read`, `:write` | Seeing an alias, creating, changing and deleting it |
| `firewall:<firewall>:rule/<interface>:read`, `:write` | Seeing the rules of an interface, creating, changing and deleting them |

Examples: `firewall:*:wireguard/*:operate` (all WireGuard peers of all firewalls),
`firewall:fw-core:alias/blocklist_*:write` (the blocklist aliases), `firewall:fw-core:rule/lan:write`
(the rules of the LAN interface). Renaming an alias or moving a rule to another interface needs
`write` on both. See [security.md](security.md#firewall-permissions).

## Audit

| Action | Recorded |
|---|---|
| `firewall.service.restart` | `target` is `<firewall>/service/<id>` |
| `firewall.wireguard.update` | `target` is `<firewall>/wireguard/<peer>`; `details` contain the new and the previous state |
| `firewall.ipsec.restart` | `target` is `<firewall>/ipsec/<connection>` |
| `firewall.alias.create`, `firewall.alias.update`, `firewall.alias.delete` | `target` is `<firewall>/alias/<name>`; `details` contain type, content and the previous values |
| `firewall.rule.create`, `firewall.rule.update`, `firewall.rule.delete` | `target` is `<firewall>/rule/<interface>`; `details` contain the rule, the previous rule, and `rolledBack` if OPNsense restores the previous rules |
