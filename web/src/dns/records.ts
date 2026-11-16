// SPDX-License-Identifier: GPL-3.0-or-later
// Helpers of the DNS views: permissions, record names, filters.
import type { Schemas } from '@/api/client'

export type DnsRecord = Schemas['DNSRecord']
export type DnsZone = Schemas['DNSZone']
export type Capabilities = Schemas['DNSCapabilities']
export type RecordType = Schemas['DNSRecordRequest']['type']

/** Record types dcdash can create and edit. */
export const EDITABLE_TYPES: RecordType[] = [
  'A',
  'AAAA',
  'CNAME',
  'MX',
  'TXT',
  'SRV',
  'PTR',
  'NS',
  'CAA',
]

/** Types Cloudflare can proxy. */
export const PROXIABLE_TYPES: string[] = ['A', 'AAAA', 'CNAME']

/** The permission for an action on a zone: dns:<provider>:<zone>:<action>. */
export function zonePermission(
  provider: string,
  zone: string,
  action: 'read' | 'write' | 'admin',
): string {
  return `dns:${provider}:${zone}:${action}`
}

/** The name relative to the zone: "@" for the apex, "www" for www.<zone>. */
export function relativeName(name: string, zone: string): string {
  if (name === zone) return '@'
  return name.endsWith(`.${zone}`) ? name.slice(0, -zone.length - 1) : name
}

export interface RecordFilter {
  text: string
  type: string
}

/** Filters records by type and by text in name or content (case-insensitive). */
export function filterRecords(records: DnsRecord[], f: RecordFilter): DnsRecord[] {
  const text = f.text.trim().toLowerCase()
  return records.filter(
    (r) =>
      (!f.type || r.type === f.type) &&
      (!text || r.name.toLowerCase().includes(text) || r.content.toLowerCase().includes(text)),
  )
}

/** Example content per type, shown as placeholder in the record dialog. */
export const CONTENT_EXAMPLES: Record<RecordType, string> = {
  A: '192.0.2.10',
  AAAA: '2001:db8::10',
  CNAME: 'www.example.org.',
  MX: '10 mail.example.org.',
  TXT: 'v=spf1 -all',
  SRV: '10 5 5060 sip.example.org.',
  PTR: 'host.example.org.',
  NS: 'ns1.example.org.',
  CAA: '0 issue "letsencrypt.org"',
}

/** Common TTL choices in seconds; 1 is "automatic" for Cloudflare. */
export const TTL_CHOICES = [60, 300, 900, 3600, 14400, 86400]
