# Permissions

This is the complete list of the permissions a role can contain. Roles map directory groups
to permissions in the `roles` section of the configuration; how roles, the `root` role and
the permission format work is described in [security.md](security.md#roles-and-permissions).

A permission has four segments, `module:instance:resource:action`:

- **module**: `proxmox`, `dns`, `dhcp`, `netbox`, `project`, `switch`, `firewall`, `directory`
  or `system`.
- **instance**: the name of the configured backend (cluster, provider, server, switch,
  firewall, directory, NetBox), the project name for `project`, and always `core` for
  `system`.
- **resource**: an object of the backend, as listed below.
- **action**: `read` < `operate` < `console` < `write` < `admin`. A higher action includes
  the lower ones, so `write` on a zone also allows reading it. `*` as action means `admin`.

`*` matches any text, as a whole segment or within one: `proxmox:lab:qemu/1*:operate` covers
the VMs 1, 10 to 19, 100 to 199 and so on. Matching is case-sensitive. Permissions only grant;
there are no deny rules. A user sees a module's page and a backend in it as soon as they have
any permission on that backend.

In the tables, `<...>` is a placeholder; the rows list the lowest action that allows
something, so each row includes the rows above it for the same resource.

## Proxmox

Instance: the cluster name (`proxmox.clusters[].name`).

| Permission | Allows |
|---|---|
| `proxmox:<cluster>:qemu/<vmid>:read` | Seeing the virtual machine, its details, hardware and tasks; cloning it if it is a template |
| `proxmox:<cluster>:qemu/<vmid>:operate` | Starting, shutting down, rebooting, suspending, resuming and stopping it |
| `proxmox:<cluster>:qemu/<vmid>:console` | Opening its VNC console |
| `proxmox:<cluster>:qemu/<vmid>:write` | Changing its options, disks and network devices |
| `proxmox:<cluster>:qemu/<vmid>:admin` | Creating it (the ID must match), deleting it, deleting its disks |
| `proxmox:<cluster>:lxc/<vmid>:read` | Seeing the container, its details and tasks |
| `proxmox:<cluster>:lxc/<vmid>:operate` | Power actions on the container |
| `proxmox:<cluster>:lxc/<vmid>:console` | Opening its terminal (needs the console user of the cluster) |
| `proxmox:<cluster>:lxc/<vmid>:admin` | Creating and deleting it |
| `proxmox:<cluster>:node/<node>:read` | Seeing the node and its usage, and the tasks of the node that belong to no guest |
| `proxmox:<cluster>:node/<node>:admin` | Opening a root shell on the node |
| `proxmox:<cluster>:storage/<node>/<storage>:read` | Seeing the storage and its usage |
| `proxmox:<cluster>:task/<node>/<task>:read` | Seeing any task, also of guests and nodes the user can't see otherwise (usually `task/*`) |

Proxmox checks the privileges of dcdash's API token in addition; see [proxmox.md](proxmox.md).

## DNS

Instance: the provider name (`dns.providers[].name`). Resource: the zone name without
trailing dot.

| Permission | Allows |
|---|---|
| `dns:<provider>:<zone>:read` | Seeing the zone and its records |
| `dns:<provider>:<zone>:write` | Creating, changing and deleting records of the zone |
| `dns:<provider>:<zone>:admin` | Creating the zone (the name must match) and deleting it |

## DHCP

Instance: the server name (`dhcp.servers[].name`). Resource: the subnet as Kea reports it
(`192.0.2.0/24`, `2001:db8:1::/64`), or `global` for global reservations.

| Permission | Allows |
|---|---|
| `dhcp:<server>:<subnet>:read` | Seeing the subnet, its utilization, leases and reservations |
| `dhcp:<server>:<subnet>:operate` | Deleting leases of the subnet |
| `dhcp:<server>:<subnet>:write` | Adding, changing and deleting reservations of the subnet |
| `dhcp:<server>:global:read` | Seeing the global reservations (valid in all subnets) |
| `dhcp:<server>:global:write` | Adding, changing and deleting global reservations |

## NetBox

Instance: `netbox.name` (`netbox` by default).

| Permission | Allows |
|---|---|
| `netbox:<netbox>:ipam:read` | VLAN names and free addresses in the creation wizard |
| `netbox:<netbox>:sync:read` | The comparison of NetBox with Proxmox, Kea and DNS |
| `netbox:<netbox>:sync:write` | Updating NetBox from the comparison, deleting VMs that exist only in NetBox (with the dcdash tag) |

Writing changes back to NetBox after a change in another module needs no NetBox permission.

## Projects

Instance: the project name. The resource is always `objects`.

| Permission | Allows |
|---|---|
| `project:<project>:objects:read` | Seeing the project and its objects |
| `project:<project>:objects:write` | Assigning objects to the project and removing them (also needs `write` on the object) |

## Switches

Instance: the switch name (`switches[].name`). Resources: `port/<interface>` (for example
`port/1/1/5`) and `vlan/<id>`.

| Permission | Allows |
|---|---|
| `switch:<switch>:port/<interface>:read` | Seeing the port and the MAC addresses learned on it |
| `switch:<switch>:port/<interface>:operate` | Enabling and disabling the port |
| `switch:<switch>:port/<interface>:write` | Changing the description and the VLANs of the port |
| `switch:<switch>:vlan/<id>:write` | Creating, renaming and deleting the VLAN |

Every user with a permission on the switch sees all its VLANs and its inventory.

## Firewalls

Instance: the firewall name (`firewalls[].name`).

| Permission | Allows |
|---|---|
| `firewall:<firewall>:system:read` | Seeing version, gateways and services |
| `firewall:<firewall>:system:operate` | Restarting services |
| `firewall:<firewall>:wireguard/<peer>:read` | Seeing the WireGuard peer |
| `firewall:<firewall>:wireguard/<peer>:operate` | Enabling and disabling the peer |
| `firewall:<firewall>:ipsec/<connection>:read` | Seeing the IPsec tunnel |
| `firewall:<firewall>:ipsec/<connection>:operate` | Restarting the tunnel |
| `firewall:<firewall>:alias/<name>:read` | Seeing the alias |
| `firewall:<firewall>:alias/<name>:write` | Creating, changing and deleting the alias |
| `firewall:<firewall>:rule/<interface>:read` | Seeing the filter rules of the interface |
| `firewall:<firewall>:rule/<interface>:write` | Creating, changing and deleting the rules of the interface (see the lockout guard in [firewalls.md](firewalls.md#lockout-guard)) |

## Directories

Instance: the directory name (`directories[].name`). Resources use the account name
(`sAMAccountName`) in lower case, because matching is case-sensitive and AD names are not:
`directory:lab:user/svc-*:write`. Only users and groups in the containers dcdash manages
(`managed_ous`) can be changed; protected ones (built-in and privileged accounts and
groups, members of privileged groups, and `protected_users` and `protected_groups`) also
need `admin`. The self-service password page needs no permission.

| Permission | Allows |
|---|---|
| `directory:<directory>:user/<name>:read` | Seeing the user |
| `directory:<directory>:user/<name>:operate` | Disabling, enabling and unlocking the user, resetting its password |
| `directory:<directory>:user/<name>:write` | Creating, changing and deleting the user |
| `directory:<directory>:user/<name>:admin` | The above on a protected user |
| `directory:<directory>:group/<name>:read` | Seeing the group and its members |
| `directory:<directory>:group/<name>:write` | Creating, changing (also renaming, which needs `write` on the new name) and deleting the group, adding and removing members |
| `directory:<directory>:group/<name>:admin` | The above on a protected group |

## System

Instance: always `core`.

| Permission | Allows |
|---|---|
| `system:core:audit:read` | Reading the audit log |
| `system:core:sessions:read` | Listing the sessions of all users |
| `system:core:sessions:write` | Terminating the sessions of other users |

Resetting another user's authenticator app is reserved for the `root` role.

## Example roles

```yaml
roles:
  # Read-only view of everything.
  - name: viewer
    groups: [dcdash-viewers]
    permissions: ["*:*:*:read"]

  # Operations: power actions and consoles on all VMs and containers, leases, switch ports
  # on and off, WireGuard peers and IPsec tunnels.
  - name: operator
    groups: [dcdash-operators]
    permissions:
      - "proxmox:*:*:console"
      - "dhcp:*:*:operate"
      - "switch:*:port/*:operate"
      - "firewall:*:*:operate"

  # A team that manages its own VMs (IDs starting with 5), its zone and its project.
  - name: webshop-team
    groups: [team-webshop]
    permissions:
      - "proxmox:lab:qemu/5*:admin"
      - "dns:cloudflare-prod:shop.example.com:write"
      - "project:webshop:objects:write"

  # Network administration.
  - name: network-admin
    groups: [dcdash-network]
    permissions:
      - "switch:*:*:write"
      - "firewall:*:*:write"
      - "netbox:*:*:write"
```
