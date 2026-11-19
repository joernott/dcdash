// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, it, expect } from 'vitest'

import { freeAddress, ipamPermission, netboxFailure, parseVid, type NetBoxVlan } from '../netbox'

const vlan: NetBoxVlan = {
  vid: 100,
  name: 'servers',
  description: '',
  prefixes: [
    { prefix: '192.0.2.0/24', family: 4, description: '', free: ['192.0.2.3/24', '192.0.2.4/24'] },
    { prefix: '198.51.100.0/24', family: 4, description: '', free: ['198.51.100.1/24'] },
    { prefix: '2001:db8::/64', family: 6, description: '', free: ['2001:db8::1/64'] },
  ],
}

describe('netbox helpers', () => {
  it('builds the permission', () => {
    expect(ipamPermission('nb')).toBe('netbox:nb:ipam:read')
  })

  it('parses VLAN IDs', () => {
    expect(parseVid('100')).toBe(100)
    expect(parseVid(' 4094 ')).toBe(4094)
    for (const s of ['', '0', '4095', '1e2', '10a', '12345']) expect(parseVid(s)).toBeNull()
  })

  it('skips addresses used by other devices', () => {
    expect(freeAddress(vlan, 4, [])).toBe('192.0.2.3/24')
    expect(freeAddress(vlan, 4, ['192.0.2.3/24', ''])).toBe('192.0.2.4/24')
    expect(freeAddress(vlan, 4, ['192.0.2.3', '192.0.2.4/25'])).toBe('198.51.100.1/24')
    expect(freeAddress(vlan, 4, ['192.0.2.3', '192.0.2.4', '198.51.100.1'])).toBeNull()
    expect(freeAddress(vlan, 6, ['2001:DB8::1/64'])).toBeNull()
    expect(freeAddress(vlan, 6, [])).toBe('2001:db8::1/64')
  })

  it('reports failed write-backs only', () => {
    const t = (k: string, p: Record<string, unknown>) => `${k}: ${String(p.error)}`
    expect(netboxFailure({ status: 'failed', error: 'down' }, t)).toBe(
      'netbox.writeBackFailed: down',
    )
    expect(netboxFailure({ status: 'ok' }, t)).toBeNull()
    expect(netboxFailure(undefined, t)).toBeNull()
  })
})
