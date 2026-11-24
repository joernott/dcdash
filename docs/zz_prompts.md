# Prompt Log

All prompts given to the AI assistant during development of dcdash, in chronological order.
Each entry records the date and the sprint it belongs to.

---

## 2026-11-12 — Sprint 0 (Planning)

dcdash is a new project to create a unified dashboard to manage central services in a data center.
I want you to review it's concept and create an implementation plan for it. That plan contains sprints and each sprint consists of multiple tasks. Compleded tasks will be marked with a checkmark in the implementation plan. All of my prompts should be stored in a file docs/zz_prompts.md and each sprint should be developed in it's own branch, using one or more commits per task in a sprint. After completing a sprint, it's branch will be squash merged into main and the next sprint branch will be forked from main. The sprint branch will remain for documentation purpose. If you need to add a task during implementation, it will be added as subtask to that task,  egg. Task 1 in Sprint 1 has the number 1.1, if you find out, that another task is needed to finish 1.1, a subtask 1.1.1 will be added. The implementation plan will be amended.

---

## 2026-11-12 — Sprint 0 (Planning)

2.7 VM and container creation must be part of v0.1.1
2.10 Use zerolog for logging

Answers to your questions:
Q1: Yes
Q2: Configurable per role
Q3: Yes, it is needed
Q4: Zone creation and deletion is needed
Q5: Yes. Additional hooks can be installed as well, if needed
Q6: Yes
Q7: English and German for now
Q8: Yes

One additional feature I almost missed, that is always requested by users is the support for a dark mode

---

## 2026-11-12 — Sprint 0 (Planning)

DCDash will copy zone files to the primary DNS server via SFTP. We can't guarantee its installation on the primary BIND server

---

## 2026-11-12 — Sprint 0 (Planning)

yes, please go ahead

---

## 2026-11-12 — Sprint 1 (Foundation)

I set up a dedicated development vm for you: [REDACTED] where you have a user "claude" with password "[REDACTED]" and sudo rights, so you can install software as needed. You can use ssh using the ssh key /c/Users/[REDACTED]/.ssh/id_ed25519_claude to connect to it. You can install whatever software you need on that. I've also added that key as a deploy key with write access to github.

---

## 2026-11-12 — Sprint 1 (Foundation)

GPL-3.0-or-later is fine with me. There will be no need to install samba as we have a samba server available in the network. I also can scale up the VM as needed.

---

## 2026-11-12 — Sprint 1 (Foundation)

Please squas-merge sprint 1 into main, push theit and start with sprint 2

---

## 2026-11-12 — Sprint 2 (TLS)

Please continue with the next sprint. You can use the ldap bind user "[REDACTED]" with password "[REDACTED]" to connect to the domain controller "ldaps://[REDACTED]:636"

---

## 2026-11-13 — Sprint 3 (Authentication)

yes, please

---

## 2026-11-13 — Sprint 4 (TOTP, roles, audit)

I have created the dashroot group in the AD and added a user dashtest with password "[REDACTED]" to that group. This should enable you to run the tests. After that, squash merge sprint 4 into main and start with sprint 5

---

## 2026-11-13 — Sprint 5 (Frontend shell)

please continue

---

## 2026-11-13 — Sprint 5 (Frontend shell)

please continue

---

## 2026-11-13 — Sprint 6 (Proxmox)

You do have sudo on the VM, use the password "[REDACTED]". You can install whatever you need on the vm as its only purpose is to develop this app. Please install the test browser and notify me if you need assistance. Afterwards, please continue with Sprint 6.
I have created a user "dashroot" with password "[REDACTED]" on the Proxmox server https://[REDACTED]:8006 and created the API token "[REDACTED]" for that user. I've granted PVEVMUser rights on / to the users group.

---

## 2026-11-13 — Sprint 6 (Proxmox)

I ran the above commands.
The VM with ID 120 is your dev VM, and ID 110 is a currently unused VM (by default powered off) that can be used for tests when running the application on the dev  VM that are supposed to start/stop/suspend VMs. I have granted you permissions for both of them with "pveum acl modify /vms/<vmid> --tokens 'dashroot@pve!dev' --roles PVEVMUser"

