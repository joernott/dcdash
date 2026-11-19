# Switches: Aruba CX

dcdash shows Aruba CX switches (AOS-CX 10.x, for example the 6000, 6100 and 6200 series):
their inventory and health on the overview page. Port overview, MAC address search, VLANs and
port configuration follow in Sprint 13. The design and the behavior it's based on are
described in [ADR 0008](adr/0008-aruba-cx.md).

dcdash uses the switch's REST API. It was tested with an Aruba 6000 and firmware 10.08.

```yaml
switches:
  - name: sw-core                      # used in the UI and in permissions (switch:sw-core:...)
    url: https://10.0.0.2
    tls:
      fingerprint: "11:37:4E:...:DA:F4"  # SHA-256 of the switch certificate
    username: dcdash
    password: "file:/etc/dcdash/switch-dcdash.password"
```

## Prerequisites on the switch

1. Enable the REST API in the VRF that dcdash reaches the switch through, and allow changes:

   ```text
   https-server vrf mgmt                # or: https-server vrf default
   https-server rest access-mode read-write
   ```

   With `access-mode read-only`, dcdash can only show the switch.
2. Create a local account for dcdash (or one in TACACS+/RADIUS). The role `administrators`
   allows changes, `operators` only reading:

   ```text
   user dcdash group administrators password plaintext <password>
   ```

3. A switch allows only a few REST sessions per user. dcdash keeps one session per switch
   and reuses it, so don't use the dcdash account in other tools.

## Certificate

The factory certificate of an Aruba CX switch is self-signed, and its subject (for example
`CN=switch,SN=<serial number>`) is no host name, so it can't be checked against a CA. Pin it
with `tls.fingerprint` instead:

```sh
openssl s_client -connect 10.0.0.2:443 </dev/null 2>/dev/null | openssl x509 -noout -fingerprint -sha256
```

The fingerprint changes when the switch creates a new certificate (for example after
`crypto pki` changes or a reset); dcdash then reports the switch as unreachable and logs the
new fingerprint. If you install a certificate of your CA on the switch, use `tls.ca_file`
instead.

## Sessions

dcdash logs in when it first needs the switch and keeps the session. The switch ends idle
sessions after 20 minutes and all sessions after 8 hours; dcdash then logs in again and
repeats the request. dcdash logs out when it stops.

## Health

The overview page shows each switch with model, host name and firmware, or why it isn't
available:

| Message | Cause |
|---|---|
| The switch rejected dcdash's credentials | Wrong user name or password, or the account is locked |
| Too many sessions are open on the switch | The account has the maximum number of REST sessions open, for example in other tools. They end after 20 minutes of inactivity |
| The switch is not reachable | Network, firewall, REST API not enabled in the VRF, or the certificate doesn't match the fingerprint (see the log) |

## Permissions

Permissions have the form `switch:<switch>:<resource>:<action>`. Resources are
`port/<interface>` (for example `port/1/1/5`) and `vlan/<id>`. A user sees a switch with
`read` on any of its resources, for example `switch:sw-core:*:read`. See
[security.md](security.md#switch-permissions).
