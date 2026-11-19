// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, it, expect } from 'vitest'

import {
  formatSpeed,
  formatVlans,
  overviewLayout,
  parseVlans,
  portMatches,
  portState,
  portVlans,
  uptimeDays,
  type SwitchPort,
} from '../switches'

function port(p: Partial<SwitchPort>): SwitchPort {
  return {
    name: '1/1/5',
    type: 'system',
    description: '',
    enabled: true,
    adminUp: true,
    linkUp: true,
    speedMbps: 1000,
    mode: 'access',
    vlan: 10,
    trunkVlans: [],
    allVlans: false,
    neighbors: [],
    uplink: false,
    canOperate: true,
    canWrite: true,
    ...p,
  }
}

describe('switch helpers', () => {
  it('formats and parses VLAN lists', () => {
    expect(formatVlans([22, 10, 20, 21, 30, 31])).toBe('10, 20-22, 30, 31')
    expect(formatVlans([])).toBe('')
    expect(parseVlans('10, 20-22 30')).toEqual([10, 20, 21, 22, 30])
    expect(parseVlans('')).toEqual([])
    expect(parseVlans('0')).toBeNull()
    expect(parseVlans('4095')).toBeNull()
    expect(parseVlans('20-10')).toBeNull()
    expect(parseVlans('a')).toBeNull()
  })

  it('describes ports', () => {
    expect(portState(port({}))).toBe('up')
    expect(portState(port({ linkUp: false }))).toBe('down')
    expect(portState(port({ enabled: false }))).toBe('disabled')
    expect(formatSpeed(1000)).toBe('1 Gbit/s')
    expect(formatSpeed(100)).toBe('100 Mbit/s')
    expect(formatSpeed(0)).toBe('')
    expect(portVlans(port({}), 'all')).toBe('10')
    expect(portVlans(port({ mode: 'trunk', vlan: 1, allVlans: true }), 'all')).toBe('1 + all')
    expect(portVlans(port({ mode: 'trunk', vlan: 10, trunkVlans: [10, 20, 21, 22] }), 'all')).toBe(
      '10 + 20-22',
    )
  })

  it('filters ports', () => {
    const p = port({
      description: 'esx1',
      neighbors: [
        { chassisId: 'x', portId: 'y', systemName: 'core-sw', capabilities: [], bridge: true },
      ],
    })
    expect(portMatches(p, '')).toBe(true)
    expect(portMatches(p, 'ESX')).toBe(true)
    expect(portMatches(p, 'core')).toBe(true)
    expect(portMatches(p, '1/1/5')).toBe(true)
    expect(portMatches(p, 'nope')).toBe(false)
  })

  it('computes the uptime', () => {
    expect(uptimeDays('2026-11-01T00:00:00Z', Date.parse('2026-11-19T12:00:00Z'))).toBe(18)
    expect(uptimeDays(undefined)).toBeNull()
  })
})

describe('overview layout', () => {
  const named = (n: number, member = 1, sfp = false) =>
    port({ name: `${member}/1/${n}`, connector: sfp ? 'SFP_PLUS' : 'RJ45' })
  const names = (rows: (SwitchPort | null)[][]) => rows.map((r) => r.map((p) => p?.name ?? '-'))

  it('lays out 24 + 4 ports in two rows', () => {
    const ports = [
      ...Array.from({ length: 24 }, (_, i) => named(i + 1)),
      ...[25, 26, 27, 28].map((n) => named(n, 1, true)),
      port({ name: 'lag1', type: 'lag' }),
    ]
    const rows = names(overviewLayout(ports))
    expect(rows).toHaveLength(2)
    expect(rows[0]!.slice(0, 2)).toEqual(['1/1/1', '1/1/3'])
    expect(rows[1]!.slice(0, 2)).toEqual(['1/1/2', '1/1/4'])
    expect(rows[0]![11]).toBe('1/1/23')
    expect(rows[0]!.slice(12)).toEqual(['1/1/25', '1/1/27'])
    expect(rows[1]!.slice(12)).toEqual(['1/1/26', '1/1/28'])
  })

  it('uses four rows for 48 ports and leaves columns of small switches blank', () => {
    const big = [
      ...Array.from({ length: 48 }, (_, i) => named(i + 1)),
      ...[49, 50, 51, 52].map((n) => named(n, 1, true)),
    ]
    const rows = names(overviewLayout(big))
    expect(rows).toHaveLength(4)
    expect(rows[2]![0]).toBe('1/1/25')
    expect(rows[3]![11]).toBe('1/1/48')
    expect(rows[0]!.slice(12)).toEqual(['1/1/49', '1/1/51'])
    expect(rows[2]!.slice(12)).toEqual(['-', '-'])

    const small = names(overviewLayout(Array.from({ length: 8 }, (_, i) => named(i + 1))))
    expect(small).toHaveLength(2)
    expect(small[1]!.slice(3, 5)).toEqual(['1/1/8', '-'])
  })

  it('stacks members', () => {
    const rows = names(overviewLayout([named(1), named(1, 2), named(2, 2)]))
    expect(rows).toHaveLength(4)
    expect(rows[2]![0]).toBe('2/1/1')
    expect(rows[3]![0]).toBe('2/1/2')
  })
})