---

## 2026-11-13 — Sprint 6 (Proxmox)

I did the requested acl modification and created a container with ID 122 for you to test. It has the user dashroot with password "[REDACTED]" and I've added your SSH key. I granted you the same rights as for the two VMs

---

## 2026-11-13 — Sprint 6 (Proxmox)

yes, please do so

---

## 2026-11-13 — Sprint 7 (DNS)

ok, go ahead

---

## 2026-11-16 — Sprint 7 (DNS)

The VM has been resized (disk and RAM) and restarted

---

## 2026-11-16 — Sprint 7 (DNS)

please squash merge sprint 7 into main and continue with sprint 8

---

## 2026-11-16 — Sprint 8 (DHCP)

please squash merge sprint 8 into main and start with sprint 9

---

## 2026-11-17 — Sprint 9 (Hardening and Release v0.1.0)

To answer your questions:
1. I'be reviewed the German translations and found one minor detail that I fixed.
2. Use Jörn Ott as person and Ott-Consult UG as company
3. From my experience, these were made public for the last images pushed there, After they have been pushed, I can check and make them public
4. Yes, please go ahead

---

## 2026-11-17 — Sprint 10 (Proxmox Provisioning)

dcdash shows up in https://github.com/joernott?tab=packages&visibility=public, so I assume it is visible. Please continue with sprint 10

---

## 2026-11-17 — Sprint 10 (Proxmox Provisioning)

The shell snippet in the chapter "Privileges of the API token" didn't work, your user is named dashroot and the token is named dev. also, having a ! in a double quoted string breaks on the standard bash of proxmox, I replaced dcdash with dashroot and the token name with dev and turned the strings into single quoted strings. That has worked now, but I recommend to modify the script into six individual commands and use two variables for who and for the token name. the for loop does not save many lines

You can create and delete test VMs there, using the ID range >1000.

---

## 2026-11-17 — Sprint 10 (Proxmox Provisioning)

yes, please go ahead

---

## 2026-11-17 — Sprint 10 (Proxmox Provisioning)

the workflow finished

---

## 2026-11-18 — Sprint 11 (LDAP Failover, VM Configuration and UI Fixes)

I have deployed dcdash on a dedicated VM an configured it our test network now. The LDAP login fails with
Nov 18 11:10:40 dcdash dcdash[5852]: {"level":"error","req_id":"dbunj85eg6itak5qqj50","ip":"[REDACTED]","component":"auth","username":"[REDACTED]","client_ip":"[REDACTED]","error":"directory unavailable: connect to ldaps://[REDACTED]:636: LDAP Result Code 200 \"Network Error\": tls: failed to verify certificate: x509: certificate relies on legacy Common Name field, use SANs instead","time":"2026-11-18T11:10:40.099732619+01:00","message":"login failed: directory unavailable"}

As this is the certificate, SAMBA creates for itself, not using the SAN is a quirk we need to be able to work with. Is there a way to get this working?

---

## 2026-11-18 — Sprint 11 (LDAP Failover, VM Configuration and UI Fixes)

For the next sprint, please add the folowing tasks:
- add support for multiple LDAP URLs for failover
- Integrate the documentation as a separate menu "Documentation" in the top bar
- The Width of the column "Actions" in the German translation is insufficient to display all buttons in one line, the same goes for the "Usage" column. The latter can be fixed by changing the translation from "Arbeitsspeicher" to "RAM", but for the buttons, I'd like to have a wider column. Currently, the dashboard is centerd in the browser and wastes a lot of real estate of the 1920 wide screen. Please make it wider and add around 20% more space to the actions column.
- When viewing a VM, I would like to reconfigure Settings like "agent", boot", etc. I would also like to be able to resize, detach, attach, create and delete disks as well as add, edit and remove network devices
- Also, I seem to get kicked out after a few minutes of inactivity. I would prefer to stay logged in for 8 hours
- When looking at the DNS zone, the Edit and Delete buttons don't fit into the Actions column in both languages, that should be fixed, too
- On the DHCP-Page, remove the status box for the dhcp server and move the server selection under the DHCP headline

