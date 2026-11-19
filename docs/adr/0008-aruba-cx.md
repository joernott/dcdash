# ADR 0008: Aruba CX switches

- Status: proposed (to be verified against a real switch)
- Date: 2026-11-19
- Task: 13.1

## Context

dcdash should show and change Aruba CX 6x00 switches (AOS-CX 10.x): port status across all
switches, finding devices by MAC address, VLANs, and port configuration. The REST API is
described in the AOS-CX REST API guide (v10.0x, for example 10.15) and used by HPE's Python
library pyaoscx. This ADR is based on the documentation; the calls are checked against a
real switch in task 13.9 and this ADR is amended then.

## API

All calls go to `https://<switch>/rest/v10.<minor>/...` (the newest version the firmware
offers; `GET /rest` lists them).

| Purpose | Call | Notes |
|---|---|---|
| Log in | `POST /rest/v10.xx/login` with `username` and `password` (form) and the header `x-use-csrf-token: true` | Returns a session cookie and the CSRF token in the `x-csrf-token` response header, which every later request sends. |
| Log out | `POST /rest/v10.xx/logout` | Sessions are limited per user and per switch; dcdash logs out when it doesn't need a session any more. Idle timeout 20 minutes, hard timeout 8 hours. |
| System, firmware | `GET /rest/v10.xx/system?attributes=hostname,platform_name,software_version` | Health check. |
| Interfaces | `GET /rest/v10.xx/system/interfaces?depth=2&attributes=...` | Admin and link state, speed, description, VLAN mode, access VLAN, trunk VLANs; statistics are separate. |
| LLDP neighbors | `GET /rest/v10.xx/system/interfaces/{name}/lldp_neighbors?depth=2` | Interface names are URL-encoded (`1%2F1%2F1`). |
| PoE | `GET /rest/v10.xx/system/interfaces/{name}/poe_interface` | Only on PoE models. |
| VLANs | `GET`, `POST`, `PUT`, `DELETE /rest/v10.xx/system/vlans[/{id}]` | `name`, `admin` state. |
| MAC address table | `GET /rest/v10.xx/system/vlans/*/macs?depth=2` | Keys `<from>,<mac>` (`dynamic`, `static`, ...), attribute `port`. |
| Change a port | `PUT`/`PATCH /rest/v10.xx/system/interfaces/{name}` | `admin`, `description`, `vlan_mode`, `vlan_tag`, `vlan_trunks`. |
| Save the configuration | `PUT /rest/v10.xx/fullconfigs/startup-config?from=/rest/v10.xx/fullconfigs/running-config` | The equivalent of `write memory`. |

## Decision (draft)

- **Several switches** in a `switches` section, each with URL, TLS (CA file or fingerprint),
  username and password of a local or TACACS/RADIUS account with the `administrators` role
  (read-only accounts for read-only use).
- **Sessions** are kept per switch and reused; requests with an expired session log in again
  once. Logout on shutdown.
- **Uplinks** are told from access ports by LLDP neighbors that are switches (system
  capabilities "bridge") and by trunk ports; the MAC search lists access ports first.
- **Changes** (VLANs, ports) are made in the running configuration and saved to the startup
  configuration right after; a failed save is reported as an error.
- **Permissions** `switch:<name>:port/<interface>:<action>`, `switch:<name>:vlan/<id>:<action>`.
- **NetBox write-back** for VLANs and ports (task 13.7), projects for switch ports on the switch
  pages (see ADR 0007).
- **Tests** with a fake AOS-CX API; a manual test against a real switch is documented.

## Sources

- [AOS-CX REST API introduction](https://developer.arubanetworks.com/aoscx/docs/introduction)
- [AOS-CX 10.15 REST API guide](https://arubanetworking.hpe.com/techdocs/AOS-CX/10.15/PDF/rest_v10-0x.pdf)
- [pyaoscx](https://github.com/aruba/pyaoscx) (configuration and MAC table)
