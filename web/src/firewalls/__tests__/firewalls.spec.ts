// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, it, expect } from 'vitest'

import {
  ageSeconds,
  endpoint,
  formatAge,
  formatBytes,
  newRule,
  parseContent,
  toRequest,
} from '../firewalls'

describe('firewall helpers', () => {
  it('formats bytes and ages', () => {
    expect(formatBytes(512)).toBe('512 B')
    expect(formatBytes(1536)).toBe('1.5 KiB')
    expect(formatBytes(8589934592)).toBe('8.0 GiB')
    expect(formatAge(45)).toBe('45 s')
    expect(formatAge(300)).toBe('5 min')
    expect(formatAge(7200)).toBe('2 h')
    expect(formatAge(172800)).toBe('2 d')
    expect(ageSeconds('0001-01-01T00:00:00Z')).toBeNull()
    expect(ageSeconds('2026-11-19T12:00:00Z', Date.parse('2026-11-19T12:01:00Z'))).toBe(60)
  })

  it('parses alias content and shows endpoints', () => {
    expect(parseContent(' 192.0.2.1\n\n192.0.2.2, 192.0.2.3 ')).toEqual([
      '192.0.2.1',
      '192.0.2.2',
      '192.0.2.3',
    ])
    expect(endpoint('10.0.0.0/8', true, '443')).toBe('! 10.0.0.0/8:443')
    expect(endpoint('', false, '')).toBe('any')
  })

  it('maps a rule to a request', () => {
    const r = { ...newRule('lan'), uuid: 'r-1', legacy: false, canWrite: true, description: 'web' }
    const req = toRequest(r as Parameters<typeof toRequest>[0])
    expect(req.description).toBe('web')
    expect(Object.keys(req)).not.toContain('uuid')
  })
})