---

## 2026-11-18 — Sprint 11 (LDAP Failover, VM Configuration and UI Fixes)

Please redact the prompts before pushing. 
When creating a VM, on tab 3 "Network and login", there should be the option to add multiple nics
When creating a VM, after tab 3, the user should have tabs for adding DHCP reservations (IPv4 and v6) and adding  DNS names.

---

## 2026-11-18 — Sprint 11 (LDAP Failover, VM Configuration and UI Fixes)

redact these names anyway. Sprint 11 should end with v0.1.1 as we are just polishing v0.1.0.
Sprint 12 will then be a new minor version 0.2.0. In that sprint, I want to:
- add support for netbox as common data source. Changes made in dcdash should update the respective entries in netbox
- add Support for Aruba CX 6x00 switches: Configuring VLANs, ports, finding devices by MAC across multiple switches, get an overview over port statuses across multiple switches
- add support for OPNSense firewall: health check, VPN status check, en-/disabling Wireguard peers, restarting IPSEC tunnels, Managing aliases and rules

---

## 2026-11-18 — Sprint 11 (LDAP Failover, VM Configuration and UI Fixes)

please split sprint 12 into one sprint per application, push the branch and start with sprint 11. There is no need to rewrite main and "dashroot" can remain in the prompts.

---

## 2026-11-19 — Sprint 11 (LDAP Failover, VM Configuration and UI Fixes)

yes, please go ahead

---

## 2026-11-19 — Sprint 12 (NetBox)

start with sprint 12

---

## 2026-11-19 — Sprint 12 (NetBox)

I have a production Netbox at netbox.thse.net but we have to be careful to not damage existing data. What do you neef from me to access those tests?
Before you squash merge the branch, I have an additional task for netbox to support tag management in Netbox. We will use tags to group various items that belong to the same Project

---

## 2026-11-19 — Sprint 12 (NetBox)

Answers:
1. Yes, use project- as a prefix
2. Yes, dcdash should manage tags and synchronize them to Netbox 
3. Yes to all three, but also switch ports and IP addresses/ranges
4. Yes, they should also show up in proxmox
5. Yes to all.
6. Ideally, we will have a folder /etc/dcdash/projects where a yaml file for every project exists and we can define metadata for it. At the moment, that will be the tag name and color, later on, we might have more informations in that file. A page listing all projects and a page per project, allowing access to all related objects.

---

## 2026-11-19 — Sprint 12 (NetBox)

To answer your open question: Solution 1 would be my preferred solution.
And yes, merge sprint 12 and start sprint 13

---

## 2026-11-19 — Sprint 13 (Aruba CX)

The dump is on the dev VM in the file /home/claude/netbox_prod.dump. We are using NetBox Community v4.7.5-Docker-5.1.1 

To answer your questionsfor the aruba switches:
1: 

```
DCDASH-SW33(config)# show version
-----------------------------------------------------------------------------
ArubaOS-CX
(c) Copyright 2017-2021 Hewlett Packard Enterprise Development LP
-----------------------------------------------------------------------------
Version      : PL.10.08.1010
Build Date   : 2021-09-21 14:53:08 UTC
Build ID     : ArubaOS-CX:PL.10.08.1010:51920eb359ca:202109211429
Build SHA    : 51920eb359caf6584f995926dea7a99242bab059
Active Image : primary

Service OS Version : PL.01.09.0003
BIOS Version       : PL.02.0002
```

2. 10.15.34.33. Currently there are no variables for the switch config. I have added DCDASH_IT_ARUBA_USER containing the user name and DCDASH_IT_ARUBA_PASSWORD containing the password to dcdash-it.env
3. You can use all ports except for port 1/1/1 which is the upstream port and VLANS in the range >=4000
4. Yes, you can run write memory, this switch is exclusively for you

I have configured the firewall to allow https access from your dev machine to the switch network

---

## 2026-11-19 — Sprint 13 (Aruba CX)

We will add the mac addresses in netbox to facilitate the matching. Please keep the database dump for testing: Please continue with the write test

---

## 2026-11-19 — Sprint 13 (Aruba CX)

