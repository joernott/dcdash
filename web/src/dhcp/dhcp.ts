// SPDX-License-Identifier: GPL-3.0-or-later
// Helpers of the DHCP views: permissions, utilization, identifiers, filters.
import type { Schemas } from '@/api/client'

export type DhcpServer = Schemas['DHCPServer']
export type DhcpSubnet = Schemas['DHCPSubnet']
export type DhcpLease = Schemas['DHCPLease']
export type DhcpReservation = Schemas['DHCPReservation']
export type SearchKey = DhcpServer['searchKeys'][number]
export type IdentifierType = Schemas['DHCPReservationRequest']['identifierType']

/** The permission resource of global reservations (subnet ID 0). */
export const GLOBAL_RESOURCE = 'global'

/** The permission for an action on a subnet: dhcp:<server>:<prefix>:<action>. */
export function subnetPermission(
  server: string,
  prefix: string,
  action: 'read' | 'operate' | 'write',
): string {
  return `dhcp:${server}:${prefix}:${action}`
}

/** Share of assigned addresses, or undefined without statistics or pool. */
export function utilization(s: DhcpSubnet): number | undefined {
  if (!s.stats || s.stats.total <= 0) return undefined
  return Math.min(1, s.stats.assigned / s.stats.total)
}

/** Share of delegated prefixes (DHCPv6), or undefined without PD pools. */
export function pdUtilization(s: DhcpSubnet): number | undefined {
  if (!s.stats?.totalPd) return undefined
  return Math.min(1, (s.stats.assignedPd ?? 0) / s.stats.totalPd)
}

/** Identifier types a user can choose for a reservation of the family. */
export function identifierTypes(family: DhcpServer['family']): IdentifierType[] {
  return family === 'v6' ? ['duid', 'hw-address'] : ['hw-address', 'client-id']
}

/** The client identifier of a lease: MAC (v4) or DUID (v6). */
export function leaseClient(l: DhcpLease): string {
  return l.hwAddress || l.duid || l.clientId || ''
}

/** The address of a lease, with the prefix length for delegated prefixes. */
export function leaseAddress(l: DhcpLease): string {
  return l.type === 'IA_PD' && l.prefixLen ? `${l.ipAddress}/${l.prefixLen}` : l.ipAddress
}

/** Splits a list typed by the user at commas, semicolons and white space. */
export function splitList(s: string): string[] {
  return s
    .split(/[\s,;]+/)
    .map((x) => x.trim())
    .filter((x) => x !== '')
}

/** Filters reservations by subnet and text (identifier, address, host name). */
export function filterReservations(
  list: DhcpReservation[],
  subnetId: string,
  text: string,
): DhcpReservation[] {
  const q = text.trim().toLowerCase()
  return list.filter(
    (r) =>
      (subnetId === '' || String(r.subnetId) === subnetId) &&
      (q === '' ||
        r.identifier.toLowerCase().includes(q) ||
        (r.hostname ?? '').toLowerCase().includes(q) ||
        r.ipAddresses.some((a) => a.toLowerCase().includes(q)) ||
        (r.prefixes ?? []).some((p) => p.toLowerCase().includes(q))),
  )
}

/** Large counts (IPv6 pools) in short form: 1.8e19 instead of 20 digits. */
export function formatCount(n: number, locale: string): string {
  if (n >= 1e9) return n.toExponential(1).replace('e+', 'e')
  return new Intl.NumberFormat(locale).format(n)
}
