# User guide

dcdash brings the Proxmox VE clusters, DNS zones and DHCP servers of a data center into one
web interface. What you see and what you can change depends on your roles; the administrators
assign them through groups in the directory. Everything you change is recorded in the audit
log with your name.

## Logging in

Log in with your directory user name and password. Depending on your roles, dcdash then asks
for a code from an authenticator app (TOTP), such as Aegis, FreeOTP, Google Authenticator or
Microsoft Authenticator.

**The first time**, dcdash shows a QR code. Scan it with the app (or type in the key shown
under "Show the key for manual entry") and enter the 6-digit code the app shows. dcdash then
shows 10 **recovery codes**, only this once. Print them or keep them in a password manager:
each one works once, instead of a code from the app, if you don't have your phone.

After several wrong passwords or codes, dcdash refuses further attempts for a while (default
15 minutes). This protects your directory account from being locked by the domain.

A session ends after 8 hours, or after 30 minutes without activity (your administrator may
have set other times). An open console doesn't count as activity.

**Lost your phone?** Log in with a recovery code, then set up the app again on the account
page. If you have no recovery codes left, ask an administrator to reset your authenticator
app; you set it up again at your next login.

## Overview

The overview shows the state of dcdash and of every connected system, one tile each: first
dcdash itself (database, user directory, certificate, projects), then each Proxmox cluster,
DNS provider, DHCP server, NetBox, switch and firewall. The tiles of the systems have the
system's color (Proxmox purple, bind9 blue, Cloudflare orange, Kea green, NetBox teal,
switches cyan, OPNsense red), and the bottom line names the system and the response time of
the check. A tile with a red border names the problem. The page refreshes every 30 seconds.

