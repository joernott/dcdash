# ADR 0002: Own Proxmox API client instead of go-proxmox

- Status: accepted
- Date: 2026-11-13
- Task: 6.2

## Context

The concept names `luthermonson/go-proxmox` (Apache-2.0) as the Proxmox library. dcdash needs
a small part of the Proxmox API in v0.1.0:

- `/version`;
- `/cluster/resources`;
- guest `status/current`, `config` and `status/{action}`;
- task status and log;
- `vncproxy`, `termproxy` and `vncwebsocket`;
- in v0.1.1, cloning, creating and deleting guests.

Requirements:

- several endpoints per cluster, with failover;
- certificate pinning per endpoint;
- precise errors for the UI and the audit log;
- consoles proxied through dcdash.

Version 0.8.2 of go-proxmox was evaluated against these requirements:

1. **Error handling.**
   - Only 400, 401, 403, 500 and 501 are treated as errors. Any other status is decoded as
     success. For example, `595 no route to host` (pveproxy cannot reach the target node)
     yields an empty result without an error.
   - 401 and 403 are merged into one error, and 500 loses its body.
2. **Consoles.** `VNCWebSocket` and `TermWebSocket` refuse API tokens and require a password
   login. dcdash has to proxy the websocket between the browser and Proxmox anyway, so it
   needs its own websocket code.
3. **Privileges.** The typed navigation (`client.Node()`, then `node.VirtualMachine()`)
   requests `/nodes/{node}/status` first. That needs `Sys.Audit`, which a token for power
   actions does not need. So the typed API either fails or demands more privileges than
   necessary.
4. **Dependencies.** It pulls in `go-diskfs`, `goterm`, `copier` and `magefile/mage`, plus
   `gorilla/websocket` v1.4.2 from 2020. None of these are needed for the calls above.

## Decision

dcdash uses its own small client, `internal/proxmox`, built on `net/http` and
`gorilla/websocket`:

- **Errors.** Every non-200 response becomes a `proxmox.Error`, which keeps the status, the
  message from the reason phrase or body, and parameter errors.
- **Failover.** Only connection errors, TLS errors, timeouts and 502/503/504 from a reverse
  proxy switch to the next endpoint. API errors don't, so a wrong password or token is never
  retried on other nodes. The working endpoint is remembered.
- **TLS.** TLS uses `internal/tlsutil`: a CA file for the cluster CA, or a SHA-256 pin per
  endpoint.
- **Types.** Only the fields dcdash shows are typed. Guest status and configuration are passed
  through as JSON objects, without secrets.
- **Tests.** `internal/proxmox/pvefake` is an in-memory fake of the API, including consoles.
  The unit tests and the end-to-end tests use it.

## Consequences

- dcdash maintains about 400 lines of client code instead of a dependency.
- New API calls, such as the provisioning in Sprint 10, are added to `internal/proxmox` as
  needed.
- The decision should be revisited if the provisioning API turns out to be large. At that
  point, go-proxmox's typed request structures could be used for those calls only.
