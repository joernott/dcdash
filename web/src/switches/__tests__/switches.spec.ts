// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, it, expect } from 'vitest'

import {
  formatSpeed,
  formatVlans,
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