yes, continue with 13.2

---

## 2026-11-19 — Sprint 13 (Aruba CX)

yes, continue with the rest of sprint 13 until you need input from me

---

## 2026-11-19 — Sprint 13 (Aruba CX)

1: yes, merge sprint 13 and start sprint 14. Please complete as many tasks as possible without a real OPNSense. Tomorrow I will try to provide you with one just for testing. We are mostly using OPNsense 26.4.2p4 or 26.4.2p5 versions.
2: yes, please. Ideally a compact view with one column spanning 2 rows containing the switch name, a https and a ssh link to it and then 12 columns where you have port1 in the first row, port 2 below, then in the next column port 3 and 4 and so on and then 2 columns at the end with the sfp-ports 49-52. Each cell just shows either a green UP or red DOWN arrow and the vlan number below. If a switch has less than 24+4 ports, some columns are left blank. If it is one of the more rare 48 port switches, it'll take 4 rows.
3: Please create the switch in the copy as it is only here for testing and won't be used in that configuration in the data center.

---

## 2026-11-20 — Sprint 14 (OPNsense)

1: Key and secret are set
2: The firewall has the IP 10.15.34.34
3: Whatever you need, the firewall is a test device not used for anything except dcdash tests
  There is only one peer for Wireguard: dcdashtest" and one IPSEC tunnel (currently not connected, I need to set up a peer for it)
you can use the test_wan_ipv6_test (opt1) interface and WAN1 (opt2) for tests
4. Yes, the firewall is exclusively for you

---

## 2026-11-20 — Sprint 14 (OPNsense)

Regarding 14.8.2: We will update the current firewalls to newer versions. Therefore please thest with the current device

---

## 2026-11-20 — Sprint 14 (OPNsense)

As the dcdash user is a member of "admins", I've granted those privileges to the "admins" group.

---

## 2026-11-20 — Sprint 14 (OPNsense)

yes, merge sprint 14 and tag v0.2.0

---

## 2026-11-20 — after Sprint 14 (project files)

my downloads folder contains a screenshot "tags.png" Please create project files for the projects using the "Name" as project name and the color in the "Color" column for the colors defined in the project files. Generate the file names from the Name column but change everything to lower letters and replace spaces by underscores

---

## 2026-11-20 — after Sprint 14 (project files)

these tags don't exist in netbox yet

---

## 2026-11-20 — after Sprint 14 (NetBox token)

I copied the token generated by netbox into the netbox.token file. The coped text starts with "Bearer" and I get complaints in the dcdash log about an invalid token. Do I need to remove "Bearer " from the file?

---

## 2026-11-20 — Sprint 15 (planning)

no need for that. Please add sprint 15 and add the following todos to it:
- On the Overview page: Make the boxes less wide. currently, 3 boxes fit next to each other, change that to 6
- Add a setting "UI", "SSH", "RDP" to everything that produces a tile on the overview. If one of those keys exists and is not empty, a small button should appear left of the status element on that tile, similar to what we have for the switches already

---

## 2026-11-23 — Sprint 15 (planning)

I have more things to add to that sprint first:
- I want to use colors for the different supportet elements. These should be used on the overview page, which allows us to get rid of the headlines: I will give you my suggestions for the base colors used for text and lines and you can derive a transparent background color from it that provides enough contrast. Please also check, whether these colors provide enough contrast in bright mode as well as in dark mode and suggest improvements.
  - Keep the color scheme for the dcdash tiles
  - Use #008037 for Kea DHCP
  - Use #0066CC for Bind
  - Use #F48120 for cloudflare
  - Use #00857D for netbox
  - Use #D302D4 for Proxmox
  - Use #90E5E6 for Switches
  - Use #E34A21 for OpnSense
