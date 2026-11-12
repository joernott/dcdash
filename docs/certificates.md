# TLS Certificates

dcdash serves its UI and API over HTTPS only. It gets its certificate in one of three ways,
set with `server.tls.mode`:

| Mode | Certificate source | Typical use |
|---|---|---|
| `acme` | Requested and renewed automatically from an ACME CA (Let's Encrypt, step-ca, …) with the DNS-01 challenge | Recommended |
| `files` | PEM files provided by the administrator or another tool; reloaded on change | Existing PKI, certificates managed elsewhere |
| `none` | No TLS (plain HTTP) | Development, or behind a TLS-terminating reverse proxy |

In every mode with TLS, dcdash accepts TLS 1.2 and 1.3 only, with forward-secret AEAD cipher
suites for TLS 1.2. The certificate status is part of `/readyz`:

```json
{"status": "ok", "checks": {"certificate": "valid until 2027-02-10T08:00:00Z"}}
```

## ACME (`mode: acme`)

dcdash uses the DNS-01 challenge: it publishes a TXT record `_acme-challenge.<domain>` that the
CA checks. This works for hosts that are only reachable internally, and it supports wildcard
certificates. Two DNS providers are supported:

- `cloudflare`: zones hosted at Cloudflare
- `rfc2136`: dynamic DNS updates signed with TSIG, for example on bind9

### Let's Encrypt with Cloudflare

```yaml
server:
  listen: ":443"
  redirect_listen: ":80"
  tls:
    mode: acme
    acme:
      # directory_url defaults to Let's Encrypt production.
      email: hostmaster@example.com
      accept_tos: true
      domains: [dcdash.example.com]
      dns:
        provider: cloudflare
        cloudflare:
          api_token: "file:/etc/dcdash/cloudflare-acme.token"
```

Create a Cloudflare API token with the permissions **Zone → Zone → Read** and
**Zone → DNS → Edit**, limited to the zone(s) containing the domains. Use a separate token
from the one dcdash uses later for DNS management. If the zone lookup should use a different
token, set `zone_token` as well.

Before going live, test against the Let's Encrypt staging environment to avoid its rate
limits:

```yaml
      directory_url: https://acme-staging-v02.api.letsencrypt.org/directory
```

### step-ca

step-ca issues certificates over ACME when it has an ACME provisioner (for example
`step ca provisioner add acme --type ACME`). Point dcdash at that provisioner's directory and
trust the step-ca root:

```yaml
server:
  tls:
    mode: acme
    acme:
      directory_url: https://ca.example.com/acme/acme/directory
      ca_file: /etc/dcdash/step-ca-root.pem
      accept_tos: true
      domains: [dcdash.example.com]
      dns:
        provider: rfc2136
        rfc2136:
          nameserver: ns1.example.com        # primary server, port 53 by default
          tsig_key: dcdash-acme
          tsig_secret: "file:/etc/dcdash/tsig-acme.secret"
          tsig_algorithm: hmac-sha256       # default
```

`ca_file` adds the CA root to the system roots for the connection to the ACME server. If the
provisioner requires external account binding, add:

```yaml
      eab:
        key_id: <key id>
        hmac_key: "file:/etc/dcdash/eab.hmac"
```

step-ca issues short-lived certificates by default (24 hours). dcdash handles this
automatically, see [Renewal](#renewal).

### bind9 setup for `rfc2136`

Create a TSIG key and allow it to change TXT records in the zone. A key restricted like this
can't change any other records:

```sh
tsig-keygen -a hmac-sha256 dcdash-acme > /etc/bind/dcdash-acme.key
```

```conf
include "/etc/bind/dcdash-acme.key";

zone "example.com" {
    type primary;
    file "/var/lib/bind/db.example.com";
    update-policy {
        grant dcdash-acme zonesub TXT;
    };
};
```

Put the `secret` value of the key into the file referenced by `tsig_secret`.

### DNS propagation checks

Before dcdash asks the CA to validate, it waits until the challenge record is visible:

| Option | Default | Meaning |
|---|---|---|
| `dns.resolvers` | system resolvers | Resolvers (`host` or `host:port`) used to look up the zone and the record. Use internal resolvers for internal zones. |
| `dns.propagation_timeout` | provider default | How long to wait for the record. |
| `dns.disable_authoritative_check` | `false` | By default the zone's authoritative name servers are also queried directly. Set this to `true` if they can't be resolved or reached from dcdash, for example in split-horizon setups. |

### Renewal

dcdash requests a certificate at startup if it doesn't have a valid one, and renews it
`renew_before` before it expires (default `720h`, 30 days). For short-lived certificates it
renews at the latest after two thirds of the lifetime: a 24-hour step-ca certificate is
renewed after 16 hours.

If a request fails, dcdash keeps using the current certificate and retries after 1, 2, 4, …
minutes, up to once an hour. Each failure is logged as an error with the time of the next
retry.

### Storage

ACME data is stored per CA in `<data_dir>/acme/<ca host and path>/` with mode `0600`:

| File | Content |
|---|---|
| `account.key`, `account.json` | ACME account key and registration |
| `certificate.pem`, `certificate.key` | Current certificate (with chain) and its key |
| `certificate.json` | Domains and key type the certificate was requested for |

After a restart, dcdash reuses the stored certificate if the domains and key type are
unchanged. Changing `domains` or `key_type` requests a new certificate. Changing the CA uses a
new directory, so a new account and certificate.

## Certificate files (`mode: files`)

```yaml
server:
  tls:
    mode: files
    cert_file: /etc/dcdash/tls/cert.pem   # certificate, optionally followed by the chain
    key_file: /etc/dcdash/tls/key.pem
    reload_interval: 1m                   # 0 disables reloading
```

dcdash checks the content of both files every `reload_interval` and switches to the new
certificate without a restart. While a tool writes the certificate and key one after the
other, the mismatching pair is rejected and the current certificate stays in use until both
files match. dcdash doesn't start if the files can't be loaded at startup.

## Plain HTTP (`mode: none`)

dcdash then serves plain HTTP and logs a warning at startup. Use this only for development or
behind a reverse proxy that terminates TLS. `redirect_listen` isn't allowed in this mode.

## HTTP redirect and HSTS

| Option | Default | Meaning |
|---|---|---|
| `server.redirect_listen` | empty (off) | Additional plain HTTP listener, for example `":80"`, that answers every request with a permanent redirect (308) to the HTTPS listener |
| `server.hsts_max_age` | `4320h` (180 days) | `Strict-Transport-Security: max-age=…` header on HTTPS responses; `0` disables it |

To listen on ports 80 and 443 without running as root, give the binary the capability
`setcap cap_net_bind_service=+ep /usr/local/bin/dcdash`, or use
`AmbientCapabilities=CAP_NET_BIND_SERVICE` in a systemd unit.

## Troubleshooting

| Symptom | Cause and fix |
|---|---|
| `/readyz` reports `no certificate available` | The first ACME request hasn't succeeded yet. Check the log for `certificate request failed`. |
| `authoritative nameservers: … no such host` | The zone's NS names can't be resolved from dcdash. Set `dns.resolvers`, or `dns.disable_authoritative_check: true`. |
| `time limit exceeded` while waiting for propagation | The record isn't visible on the resolvers in time. Check `dns.resolvers`, or raise `dns.propagation_timeout`. |
| `x509: certificate signed by unknown authority` towards the CA | The CA's root isn't trusted. Set `ca_file`. |
| `REFUSED` from the name server | The TSIG key isn't allowed to update the zone. Check `update-policy` and the key name, secret and algorithm. |
