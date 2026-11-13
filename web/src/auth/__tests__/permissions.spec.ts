// SPDX-License-Identifier: GPL-3.0-or-later
// Mirrors internal/rbac/permission_test.go, so that frontend and backend agree.
import { describe, it, expect } from 'vitest'

import { can, canAny, matchPattern, parsePermission } from '../permissions'

describe('permissions', () => {
  it('parses permissions like the backend', () => {
    expect(parsePermission('dhcp:kea:2001:db8::/64:read')).toEqual({
      module: 'dhcp',
      instance: 'kea',
      resource: '2001:db8::/64',
      level: 1,
    })
    for (const bad of ['', 'dns', 'dns:cf:write', 'dns::x:read', 'dns:a:b:delete']) {
      expect(parsePermission(bad)).toBeUndefined()
    }
  })

  it.each([
    ['dns:*:*:write', 'dns:cf:example.com:read', true],
    ['dns:*:*:read', 'dns:cf:example.com:write', false],
    ['dns:cf:*:write', 'dns:bind:example.com:write', false],
    ['*:*:*:admin', 'proxmox:a:vm/1:console', true],
    ['*:*:*:*', 'proxmox:a:vm/1:admin', true],
    ['dns:*:*.example.com:write', 'dns:cf:www.example.com:write', true],
    ['dns:*:*.example.com:write', 'dns:cf:example.com:write', false],
    ['dns:*:*.example.com:write', 'dns:cf:evil-example.com:write', false],
    ['proxmox:prod-*:*:operate', 'proxmox:prod-a:vm/1:operate', true],
    ['proxmox:*:vm/*0:read', 'proxmox:c:vm/101:read', false],
    ['x:*:a*b*c:read', 'x:i:a-b-c:read', true],
    ['x:*:ab*ba:read', 'x:i:aba:read', false],
    ['dns:CF:*:read', 'dns:cf:x:read', false],
  ])('%s grants %s: %s', (grant, req, want) => {
    expect(can([grant], req)).toBe(want)
  })

  it('checks modules', () => {
    const granted = ['dns:cf:*:write', 'proxmox:*:*:read']
    expect(canAny(granted, 'dns', 'write')).toBe(true)
    expect(canAny(granted, 'dns', 'admin')).toBe(false)
    expect(canAny(granted, 'dns', 'read', 'bind')).toBe(false)
    expect(canAny(granted, 'dhcp')).toBe(false)
    expect(canAny(['*:*:*:admin'], 'system')).toBe(true)
    expect(can([], 'a:b:c:read')).toBe(false)
  })

  it('matches patterns', () => {
    expect(matchPattern('*', '')).toBe(true)
    expect(matchPattern('a*', 'a')).toBe(true)
    expect(matchPattern('*b', 'ab')).toBe(true)
    expect(matchPattern('a*a', 'a')).toBe(false)
  })
})