- Highlight the rows in the DNS zones in the color of either Clodflare or Bind9
- Make sure, we embed all resources like fonts etc. into the static binary
- I want to use more icons instead of texts in buttons. Please use the fontawesome icons for that:
  - On the proxmox overview use https://fontawesome.com/icons/classic/solid/power-off in red for the "Shutdown" and green for "Start",  Use https://fontawesome.com/icons/classic/solid/arrow-rotate-left for Reboot, https://fontawesome.com/icons/classic/regular/moon for suspend, https://fontawesome.com/icons/classic/solid/bolt-lightning for stop and https://fontawesome.com/icons/classic/solid/display for console
  - On a VM detail page, please use the same icons
  - Use  https://fontawesome.com/icons/classic/regular/trash-can for delete everywhere
  - On a console page, use the same icon proxmox uses for ctrl-alt-delete (2 smal squares at the bottom half of the icon, centered and one small square in the top half, slightly more to the right, mimicing the locations of the ctrl, alt and delete keys on a keyboard
  - Use https://fontawesome.com/icons/classic/solid/arrows-rotate for refresh everywhere
  - On the zone page, use the same icon fro refresh and https://fontawesome.com/icons/classic/solid/plus for "Add record". Use https://fontawesome.com/icons/classic/solid/pencil for edit and the trashcan for delete
  - On the dhcp page use https://fontawesome.com/icons/classic/solid/bookmark for Show leases
  - Use https://fontawesome.com/icons/classic/solid/magnifying-glass for search
- On the netbox page keep the "Update netbox" and "Compare" button as text
- On the firewall page use the same "Restart" as for the VMs and the Power button for "Disable"/"Enable", To create rules and aliases, use the plus sign, use the pencil for edit and trashcan for delete
- Keep the text buttons for apply and reset on the audit log

In the documentation, create a complete list of permissions that can be defined for roles

---

## 2026-11-23 — Sprint 15 (planning)

use your suggested colors.
Also add a task to create a dependabot.yml file for the project

---

## 2026-11-23 — Sprint 15 (Overview, colors and icons)

yes, start with sprint 15

---

## 2026-11-23 — Sprint 15 (Overview, colors and icons)

There is one more issue we should address in sprint 15. I have noticed that the kea tiles on the overview show 408 errors once in a while. Please add a routine that waits for a configurable timeout (default 1s) and then checks the status again if any of those status icons return a 4xx response before showing the final result.. Then go ahead squash merge the sprint and release v0.2.1

---

## 2026-11-23 — after Sprint 15 (Dependabot)

yes, go through the dependabot PRs starting with #1

---

## 2026-11-23 — after Sprint 15 (Dependabot)

yes, merge it with a merge commit and push

---

## 2026-11-23 — after Sprint 15 (maintenance)

clean up the disk on the dev VM

---

## 2026-11-23 — Sprint 16 (planning)

sprint 16 will have active directory and samba support as a focus. As Samba does not provide an API to interact with external tools but has samba-tool on the samba AD server, many applications build wrappers around that. Please also consider creating an agent that runs on the samba domain controller as a possible alternative.

We currently have two scenarios we need to support: A Windows domain running on a Windows Server 2019 domain controller in our airgapped network and two up-to-date samba active directory servers running in the two test labs.

I want to implement the following features:
- Managing DNS zones and records
- Managing Users and groups (creating, locking, deleting users, reset passwords for users, managing group membership, creating, modifying and deleting groups)
- Offer a "change password" via LDAP feature similar to what https://github.com/ltb-project/self-service-password offers, that is available for every user in one of the configured active directories
- groups can be associated with projects in the config files and show on a project page

---

## 2026-11-23 — Sprint 16 (Active Directory and Samba)

1. dcdash currently uses one of the Samba DCs (10.15.44.3) for its LDAP authentication, The user and password  for the bind user are already in the config. I have added DCDASH_IT_AD_ADMIN and DCDASH_IT_AD_ADMIN_PASSWORD with the administrator user. You can use all existing OUs on the server and work in the windows.test.sf.thse.net domain as long as you keep existing users/groups and DNS records as they are. Use "dcdashdev" as prefix andthere should not be any collisions.
2. Yes
3. At the moment, we don't have a windows DC available that is reachable by you
4. An agent is acceptable in both envirnments. dcdash will be deployed in the airgapped network on a server that can reach the AD server in that network.
5. The self service page should be reachable without loggin in to dcdash. Our firewall takes care that only internal users from internal networks can reach dcdash anyway.
6. Yes, the directory in auth.ldap is one of the domains. I would prefer the auth.ldap setting to be completely independent from the management of Active directories. There are users who have an account in all three, some with identical user names, some with different user names and definitely with different passwords, checking logins against all configured domains would lead to confusion.

---

## 2026-11-23 — Sprint 16 (Active Directory and Samba)

The VM is quite busy (>100% CPU, 93% RAM. Should I resize it before going ahead?

---

## 2026-11-23 — Sprint 16 (Active Directory and Samba)

The VM is back with 8 GiB RAM and 4 cores. I don't want to overcommit on memory on the proxmox host, so I have to limit it. Please rerun the test and go ahead with the remaining tasks in this sprint

---

## 2026-11-24 — Sprint 16 (Active Directory and Samba)

yes, merge sprint 16 and tag v0.3.0

---

## 2026-11-24 — Sprint 17 (Configuration directory)

I just installed v0.3.0 and this time it didn't ask me about changes in the configuration file. Please show me the options I need to configure to set up the directories.

Then start sprint 17 and add a task to add these settings to the config.example.yaml file. Also, the configuration file might get quite long when adding a lot of elements. Therefore, I would like another task that we change the configuration handling:
- Instead of defining a config file, we define a config directory with -c
- All *.yaml files are read in alphabetical order and merged together into the configuration hash. This would allow us to have either a central configuration file or a lot of small ones (e.g. when we already have 25 switches, you can just drop another file when adding the next one instead of searching through one long file, the same applies when deleting elements)
- The example should reflect that changes, so we have a 00_server.yaml, a 01_auth.yaml, a 02_audit.yaml, a 03_security.yaml, a 04_roles.yaml, a 05_log.yaml, a 06_metrics.yaml a 07_global.yaml collecting things like status and data_dir, then a 10_projects.yaml, a 20_directories.yaml, a 30_netbox.yaml, a 40_proxmox.yaml, a 50_dns.yaml, a 60_dhcp.yaml, a 70_firewalls.yaml a 80_switches.yaml

---

## 2026-11-24 — Sprint 17 (Configuration directory)

Another issue: Omn the overview page: After adding the buttons for UI, ss and rdp, these buttons and the status are no longer in the same line as the heading of the tiles.

---

## 2026-11-24 — Sprint 17 (Configuration directory)

What firewall permissions are needed apart from allowing LDAPS on the firewall to show "OK" fro a directory?

---

## 2026-11-24 — Sprint 17 (Configuration directory)

Ich bekomme den Fehler LDAP Result Code 49 "Invalid Credentials": 80090308: LdapErr: DSID-0C0903A9, comment: AcceptSecurityContext error, data 52e, v1db1

---

## 2026-11-24 — Sprint 17 (Configuration directory)

ldap_sasl_bind(SIMPLE): Can't contact LDAP server (-1)

---

## 2026-11-24 — Sprint 17 (Configuration directory)

I validated the fingerprint and used 
 LDAPTLS_CACERT=/tmp/dc1-chain.pem ldapwhoami -d 1 -H ldaps://dc1.windows.kunden.sf.thse.net -D 'ssp-service@windows.kunden.sf.thse.net' -W

The result was 

(ldapwhoami debug output: connect to 10.15.172.3:636 succeeds; certificate verification fails at depth 0, err 20 "unable to get local issuer certificate"; subject /O=Samba Administration/OU=Samba - temporary autogenerated HOST certificate/CN=DC1.windows.kunden.thse.net, issuer /O=Samba Administration/OU=Samba - temporary autogenerated CA certificate/CN=DC1.windows.kunden.thse.net; "ldap_sasl_bind(SIMPLE): Can't contact LDAP server (-1)")

---

## 2026-11-24 — Sprint 17 (Configuration directory)

ok, that was the issue, the kunden domain exists withut the "sf"

---

## 2026-11-24 — Sprint 17 (Configuration directory)

Another task for sprint 17: The AD zones don't show on the DNS tab but I have the DNS tiles on the overview and manage_zones is set to true

---

## 2026-11-24 — Sprint 17 (Configuration directory)

I have 2 directories configured:
1. The directory tile shows "windows.test.sf.thse.net, one domain controller"
    The DNS tile shows "AD DNS at dc1.windows.test.sf.thse.net, 2 zones"
2. The directory tile shows "windows.kunden.thse.net, one domain controller"
   The DNS tile shows "AD DNS at dc1.windows.kunden.sf.thse.net, 2 zones"

My user is member of the "dashroot" group that is listed as root_group under auth

ldbsearch does not exist on the samba dc

---

## 2026-11-24 — Sprint 17 (Configuration directory)

a) I found it. I need to change the provider in the top right. on the dns page from the navigation. /dns always redirects auf /dns/cloudflare-thse which is the first DNS provider. Please change the page so that all providers are listed, each with a create zone button next to it with their zones below. The provider field acts as an optional filter

---

## 2026-11-24 — Sprint 17 (Configuration directory)

More tasks for sprint 17:
- Convert the https and ssh links for every switch to the same UI/SSH buttons as on the overview
- Convert the "Shell" button on the proxmox node tiles on the proxmox pages to a similar button and also add the UI and SSH buttons there
- A change in coloring: Always use the same color for each DNS tile. Take the color from bind9 for them

And another question:
All users in the directory show "read-only"  Do I need to use a different user than the ldap-bind user I've used so far (e.g. the "Administrator" user to be able to edit them?"

---

## 2026-11-24 — Sprint 17 (Configuration directory)

And another task: The "UI" buttons should open in new tabs.

Please implement as many tasks in sprint 17 as you can.

---

## 2026-11-24 — Sprint 17 (Configuration directory)

At the moment, the "change domain password" link is not very visible on the login page. Please redesign the login page:
- the application name and version should appear in a top bar.
- The display settings should also be in that top bar
- Below that, there should be two tiles: One for the login and a second tile with a button for each registry. That should be the preselected registry in the dialog that is shown

---

## 2026-11-24 — Sprint 17 (Configuration directory)

On the switch page, I also get redirected to the first switch. I would like a similar behaviour as for the Domains and see all configured switches on the overview page. Don't show the  tabbed view on that page. Use a larger font for the switch name there. Only when clicking on a switch,  the user should get the details page with the ports  and vlans. Only use the dropdown on the overview page for filtering and move the "Find a device" functionality onto the same line as that dropdown. Move the refresh button to the top right on the same level as the "Switches" Header

---

## 2026-11-24 — Sprint 17 (Configuration directory)

yes, merge sprint 17 and tag v0.4.0

---

## 2026-11-24 — Sprint 18 (Page layout)

No need for the domain mismatch check. For the next sprint, please add the following tasks:
- I tried to deactivate an AD user but forgot to put the right user into the configs. That failed as expected and I got an error message. However, that error message box is still in the lower right corner of my screen, even though I clicked through to other pages. These message boxes should have a way to close/acknowledge them
- On the DNS page, the ui/ssh/rdp buttons should show up next to the header for every server
- On the DNS page,always use the same background color as for the tiles on the overview page, remove references for different colors for different types of DNS servers
- Redesign the DHCP page in a similar way to the DNS page, showing all DHCP servers with their subnets, convert the server  dropdown to a filter and move it to the top right. Take the search part of the "Leases" tab and move it there as well. We don't need the "Leases of a subnet" dropdown any more as we have a button next to each subnet
- Change the refresh button on the netbox page to an icon as well and place it in the top right corner, move the filter for the proxmox cluster there as well and also show physical machines and switches in the list. Also move the DNS/DHCP compare button up to the top right
- On the switches page: Move the switch filter and search box into the top line, similar to what we have with DNS.
- On the firewall page and directories, also adapt the design in a similar way.
- On all pages switch the backgrounds of table elements to the colors used for the tiles on the front page, introduce a second, more transparent version of those colors for alternating lines in the tables
- Remove the unnecessary "Overview" button from the top bar, it has the same effect as clicking on dcdash
- Change the order of the buttons in the top bar to Projects, Directories, Netbox, Proxmox, DNS, DHCP, Firewalls, Switches. Move  Audit log and Sessions towards Documentation to the right
