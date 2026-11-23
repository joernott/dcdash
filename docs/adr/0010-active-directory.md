# ADR 0010: Active Directory and Samba

- Status: accepted (verified against the lab Samba AD DC on 2026-11-23, see "Verification";
  not verified against Windows, see "Windows")
- Date: 2026-11-23
- Task: 16.1

## Context

dcdash should manage Active Directory domains: users, groups and group membership, the
AD-integrated DNS zones and records, a self-service password change for every user of a
configured domain, and AD groups of projects. Two scenarios exist:

- a Windows domain with a Windows Server 2019 domain controller in the airgapped network,
  reached by the dcdash server in that network;
- two current Samba AD domain controllers in the test labs (Samba 4.22).

Samba has no management API of its own besides `samba-tool` on the DC, so many tools wrap it
with an agent. Windows has the same LDAP and Kerberos interfaces as Samba plus PowerShell and
RPC. An agent is acceptable in both environments, but no Windows DC is reachable for tests.

The management of directories is independent of the login directory (`auth.ldap`): users
can have accounts with the same or different names and different passwords in several
domains, so dcdash never checks a login against the managed directories.

## Options

1. **Agentless:** LDAPS for users, groups and passwords, Kerberos for what needs it, RFC 2136
   updates signed with GSS-TSIG for DNS records, DNS zones as `dnsZone` objects in the
   directory. Works the same way for Windows and Samba and needs nothing on the DCs.
2. **Agent on the DC:** a small dcdash agent (Go, mTLS) that wraps `samba-tool` on Samba, or
   PowerShell (`ActiveDirectory`, `DnsServer` modules) on Windows. Two implementations,
   software to install, update and secure on every DC, with domain admin rights on the DC.
3. **MS-DNSP (DNS RPC)** for zones and records, as `samba-tool dns` and `dnscmd` do: DCE/RPC
   over SMB or TCP with NTLM or Kerberos. There is no Go library; it would have to be
   written from the specification.

## Decision

**Agentless (option 1) for every feature.** Everything was verified on the lab Samba DC with
plain LDAP, Kerberos and DNS from Go; Windows implements the same protocols (Samba copied
them from Windows). The agent (task 16.7) is not built in this sprint; it stays the fallback
for zone management on Windows if the manual test there fails (see "Windows").

| Feature | How |
|---|---|
| Connection | LDAPS (`ldaps://`, port 636) with failover over several URLs, certificate verified against the system roots and `ca_file` or pinned by fingerprint (`tlsutil`), bind with a service account by UPN or DN. Setting passwords needs an encrypted connection. |
| Read users and groups | Paged LDAP searches; transitive membership with the matching rule `LDAP_MATCHING_RULE_IN_CHAIN` (1.2.840.113556.1.4.1941) |
| Create user | Add with `objectClass user`, `sAMAccountName`, `userPrincipalName`, names, `unicodePwd` (the password in double quotes, UTF-16LE) and `userAccountControl` 512 in one request; "must change at next logon" is `pwdLastSet` 0 |
| Disable, enable | `userAccountControl` bit 2 (`ACCOUNTDISABLE`); a bind of a disabled user fails with `data 533` |
| Lock, unlock | AD has no administrative lock: an account is locked out only by failed logins (`lockoutThreshold`). dcdash offers **disable** for "lock" and **unlock** (`lockoutTime` 0) for accounts locked out by failed logins. |
| Reset password | Replace `unicodePwd` (needs the "Reset Password" right); optionally `pwdLastSet` 0 |
| Rename, move | Modify DN (`CN`), plus `sAMAccountName`/`userPrincipalName` |
| Delete | Delete request |
| Groups | Add with `objectClass group`, `sAMAccountName`, `groupType` (scope and security flag); change scope, description and name; add and remove `member` values; delete (also with members) |
| DNS: list zones | `dnsZone` objects under `CN=MicrosoftDNS` in `DC=DomainDnsZones`, `DC=ForestDnsZones` and (legacy) `CN=System`; `RootDNSServers` and `..TrustAnchors` are hidden |
| DNS: list records | `dnsNode` objects of the zone, each with `dnsRecord` values in the `DNS_RPC_RECORD` format (MS-DNSP 2.3.2.2: length, type, version 5, rank, serial, TTL big-endian, timestamp, data); tombstoned nodes (`dNSTombstoned`, type 0) are skipped. Samba refuses AXFR, so the bind provider's way doesn't work. |
| DNS: change records | RFC 2136 updates signed with GSS-TSIG (Kerberos, `bodgit/tsig` with `gokrb5`), with the SOA as prerequisite like the bind provider. Unsigned updates are refused. The Kerberos service principal is `DNS/<host name of the DC>`, so the configuration needs the DC's host name, not only its address. |
| DNS: create, delete zone | Add a `dnsZone` object and its `@` node with SOA and NS records via LDAP; delete the zone's nodes and the zone. dNSProperty values are copied from an existing AD-integrated zone of the same directory. |
| Self-service password change | LDAP modify of the user's `unicodePwd` with "delete old value, add new value" in one request, bound as the service account. AD treats it as a password **change** (the "Change Password" right, which "Everyone" has by default): it checks the old password, applies the password policy and history, and counts wrong old passwords towards the lockout. It works for accounts with "must change at next logon", where a bind of the user fails (`data 773`). |
| Password policy | Read from the domain object (`minPwdLength`, `pwdProperties` for complexity, `pwdHistoryLength`, `minPwdAge`, `maxPwdAge`, `lockoutThreshold`) and shown on the self-service page; fine-grained policies (PSOs) are not evaluated |

