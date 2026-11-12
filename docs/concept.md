# dcdash
dcdash is a dashboard for administrators to manage multiple infrastructure components in a data center.

Its backend is written in go and the frontend uses vue and tailwindcss. Users authenticate against ActiveDirectory/SAMBA Active directory or another LDAP server.

It uses GPLv3 as its license.

The initial release (v0.1.0) has the following features:
- A Backend that serves its own API and the static frontend via https. It gets it's certificate from Let's Encrypt and uses the cloudflare API to handle the DNS challenge. It should also be able to use stepca or a set of provided certificates instead of letsencrypt.
- The backend stores its configuration in a yaml file
- The backend uses LDAP to authenticate against a SAMBA/Active Directory or other LDAP server. The users roles depend on their membership in specific groups.
- The builtin role "root" always maps to the group "dashroot" and grants its members full access to everything. Other roles can be defined in the configuration file with its mapping to LDAP groups and permissions.
- It uses TOTP 2FA for logins
- The duration of a session can be configured in its configuration
- It allows the management of multiple independent proxmox clusters using https://github.com/luthermonson/go-proxmox
- It implements the cloudflare DNS API to manage DNS entries as well as bind9 with rndc
- It manages KEA v3 DHCP4 and DHCP6 server via it's http/https API
