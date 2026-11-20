# Configuration reference

dcdash reads one YAML file. The default path is `/etc/dcdash/config.yaml`. Use `--config`
(`-c`) or the environment variable `DCDASH_CONFIG` to read another file.

This page lists every key. The module documents explain the concepts behind them. For a
commented example of the whole file, see
[configs/config.example.yaml](../configs/config.example.yaml).

## General rules

**Unknown keys are rejected.** A typo such as `listn:` stops dcdash with an error instead of
being ignored. Keys that are left out get their default.

**Validation.** dcdash checks the whole file at startup and reports all problems at once.
Check a file without starting the server:

```sh
dcdash config validate
dcdash --config /tmp/new-config.yaml config validate
```

The command also prints warnings. Warnings don't stop dcdash:

- The configuration file or a file referenced with `file:` is accessible by other users (any
  permission bit for "other" is set). Use `chmod 0640` or `0600`. This check is skipped on
  Windows.
- No login method is configured: neither `auth.ldap.url` nor `auth.ldap.servers` is set, and `auth.local.enabled` is false.
- `server.tls.mode` is `none`.

**Secrets.** Keys marked with type *secret* accept three forms:

| Form | Value |
|---|---|
| `"env:NAME"` | The value of the environment variable `NAME`. It's an error if the variable isn't set. |
| `"file:/path"` | The content of the file. One trailing newline (`\n` or `\r\n`) is removed. |
| anything else | The literal value. |

Prefer `file:` or `env:` so the configuration file can be shared without the secrets in it.
dcdash never prints secret values; logs and dumps show `********`.

The secret keys are: `server.tls.acme.eab.hmac_key`,
`server.tls.acme.dns.cloudflare.api_token`, `server.tls.acme.dns.cloudflare.zone_token`,
`server.tls.acme.dns.rfc2136.tsig_secret`, `auth.ldap.bind_password`,
`auth.local.password_hash`, `proxmox.clusters[].token_secret`,
`proxmox.clusters[].console.password`, `dns.providers[].cloudflare.api_token`,
`dns.providers[].bind.tsig_secret`, `dhcp.servers[].password`, `metrics.token`,
`security.encryption_key` and `security.previous_encryption_keys[]`.

**Durations.** Keys of type *duration* use Go's duration syntax: a number with a unit `ns`,
`us`, `ms`, `s`, `m` or `h`, combinable as in `1h30m`. In addition, whole days are accepted
as `<n>d` (for example `365d`, up to 36500 days). Days can't be combined with other units:
`1d12h` is invalid, write `36h`. `0` means zero.

**TLS client settings.** Several modules connect to servers over TLS and share these keys,
shown below as `<prefix>.tls.*`:

| Key | Type | Default | Description |
|---|---|---|---|
| `<prefix>.tls.ca_file` | string | empty | PEM file with additional trusted root certificates. The system roots stay trusted. |
| `<prefix>.tls.server_name` | string | empty | Host name expected in the server certificate, if it differs from the one in the URL. |
| `<prefix>.tls.fingerprint` | string | empty | Pins the server certificate by its SHA-256 fingerprint (64 hex digits, colons allowed). The certificate is then accepted regardless of issuer and host name. |
| `<prefix>.tls.cert_file` | string | empty | Client certificate (PEM), for servers that require one. Set together with `key_file`. |
| `<prefix>.tls.key_file` | string | empty | Key of the client certificate (PEM). Set together with `cert_file`. |

## server

The HTTP(S) listener. See [certificates.md](certificates.md) for TLS.

