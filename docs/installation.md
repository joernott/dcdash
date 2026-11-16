# Installation

dcdash is a single static binary for Linux (amd64 and arm64) with the web interface built in.
It needs no runtime, no database server and no web server. Each release on
[GitHub](https://github.com/joernott/dcdash/releases) provides:

- `.deb` and `.rpm` packages with a systemd unit (recommended)
- `.tar.gz` archives with the binary, the documentation and the example configuration
- the container image `ghcr.io/joernott/dcdash`
- `SHA256SUMS` with the checksums of all files

## Requirements

- Linux on amd64 or arm64. The packages target systemd-based distributions such as Debian 12,
  Ubuntu 24.04 and RHEL/Rocky/Alma 9.
- A TLS certificate for the dcdash host name, or an ACME CA such as Let's Encrypt or step-ca
  (see [certificates.md](certificates.md)). dcdash can also serve plain HTTP behind a reverse
  proxy that terminates TLS.
- An LDAP directory or Active Directory for the users, and a service account to search it
  (see [security.md](security.md#ldap)).
- Network access from the dcdash host to the backends you want to manage, and their
  preparation: [Proxmox VE](proxmox.md), [DNS](dns.md), [DHCP](dhcp.md).
- For creating and deleting bind9 zones: `rndc` on the dcdash host (package `bind9-utils` on
  Debian and Ubuntu, `bind-utils` on RHEL).

dcdash runs as a single instance. Its state (sessions, TOTP enrollments, audit log,
preferences) is kept in an SQLite database in `/var/lib/dcdash`.

## Install the package

Download the package for your architecture and `SHA256SUMS` from the release page, and check
the download:

```sh
sha256sum --ignore-missing -c SHA256SUMS
```

Then install it:

```sh
# Debian, Ubuntu
sudo apt install ./dcdash_0.1.0_amd64.deb
# RHEL, Rocky, Alma
sudo dnf install ./dcdash_0.1.0_amd64.rpm
```

The package:

- creates the system user and group `dcdash`
- installs `/usr/bin/dcdash` and the systemd unit `dcdash.service`
- installs the example configuration as `/etc/dcdash/config.yaml` (owner `root:dcdash`, mode
  0640). Upgrades keep your changes.
- creates the encryption key `/etc/dcdash/encryption.key` at the first installation
- doesn't enable or start the service: the configuration needs editing first

The service runs as `dcdash` with a restricted set of permissions (read-only file system
except its data and log directories, no access to home directories, no privilege
escalation). It may bind ports below 1024, for example 443.

Continue with [First configuration](#first-configuration).

## Install from the archive

```sh
tar -xzf dcdash_0.1.0_linux_amd64.tar.gz
sudo install -m 0755 dcdash /usr/bin/dcdash
sudo groupadd --system dcdash
sudo useradd --system --gid dcdash --home-dir /var/lib/dcdash --shell /usr/sbin/nologin dcdash
sudo install -d -m 0750 -o root -g dcdash /etc/dcdash
sudo install -m 0640 -o root -g dcdash configs/config.example.yaml /etc/dcdash/config.yaml
sudo sh -c 'umask 027; dcdash gen-key > /etc/dcdash/encryption.key'
sudo chgrp dcdash /etc/dcdash/encryption.key
sudo install -m 0644 packaging/systemd/dcdash.service /etc/systemd/system/dcdash.service
sudo systemctl daemon-reload
```

The unit creates `/var/lib/dcdash` and `/var/log/dcdash` when the service starts.

## Run the container

The image is based on distroless and runs as the user `nonroot` (UID 65532). It expects the
configuration at `/etc/dcdash/config.yaml` and keeps its data in the volume
`/var/lib/dcdash`.

```sh
mkdir -p /srv/dcdash
docker run --rm ghcr.io/joernott/dcdash:0.1 gen-key > /srv/dcdash/encryption.key
# Edit /srv/dcdash/config.yaml, with
#   security.encryption_key: "file:/etc/dcdash/encryption.key"
#   data_dir: /var/lib/dcdash
sudo chown -R 65532:65532 /srv/dcdash
sudo chmod 0600 /srv/dcdash/config.yaml /srv/dcdash/encryption.key

docker run -d --name dcdash --restart unless-stopped \
  -p 443:8443 \
  -v /srv/dcdash:/etc/dcdash:ro \
  -v dcdash-data:/var/lib/dcdash \
  ghcr.io/joernott/dcdash:0.1
```

Tags: `0.1.0` (exactly this version), `0.1` (the latest patch release of 0.1) and `latest`.

Differences to the packages:

- The image contains only dcdash: no `rndc`, so bind9 zones can't be created or deleted
  (records work), and no local syslog. Send the audit log to a remote syslog server
  (`audit.syslog.network: udp` or `tcp`) or to a file in a volume.
- Files referenced in the configuration (keys, certificates, tokens) must be mounted into the
  container and readable for UID 65532.
- The image has no shell. Run commands such as `totp enroll` with
  `docker exec -it dcdash /usr/bin/dcdash totp enroll breakglass`.
- Use `/healthz` as the liveness and `/readyz` as the readiness check of your orchestrator
  (see [monitoring.md](monitoring.md)).

## First configuration

The example configuration explains every option in comments; the full reference is in
[configuration.md](configuration.md). The minimum is:

1. **TLS:** the certificate and key files, or ACME (see [certificates.md](certificates.md)).
   The files must be readable for the user `dcdash`.
2. **Directory:** the LDAP or Active Directory connection and the service account (see
   [security.md](security.md#ldap)). Keep the password in a file, for example
   `bind_password: "file:/etc/dcdash/ldap-bind.password"` (mode 0640, group `dcdash`).
3. **Root group:** `auth.root_group` names the directory group whose members hold all
   permissions. They must set up an authenticator app (TOTP) at their first login.
4. **Roles:** map directory groups to permissions (see
   [security.md](security.md#roles-and-permissions)).
5. **Modules:** the Proxmox clusters, DNS providers and Kea servers (see
   [proxmox.md](proxmox.md), [dns.md](dns.md), [dhcp.md](dhcp.md)). Leave out what you don't
   use.

Check the configuration, then start the service:

```sh
sudo -u dcdash dcdash config validate
sudo systemctl enable --now dcdash
sudo journalctl -u dcdash -f
```

Open `https://<host>:8443/` (or the port in `server.listen`) and log in as a member of the root
group. dcdash asks you to set up an authenticator app and shows 10 recovery codes once. Keep
them in a safe place.

### Break-glass account

For the case that the directory is unavailable, set up the local break-glass account right
away, as described in [security.md](security.md#break-glass-account). It always requires
TOTP, which is enrolled on the server:

```sh
sudo -u dcdash dcdash totp enroll breakglass
```

## Behind a reverse proxy

dcdash can run behind a reverse proxy, for example to share port 443 with other services:

- List the proxy in `server.trusted_proxies`, so that dcdash sees the real client addresses
  (logging, audit log, login lockout).
- The proxy must pass websockets through for the consoles (`/api/v1/proxmox/console/`).
- If the proxy terminates TLS and dcdash serves plain HTTP (`server.tls.mode: none`), the
  proxy must set `Strict-Transport-Security` itself, and the connection between proxy and
  dcdash must be trusted.

An example for nginx, with TLS to dcdash as well:

```nginx
location / {
    proxy_pass https://127.0.0.1:8443;
    proxy_ssl_verify on;
    proxy_ssl_trusted_certificate /etc/nginx/dcdash-ca.pem;
    proxy_ssl_name dcdash.example.com;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection $connection_upgrade;  # map $http_upgrade, see the nginx docs
    proxy_read_timeout 1h;                            # open consoles
}
```

## Backup and restore

Back up:

- `/etc/dcdash/` with the configuration and the files it references
- the encryption key `/etc/dcdash/encryption.key`, **separately** from the data. Without it,
  the TOTP enrollments in the database can't be read, and every user has to enroll again.
- `/var/lib/dcdash/`: the database (`dcdash.db` with `dcdash.db-wal` and `dcdash.db-shm`) and,
  with ACME, the account and certificates

Copy the database files while dcdash is stopped, or use SQLite's online backup:
`sqlite3 /var/lib/dcdash/dcdash.db ".backup /backup/dcdash.db"`.

To restore, stop dcdash, put the files back (owner `dcdash`, mode 0600 for the database), and
start it. dcdash refuses to start if the encryption key doesn't match the database (see
[security.md](security.md#the-encryption-key)).

## Upgrades

Install the new package; the service restarts if it was running. dcdash updates its database
schema at startup. Read [CHANGELOG.md](../CHANGELOG.md) before an upgrade, and back up the
database first: an older version can't use a database that a newer one has updated.

For the container, pull the new tag and recreate the container with the same volume.

## Uninstall

```sh
sudo apt remove dcdash      # or: sudo dnf remove dcdash
```

The configuration, the encryption key, the data in `/var/lib/dcdash` and the user `dcdash`
are kept. Remove them by hand if you no longer need them.
