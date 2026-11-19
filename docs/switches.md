# Switches: Aruba CX

dcdash manages Aruba CX switches (AOS-CX 10.x, for example the 6000, 6100 and 6200 series):

- **Overview:** inventory and health of each switch on the overview page.
- **Ports:** state, speed, VLANs, LLDP neighbors and PoE of all ports.
- **Port changes:** enable and disable, description, access VLAN, trunk VLANs.
- **VLANs:** create (on several switches at once), rename and delete.
- **Find a device:** search a MAC address on all switches; the port where the device is
  connected comes first.
- **NetBox:** port and VLAN changes are written to NetBox, and ports show their projects.

Every change is saved to the startup configuration ("write memory") right away and recorded
in the audit log. The design and the behavior it's based on are described in
[ADR 0008](adr/0008-aruba-cx.md). dcdash was tested with an Aruba 6000 and firmware 10.08.

```yaml
switches:
  - name: sw-core                      # used in the UI and in permissions (switch:sw-core:...)
    url: https://10.0.0.2
    tls:
      fingerprint: "11:37:4E:...:DA:F4"  # SHA-256 of the switch certificate
    username: dcdash
    password: "file:/etc/dcdash/switch-dcdash.password"
    netbox_device: sw-core             # optional: the switch's device in NetBox
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

## Overview

The "Overview" tab shows all switches at a glance, laid out like their front panels: per
switch two rows (four for a 48-port switch, two more per stack member), ports 1 and 2 in the
first column, 3 and 4 in the second and so on, the SFP ports in the last two columns. Each
port shows ▲ (up) or ▼ (no link or disabled) and its VLAN; the native VLAN of a trunk is
underlined and italic. The first column has the switch name (to its ports) and links to its
web interface (https) and to SSH. Columns a smaller switch doesn't have stay empty.

## Ports

The "Switches" page shows one switch at a time; choose it at the top. The "Ports" tab lists
the physical ports and LAGs:

| Column | Meaning |
|---|---|
| Port | The name, "uplink" for a port to another switch, the LAG a port belongs to |
| State | "Up", "No link" or "Disabled" (shut down), the speed and the PoE power |
| VLANs | "Access" with the VLAN, or "Trunk" with the native VLAN and the allowed VLANs ("all" if not restricted) |
| LLDP neighbor | Name, port and management address of the device at the other end |
| Projects | The projects of the port's NetBox interface (with `netbox_device`) |

A port is an **uplink** if it is a trunk or its LLDP neighbor is a switch (the capability
"Bridge"). The filter searches port names, descriptions, VLANs and neighbors.

## Changing a port

"Change" opens the port dialog. Users with `operate` may only enable and disable the port;
with `write` they may also change the description (up to 64 ASCII characters) and the VLANs:

- **Access:** the port carries one VLAN untagged.
- **Trunk:** the native VLAN is untagged, the allowed VLANs are tagged (for example
  `10, 20-25`). "Allow all VLANs" removes the restriction.

The VLANs of a port in a LAG are set on the LAG. Changing an **uplink** needs an extra
confirmation, because a wrong VLAN or a disabled uplink can cut off the switch or the
network behind it.

A change takes effect immediately. dcdash then saves the configuration, which takes a few
seconds (about 10 seconds on a 6000). If saving fails, the change stays active, and a
message says that it isn't saved; it is lost when the switch restarts. Changes on the same
switch are made one after the other, so that two users don't overwrite each other.

## VLANs

The "VLANs" tab lists the VLANs with the ports that use them. "Create VLAN" creates a VLAN
(ID 2 to 4094, name up to 32 characters) on the chosen switches; the dialog shows the result
for each switch. "Rename" changes the name. "Delete" is offered only for VLANs that no port
uses: dcdash refuses to delete a VLAN in use (it would cut off its ports) and VLAN 1.

## Finding a device

"Find a device" searches the MAC address tables of all switches. Enter at least 4 hex digits;
colons, dashes and dots are ignored, so part of an address is enough. Each hit shows the
switch, port, VLAN and the port description:

- **Connected here:** the MAC address was learned on an access port: the device is connected
  to this port.
- **Behind another switch:** the port is an uplink; the device is somewhere behind it.

Hits on access ports come first. Switches that could not be searched are named above the
results.

## NetBox

With `netbox_device` set and NetBox configured (see [netbox.md](netbox.md)), dcdash
writes changes to NetBox:

| Change | NetBox |
|---|---|
| A port is changed | The interface of the device with the port's name: description, enabled, 802.1Q mode (`access`, `tagged`, `tagged-all`), untagged and tagged VLANs. VLANs that NetBox doesn't know are left out and noted |
| A VLAN is created | The VLAN with this ID (in `netbox.vlan_group` or `netbox.site`, if set) is created with the tag if NetBox doesn't have it |
| A VLAN is renamed or deleted | The NetBox VLAN is renamed or deleted only if dcdash created it (it has the tag). NetBox VLANs are shared by all switches, so others keep their name and stay; the outcome notes it |

dcdash never creates or deletes interfaces of a device: they come from the device type in
NetBox. A failed write-back doesn't undo the change on the switch; it is shown and audited
as `netbox.sync`.

The "Projects" column shows the projects of each port's NetBox interface; "Change projects"
adds the port to projects or removes it (see [projects.md](projects.md)).

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
`port/<interface>` (for example `port/1/1/5`) and `vlan/<id>`:

| Permission | Allows |
|---|---|
| `switch:<switch>:*:read` | Seeing the switch, all ports and VLANs, and searching MAC addresses |
| `switch:<switch>:port/<port>:read` | Seeing a port (and MAC addresses learned on it) |
| `switch:<switch>:port/<port>:operate` | Enabling and disabling the port |
| `switch:<switch>:port/<port>:write` | Changing the description and VLANs of the port |
| `switch:<switch>:vlan/<id>:write` | Creating, renaming and deleting the VLAN |

Examples: `switch:*:port/1/1/*:operate` (enable and disable the ports of the first member of
all switches), `switch:sw-core:vlan/4*:write` (VLANs 4, 40-49 and 400-4094 on sw-core). A
user sees a switch with `read` on any of its resources. Give `write` on uplink ports only
to network administrators. See [security.md](security.md#switch-permissions).

## Audit

| Action | Recorded |
|---|---|
| `switch.port.update` | `target` is `<switch>/<port>`; `details` contain the state before and after, the change, whether the configuration was saved, and the outcome of the NetBox write-back |
| `switch.vlan.create`, `switch.vlan.update`, `switch.vlan.delete` | `target` is `<switch>/vlan/<id>`; `details` contain the ID, the name (and the previous name), whether the configuration was saved, and the NetBox outcome |