| Key | Type | Default | Description |
|---|---|---|---|
| `server.listen` | string | `:8443` | Address of the main listener, `host:port`. An empty host listens on all addresses. |
| `server.redirect_listen` | string | empty (off) | Optional plain HTTP listener, for example `:80`, that redirects every request to HTTPS. Not allowed with `server.tls.mode: none`. See [certificates.md](certificates.md#http-redirect-and-hsts). |
| `server.trusted_proxies` | list of strings | empty | Reverse proxies (IP addresses or CIDR ranges) whose `X-Forwarded-For` header determines the client IP address. Used for the audit log and the lockout. |
| `server.hsts_max_age` | duration | `4320h` (180 days) | `max-age` of the `Strict-Transport-Security` header on HTTPS responses. `0` disables the header. Must not be negative. |
| `server.read_header_timeout` | duration | `10s` | Time allowed to read the request headers. Must be positive. |
| `server.shutdown_timeout` | duration | `15s` | Time open requests get to finish when dcdash stops. Must be positive. |

### server.tls

| Key | Type | Default | Description |
|---|---|---|---|
| `server.tls.mode` | string | `files` | Where the certificate comes from: `files`, `acme` or `none` (plain HTTP, only for development or behind a TLS-terminating proxy). See [certificates.md](certificates.md). |
| `server.tls.cert_file` | string | empty | Certificate in PEM, optionally followed by the chain. Required for mode `files`. |
| `server.tls.key_file` | string | empty | Key of the certificate in PEM. Required for mode `files`. |
| `server.tls.reload_interval` | duration | `1m` | How often `cert_file` and `key_file` are checked for changes in mode `files`. `0` disables reloading. Must not be negative. |

### server.tls.acme

Used in mode `acme` only; validated only in that mode. See
[certificates.md](certificates.md#acme-mode-acme).

| Key | Type | Default | Description |
|---|---|---|---|
| `server.tls.acme.directory_url` | string | `https://acme-v02.api.letsencrypt.org/directory` | Directory URL of the ACME CA. Must be an `https` URL. |
| `server.tls.acme.email` | string | empty | Optional contact address of the ACME account. |
| `server.tls.acme.accept_tos` | bool | `false` | Accepts the terms of service of the CA. Must be `true`. |
| `server.tls.acme.domains` | list of strings | empty | Names in the certificate; the first one is the common name. Wildcards (`*.example.com`) are allowed. At least one is required. |
| `server.tls.acme.key_type` | string | `EC256` | Key type of the certificate: `EC256`, `EC384`, `RSA2048`, `RSA3072` or `RSA4096`. |
| `server.tls.acme.ca_file` | string | empty | Additional trusted root for the connection to the ACME server, for example the step-ca root. |
| `server.tls.acme.renew_before` | duration | `720h` (30 days) | How long before expiry the certificate is renewed. For short-lived certificates at most a third of the lifetime is used. Must be positive. See [certificates.md](certificates.md#renewal). |
| `server.tls.acme.eab.key_id` | string | empty | Key ID for external account binding, if the CA requires it. Set together with `hmac_key`. |
| `server.tls.acme.eab.hmac_key` | secret | empty | HMAC key for external account binding. Set together with `key_id`. |
| `server.tls.acme.dns.provider` | string | none | How the DNS-01 challenge record is published: `cloudflare` or `rfc2136`. Required. |
| `server.tls.acme.dns.cloudflare.api_token` | secret | empty | Cloudflare token with Zone:Read and DNS:Edit for the zones of all domains. Required for provider `cloudflare`. |
| `server.tls.acme.dns.cloudflare.zone_token` | secret | empty | Optional separate token with Zone:Read for the zone lookup. |
| `server.tls.acme.dns.rfc2136.nameserver` | string | empty | Primary name server that accepts updates, `host` or `host:port` (port 53 by default). Required for provider `rfc2136`. See [certificates.md](certificates.md#bind9-setup-for-rfc2136). |
| `server.tls.acme.dns.rfc2136.tsig_key` | string | empty | Name of the TSIG key. Required for provider `rfc2136`. |
| `server.tls.acme.dns.rfc2136.tsig_secret` | secret | empty | Secret of the TSIG key. Required for provider `rfc2136`. |
| `server.tls.acme.dns.rfc2136.tsig_algorithm` | string | `hmac-sha256` | `hmac-sha1`, `hmac-sha224`, `hmac-sha256`, `hmac-sha384` or `hmac-sha512`. |
| `server.tls.acme.dns.resolvers` | list of strings | system resolvers | Resolvers (`host` or `host:port`) used to check that the challenge record is visible. See [certificates.md](certificates.md#dns-propagation-checks). |
| `server.tls.acme.dns.propagation_timeout` | duration | `0` (provider default) | How long to wait for the record to become visible. Must not be negative. |
| `server.tls.acme.dns.disable_authoritative_check` | bool | `false` | Skips querying the zone's authoritative name servers directly, for example when they can't be reached from dcdash. |

## auth

Login, sessions and lockout. See [security.md](security.md#authentication).

### auth.ldap

LDAP login is enabled when `auth.ldap.url` or `auth.ldap.servers` is set. The other `auth.ldap`
keys are validated only then. See [security.md](security.md#ldap).

| Key | Type | Default | Description |
|---|---|---|---|
| `auth.ldap.url` | string | empty (LDAP off) | `ldaps://host[:port]`, or `ldap://host[:port]` with `start_tls` or `insecure`. For several servers use `servers` instead. |
| `auth.ldap.servers` | list | empty | Several directory servers for failover, instead of `url`. Each entry has `url` (as above) and an optional `fingerprint`, which pins that server's certificate and overrides `tls.fingerprint` (Samba creates a different certificate on each domain controller). dcdash uses the server that worked last; if it can't be reached (network or TLS error), the next servers are tried in order. A wrong password never causes a failover. The overview page names servers that are not reachable. |
| `auth.ldap.start_tls` | bool | `false` | Upgrades an `ldap://` connection to TLS. Not possible with `ldaps://`. |
| `auth.ldap.insecure` | bool | `false` | Allows `ldap://` without StartTLS, which sends passwords in clear text. For tests only. |
| `auth.ldap.tls.*` | | | TLS client settings, see [General rules](#general-rules). |
| `auth.ldap.mode` | string | `ad` | Directory type: `ad` (Active Directory, Samba AD) or `generic` (for example OpenLDAP). Sets the defaults of the filters and attributes. |
| `auth.ldap.bind_dn` | string | empty | Service account used to search users and groups. Read access is enough. Required. |
| `auth.ldap.bind_password` | secret | empty | Password of the service account. Required. |
| `auth.ldap.base_dn` | string | empty | DN below which users are searched. Required. |
| `auth.ldap.user_filter` | string | depends on `mode` | Filter that finds the user. Must contain `{username}`, which is replaced by the escaped login name. |
| `auth.ldap.group_base_dn` | string | value of `base_dn` | DN below which groups are searched. |
| `auth.ldap.group_filter` | string | depends on `mode` | Mode `generic` only: finds the groups of a user. Must contain `{dn}` (the user's DN) or `{username}`. In mode `ad`, nested groups are resolved without a filter. |
| `auth.ldap.attributes.username` | string | `sAMAccountName` (ad), `uid` (generic) | Attribute with the user name. |
| `auth.ldap.attributes.display_name` | string | `displayName` (ad), `cn` (generic) | Attribute with the display name. |
| `auth.ldap.attributes.email` | string | `mail` | Attribute with the email address. |
| `auth.ldap.timeout` | duration | `10s` | Timeout for connecting and for each operation. Must be positive. |

Default filters:

| Mode | `user_filter` | `group_filter` |
|---|---|---|
| `ad` | `(&(objectCategory=person)(objectClass=user)(\|(sAMAccountName={username})(userPrincipalName={username})))` | empty (not used) |
| `generic` | `(&(objectClass=inetOrgPerson)(uid={username}))` | `(\|(&(objectClass=groupOfNames)(member={dn}))(&(objectClass=groupOfUniqueNames)(uniqueMember={dn}))(&(objectClass=posixGroup)(memberUid={username})))` |

### auth.session

See [security.md](security.md#sessions).

| Key | Type | Default | Description |
|---|---|---|---|
| `auth.session.lifetime` | duration | `8h` | Maximum duration of a session, regardless of activity. Must be positive. |
| `auth.session.idle_timeout` | duration | `8h` | Ends a session after this time without requests. Open dcdash pages poll only while they are visible, so a tab in the background counts as inactive. Must be positive and not longer than `lifetime`. |

### auth.lockout

Each policy locks its key for `duration` after `max_failures` failures within `window`. All
three values must be positive. See [security.md](security.md#lockout).

| Key | Type | Default | Description |
|---|---|---|---|
| `auth.lockout.user.max_failures` | int | `5` | Failed logins per user name before it's locked. |
| `auth.lockout.user.window` | duration | `15m` | Time in which the failures are counted. |
| `auth.lockout.user.duration` | duration | `15m` | How long the user name stays locked. |
| `auth.lockout.ip.max_failures` | int | `20` | Failed logins per client IP address (IPv6: per /64) before it's locked. |
| `auth.lockout.ip.window` | duration | `15m` | Time in which the failures are counted. |
| `auth.lockout.ip.duration` | duration | `15m` | How long the address stays locked. |
| `auth.lockout.totp.max_failures` | int | `5` | Wrong TOTP or recovery codes per user before the TOTP step is locked. |
| `auth.lockout.totp.window` | duration | `15m` | Time in which the failures are counted. |
| `auth.lockout.totp.duration` | duration | `15m` | How long the TOTP step stays locked. |

### auth.local

The break-glass account for when the directory is unavailable. It always requires TOTP and
holds the `root` role. See [security.md](security.md#break-glass-account).

| Key | Type | Default | Description |
|---|---|---|---|
| `auth.local.enabled` | bool | `false` | Enables the account. |
| `auth.local.username` | string | empty | Name of the account. Logins with this name never reach the directory. Required when enabled. |
| `auth.local.password_hash` | secret | empty | argon2id hash from `dcdash hash-password`. Required when enabled; must start with `$argon2id$`. |

### auth.root_group and auth.totp

| Key | Type | Default | Description |
|---|---|---|---|
| `auth.root_group` | string | `dashroot` | Directory group (CN or DN) whose members hold the built-in `root` role. An empty value means `dashroot`. See [security.md](security.md#the-root-role). |
| `auth.totp.issuer` | string | `dcdash` | Name shown in authenticator apps. Required; must not contain `:`. See [security.md](security.md#totp). |

## roles

A list of roles that map directory groups to permissions. Default: empty (only the built-in
`root` role exists). See [security.md](security.md#roles-and-permissions).

| Key | Type | Default | Description |
|---|---|---|---|
| `roles[].name` | string | none | Unique name: lower-case letters, digits, `-` and `_`, at most 64 characters, starting with a letter or digit. `root` is reserved. Required. |
| `roles[].groups` | list of strings | none | Directory groups by CN (`dcdash-dns`) or DN (anything containing `=`). Compared without regard to case. At least one, none empty. |
| `roles[].require_totp` | bool | `true` | Members must use TOTP. A user needs TOTP if any of their roles requires it. |
| `roles[].permissions` | list of strings | empty | Permissions as `module:instance:resource:action`, for example `dns:cloudflare-prod:*:write`. See [security.md](security.md#permission-format). |

## audit

The audit log. Entries are always stored in the database; the file and syslog are optional
additional outputs. See [security.md](security.md#audit-log).

| Key | Type | Default | Description |
|---|---|---|---|
| `audit.retention` | duration | `0` (keep forever) | Deletes database entries older than this. Must not be negative. |
| `audit.file` | string | empty (off) | Appends every entry as a JSON line to this file. Must be an absolute path. |
| `audit.syslog.enabled` | bool | `false` | Sends every entry to syslog (facility `authpriv`). The other syslog keys are validated only when enabled. |
| `audit.syslog.network` | string | empty | Empty for the local syslog daemon, or `udp`, `tcp` or `unix`. |
| `audit.syslog.address` | string | empty | `host:port` of the syslog server, or a socket path for `unix`. Set together with `network`; both empty means the local daemon. |
| `audit.syslog.tag` | string | `dcdash` | Syslog tag. |

## proxmox

Proxmox VE clusters. Default: no clusters. See [proxmox.md](proxmox.md).

| Key | Type | Default | Description |
|---|---|---|---|
| `proxmox.clusters[].name` | string | none | Unique name, used in the UI and in permissions (`proxmox:<name>:...`). Lower-case letters, digits, `.`, `-` and `_`, at most 64 characters, starting with a letter or digit. Required. |
| `proxmox.clusters[].endpoints[].url` | string | none | API URL of a node, `https://host[:port]` without a path. At least one endpoint is required. Endpoints are tried in order; see [proxmox.md](proxmox.md#failover). |
| `proxmox.clusters[].endpoints[].fingerprint` | string | empty | Pins this node's certificate (SHA-256). Overrides `tls.fingerprint` of the cluster. |
| `proxmox.clusters[].tls.*` | | | TLS client settings for all endpoints, see [General rules](#general-rules). Typically `ca_file` with the cluster CA. See [proxmox.md](proxmox.md#certificates). |
| `proxmox.clusters[].token_id` | string | none | API token, `user@realm!tokenname`. Required. See [proxmox.md](proxmox.md#create-the-api-token). |
| `proxmox.clusters[].token_secret` | secret | none | Secret of the API token. Required. |
| `proxmox.clusters[].timeout` | duration | `15s` | Timeout of a single API request. `0` uses the default. Must not be negative. |
| `proxmox.clusters[].console.username` | string | empty | Optional Proxmox user (`user@realm`) for container terminals and node shells, which don't accept API tokens. Empty: only VNC consoles. See [proxmox.md](proxmox.md#consoles). |
| `proxmox.clusters[].console.password` | secret | empty | Password of the console user. Required with `console.username`. |

### proxmox.clusters[].provisioning

Allows creating and deleting virtual machines and containers in the cluster, and limits what
users may create. Without this section (or without `storages`), creating and deleting is off.
See [proxmox.md](proxmox.md#creating-and-deleting-guests).

| Key | Type | Default | Description |
|---|---|---|---|
| `storages` | list of strings | none | Storages for VM disks and container root file systems. Required for provisioning. |
| `image_storages` | list of strings | `storages` | Storages that hold ISO images and container templates. |
| `bridges` | list of strings | none | Bridges or SDN VNets for the network device. Required when the section is set. |
| `vlans` | ranges | empty | Allowed VLAN tags, e.g. `["100-199", 300]`, between 1 and 4094. Empty: untagged only. |
| `vmids` | ranges | empty | IDs for new guests, e.g. `["5000-5999"]`, between 100 and 999999999. Without a requested ID, dcdash takes the lowest free one the user may create. Empty: any ID, by default the next free ID of Proxmox. |
| `templates` | ranges | empty | VM IDs of the templates that may be cloned. Empty: all templates. Users also need `read` on a template. |
| `pool` | string | empty | Proxmox pool new guests are added to. |
| `limits.cores` | integer | `16` | Maximum CPU cores of a new guest (at most 512). |
| `limits.memory` | size | `64G` | Maximum memory. |
| `limits.disk` | size | `1024G` | Maximum disk size. |
| `defaults.cores` | integer | `2` | Start value in the creation wizard; at most `limits.cores`. |
| `defaults.memory` | size | `2G` | Start value for the memory (at least `16M`). |
| `defaults.disk` | size | `32G` | Start value for the disk (at least `1G`). |
| `defaults.storage` | string | first of `storages` | Preselected storage; must be in `storages`. |
| `defaults.bridge` | string | first of `bridges` | Preselected bridge; must be in `bridges`. |

*Ranges* are lists of numbers and ranges `"<from>-<to>"`. *Sizes* are a number with a binary
unit `K`, `M`, `G` or `T` (also `MiB`, `GiB`): `512M`, `32G`.

## dns

DNS providers: Cloudflare accounts and bind9 primaries. Default: no providers. See
[dns.md](dns.md).

| Key | Type | Default | Description |
|---|---|---|---|
| `dns.providers[].name` | string | none | Unique name, used in the UI and in permissions (`dns:<name>:...`). Same rules as `proxmox.clusters[].name`. Required. |
| `dns.providers[].type` | string | none | `cloudflare` or `bind`. Required. Only the section of that type is validated. |
| `dns.providers[].timeout` | duration | `15s` | Timeout of a single request to the backend. `0` uses the default. Must not be negative. |

### Type cloudflare

See [dns.md](dns.md#cloudflare).

| Key | Type | Default | Description |
|---|---|---|---|
| `dns.providers[].cloudflare.api_token` | secret | none | API token with Zone:Read and DNS:Edit; Zone:Edit to create and delete zones. Use a different token than for ACME. Required. |
| `dns.providers[].cloudflare.account_id` | string | empty | Account ID, needed only to create zones. |
| `dns.providers[].cloudflare.base_url` | string | empty | Overrides the API URL. For tests only; must be an `http` or `https` URL. |

### Type bind

Records are read with AXFR and changed with RFC 2136 updates, both signed with the TSIG key.
See [dns.md](dns.md#bind9).

| Key | Type | Default | Description |
|---|---|---|---|
| `dns.providers[].bind.nameserver` | string | none | The primary, `host` or `host:port` (port 53 by default). Required. |
| `dns.providers[].bind.tsig_key` | string | none | Name of the TSIG key. Required. See [dns.md](dns.md#tsig-key-and-zone-settings). |
| `dns.providers[].bind.tsig_secret` | secret | none | Secret of the TSIG key. Required. |
| `dns.providers[].bind.tsig_algorithm` | string | `hmac-sha256` | `hmac-sha1`, `hmac-sha224`, `hmac-sha256`, `hmac-sha384` or `hmac-sha512`. |
| `dns.providers[].bind.zones` | list of strings | empty | Zones to manage in addition to those in the catalog zone, for example zones from `named.conf`. `zones` or `catalog_zone` is required. |
| `dns.providers[].bind.catalog_zone` | string | empty | Catalog zone (RFC 9432) that lists the zones of the primary. dcdash adds and removes the zones it creates or deletes. See [dns.md](dns.md#catalog-zone). |
| `dns.providers[].bind.rndc.binary` | string | empty (rndc off) | Absolute path of `rndc`, used for server status and to add and delete zones. See [dns.md](dns.md#rndc). |
| `dns.providers[].bind.rndc.server` | string | empty | Address of the rndc control channel. Required when `rndc.binary` is set. |
| `dns.providers[].bind.rndc.port` | int | `953` | Port of the rndc control channel (1 to 65535). |
| `dns.providers[].bind.rndc.key_file` | string | empty | File with the rndc key (`rndc -k`). Required when `rndc.binary` is set. |
| `dns.providers[].bind.sftp.address` | string | empty (SFTP off) | SSH server of the primary, `host[:port]` (port 22 by default), used to upload and remove zone files. See [dns.md](dns.md#sftp-user-for-zone-files). |
| `dns.providers[].bind.sftp.user` | string | empty | SFTP user. Required when `sftp.address` is set. |
| `dns.providers[].bind.sftp.private_key_file` | string | empty | Private key of dcdash for this primary. Required when `sftp.address` is set. |
| `dns.providers[].bind.sftp.host_key` | string | empty | Pins the server's host key, for example `ssh-ed25519 AAAA...`. Exactly one of `host_key` and `known_hosts_file` is required when `sftp.address` is set. |
| `dns.providers[].bind.sftp.known_hosts_file` | string | empty | `known_hosts` file with the server's host key. |
| `dns.providers[].bind.sftp.zone_dir` | string | empty | Zone directory as seen over SFTP (for example inside a chroot). Must be an absolute path when `sftp.address` is set. |
| `dns.providers[].bind.new_zones.file_dir` | string | empty (zone creation off) | Zone directory as seen by `named`, an absolute path. Setting it enables zone creation and requires `rndc`, `sftp`, `catalog_zone`, `soa.mname`, `soa.rname` and `nameservers`. See [dns.md](dns.md#zones-created-by-dcdash). |
| `dns.providers[].bind.new_zones.options` | string | updates and transfers for the TSIG key | Zone options for `rndc addzone`, without `type` and `file`. Must be a single line. |
| `dns.providers[].bind.new_zones.ttl` | duration | `1h` | TTL of the SOA and NS records of a new zone. |
| `dns.providers[].bind.new_zones.soa.mname` | string | empty | Primary name server in the SOA record. |
| `dns.providers[].bind.new_zones.soa.rname` | string | empty | Contact mailbox in domain form, for example `hostmaster.example.com`. |
| `dns.providers[].bind.new_zones.soa.refresh` | duration | `1h` | SOA refresh. |
| `dns.providers[].bind.new_zones.soa.retry` | duration | `15m` | SOA retry. |
| `dns.providers[].bind.new_zones.soa.expire` | duration | `336h` (14 days) | SOA expire. |
| `dns.providers[].bind.new_zones.soa.minimum` | duration | `5m` | SOA minimum (negative caching TTL). |
| `dns.providers[].bind.new_zones.nameservers` | list of strings | empty | NS records of a new zone. At least one when `file_dir` is set. |

The default of `new_zones.options` is
`update-policy { grant <tsig_key>. zonesub ANY; }; allow-transfer { key <tsig_key>.; };`.

## dhcp

Kea 3.x DHCP servers, each `kea-dhcp4` or `kea-dhcp6` daemon reached over its own control
socket. Default: no servers. See [dhcp.md](dhcp.md).

| Key | Type | Default | Description |
|---|---|---|---|
| `dhcp.servers[].name` | string | none | Unique name, used in the UI and in permissions (`dhcp:<name>:...`). Same rules as `proxmox.clusters[].name`. Required. |
| `dhcp.servers[].family` | string | none | `v4` (kea-dhcp4) or `v6` (kea-dhcp6). Required. |
| `dhcp.servers[].url` | string | none | URL of the control socket, `https://host[:port]` without a path. Required. See [dhcp.md](dhcp.md#control-socket). |
| `dhcp.servers[].insecure` | bool | `false` | Allows an `http://` URL, which sends the password in clear text. For tests only. |
| `dhcp.servers[].tls.*` | | | TLS client settings, see [General rules](#general-rules). `cert_file` and `key_file` are for Kea's `cert-required`. |
| `dhcp.servers[].username` | string | none | User for Kea's basic authentication. Required. |
| `dhcp.servers[].password` | secret | none | Password for Kea's basic authentication. Required. |
| `dhcp.servers[].timeout` | duration | `15s` | Timeout of a single command. `0` uses the default. Must not be negative. |
| `dhcp.servers[].ha_group` | string | empty (standalone) | Groups the servers of a Kea HA pair. Same naming rules as `name`; all servers of a group must have the same `family`. See [dhcp.md](dhcp.md#ha-pairs). |

## netbox

NetBox as a common data source with write-back. Disabled without `url`. See
[netbox.md](netbox.md).

| Key | Type | Default | Description |
|---|---|---|---|
| `netbox.name` | string | `netbox` | Name in the UI and in permissions (`netbox:<name>:...`). Same rules as `proxmox.clusters[].name`. |
| `netbox.url` | string | empty (disabled) | URL of NetBox without `/api`, `https://host[:port][/path]`. |
| `netbox.insecure` | bool | `false` | Allows an `http://` URL, which sends the token in clear text. For tests only. |
| `netbox.tls.*` | | | TLS client settings, see [General rules](#general-rules). |
| `netbox.token` | secret | none | API token: a v2 token `nbt_<key>.<token>` (sent as `Bearer`) or a v1 token (sent as `Token`). Required. |
| `netbox.timeout` | duration | `15s` | Timeout of a single request. Must not be negative. |
| `netbox.write_back` | bool | `true` | Writes changes made in dcdash to NetBox. `false` only reads. |
| `netbox.tag` | string | `dcdash` | Slug of the tag on the objects dcdash creates. It must exist in NetBox. |
| `netbox.on_delete` | string | `tagged` | When a guest is deleted: `tagged` deletes NetBox VMs with the tag and sets the others to `decommissioning`, `delete` always deletes, `decommission` never deletes. |
| `netbox.vmid_field` | string | empty | Name of an integer custom field of virtual machines with the Proxmox VMID. Without it, VMs are found by name. |
| `netbox.clusters` | map | empty | Proxmox cluster name (`proxmox.clusters[].name`) to NetBox cluster name. Guests of other clusters are not written to NetBox. |
| `netbox.vlan_group`, `netbox.site` | string | empty | Slugs that restrict the VLAN lookup by VLAN ID. |
| `netbox.vrf` | string | empty (global table) | Name of the VRF of the IP addresses and prefixes. |

## switches

Aruba CX switches, reached over their REST API. Default: no switches. See
[switches.md](switches.md).

| Key | Type | Default | Description |
|---|---|---|---|
| `switches[].name` | string | none | Unique name, used in the UI and in permissions (`switch:<name>:...`). Same rules as `proxmox.clusters[].name`. Required. |
| `switches[].type` | string | `aoscx` | Type of the switch API. Only `aoscx` (Aruba CX, AOS-CX 10.x) so far. |
| `switches[].url` | string | none | URL of the switch, `https://host[:port]` without a path. Required. |
| `switches[].insecure` | bool | `false` | Allows an `http://` URL, which sends the password in clear text. For tests only. |
| `switches[].tls.*` | | | TLS client settings, see [General rules](#general-rules). The factory certificates are self-signed: use `fingerprint`, see [switches.md](switches.md#certificate). |
| `switches[].username` | string | none | Switch account. The role `administrators` allows changes, `operators` only reading. Required. |
| `switches[].password` | secret | none | Password of the account. Required. |
| `switches[].api_version` | string | empty (newest) | REST API version, for example `v10.08`. Empty uses the newest version the switch offers. |
| `switches[].netbox_device` | string | empty | Name of the switch's device in NetBox. With it and `netbox`, port and VLAN changes are written to NetBox and ports show their projects. See [switches.md](switches.md#netbox). |
| `switches[].timeout` | duration | `20s` | Timeout of a single request. `0` uses the default. Must not be negative. |

## firewalls

OPNsense firewalls, reached over their REST API. Default: no firewalls. See
[firewalls.md](firewalls.md).

| Key | Type | Default | Description |
|---|---|---|---|
| `firewalls[].name` | string | none | Unique name, used in the UI and in permissions (`firewall:<name>:...`). Same rules as `proxmox.clusters[].name`. Required. |
| `firewalls[].type` | string | `opnsense` | Type of the firewall API. Only `opnsense` so far. |
| `firewalls[].url` | string | none | URL of the web interface, `https://host[:port]` without a path. Required. |
| `firewalls[].insecure` | bool | `false` | Allows an `http://` URL, which sends the API secret in clear text. For tests only. |
| `firewalls[].tls.*` | | | TLS client settings, see [General rules](#general-rules). The default certificate is self-signed: use `fingerprint`, see [firewalls.md](firewalls.md#certificate). |
| `firewalls[].api_key` | string | none | API key of the dcdash user in OPNsense. Required. |
| `firewalls[].api_secret` | secret | none | API secret of the key. Required. |
| `firewalls[].timeout` | duration | `30s` | Timeout of a single request. `0` uses the default. Must not be negative. |
| `firewalls[].protected_interfaces` | list of strings | none | Interface keys (`lan`, `opt1`, ...) whose rules dcdash doesn't change on a firewall without savepoints (OPNsense 26.7 and later), in addition to the interface it detects itself. Needed if dcdash reaches the firewall through a proxy or NAT. See [firewalls.md](firewalls.md#lockout-guard). |

## projects

Projects group objects with the tag `project-<name>`. See [projects.md](projects.md).

| Key | Type | Default | Description |
|---|---|---|---|
| `projects.dir` | string | `/etc/dcdash/projects` | Directory with one file `<name>.yaml` per project. A missing directory means no projects. |
| `projects.reload_interval` | duration | `1m` | How often the directory is read again. `0` reads it only at startup. Must not be negative. |

## metrics

The optional Prometheus endpoint `/metrics`. The other keys are validated only when
`metrics.enabled` is true. See [monitoring.md](monitoring.md#prometheus-metrics).

| Key | Type | Default | Description |
|---|---|---|---|
| `metrics.enabled` | bool | `false` | Enables `/metrics`. |
| `metrics.listen` | string | empty | Separate plain HTTP listener for `/metrics`, `host:port`, for example `127.0.0.1:9100`. Empty: `/metrics` is served on the main listener. |
| `metrics.token` | secret | empty | Bearer token Prometheus must send. Required when `listen` is empty, optional otherwise. At least 16 characters. |

## security

Protection of data at rest. See [security.md](security.md#data-at-rest).

| Key | Type | Default | Description |
|---|---|---|---|
| `security.encryption_key` | secret | none | Key (base64, 32 bytes) that encrypts secrets in the database, such as TOTP seeds. Create one with `dcdash gen-key`. Required. See [security.md](security.md#the-encryption-key). |
| `security.previous_encryption_keys` | list of secrets | empty | Old keys, still accepted for decryption after a key rotation. Each must be a valid key. See [security.md](security.md#key-rotation). |

## log

| Key | Type | Default | Description |
|---|---|---|---|
| `log.level` | string | `info` | `trace`, `debug`, `info`, `warn` or `error`. |
| `log.format` | string | `json` | `json`, or `console` for human-readable output. |

## data_dir

| Key | Type | Default | Description |
|---|---|---|---|
| `data_dir` | string | `/var/lib/dcdash` | Directory for persistent state: the database and the ACME account and certificates. Required (must not be empty). |

## Minimal example

A working configuration with certificate files, LDAP login against Active Directory, one role
and no modules. Members of the group `dashroot` get the `root` role by default.

```yaml
server:
  listen: ":8443"
  tls:
    mode: files
    cert_file: /etc/dcdash/tls/cert.pem
    key_file: /etc/dcdash/tls/key.pem

auth:
  ldap:
    url: ldaps://dc1.example.com:636
    mode: ad
    bind_dn: dcdash-svc@example.com
    bind_password: "file:/etc/dcdash/ldap-bind.password"
    base_dn: DC=example,DC=com

roles:
  - name: auditor
    groups: ["dcdash-audit"]
    permissions:
      - "system:core:audit:read"

security:
  encryption_key: "file:/etc/dcdash/encryption.key"
```

Create the key with `dcdash gen-key > /etc/dcdash/encryption.key`, then check the file with
`dcdash config validate`.

For a commented example of the whole file, see
[configs/config.example.yaml](../configs/config.example.yaml).
