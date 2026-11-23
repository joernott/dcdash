# Administrator guide

This guide is the starting point for setting up and running dcdash. It links to the detailed
documents.

| Topic | Document |
|---|---|
| Installing, the first configuration, reverse proxies, backup and upgrades | [installation.md](installation.md) |
| Every configuration option | [configuration.md](configuration.md) |
| TLS certificates from files or ACME | [certificates.md](certificates.md) |
| Login, TOTP, sessions, roles and permissions, audit log, break-glass account | [security.md](security.md) |
| Proxmox VE | [proxmox.md](proxmox.md) |
| DNS: Cloudflare and bind9 | [dns.md](dns.md) |
| DHCP: Kea | [dhcp.md](dhcp.md) |
| NetBox as a common data source | [netbox.md](netbox.md) |
| Switches: Aruba CX | [switches.md](switches.md) |
| Firewalls: OPNsense | [firewalls.md](firewalls.md) |
| Directories: Active Directory and Samba | [directories.md](directories.md) |
| Projects: grouping objects with tags | [projects.md](projects.md) |
| Health endpoints and Prometheus metrics | [monitoring.md](monitoring.md) |
| Using the web interface | [user-guide.md](user-guide.md) |

## Preparing the backends

dcdash uses its own accounts on every backend. Give each one only the rights dcdash needs,
and keep its secrets in files readable only for dcdash (`file:` in the configuration).

| Backend | What dcdash needs | Details |
|---|---|---|
| LDAP / Active Directory | A service account that may search users and groups; LDAPS or StartTLS | [security.md](security.md#ldap) |
| Proxmox VE | An API token with the privileges for the inventory, power actions and consoles; for container terminals and node shells also a console user; for creating and deleting guests more privileges | [proxmox.md](proxmox.md#create-the-api-token) |
| Cloudflare | An API token with Zone:Read and DNS:Edit (Zone:Edit to create zones) | [dns.md](dns.md#cloudflare) |
| bind9 | A TSIG key allowed to transfer and update the zones; for zone creation also rndc, an SFTP account for zone files and a catalog zone | [dns.md](dns.md#bind9) |
| Kea 3.0 | An HTTPS control socket with basic authentication on each daemon, the hook libraries, and a host database for changing reservations | [dhcp.md](dhcp.md#prerequisites-on-the-kea-servers) |
| NetBox 4.2 or later | An API token with object permissions for virtual machines, interfaces, disks, MAC and IP addresses; a tag; a cluster per Proxmox cluster | [netbox.md](netbox.md#prerequisites-in-netbox) |
| Aruba CX (AOS-CX 10.x) | The REST API enabled (`https-server rest access-mode read-write`) and an account with the role `administrators` | [switches.md](switches.md#prerequisites-on-the-switch) |
| OPNsense 26.1 or Business Edition 26.4 | A user with an API key and the privileges for the parts dcdash should manage | [firewalls.md](firewalls.md#prerequisites-on-the-firewall) |
| Active Directory (Windows or Samba) | LDAPS on the domain controllers and a service account with delegated rights on the managed OUs; for AD DNS also rights on the zones | [directories.md](directories.md#the-service-account) |
| ACME CA (optional) | Access to the CA; for DNS-01 a DNS provider token | [certificates.md](certificates.md) |

Each module is optional: configure only the backends you want to manage.

## Users and permissions

dcdash has no user database of its own. Users log in with their directory account, and their
directory groups decide their roles:

1. `auth.root_group` names the group of the administrators. They hold every permission and
   must use TOTP.
2. `roles` map further groups to permissions, for example
   `proxmox:lab:qemu/*:operate` for power actions on all VMs of the cluster `lab`, or
   `dns:bind-internal:*.dev.example.internal:write` for records in the zones below
   `dev.example.internal`. See [security.md](security.md#roles-and-permissions) for the format
   and the permissions of each module.
3. `require_totp` per role decides whether its members must use an authenticator app
   (default: yes).

Changes to the roles in the configuration take effect after a restart (`systemctl restart
dcdash`). Changes to a user's groups in the directory take effect at their next login; to
apply them right away, terminate the user's sessions on the Sessions page.

## Daily operation

- **Logs:** dcdash logs to standard error (in the journal: `journalctl -u dcdash`), as JSON
  by default (`log.format: console` for readable lines).
- **Audit log:** in the web interface (Administration, Audit log), via `GET /api/v1/audit`,
  and optionally in a file or syslog for a SIEM (see [security.md](security.md#audit-log)).
  Entries are kept for `audit.retention` (default: forever).
- **State of the backends:** the overview page, or the metric `dcdash_backend_up` (see
  [monitoring.md](monitoring.md)).
- **Certificates:** with ACME, dcdash renews the certificate by itself. Certificate files are
  reloaded when they change; no restart is needed.
- **Configuration changes:** check them with `dcdash config validate`, then restart the
  service. Sessions survive a restart; open consoles are closed.

## Command line

Run the commands as the user dcdash runs as, so that file permissions fit, for example with
`sudo -u dcdash`. They read `/etc/dcdash/config.yaml`, or the file given with `--config` or
`DCDASH_CONFIG`.

| Command | Purpose |
|---|---|
| `dcdash serve` | Run the server (used by the systemd unit) |
| `dcdash config validate` | Check the configuration and the files it references |
| `dcdash version` | Show version, commit and build date (`--json` for JSON) |
| `dcdash gen-key` | Create a new encryption key for `security.encryption_key` |
| `dcdash hash-password` | Create the password hash of the break-glass account |
| `dcdash totp enroll <user>` | Set up TOTP for the break-glass account |
| `dcdash totp reset <user>` | Reset a user's TOTP, for example after a lost phone; also ends the user's sessions |
| `dcdash reencrypt` | Encrypt the stored secrets with the current key after a key rotation (see [security.md](security.md#key-rotation)) |

## Troubleshooting

| Problem | Where to look |
|---|---|
| dcdash doesn't start | `journalctl -u dcdash`: configuration errors name the option. `dcdash config validate` shows all problems at once |
| Nobody can log in | The directory tile on the overview, and `login failed` entries in the log. Use the break-glass account if the directory is down |
| A user is locked out | Lockouts end after `auth.lockout.*.duration` (default 15 minutes) and are reset by a restart |
| A user sees too little | My account shows the user's roles and groups; compare them with `roles` in the configuration. Refusals are in the audit log (`authz.denied`) |
| A backend tile is red | The tile names the error; the module documents have a troubleshooting section |
| Consoles don't connect behind a proxy | The proxy must pass websockets (see [installation.md](installation.md#behind-a-reverse-proxy)) |