The rights of the service account: on the managed OUs, create and delete users and groups,
write their attributes, reset passwords and unlock; for DNS, membership in `DnsAdmins` or
rights on the zone objects. Task 16.12 documents a delegation; tests use the lab's
Administrator.

### Self-service alternatives

kpasswd (port 464) was verified as well, but not with `gokrb5`'s `ChangePasswd`: it sends the
RFC 3244 "set password" form with the user as target, which Samba treats as an administrative
reset and refuses ("Not permitted to change password"). The RFC 3244 version 1 request (only
the new password, in a KRB-PRIV after an AS exchange for `kadmin/changepw`) works, also for
"must change" accounts. dcdash uses the LDAP change because it needs no Kerberos
configuration and returns readable policy errors (for example "the password is too short.
It should be equal to or longer than 7 characters"), while kpasswd only returns a result
code. A bind as the user is not usable: it fails for "must change" and expired passwords.

## Windows

Not verified, because no Windows DC is reachable. Expected differences:

- LDAPS needs a certificate on the DC (AD CS or an own one); dcdash pins it or trusts its CA.
- Error texts differ from Samba's; dcdash maps the `data` codes of bind errors (525, 52e, 530,
  531, 532, 533, 701, 773, 775) and the Windows error codes in constraint violations
  (`0000052D` password policy, `00000056` wrong old password).
- **Zones created via LDAP:** the Windows DNS server reads AD-integrated zones from the
  directory and picks up new ones by polling (default every 180 seconds), so a new zone may
  take a few minutes to answer. Microsoft documents only `dnscmd`, PowerShell and RPC for
  creating zones, so this is the riskiest part; it is behind a setting of the DNS
  provider (`ad.manage_zones`) and listed in the manual test. If it fails on Windows, zone management
  for Windows moves to an agent with the `DnsServer` PowerShell module in a later sprint.
- Records via GSS-TSIG are the way Windows clients register themselves and work in zones
  with "secure only" dynamic updates.

## Verification

Against `dc1.windows.test.sf.thse.net` (Samba 4.22.11, domain functional level 2008 R2), on
2026-11-23, with a spike program from the dev VM; every object had the prefix `dcdashdev` and
was removed afterwards (only the empty `OU=dcdashdev-test` stays for the integration tests).

- LDAPS with the pinned certificate; bind as Administrator (UPN) and as the `auth.ldap`
  service account.
- Users: create with password and `userAccountControl` 512, bind as the user, self change
  (delete/add), admin reset, "must change" (bind fails with `data 773`), disable (`data
  533`), enable, unlock, rename, delete.
- Self-service for a "must change" user: LDAP change bound as the service account succeeds;
  with a wrong old password `00000056 ... The old password specified doesn't match!`; with a
  password that is too short `0000052D ... the password is too short`. kpasswd version 1
  succeeds, the set-password form fails as described above.
- Lockout itself could not be tested: the lab domain has `lockoutThreshold` 0.
- Groups: create (global security), nested group, add and remove members, transitive lookup,
  change to universal and the description, rename, delete with members.
- DNS records: unsigned update `REFUSED`; GSS-TSIG context for `DNS/dc1...`; insert A and TXT
  on one name, remove both; LDAP lists the zone's `dnsNode` objects with their records; AXFR
  is refused.
- DNS zone: a `dnsZone` with an `@` node (SOA, NS) and an A record created via LDAP is served
  immediately; a GSS-TSIG update into it works; after deleting the objects the zone returns
  NXDOMAIN.

## Consequences

- One implementation for Windows and Samba, configured per directory with `type` only for
  differences in defaults and error texts.
- New dependencies: `bodgit/tsig` and `jcmturner/gokrb5` become direct dependencies (already
  indirect through lego).
- dcdash parses and writes `DNS_RPC_RECORD` for the record types it supports (A, AAAA, CNAME,
  MX, NS, PTR, SRV, TXT, SOA read-only) and shows others read-only.
- The Samba AD DC container for tests (task 16.3) only needs LDAP, Kerberos and its internal
  DNS, not `samba-tool` from dcdash.
- Task 16.7 (agent) is dropped for this sprint.
