# ADR 0009: OPNsense firewalls

- Status: proposed (from the source of OPNsense core stable/26.1, to be verified on a real firewall)
- Date: 2026-11-19
- Task: 14.1

## Context

dcdash should show and change OPNsense firewalls: health (system, gateways, services), VPN
status (WireGuard, IPsec), enabling and disabling WireGuard peers, restarting IPsec tunnels,
aliases and filter rules. The firewalls in use run "26.4.2p4" and "26.4.2p5". The community
edition has only the release series 26.1 and 26.7; YY.4 versions are the **Business
Edition**, which is based on the community release before it. 26.4 should therefore behave
like 26.1. This matters for filter rules: the savepoint mechanism (automatic rollback) exists
in 26.1 and was removed in 26.7 (core pull request 10411). The calls below were read from the
source of core `stable/26.1`; they are checked against a real firewall in task 14.8, and this
ADR is amended then.

## API

All calls go to `https://<firewall>/api/<module>/<controller>/<command>[/<parameters>]` with
HTTP basic authentication (API key and secret of a user). There are no sessions. A wrong key
returns 401, a missing privilege 403. A POST with a JSON content type needs a JSON body (`{}`).
The privileges match the request path case-sensitively, so dcdash uses the snake_case names
(`search_phase1`, not `searchPhase1`).

| Purpose | Call | Privilege |
|---|---|---|
| Version | `GET /api/core/firmware/status` → `product.product_version`, `product_name` (a POST runs an update check) | System: Firmware |
| System | `GET /api/diagnostics/system/system_information`, `system_time` (uptime), `system_resources` (memory) | Lobby: Dashboard |
| Gateways | `GET /api/routes/gateway/status` → `items[]` with `name`, `address`, `status` (`none` = online, `down`, `force_down`, `delay`, `loss`, `delay+loss`), `delay`, `stddev`, `loss` (text, "~" if not monitored) | System: Gateways |
| Services | `GET /api/core/service/search` → `rows[]` with `id`, `name`, `description`, `running` (0/1), `locked`; `POST /api/core/service/restart/{id}` | Status: Services |
| WireGuard peers | `/api/wireguard/client/search_client`, `get_client/{uuid}`, `set_client/{uuid}`, `POST toggle_client/{uuid}` (→ `result` `Enabled`/`Disabled`), then `POST /api/wireguard/service/reconfigure` | VPN: WireGuard: Configuration |
| WireGuard status | `/api/wireguard/service/show` → rows with `type` `peer` or `interface`; peers: `name`, `public-key`, `endpoint`, `allowed-ips`, `latest-handshake` (epoch, 0 = never), `transfer-rx`, `transfer-tx` (bytes), `peer-status` (`online` ≤ 300 s, `stale`, `offline`) | VPN: WireGuard: Status |
| IPsec status | `/api/ipsec/sessions/search_phase1` → rows with `name` (connection), `phase1desc`, `connected`, `remote-addrs`, `bytes-in`, `bytes-out`, `install-time`; `POST search_phase2` with `id` = phase 1 name | Status: IPsec |
| IPsec restart | `POST /api/ipsec/sessions/disconnect/{name}`, then `connect/{name}` (asynchronous: poll the status) | Status: IPsec |
| Aliases | `search_item`, `get_item/{uuid}`, `POST add_item`, `set_item/{uuid}` (`{"alias": {...}}`, `content` newline-separated), `del_item/{uuid}` (fails if in use), `toggle_item/{uuid}`, then `POST /api/firewall/alias/reconfigure` | Firewall: Alias: Edit |
| Filter rules | `search_rule`, `get_rule/{uuid}`, `POST add_rule`, `set_rule/{uuid}` (`{"rule": {...}}`), `del_rule/{uuid}`, `toggle_rule/{uuid}` | Firewall: Rules [new] |
| Savepoint | `POST /api/firewall/filter/savepoint` → `revision`; `POST apply/{revision}` starts a 60-second rollback timer; `POST cancel_rollback/{revision}` keeps the change; without it, OPNsense reverts and reloads the filter | Firewall: Rules [new] |

Search calls take `current`, `rowCount` (-1 for all) and `searchPhrase` and return `{total,
rowCount, current, rows}`. Changes return `{"result": "saved"}` (with `uuid` for new items),
or `{"result": "failed", "validations": {"<model>.<field>": "message"}}` with HTTP 200.

Since 26.1 the filter API manages the rules of the new rules page ("Rules [new]").
`search_rule` also lists legacy and automatic rules, read-only (`legacy: true`); legacy rules
can only be changed after migrating them in OPNsense.

## Decision (draft)

- **Several firewalls** in a `firewalls` section, each with URL, TLS, API key and secret.
- **TLS:** the default web interface certificate is self-signed, issued for
  `<hostname>.<domain>` without IP addresses: pin it with `tls.fingerprint`, or use the FQDN
  with `tls.ca_file`.
- **Health** on the overview page: version, gateways that are not online, services that are
  not running.
- **Rule changes with a savepoint:** dcdash creates a savepoint, makes the change, applies it
  with the revision, then checks with a new connection that it still reaches the firewall's
  API, and only then cancels the rollback. If the rule cuts off dcdash, OPNsense reverts it
  after 60 seconds by itself. Without the savepoint calls (26.7 and later), dcdash refuses
  rule changes; only reading is possible.
- **Aliases and WireGuard** changes are applied with `reconfigure` right after the change.
- **Permissions** `firewall:<name>:<resource>:<action>` with the resources `system`,
  `wireguard/<peer>`, `ipsec/<connection>`, `alias/<name>` and `rule/<uuid>`.
- **Tests** with a fake OPNsense API; a manual test against a real firewall is documented.

## Open

- The exact version string and API of the Business Edition 26.4 (savepoints?).
- The fields of `firmware/status` beyond `product`.

## Sources

Source of [opnsense/core](https://github.com/opnsense/core), branch `stable/26.1` (and
`stable/26.7` for the removed savepoints), below `src/opnsense/mvc/app/controllers/OPNsense/`:
`Base/ApiControllerBase.php`, `Base/ApiMutableModelControllerBase.php`,
`Firewall/Api/{FilterBaseController,FilterController,AliasController,AliasUtilController}.php`,
`Wireguard/Api/{ClientController,ServiceController}.php`, `IPsec/Api/SessionsController.php`,
`Core/Api/{FirmwareController,ServiceController}.php`, `Diagnostics/Api/SystemController.php`,
`Routes/Api/GatewayController.php`; the ACL files of these modules; the scripts
`wireguard/wg_show.py`, `ipsec/list_status.py`, `filter/rollback_timer.php` and
`routes/gateway_status.php`; `etc/inc/plugins.inc.d/webgui.inc` (certificate).
