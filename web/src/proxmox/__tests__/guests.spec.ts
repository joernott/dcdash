// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, it, expect } from 'vitest'

import {
  availableActions,
  filterGuests,
  formatBytes,
  guestPermission,
  nodeShellPermission,
  ratio,
  statusStyle,
  uptimeParts,
  type Guest,
} from '../guests'
import { consoleSocketURL, inputMessage, resizeMessage } from '../termproxy'

describe('guests', () => {
  it('builds permissions as the backend checks them', () => {
    expect(guestPermission('lab', 'qemu', 101, 'operate')).toBe('proxmox:lab:qemu/101:operate')
    expect(nodeShellPermission('lab', 'pve1')).toBe('proxmox:lab:node/pve1:admin')
  })

  it('offers actions by state', () => {
    expect(availableActions({ status: 'running', template: false, lock: '' })).toContain('stop')
    expect(availableActions({ status: 'stopped', template: false, lock: '' })).toEqual(['start'])
    expect(availableActions({ status: 'paused', template: false, lock: '' })).toEqual([
      'resume',
      'stop',
    ])
    expect(availableActions({ status: 'stopped', template: true, lock: '' })).toEqual([])
    expect(availableActions({ status: 'unknown', template: false, lock: '' })).toEqual([])
  })

  it('formats values', () => {
    expect(formatBytes(512, 'en')).toBe('512 B')
    expect(formatBytes(1536 * 1024 * 1024, 'en')).toBe('1.5 GiB')
    expect(formatBytes(1536 * 1024 * 1024, 'de')).toBe('1,5 GiB')
    expect(ratio(1, 4)).toBe(0.25)
    expect(ratio(1, 0)).toBeUndefined()
    expect(uptimeParts(90061)).toEqual({ days: 1, hours: 1, minutes: 1 })
    expect(statusStyle('running')).toBe('ok')
    expect(statusStyle('paused')).toBe('warning')
    expect(statusStyle('whatever')).toBe('unknown')
  })

  it('filters by text, type, status and node', () => {
    const g = (vmid: number, name: string, type: 'qemu' | 'lxc', tags: string[] = []) =>
      ({ vmid, name, type, tags, status: 'running', node: 'pve1' }) as unknown as Guest
    const list = [g(101, 'web', 'qemu', ['Prod']), g(200, 'proxy', 'lxc'), g(1101, 'mail', 'qemu')]
    const f = { text: '', type: '' as const, status: '', node: '' }
    expect(filterGuests(list, { ...f, text: 'prod' }).map((x) => x.vmid)).toEqual([101])
    expect(filterGuests(list, { ...f, text: '101' }).map((x) => x.vmid)).toEqual([101, 1101])
    expect(filterGuests(list, { ...f, type: 'lxc' }).map((x) => x.vmid)).toEqual([200])
    expect(filterGuests(list, { ...f, node: 'pve2' })).toEqual([])
  })
})

describe('termproxy', () => {
  it('encodes input with the UTF-8 byte length', () => {
    expect(inputMessage('ls\r')).toBe('0:3:ls\r')
    expect(inputMessage('ä')).toBe('0:2:ä')
    expect(resizeMessage(80, 24)).toBe('1:80:24:')
  })

  it('builds the websocket URL', () => {
    expect(consoleSocketURL('a/b', { protocol: 'https:', host: 'dash:8443' })).toBe(
      'wss://dash:8443/api/v1/proxmox/console/a%2Fb',
    )
    expect(consoleSocketURL('x', { protocol: 'http:', host: 'localhost' })).toBe(
      'ws://localhost/api/v1/proxmox/console/x',
    )
  })
})
