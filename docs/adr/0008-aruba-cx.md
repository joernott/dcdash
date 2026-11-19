# ADR 0008: Aruba CX switches

- Status: accepted (verified against a real switch)
- Date: 2026-11-19
- Task: 13.1

## Context

dcdash should show and change Aruba CX 6x00 switches (AOS-CX 10.x): port status across all
switches, finding devices by MAC address, VLANs, and port configuration. The REST API is
described in the AOS-CX REST API guide (v10.0x, for example 10.15) and used by HPE's Python
library pyaoscx.

All calls below were checked on 2026-11-19 against a test switch: Aruba 6000 (24 × 1 GbE
RJ45, 4 × SFP+), firmware PL.10.08.1010, except PoE (the switch has none). The write test
created VLAN 4000, renamed and deleted it, changed the unused port 1/1/5 (description,
access VLAN, trunk, shut down and enabled), restored it and saved the configuration.

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
| Create a VLAN | `POST /system/vlans` with `{"id": 4000, "name": "..."}` | yes | 201 |
| Rename a VLAN | `GET /system/vlans/{id}?selector=writable`, change `name`, `PUT` it back | yes | 200 |
| Delete a VLAN | `DELETE /system/vlans/{id}` | yes | 204 |
| Change a port | `GET /system/interfaces/{name}?selector=writable`, change attributes, `PUT` the whole object back | yes | 200. 10.08 has no `PATCH` (the switch closes the connection). A `PUT` replaces all writable attributes, so dcdash always sends the complete object it just read. See "Port attributes". |
| Save the configuration | `PUT /fullconfigs/startup-config?from=/rest/v10.xx/fullconfigs/running-config` | yes | 200, the equivalent of `write memory`. Startup and running configuration then differ only in defaults the firmware omits. |

### Port attributes (10.08)

| Attribute | Meaning on the test switch |
|---|---|
| `admin_state`, `link_state`, `link_speed` | Operational state (`up`/`down`; an enabled port without link shows `admin_state: down`), speed in bit/s |
| `admin` | `null` on ports. The configured state is `user_config.admin`: `"down"` (shut down), `"up"` or missing (enabled, the default on the 6000) |
| `description` | Port description, `null` when empty |
| `vlan_mode` | `null` for an access port (the default), `native-untagged` or `native-tagged` for a trunk |
| `vlan_tag` | Access or native VLAN, read as `{"1404": "/rest/v10.08/system/vlans/1404"}`, written as the URI string `"/rest/v10.08/system/vlans/1404"`; `null` for VLAN 1 |
| `vlan_trunks` | Allowed VLANs of a trunk, written as a list of URIs; empty means all VLANs |
| `applied_vlan_mode`, `applied_vlan_tag`, `applied_vlan_trunks` | What is active, with VLAN 1 and "all VLANs" resolved |
| `hw_intf_info.connector`, `max_speed` | `RJ45` or `SFP_PLUS`, maximum speed in Mbit/s |

## Decision

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
- **Changes** (VLANs, ports) are made by read-modify-write of the writable object (no `PATCH`
  on 10.08), serialized per switch, in the running configuration and saved to the startup
  configuration right after; a failed save is reported as an error.
- **Permissions** `switch:<name>:port/<interface>:<action>` (`operate` enables and disables a
  port, `write` changes it) and `switch:<name>:vlan/<id>:write`. Creating a VLAN is offered
  if the user has `write` on some `vlan/...` resource (`rbac.Set.AllowsPrefix`).
- **Safety:** dcdash refuses to delete VLAN 1 and VLANs that ports use; the UI asks for a
  confirmation before an uplink is changed. Saving takes about 10 seconds on a 6000.
- **MAC search** reads the MAC address tables of all switches in parallel; hits on access
  ports come first, ports in a LAG count as uplinks if the LAG is one.
- **NetBox write-back** (task 13.7) only for switches with `netbox_device`: ports update the
  existing interfaces of that device (dcdash never creates or deletes interfaces), VLANs are
  created in the configured VLAN group or site if missing, and renamed or deleted only if they
  have the dcdash tag (NetBox VLANs are shared by all switches). Projects of switch ports are the project tags of their NetBox interfaces and
  are changed through the project API (ADR 0007).
- **Tests** with a fake AOS-CX API that returns the data shapes above; a manual test against
  the real switch is documented.

## Sources

- [AOS-CX REST API introduction](https://developer.arubanetworks.com/aoscx/docs/introduction)
- [AOS-CX 10.15 REST API guide](https://arubanetworking.hpe.com/techdocs/AOS-CX/10.15/PDF/rest_v10-0x.pdf)
- [pyaoscx](https://github.com/aruba/pyaoscx) (configuration and MAC table)
