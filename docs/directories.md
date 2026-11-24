# Directories: Active Directory and Samba

dcdash manages users and groups of Active Directory domains, Windows or Samba, without
software on the domain controllers ([ADR 0010](adr/0010-active-directory.md)):

- **Users:** create, change, disable and enable, unlock, reset passwords, delete.
- **Groups:** create, change (also rename and change the scope), delete, add and remove
  members.
- **Self-service:** every user of a domain can change their own password at `/password`,
  without a dcdash login (like LTB Self Service Password).
- **DNS:** the AD-integrated zones appear on the DNS page as a provider of type `ad` (see
  [dns.md](dns.md#active-directory)).
- **Projects:** project files can name AD groups (see [projects.md](projects.md)).

The managed domains are configured under [`directories`](configuration.md#directories). They
have nothing to do with the login directory `auth.ldap`, even if it points to the same
domain: dcdash never checks a login against `directories`, and users can have accounts with
different names and passwords in several domains.

## Configuration

```yaml
directories:
  - name: lab                          # in the UI, permissions and project files
    type: samba                        # or windows
    domain: ad.example.com
    servers:                           # domain controllers, tried in order (LDAPS only)
      - url: ldaps://dc1.ad.example.com
        fingerprint: "AB:CD:..."       # Samba's self-signed certificate, see below
      - url: ldaps://dc2.ad.example.com
        fingerprint: "12:34:..."
    bind_dn: svc-dcdash@ad.example.com # service account (UPN or DN)
    bind_password: "file:/etc/dcdash/ad-lab.secret"
    managed_ous:                       # where dcdash may change users and groups
      - OU=Lab,DC=ad,DC=example,DC=com
    protected_users: [backup]          # only with admin, besides the built-in ones
    protected_groups: [Lab-Admins]
    ui: https://dc1.ad.example.com     # optional links on the overview tile
    rdp: dc1.ad.example.com
```

Each directory gets an overview tile that checks every domain controller with a bind of the
service account.

### Certificates

LDAPS needs a certificate on every domain controller. dcdash verifies it against the system
roots and `tls.ca_file`, or pins it with `fingerprint` (per server, as Samba generates a
different self-signed certificate on each DC). Get the fingerprint with:

```sh
openssl s_client -connect dc1.ad.example.com:636 </dev/null 2>/dev/null \
  | openssl x509 -noout -fingerprint -sha256
```

Samba's self-generated certificates can have a negative serial number, which Go, and so
dcdash, rejects ("x509: negative serial number"). Then give the DC a proper certificate
(`tls certfile`, `tls keyfile` and `tls cafile` in `smb.conf`), for example from your CA or:

```sh
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 3650 \
  -subj "/CN=dc1.ad.example.com" -addext "subjectAltName=DNS:dc1.ad.example.com" \
  -keyout /var/lib/samba/private/tls/key.pem -out /var/lib/samba/private/tls/cert.pem
chmod 600 /var/lib/samba/private/tls/key.pem
```

### The service account

dcdash works with one service account per domain. It needs on the managed OUs:

- create and delete user and group objects;
- write the attributes dcdash changes (names, mail, description, `userAccountControl`,
  `lockoutTime`, `pwdLastSet`, `member`, `groupType`, `sAMAccountName`,
  `userPrincipalName`);
- the extended right "Reset Password".

The self-service change uses the "Change Password" right, which Everyone has by default. For
AD DNS, the account also needs to change the records of the zones (membership in `DnsAdmins`
or rights on the zone objects). On Samba, a delegation can be set up with
`samba-tool dsacl set`; on Windows with the "Delegation of Control" wizard. Membership in
Domain Admins works as well, but gives the account far more than it needs.

## What dcdash protects

- **Containers:** objects outside `managed_ous` are shown, but never changed.
- **Protected objects** can only be changed with the `admin` permission:
  - the built-in and critical system objects (Administrator, krbtgt, Domain Admins, ...);
  - the built-in groups;
  - the members of privileged groups (Administrators, Domain, Enterprise and Schema Admins,
    Account, Server, Print and Backup Operators, Group Policy Creator Owners, Key Admins);
  - `protected_users` and `protected_groups`.
- **Permissions:** `directory:<name>:user/<account>:<action>` and
  `directory:<name>:group/<account>:<action>` (see [permissions.md](permissions.md#directories)).
  - `operate` on a user allows disabling, enabling, unlocking and resetting the password
    (for a help desk).
  - `write` allows creating, changing and deleting.

**Locking:** Active Directory has no administrative lock. An account is locked out only by
failed logins (when the domain has a lockout threshold). In dcdash, **disable** stops a user
from logging in, and **unlock** clears a lockout by failed logins.

Every change is audited (`directory.user.*`, `directory.group.*`); passwords never appear in
the audit log or the server log.

## Self-service password change

`/password` (linked from the login page) lists the directories with `self_service` (the
default) and their password rules. A user enters the account name or user principal name,
the current password and the new one twice.

- **How it changes:** dcdash changes the password with the current one over the service
  account's connection. This also works when the password expired or must be changed at the
  next logon, where the user can't bind.
- **Policy:** the domain checks the old password, the password policy and the history. A
  wrong old password counts towards the domain's lockout.
- **No enumeration:** unknown users and wrong passwords get the same answer, which takes at
  least a second.
- **Rate limits:** per user and per client address, with the policies of `auth.lockout.user`
  and `auth.lockout.ip`.
- **Audit:** recorded as `directory.password.change` with the user as actor and source
  `self-service`.
- **Exposure:** the page and its API need no login. Protect them like the login page; they
  are meant for internal networks.

To offer the page for a domain, nothing has to be done; `self_service: false` removes the
domain from it.

## Windows

The way dcdash talks to the domain is the same for Windows and Samba, but it was only tested
against Samba (4.22). Before you rely on it with a Windows domain controller, test in a lab
domain:

1. The status tile is green (LDAPS, certificate, service account).
2. Create, change, disable, enable, reset and delete a test user; unlock it after failed
   logins.
3. Create a group, add and remove members, change it to universal, rename and delete it.
4. Change a test user's password on `/password`, also with "must change at next logon".
5. With an `ad` DNS provider: add, change and delete a record. With `manage_zones`: create
   a zone, wait a few minutes until the DNS server serves it, add a record, delete the zone.

Please report what doesn't work; zone management on Windows may need an agent on the DC
(see the ADR).
