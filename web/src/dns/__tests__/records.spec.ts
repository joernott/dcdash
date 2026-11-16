// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, it, expect } from 'vitest'

import { filterRecords, relativeName, zonePermission, type DnsRecord } from '../records'

function rec(name: string, type: string, content: string): DnsRecord {
  return { id: name + type, name, type, ttl: 300, content, version: '1' }
}

describe('dns records', () => {
  it('builds zone permissions', () => {
    expect(zonePermission('cf', 'example.org', 'write')).toBe('dns:cf:example.org:write')
  })

  it('shows names relative to the zone', () => {
    expect(relativeName('example.org', 'example.org')).toBe('@')
    expect(relativeName('www.example.org', 'example.org')).toBe('www')
    expect(relativeName('_sip._tcp.example.org', 'example.org')).toBe('_sip._tcp')
    expect(relativeName('wwwexample.org', 'example.org')).toBe('wwwexample.org')
  })

  it('filters by type and text', () => {
    const rs = [
      rec('example.org', 'MX', '10 mail.example.org.'),
      rec('www.example.org', 'A', '192.0.2.1'),
      rec('mail.example.org', 'A', '192.0.2.2'),
    ]
    expect(filterRecords(rs, { text: '', type: 'A' })).toHaveLength(2)
    expect(filterRecords(rs, { text: 'MAIL', type: '' })).toHaveLength(2)
    expect(filterRecords(rs, { text: '192.0.2.2', type: 'A' })).toHaveLength(1)
    expect(filterRecords(rs, { text: ' ', type: '' })).toHaveLength(3)
  })
})
