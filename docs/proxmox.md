# Proxmox VE

dcdash shows the nodes, virtual machines and containers of one or more Proxmox VE clusters.
Users can run power actions and open consoles, depending on their dcdash permissions (see
[security.md](security.md#proxmox-permissions)).

dcdash talks to the Proxmox API with an **API token** per cluster. Proxmox checks the token's
privileges in addition to dcdash's own permissions, so a token with read-only privileges keeps
dcdash read-only, whatever a role in dcdash grants.

## Create the API token

Create a user for dcdash and a token with privilege separation on a cluster node:

```sh
pveum user add dcdash@pve --comment "dcdash dashboard"
pveum user token add dcdash@pve dcdash --privsep 1 --comment "dcdash"
```

Proxmox shows the token secret only once. Store it on the dcdash host, for example in
`/etc/dcdash/pve-lab.token` (mode 0600, owned by the dcdash user).

With privilege separation, the token gets only the privileges granted to the token itself, and
only those that the user also has. Grant the roles to both the user and the token:

| dcdash uses | Proxmox role | Privileges |
|---|---|---|
| Inventory, details, tasks | `PVEAuditor` on `/` | `Sys.Audit`, `VM.Audit`, `Datastore.Audit` |
| Power actions, VNC consoles | `PVEVMUser` on `/vms` or a pool | `VM.PowerMgmt`, `VM.Console`, ... |

Container terminals and node shells don't work with an API token, because Proxmox rejects the
terminal login with tokens. They need a console user (see [Consoles](#consoles)).

For example, read access everywhere and power actions and consoles on all guests:

```sh
for who in "--users dcdash@pve" "--tokens dcdash@pve!dcdash"; do
  pveum acl modify / $who --roles PVEAuditor
  pveum acl modify /vms $who --roles PVEVMUser
done
```

An ACL entry for a user replaces the entries of the user's groups **on the same path**. For
example, `PVEAuditor` for `dcdash@pve` on `/` hides a `PVEVMUser` granted to one of its
groups on `/`. Grant all roles the user needs on that path in one entry
(`--roles PVEAuditor,PVEVMUser`). `pveum user permissions dcdash@pve` and
`pveum user token permissions dcdash@pve dcdash` show the effective privileges.

Without `Sys.Audit`, dcdash shows nodes without CPU, memory and disk usage. Grant node shells
only if they are needed: a node shell is a root shell on the hypervisor.

## Configure dcdash

```yaml
proxmox:
  clusters:
    - name: lab                       # used in the UI and in permissions (proxmox:lab:...)
      endpoints:                      # tried in order; the first reachable one is used
        - url: https://pve1.example.com:8006
        - url: https://pve2.example.com:8006
      tls: {ca_file: /etc/dcdash/pve-lab-ca.pem}
      token_id: "dcdash@pve!dcdash"
      token_secret: "file:/etc/dcdash/pve-lab.token"
      timeout: 15s
```

### Certificates

Proxmox nodes use certificates from the cluster's own CA by default. Either:

- copy the CA certificate (`/etc/pve/pve-root-ca.pem` on any node) to the dcdash host and set
  `tls.ca_file`. dcdash then verifies each node's certificate, including the host name, so the
  endpoint URLs must use names or addresses that are in the certificates; or
- pin each node's certificate by its SHA-256 fingerprint with `fingerprint` on the endpoint.
  `pvenode cert info` on the node shows it, or:

  ```sh
  openssl s_client -connect pve1.example.com:8006 </dev/null 2>/dev/null \
    | openssl x509 -noout -fingerprint -sha256
  ```

  A pin must be updated when the node's certificate is renewed.

If the nodes use certificates from a public CA (for example via ACME), neither is needed.

### Failover

dcdash sends each request to the endpoint that worked last. If it can't be reached (connection
or TLS error, timeout, or 502/503/504 from a reverse proxy in front of it), dcdash tries the
next one. Errors from the Proxmox API itself, such as a wrong token or missing privileges, are
not retried on other nodes. The dashboard shows each cluster with the endpoint in use.

## Consoles

dcdash proxies console connections, so neither the browser nor the user needs access to
Proxmox: virtual machines get a VNC console (noVNC), containers and nodes a terminal
(xterm.js). A user opens a console with the `console` permission on the guest, or with `admin`
on the node for a node shell. dcdash records the start and end of every console session in the
audit log. It closes the console when the user's dcdash session ends.

VNC consoles use the API token. Container terminals and node shells need a separate Proxmox
user with a password, because Proxmox doesn't accept API tokens for the terminal login (see
[ADR 0001](adr/0001-proxmox-console-authentication.md)). Without a console user, dcdash offers
only VNC consoles and hides the terminal buttons.

Create the console user and grant it the console privileges:

```sh
pveum user add dcdash-console@pve --comment "dcdash terminals"
pveum passwd dcdash-console@pve
pveum acl modify /vms --users dcdash-console@pve --roles PVEVMUser
# Only if node shells are wanted (root shells on the hypervisors):
pveum acl modify /nodes --users dcdash-console@pve --roles PVESysAdmin
```

Store the password on the dcdash host (mode 0600) and configure it:

```yaml
      console:
        username: "dcdash-console@pve"
        password: "file:/etc/dcdash/pve-lab-console.password"
```

Proxmox checks the console user's privileges for terminals, not the token's. dcdash still
checks its own `console` and `admin` permissions first, so the console user doesn't widen what
a dcdash user can do.

## Troubleshooting

| Message in dcdash | Cause |
|---|---|
| "The backend cannot be reached" | No endpoint answered: network, firewall, certificate (fingerprint or CA) or timeout. The dcdash log names the endpoints and errors. |
| "The backend refused the request" | The API token lacks a Proxmox privilege for the action (see the table above), or the token is wrong or expired. |
| A cluster shows no or only some guests | The token lacks `VM.Audit` on those guests, or the user lacks `read` on them in dcdash. |
| Nodes show no usage | The token lacks `Sys.Audit`. |
| No console button for containers, no "Shell" link on nodes | No console user is configured for the cluster (see [Consoles](#consoles)). |
| "Terminals are not available for this cluster" | Same: the console user is missing. |
| The status in the list lags behind after a power action | Proxmox updates the cluster resource list about every 10 seconds. The guest page shows the current status. |
| A terminal shows `[EOF - dtach terminating]` | Proxmox ended an older console session of the container, for example after a restart. Select "Connect again". |
| `403 Permission check failed (/vms/…, VM.PowerMgmt)` although the token has `PVEVMUser` | With privilege separation, the user needs the privilege too. Check `pveum user token permissions`, and look for a user ACL that hides a group ACL on the same path (see above). |
