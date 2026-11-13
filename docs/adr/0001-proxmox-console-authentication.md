# ADR 0001: Authentication of Proxmox consoles

- Status: proposed (the API-token test is pending, see "Open")
- Date: 2026-11-13
- Task: 6.6

## Context

dcdash proxies console connections between the browser and Proxmox, so Proxmox credentials
never reach the browser (concept review 2.7). A console has two steps:

1. `POST /nodes/{node}/qemu/{vmid}/vncproxy` (with `websocket=1`), or `.../lxc/{vmid}/termproxy`,
   or `/nodes/{node}/termproxy` for a node shell. The answer contains a port and a one-time
   `vncticket`.
2. A websocket to `.../vncwebsocket?port=…&vncticket=…` with the subprotocol `binary`, using
   the same authentication as step 1:
   - VNC then speaks RFB, and the vncticket is the VNC password.
   - termproxy first expects the line `<user>:<vncticket>\n` and answers `OK`. After that it
     speaks the termproxy protocol: `0:<len>:<data>` input, `1:<cols>:<rows>:` resize, `2`
     ping.

The go-proxmox library refuses API tokens for this websocket, with the note "Proxmox does not
accept API tokens for vncwebsocket" (see ADR 0002). The question is whether that holds for VNC,
for termproxy, or for both.

## Spike

The spike ran against Proxmox VE 9.0.11 (proxtester2) with a small Go program that performs
only the handshake and sends no console input. The guests were VM 201 and container 202.

| Authentication | VM (vncproxy + RFB) | Container (termproxy) |
|---|---|---|
| Ticket of a Proxmox user (password login) | works: `RFB 003.008` | works: `OK` |
| API token `dashroot@pve!dev` | pending: the token has no ACL yet (403 `VM.Console`) | pending |

Further results:

- termproxy rejects the parameter `websocket=1` (400). Only vncproxy takes it.
- The websocket must be opened on the endpoint (node) that answered step 1, with the same
  authentication.

## Decision (proposed)

- dcdash supports both modes. By default, consoles use the API token. Optionally, a separate
  Proxmox user with a password (`proxmox.clusters[].console`) is used for consoles; dcdash logs
  it in with a ticket, which it renews after 90 minutes.
- dcdash itself performs the termproxy login on the server side. So the browser never sees the
  vncticket of a terminal.
- For VNC, the browser gets the vncticket as the VNC password, because noVNC performs the RFB
  authentication. This is acceptable, because the vncticket is bound to a single prepared
  console (port). It is also only usable through the authenticated `vncwebsocket`, which the
  browser can only reach through dcdash with its own one-time console ticket.

## Open

- Repeat the spike with the API token once it has `VM.Console` on test guests (integration test
  `TestProxmoxConsoles`, mode "token"). If the token works for both kinds, the console user stays
  optional. Otherwise the documentation states for which kinds the console user is required.
