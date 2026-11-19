# ADR 0008: Aruba CX switches

- Status: proposed (reading verified against a real switch, writing not yet)
- Date: 2026-11-19
- Task: 13.1

## Context

dcdash should show and change Aruba CX 6x00 switches (AOS-CX 10.x): port status across all
switches, finding devices by MAC address, VLANs, and port configuration. The REST API is
described in the AOS-CX REST API guide (v10.0x, for example 10.15) and used by HPE's Python
library pyaoscx.

The read calls were checked on 2026-11-19 against a test switch: Aruba 6000 (24 × 1 GbE RJ45,
4 × SFP+), firmware PL.10.08.1010. The write calls are still unverified (see "Open").

## API

All calls go to `https://<switch>/rest/v10.<minor>/...`. `GET /rest` (no login) lists the
versions; dcdash uses `latest` (`v10.08` on the test switch).

| Purpose | Call | Verified | Notes |
|---|---|---|---|
| Versions | `GET /rest` | yes | `{"latest": {"version": "v10.08", "prefix": "/rest/v10.08"}, ...}` |
| Log in | `POST /rest/v10.xx/login`, form fields `username`, `password` | yes | Session cookie. Newer firmware returns a CSRF token in the `x-csrf-token` header when asked with `x-use-csrf-token: true`; 10.08 returns none. dcdash sends the token back when there is one. |
| Log out | `POST /rest/v10.xx/logout` | yes | Sessions per user are limited; idle timeout 20 minutes, hard timeout 8 hours. |
| System | `GET /system?attributes=hostname,platform_name,software_version` | yes | `DCDASH-SW33`, `6000`, `PL.10.08.1010` |
| Interfaces | `GET /system/interfaces?depth=2&attributes=...` | yes | Map by name. Includes VLAN interfaces (`vlan34`, `type: vlan`); ports have `type: system`. |
| LLDP neighbors | `GET /system/interfaces/{name}/lldp_neighbors?depth=2` | yes | Keys `<chassis id>,<port id>`; `neighbor_info.chassis_name`, `port_description`, `mgmt_ip_list`, `chassis_capability_enabled` (`"Bridge, Router"`). Names are URL-encoded (`1%2F1%2F1`). |
| PoE | `GET /system/interfaces/{name}/poe_interface` | no | The 6000 test switch has no PoE. |
| VLANs | `GET /system/vlans?depth=2` | yes | `id`, `name`, `admin`, `oper_state`, `type` (`default`, `static`). |
| MAC address table | `GET /system/vlans/*/macs?depth=2` | yes | Per VLAN: keys `<from>,<mac>` (`dynamic`, ...), `mac_addr`, `from`, `port` (`{"1/1/1": "/rest/..."}`). |
| Create, rename, delete VLANs | `POST /system/vlans`, `PUT`/`PATCH`/`DELETE /system/vlans/{id}` | no | |
| Change a port | `PATCH /system/interfaces/{name}` | no | See "Port attributes". |
| Save the configuration | `PUT /fullconfigs/startup-config?from=/rest/v10.xx/fullconfigs/running-config` | no | The equivalent of `write memory`. |

### Port attributes (10.08)

| Attribute | Meaning on the test switch |
|---|---|
| `admin_state`, `link_state`, `link_speed` | Operational state (`up`/`down`), speed in bit/s |
| `admin` | `null` on ports; the configured state is in `user_config.admin` (`"down"` when shut down, missing when enabled by default). To be verified by a write. |
| `description` | Port description, `null` when empty |
| `vlan_mode` | `null` for an access port (the default), `native-untagged` or `native-tagged` for a trunk |
| `vlan_tag` | Access or native VLAN as a reference `{"1404": "/rest/v10.08/system/vlans/1404"}`, `null` for VLAN 1 |
| `vlan_trunks` | Allowed VLANs of a trunk; empty means all VLANs |
| `applied_vlan_mode`, `applied_vlan_tag`, `applied_vlan_trunks` | What is active, with VLAN 1 and "all VLANs" resolved |
| `hw_intf_info.connector`, `max_speed` | `RJ45` or `SFP_PLUS`, maximum speed in Mbit/s |

## Decision (draft)

- **Several switches** in a `switches` section, each with URL, TLS and username and password
  of an account with the `administrators` role (read-only accounts for read-only use).
- **TLS:** the switches' factory certificates are self-signed, and their subject (for
  example `CN=switch,SN=<serial>`) is no host name, so a CA file cannot be verified. They are
  pinned by `tls.fingerprint` (SHA-256), as for Proxmox and LDAP; a CA file is for
  certificates issued by the user's CA.
- **Sessions** are kept per switch and reused; requests with an expired session log in again
  once. Logout on shutdown.
- **Uplinks** are told from access ports by LLDP neighbors that are bridges and by trunk ports;
  the MAC search lists access ports first.
- **Changes** (VLANs, ports) are made in the running configuration and saved to the startup
  configuration right after; a failed save is reported as an error.
- **Permissions** `switch:<name>:port/<interface>:<action>`, `switch:<name>:vlan/<id>:<action>`.
- **NetBox write-back** for VLANs and ports (task 13.7), projects for switch ports on the switch
  pages (see ADR 0007).
- **Tests** with a fake AOS-CX API that returns the data shapes above; a manual test against
  the real switch is documented.

## Open

Write calls on the test switch (allowed: all ports except the uplink 1/1/1, VLANs 4000 and
higher, `write memory`): create VLAN 4000, set description, access VLAN, trunk VLANs and the
admin state of an unused port, revert everything, save the configuration.

## Sources

- [AOS-CX REST API introduction](https://developer.arubanetworks.com/aoscx/docs/introduction)
- [AOS-CX 10.15 REST API guide](https://arubanetworking.hpe.com/techdocs/AOS-CX/10.15/PDF/rest_v10-0x.pdf)
- [pyaoscx](https://github.com/aruba/pyaoscx) (configuration and MAC table)
