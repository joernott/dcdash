# DNS: Cloudflare and bind9

dcdash manages DNS zones and records in Cloudflare accounts and on bind9 primary servers.
Users see the zones they may read, change records with `write`, and create or delete zones
with `admin` (see [security.md](security.md#dns-permissions)). Every change is recorded in
the audit log.

Each provider in the configuration is one Cloudflare account or one bind9 primary:

```yaml
dns:
  providers:
    - name: cloudflare-prod          # used in the UI and in permissions (dns:cloudflare-prod:...)
      type: cloudflare
      cloudflare:
        api_token: "file:/etc/dcdash/cloudflare-dns.token"
        account_id: "0123456789abcdef0123456789abcdef"   # only to create and delete zones
    - name: bind-internal
      type: bind
      bind:
        nameserver: ns1.example.com           # port 53 by default
        tsig_key: dcdash
        tsig_secret: "file:/etc/dcdash/bind-dcdash.tsig"
        tsig_algorithm: hmac-sha256
        zones: [example.internal]             # zones from named.conf
        catalog_zone: catalog.example.internal
        rndc: {...}                           # optional, see below
        sftp: {...}                           # optional, see below
        new_zones: {...}                      # optional, see below
```

## Records

Record contents use the format of zone files, for example `10 mail.example.com.` for an MX
record or `10 5 5060 sip.example.com.` for SRV. Names in the content end with a dot. Without
the dot, dcdash appends the zone name. Plain text for TXT records is quoted automatically, and
dcdash splits it into strings of 255 bytes. dcdash can create and change A, AAAA, CNAME, MX,
TXT, SRV, PTR, NS and CAA records. It shows other types, such as SOA, read-only.

dcdash detects concurrent changes. Each record carries a version: the zone's SOA serial for
bind9, or the record's modification time for Cloudflare. If the zone or record changed after
the user loaded it, dcdash rejects the change and reloads the records.

## Cloudflare

Create an API token under *My Profile → API Tokens → Create Token → Custom token*. Use a
different token than the one for ACME certificates.

| dcdash uses | Token permission |
|---|---|
| List zones and records | Zone → Zone → Read, Zone → DNS → Read |
| Change records | Zone → DNS → Edit |
| Create and delete zones | Zone → Zone → Edit, and `account_id` in the configuration |

Restrict the token to the zones (or the account) dcdash should manage. dcdash shows only the
zones the token can see.

A new Cloudflare zone stays in the state "pending" until the registrar delegates the domain
to the Cloudflare name servers. dcdash shows them after creating the zone and in the zone
list.

## bind9

dcdash talks to the primary in three ways:

1. **Records:** zone transfers (AXFR) to read them, RFC 2136 dynamic updates to change them.
   Both are signed with a TSIG key. This is all dcdash needs to manage records.
2. **rndc** (optional): server status, and adding and deleting zones.
3. **SFTP** (optional): uploading zone files of new zones and removing them again.

Zone creation needs all three, a catalog zone and the `new_zones` template.

### TSIG key and zone settings

Generate a key on the primary and copy its secret to the dcdash host (mode 0600):

```sh
tsig-keygen -a hmac-sha256 dcdash > /etc/bind/dcdash.key
```

In `named.conf`, include the key and allow it to transfer and update the zones dcdash
manages:

```
include "/etc/bind/dcdash.key";

zone "example.internal" {
    type primary;
    file "/var/lib/bind/example.internal.db";
    update-policy { grant dcdash zonesub ANY; };
    allow-transfer { key dcdash; };     // add your secondaries here as well
};
```

Zones with dynamic updates must be in a directory that `named` can write to, because it keeps a
journal (`.jnl`). Don't edit such zone files by hand while `named` runs. Use `rndc freeze
<zone>`, edit, then `rndc thaw <zone>`.

dcdash uses the TSIG key only for the zones in `zones` and in the catalog zone. If bind9
silently ignores an update, for example a CNAME next to other records (RFC 2136 section
3.4.2.2), dcdash notices and reports it. It checks each change after sending it.

### Catalog zone

A catalog zone (RFC 9432) lists the zones of the primary. dcdash reads it to find the zones.
When it creates or deletes a zone, it adds or removes the zone there, and secondaries that
consume the catalog follow. Create it on the primary:

```
; /var/lib/bind/catalog.example.internal.db
$TTL 0
@       IN SOA  ns1.example.com. hostmaster.example.com. ( 1 3600 600 86400 0 )
@       IN NS   invalid.
version IN TXT  "2"
```

```
zone "catalog.example.internal" {
    type primary;
    file "/var/lib/bind/catalog.example.internal.db";
    update-policy { grant dcdash zonesub ANY; };
    allow-transfer { key dcdash; 192.0.2.53; };   // the secondaries
    also-notify { 192.0.2.53; };
};
```

On each secondary:

```
options {
    catalog-zones {
        zone "catalog.example.internal" default-primaries { 192.0.2.1; };
    };
};
zone "catalog.example.internal" {
    type secondary;
    primaries { 192.0.2.1; };
    file "catalog.example.internal.db";
};
```

### rndc

dcdash calls the `rndc` binary (package `bind9-utils`) with its own key. On the primary:

```sh
rndc-confgen -a -k dcdash-rndc -c /etc/bind/dcdash-rndc.key
```

```
include "/etc/bind/dcdash-rndc.key";
controls {
    inet 192.0.2.1 port 953 allow { 192.0.2.10; } keys { "dcdash-rndc"; };   // the dcdash host
};
options {
    allow-new-zones yes;    // rndc addzone and delzone
};
```

Copy the key file to the dcdash host (mode 0600, owned by the dcdash user) and configure it:

```yaml
        rndc:
          binary: /usr/sbin/rndc
          server: ns1.example.com
          port: 953
          key_file: /etc/dcdash/dcdash-rndc.key
```

The rndc key can control the whole server. Restrict `allow` to the dcdash host and protect
the key file like a password.

### SFTP user for zone files

dcdash can't assume that it runs on the bind9 server, so it uploads the files of new zones
over SFTP. Use a dedicated user that can only use SFTP, chrooted to a directory that contains
the zone directory, and a key pair used only for this primary.

On the primary:

```sh
adduser --system --group --home /srv/dcdash-zones --shell /usr/sbin/nologin dcdash-sftp
usermod -aG bind dcdash-sftp
install -d -o root -g root -m 0755 /srv/dcdash-zones           # chroot: owned by root
install -d -o bind -g bind -m 2775 /srv/dcdash-zones/zones     # shared with named (setgid)
install -d -m 0755 /etc/ssh/authorized_keys
```

In `/etc/ssh/sshd_config`:

```
Match User dcdash-sftp
    ChrootDirectory /srv/dcdash-zones
    ForceCommand internal-sftp -u 0002
    AuthorizedKeysFile /etc/ssh/authorized_keys/%u
    PasswordAuthentication no
    AllowTcpForwarding no
    X11Forwarding no
```

On the dcdash host, create the key pair and install the public key on the primary:

```sh
ssh-keygen -t ed25519 -N "" -C dcdash -f /etc/dcdash/bind-sftp.key
# on the primary: /etc/ssh/authorized_keys/dcdash-sftp gets bind-sftp.key.pub
```

dcdash rejects unknown host keys. Pin the primary's host key (the first two fields of
`/etc/ssh/ssh_host_ed25519_key.pub`) or point to a `known_hosts` file:

```yaml
        sftp:
          address: ns1.example.com:22
          user: dcdash-sftp
          private_key_file: /etc/dcdash/bind-sftp.key
          host_key: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA..."
          zone_dir: /zones          # as seen over SFTP (inside the chroot)
```

dcdash writes a file under a temporary name and renames it, so `named` never reads a half
written file. Uploaded files are group-writable, because `named` rewrites zone files with
dynamic updates. AppArmor or SELinux may need to allow `named` to write to the directory.

### Zones created by dcdash

```yaml
        new_zones:
          file_dir: /srv/dcdash-zones/zones   # the zone directory as seen by named
          # Options for rndc addzone (default: updates and transfers for the TSIG key).
          options: 'update-policy { grant dcdash zonesub ANY; }; allow-transfer { key dcdash; 192.0.2.53; }; also-notify { 192.0.2.53; };'
          ttl: 1h
          soa: {mname: ns1.example.com, rname: hostmaster.example.com, refresh: 1h, retry: 15m, expire: 336h, minimum: 5m}
          nameservers: [ns1.example.com, ns2.example.com]
```

Creating a zone:
1. dcdash uploads `<zone>.db` with the SOA and NS records from the template.
2. It runs `rndc addzone`.
3. It adds the zone to the catalog zone.

If a step fails, dcdash undoes the earlier ones. Deleting a zone works the other way round:
1. dcdash removes the zone from the catalog, so the secondaries drop it.
2. It runs `rndc delzone`.
3. It removes the zone file and its journal.

The user must type the zone name to confirm. dcdash deletes only zones from the catalog zone.
Zones defined in `named.conf` stay untouched.

## Troubleshooting

| Message in dcdash | Cause |
|---|---|
| "The backend cannot be reached" | Network, firewall, wrong port, or the token or TSIG key is invalid (TSIG errors are logged). |
| "The backend refused the request" (bind9) | `update-policy` or `allow-transfer` doesn't cover the TSIG key, or the zone isn't on this server. |
| "The backend refused the request" (Cloudflare) | The token lacks a permission (see the table above) or is expired. |
| "The data was changed in the meantime" | Someone else changed the zone (bind9) or the record (Cloudflare). dcdash reloaded the records; repeat the change. |
| "The server did not accept the change" | bind9 ignored the update, for example a CNAME next to other records with the same name. |
| A zone is missing in the list (bind9) | It's neither in `zones` nor in the catalog zone, or the catalog can't be transferred with the TSIG key. |
| "Create zone" is missing | bind9: `rndc`, `sftp`, `new_zones` or `catalog_zone` is not configured. Cloudflare: `account_id` is missing. Or the user lacks `admin`. |
