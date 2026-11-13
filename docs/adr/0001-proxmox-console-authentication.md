# ADR 0001: Authentication of Proxmox consoles

- Status: accepted
- Date: 2026-11-13
- Task: 6.6

## Context

dcdash proxies console connections between the browser and Proxmox, so Proxmox credentials
never reach the browser (concept review 2.7). A console has two steps:

1. `POST /nodes/{node}/qemu/{vmid}/vncproxy` (with `websocket=1`), or `.../lxc/{vmid}/termproxy`,
   or `/nodes/{node}/termproxy` for a node shell. The answer contains a port, a one-time
   `vncticket` and the user the ticket was issued to.
2. A websocket to `.../vncwebsocket?port=…&vncticket=…` with the subprotocol `binary`, using
   the same authentication as step 1:
   - VNC then speaks RFB. Proxmox offers only "VNC authentication", and the vncticket is the
     VNC password.
   - termproxy first expects the line `<user>:<vncticket>\n` and answers `OK`. After that it
     speaks the termproxy protocol: `0:<len>:<data>` input, `1:<cols>:<rows>:` resize, `2`
     ping.

The go-proxmox library refuses API tokens for this websocket, with the note "Proxmox does not
accept API tokens for vncwebsocket" (see ADR 0002). The question is whether that holds for VNC,
for termproxy, or for both.

## Spike

The spike ran against Proxmox VE 9.0.11 (proxtester2), first with a small Go program, then with
the integration test `TestProxmoxPowerActions` and the dcdash client. The guests were VM 110 and
container 122, which the user set aside for tests (the API token has `PVEVMUser` only on them).

| Authentication | VM (vncproxy + RFB) | Container (termproxy) |
|---|---|---|
| Ticket of a Proxmox user (password login) | works | works: `OK`, then the login prompt |
| API token `dashroot@pve!dev` | works | fails: Proxmox closes the websocket without an answer |

Further results:

- With the API token, termproxy issues a ticket for `dashroot@pve!dev`, but rejects the login
  line with it. The login also fails with `dashroot@pve` and with the user returned by
  termproxy. So the termproxy login can't verify tickets issued to API tokens.
- termproxy rejects the parameter `websocket=1` (400). Only vncproxy takes it.
- The websocket must be opened on the endpoint (node) that answered step 1, with the same
  authentication.
- For VNC, Proxmox offers only security type 2 (VNC authentication), with both kinds of
  authentication.

## Decision

- VNC consoles of VMs use the **API token**. Nothing else needs to be configured.
- Container terminals and node shells use a **console user**: a separate Proxmox user with a
  password (`proxmox.clusters[].console`). dcdash logs it in with a ticket, which it renews
  after 90 minutes. Without a console user, dcdash doesn't offer terminals: the cluster list
  reports `terminals: false`, the interface hides the buttons, and the API answers 409
  `terminal_unavailable`.
- dcdash itself performs the termproxy login on the server side. So the browser never sees the
  vncticket of a terminal.
- For VNC, the browser gets the vncticket as the VNC password, because noVNC performs the RFB
  authentication. This is acceptable, because the vncticket is bound to a single prepared
  console (port). It is also only usable through the authenticated `vncwebsocket`, which the
  browser can only reach through dcdash with its own one-time console ticket.

## Consequences

- The console user needs only `VM.Console` on the containers (for example `PVEVMUser` on a pool
  or on `/vms`) and `Sys.Console` on the nodes if node shells are wanted. Its password is a
  second secret per cluster.
- Proxmox checks the console user's privileges, not the token's. dcdash checks its own
  `console` and `admin` permissions before it opens a terminal, so the console user doesn't
  widen what a dcdash user can do. It does mean that the token's privileges don't limit
  terminals.
- If a later Proxmox version accepts tokens for termproxy, `Cluster.consoleAuth` is the only
  place to change.
