# Monitoring

dcdash offers three ways to watch it: health endpoints for load balancers and service
managers, the status page in the UI, and optional Prometheus metrics.

## Health endpoints

| Endpoint | Answers | Use it for |
|---|---|---|
| `GET /healthz` | Always `200 {"status":"ok"}` while the process serves HTTP | Liveness checks (systemd, Kubernetes) |
| `GET /readyz` | `200`, or `503` if the database or the server certificate isn't usable; the body lists each check | Readiness checks of a load balancer |

Neither needs a login. `/readyz` deliberately leaves out the backends (directory, Proxmox,
DNS, DHCP): one unreachable backend must not take dcdash out of the load balancer. Their state
is on the status page (`GET /api/v1/status`, for logged-in users) and in the metrics.

## Prometheus metrics

Metrics are off by default. Enable them in the configuration:

```yaml
metrics:
  enabled: true
  listen: "127.0.0.1:9100"      # separate plain HTTP listener
  token: "file:/etc/dcdash/metrics.token"   # optional here
```

- **Separate listener** (`listen` set): only `/metrics` is served there, over plain HTTP.
  Bind it to the loopback interface or a management network, or protect it with a firewall.
  A token is optional.
- **Main listener** (`listen` empty): `/metrics` is served next to the UI, over HTTPS, and a
  `token` is required.

With a token, Prometheus sends it as a bearer token. Use at least 16 random characters, for
example `openssl rand -hex 32 > /etc/dcdash/metrics.token`, and make the file readable only
for dcdash (`chmod 600`).

```yaml
scrape_configs:
  - job_name: dcdash
    scheme: https
    authorization:
      credentials_file: /etc/prometheus/dcdash.token
    static_configs:
      - targets: ["dcdash.example.com:8443"]
```

### Metrics

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `dcdash_build_info` | gauge | `version`, `commit`, `goversion` | Always 1 |
| `dcdash_certificate_expiry_timestamp_seconds` | gauge | – | Expiry of the server certificate (Unix time). Missing while there is none and with `tls.mode: none` |
| `dcdash_backend_up` | gauge | `kind`, `name` | 1 if the check of a component passed, 0 if not. The same checks as on the status page: `system` (database, certificate, directory), `proxmox`, `dns`, `dhcp` |
| `dcdash_backend_check_duration_seconds` | gauge | `kind`, `name` | Duration of that check |
| `dcdash_http_requests_total` | counter | `route`, `method`, `code` | HTTP requests by route pattern, e.g. `GET /api/v1/dhcp/servers/{server}/leases`. Requests that match no route have `route="unmatched"` |
| `dcdash_http_request_duration_seconds` | histogram | `route` | Request duration; console websockets aren't included |
| `dcdash_audit_events_total` | counter | `action`, `result` | Audit log entries, e.g. `action="auth.login", result="failure"` |
| `go_*`, `process_*` | | | Go runtime and process metrics |

The backend checks run on every scrape, each limited to 5 seconds. A scrape interval of 30
seconds or more is enough.

### Alert examples

```yaml
groups:
  - name: dcdash
    rules:
      - alert: DcdashCertificateExpiresSoon
        expr: dcdash_certificate_expiry_timestamp_seconds - time() < 14 * 86400
        labels: {severity: warning}
        annotations:
          summary: "The dcdash server certificate expires in less than 14 days"
      - alert: DcdashBackendDown
        expr: dcdash_backend_up == 0
        for: 5m
        labels: {severity: warning}
        annotations:
          summary: "dcdash can't use {{ $labels.kind }} backend {{ $labels.name }}"
      - alert: DcdashLoginFailures
        expr: sum(increase(dcdash_audit_events_total{action="auth.login",result="failure"}[10m])) > 20
        labels: {severity: info}
        annotations:
          summary: "Many failed dcdash logins"
```

With ACME, dcdash renews the certificate about 30 days before it expires, so the first alert
only fires if renewal keeps failing.