If the configuration names a system's own interfaces, its tile shows buttons left of the
status: **UI** opens the web interface in a new tab, **SSH** the SSH client registered in
your operating system, and **RDP** downloads a Remote Desktop file that your RDP client
opens (see [configuration.md](configuration.md#general-rules)).

The **Documentation** link in the top bar opens this documentation inside dcdash, in the
version that matches the running release. It is available in English only.

## Your account and settings

Open the menu with your name in the top right corner:

- **Language:** English or German. **Theme:** light, dark, or the setting of your system.
  dcdash stores both for your user, so they apply in every browser.
- **My account** shows your roles and groups, when your session ends, and the state of your
  authenticator app. There you can:
  - set up the app voluntarily, if your roles don't require it. From then on, it's required
    at every login.
  - get new recovery codes. You need a current code from the app; the old recovery codes stop
    working.
- **Log out** ends the session on the server.

## Proxmox VE

The Proxmox page shows the nodes of a cluster with their load, and the virtual machines (VMs)
and containers with state, resources and tags. Search by name, ID or tag. With more than one
cluster, choose it at the top.

- **Power actions:** start, shut down (asks the guest operating system), stop (immediately,
  like pulling the plug), reboot, suspend and resume. dcdash asks for confirmation and
  follows the Proxmox task until it's done.
- **Details:** click a guest's name for its configuration, current usage and recent tasks.
- **Console:** VMs open a graphical console (VNC) in the browser. The toolbar sends
  Ctrl+Alt+Del and switches to full screen. Containers open a terminal.
- **Node shell:** a root shell on a node. It requires administrator rights on the node.
- **Create:** if your administrator allows it for the cluster, "Create" opens a wizard for
  a virtual machine from a template, a virtual machine from an ISO image, or a container. You
  choose the name, the resources, one or more network devices and, for templates and containers, the login
  (password or SSH keys) and IP settings. Leave the ID empty to get the next free one. After
  If you may add DHCP reservations or DNS records, two more steps add them for the new
  guest.
  "Create", the page shows the progress of each step; it continues if you close the page.
  If a step fails, check the guest and delete it if necessary.
- **Delete:** on the guest's page, for stopped guests. You type the name to confirm. The
  guest and its disks are deleted; this can't be undone.
- **Hardware:** on the page of a VM, change its options (for example memory, cores, boot
  order, guest agent), add, grow, detach, attach and delete disks, and add, change and remove
  network devices, if you may change the VM. Changes Proxmox can't apply to a running VM are
  marked as pending and take effect when the VM is started again. See
  [proxmox.md](proxmox.md#changing-virtual-machines).

## DNS

The DNS page lists the zones of a provider (Cloudflare or bind9). Click a zone for its
records.

- **Records:** add, change and delete A, AAAA, CNAME, MX, TXT, SRV, PTR, NS and CAA records.
  Other types, such as SOA, are shown read-only. Names are relative to the zone ("www" for
  www.example.org, "@" for the zone itself). Content uses the zone file format, for example
  `10 mail.example.org.` for MX; a name in the content ends with a dot. For Cloudflare you
  can also set "proxied" and a comment.
- **Concurrent changes:** if someone else changed the zone or the record since you loaded it,
  dcdash refuses your change and reloads the records. Check them and try again.
- **Zones:** with the permission to administer zones, you can create and delete zones. To
  delete one, you type its name to confirm. For Cloudflare, dcdash shows the name servers the
  domain must be delegated to.

## DHCP

The DHCP page shows one Kea server at a time; choose it at the top. The server card shows
its version, whether it belongs to an HA pair, and the state of the pair.

- **Subnets:** each subnet with its pools and how many addresses are in use.
- **Leases:** "Show leases" lists the leases of a subnet. To find a lease in any subnet,
  search by IP address, MAC address, client ID (DHCPv4), DUID (DHCPv6) or host name.
  Deleting a lease frees the address; a client that still uses it gets a new lease when it
  renews. On an HA pair, dcdash deletes the lease on both servers.
- **Reservations:** add, change and delete fixed addresses for a client, identified by MAC
  address, client ID or DUID. Reservations from Kea's configuration file are marked
  read-only and can only be changed there.

## NetBox

If NetBox is connected, dcdash keeps it up to date: creating, changing and deleting guests,
DHCP reservations and DNS `A`/`AAAA` records are written to NetBox. If that fails, the change
in dcdash is still made, and an error message tells you that NetBox could not be updated.

In the creation wizard, the VLAN field shows the name of the VLAN in NetBox, and with static
addresses a button offers a free address from the VLAN's prefixes ("Use the free address ...
from NetBox"). The address is only taken when the guest is created.

The NetBox page compares a Proxmox cluster with NetBox. By default it lists only guests that
differ, are missing in NetBox or exist only there; "Only differences" switches to all guests.
"Update NetBox" writes a guest's current state to NetBox, "Delete from NetBox" removes a
virtual machine that exists only in NetBox (only those dcdash created). "Compare" checks the
DHCP reservations and DNS records against the addresses in NetBox. See
[netbox.md](netbox.md).

## Projects

A project groups what belongs together: guests, addresses, DHCP reservations, DNS names,
VLANs, prefixes, IP ranges and switch ports. Projects are defined by the administrators.

- **Assigning:** choose projects in the last step of the creation wizard, with "Change
  projects" on the page of a guest, or in the dialogs of DHCP reservations and DNS records.
  You can only assign projects you have permission for.
  Switch ports get projects on the Switches page.
- **Projects page:** "Projects" in the menu lists your projects. The page of a project shows
  its guests and its objects in NetBox. "Add NetBox objects" searches NetBox for VLANs,
  prefixes, addresses, ranges and switch ports and adds them; "Remove" takes them out of the
  project again.

See [projects.md](projects.md).

## Switches

The Switches page starts with an **overview** of all switches, laid out like their front
panels: ▲ for ports that are up, ▼ for the others, with the VLAN below; the switch name
leads to its ports, "https" and "ssh" to the switch itself. The other tabs show one switch at
a time with its model, firmware and uptime:

- **Ports:** state, speed, VLANs, LLDP neighbor and projects of each port; the filter
  searches names, descriptions, VLANs and neighbors. "Change" opens a dialog to enable or
  disable the port and change its description and VLANs (access or trunk). Uplinks to other
  switches need an extra confirmation.
- **VLANs:** the VLANs with the ports that use them. Create a VLAN on one or several switches,
  rename it, or delete it if no port uses it.
- **Find a device:** enter (part of) a MAC address. "Connected here" marks the port the
  device is plugged into; "Behind another switch" marks uplinks.

Changes take effect immediately and are saved to the startup configuration, which takes a
few seconds. See [switches.md](switches.md).

## Firewalls

The Firewalls page shows one firewall at a time with its version, uptime and memory, and
links to its web interface and SSH:

- **Health:** the gateways and services; "Restart" restarts a service.
- **VPN:** WireGuard peers with their last handshake and traffic ("Enable", "Disable"), and
  IPsec tunnels ("Restart").
- **Aliases:** create, change and delete aliases; the content has one entry per line.
- **Rules:** the filter rules, optionally of one interface. "Apply" in the rule dialog makes
  the change with a savepoint: if dcdash can't reach the firewall afterwards, OPNsense
  restores the previous rules within a minute, and the page tells you.

See [firewalls.md](firewalls.md).

## Administration

Users with the corresponding permissions see two more pages:

- **Audit log:** every login and every change, newest first. Filter by user, action (exactly,
  or a prefix ending with `*`, for example `dns.*`), target, result and time. "Details" shows
  what was changed.
- **Sessions:** the active sessions with user, state, client address and last activity.
  Terminate a single session or all sessions of a user. **Reset authenticator app** is for
  users who lost their phone and their recovery codes; it also ends all their sessions.

## When something is refused

- **"You don't have permission to do this":** your roles don't include this action. Buttons
  for actions you can't use are usually hidden; ask an administrator if you need more rights.
- **"The data was changed in the meantime":** see [Concurrent changes](#dns).
- **A backend is unavailable:** the overview shows which one. Most pages say so as well;
  the other modules keep working.
