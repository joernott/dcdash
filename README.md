# dcdash

dcdash is a web dashboard for administrators to manage the central services of a data center
in one place: Proxmox VE clusters, DNS zones and DHCP servers. Users log in with their
directory account; roles from directory groups decide what each of them may see and change,
and every change is recorded in an audit log.

- **Proxmox VE:** inventory of nodes, VMs and containers across several clusters, power
  actions with task progress, the VNC console, container terminals and node shells in the
  browser; creating VMs (from templates with cloud-init, or from ISO images) and containers
  within configured limits, with several network devices and their DHCP reservations and DNS
  records; changing the options, disks and network devices of VMs; deleting guests.
- **DNS:** records of Cloudflare and bind9 zones, with protection against concurrent changes;
  creating and deleting zones (bind9 with rndc, SFTP and a catalog zone, and Cloudflare).
- **DHCP:** Kea 3.0 DHCPv4 and DHCPv6: subnet utilization, lease search and deletion,
  reservations, HA pairs.
- **Security:** LDAP and Active Directory login with failover between servers, TOTP per role
  with recovery codes, a break-glass account, permissions per cluster, zone and subnet, an
  audit log (also to a file or syslog), HTTPS with certificates from files or ACME.
- **Operation:** a single binary with the web interface built in, `.deb`/`.rpm` packages with
  a hardened systemd unit, a container image, health endpoints and Prometheus metrics; the
  documentation is built into the web interface.
- English and German, light and dark mode.

## Getting started

Download a package from the [releases](https://github.com/joernott/dcdash/releases), install
it, edit `/etc/dcdash/config.yaml` and start the service:

```sh
sudo apt install ./dcdash_0.1.0_amd64.deb        # or: sudo dnf install ./dcdash_0.1.0_amd64.rpm
sudo -u dcdash dcdash config validate
sudo systemctl enable --now dcdash
```

The [installation guide](docs/installation.md) describes the packages, the archive and the
container image, the first configuration, reverse proxies, backup and upgrades.

## Documentation

For administrators:

- [Administrator guide](docs/admin-guide.md): the starting point, with the backend
  prerequisites and daily operation
- [Installation](docs/installation.md)
- [Configuration reference](docs/configuration.md)
- [TLS certificates](docs/certificates.md)
- [Security](docs/security.md): login, TOTP, sessions, roles and permissions, audit log
- Modules: [Proxmox VE](docs/proxmox.md), [DNS](docs/dns.md), [DHCP (Kea)](docs/dhcp.md)
- [Monitoring](docs/monitoring.md): health endpoints and Prometheus metrics

For users: the [user guide](docs/user-guide.md).

For developers:

- [Development guide](docs/development.md): building, testing, conventions, releases
- [Concept](docs/concept.md), [concept review](docs/concept-review.md) and
  [architecture decisions](docs/adr/)
- [Implementation plan](docs/implementation-plan.md)
- [Changelog](CHANGELOG.md)

## License

dcdash is licensed under the [GNU General Public License v3.0 or later](LICENSE).
