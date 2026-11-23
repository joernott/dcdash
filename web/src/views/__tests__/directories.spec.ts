// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'

const fake = vi.hoisted(() => ({ api: undefined as unknown }))
vi.mock('@/api/client', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/client')>()),
  api: new Proxy({}, { get: (_, prop) => (fake.api as Record<string | symbol, unknown>)[prop] }),
}))

import DirectoriesView from '../directories/DirectoriesView.vue'
import { cnOf, userStates } from '@/directories/directories'
import { FakeApi } from '@/__tests__/fakeApi'
import { buttonName, mountWith } from '@/__tests__/helpers'

let api: FakeApi
beforeEach(() => {
  api = new FakeApi()
  fake.api = api.client
})

function router() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/directories/:directory?', name: 'directories', component: DirectoriesView }],
  })
}

const lab = {
  name: 'lab',
  type: 'samba',
  domain: 'ad.test',
  managedOUs: ['OU=Lab,DC=ad,DC=test'],
  canCreateUsers: true,
  canCreateGroups: true,
}

function user(name: string, over: Record<string, unknown> = {}) {
  return {
    dn: `CN=${name},OU=Lab,DC=ad,DC=test`,
    name,
    displayName: `${name} Example`,
    disabled: false,
    lockedOut: false,
    mustChangePassword: false,
    passwordExpired: false,
    passwordNeverExpires: false,
    groups: [],
    managed: true,
    protected: false,
    canOperate: true,
    canWrite: true,
    ...over,
  }
}

async function mountPage(query = '') {
  api.on('GET /api/v1/directories', () => [200, { directories: [lab] }])
  const r = router()
  await r.push(`/directories/lab${query}`)
  const w = mountWith(DirectoriesView, { attachTo: document.body, global: { plugins: [r] } })
  await flushPromises()
  return w
}

describe('directories helpers', () => {
  it('reads the CN of a DN', () => {
    expect(cnOf('CN=Doe\\, John,OU=x,DC=ad')).toBe('Doe, John')
    expect(cnOf('OU=Lab,DC=ad')).toBe('Lab')
  })
  it('lists the notable states', () => {
    expect(userStates(user('a', { disabled: true, protected: true })).map((s) => s.key)).toEqual([
      'disabled',
      'protected',
    ])
    expect(userStates(user('b', { managed: false })).map((s) => s.key)).toEqual(['unmanaged'])
  })
})

describe('DirectoriesView', () => {
  it('lists users and disables, unlocks and creates them', async () => {
    let users = [
      user('alice'),
      user('bob', { lockedOut: true }),
      user('admin', { protected: true, canOperate: false, canWrite: false }),
    ]
    api.on('GET /api/v1/directories/{directory}/users', () => [200, { users, truncated: false }])
    const w = await mountPage()
    expect(w.text()).toContain('ad.test')
    expect(w.text()).toContain('alice Example')
    expect(w.text()).toContain('locked out')
    expect(w.text()).toContain('protected')
    const names = w.findAll('button').map((b) => buttonName(b))
    expect(names).toContain('Disable alice')
    expect(names).toContain('Unlock bob')
    expect(names).not.toContain('Disable admin')

    let disabled = ''
    api.on('POST /api/v1/directories/{directory}/users/{name}/disable', (_, p) => {
      disabled = String(p?.path?.name)
      users = users.map((u) => (u.name === disabled ? { ...u, disabled: true } : u))
      return [200, users[0]]
    })
    await w
      .findAll('button')
      .find((b) => buttonName(b) === 'Disable alice')!
      .trigger('click')
    await flushPromises()
    expect(disabled).toBe('alice')
    expect(w.findAll('button').map((b) => buttonName(b))).toContain('Enable alice')

    api.on('GET /api/v1/directories/{directory}/containers', () => [
      200,
      { containers: [{ dn: 'OU=Lab,DC=ad,DC=test', name: 'Lab' }] },
    ])
    let created: unknown
    api.on('POST /api/v1/directories/{directory}/users', (b) => {
      created = b
      return [201, user('carol')]
    })
    await w
      .findAll('button')
      .find((b) => buttonName(b) === 'Create a user')!
      .trigger('click')
    await flushPromises()
    const dialog = w.get('dialog[open]')
    const inputs = dialog.findAll('input:not([type=checkbox])')
    await inputs[0]!.setValue('carol')
    await inputs[inputs.length - 1]!.setValue('Carol-12345')
    await dialog.get('form').trigger('submit')
    await flushPromises()
    expect(created).toMatchObject({
      ou: 'OU=Lab,DC=ad,DC=test',
      name: 'carol',
      password: 'Carol-12345',
      mustChangePassword: true,
      disabled: false,
    })
  })

  it('shows and changes group members', async () => {
    const group = {
      dn: 'CN=lab-admins,OU=Lab,DC=ad,DC=test',
      name: 'lab-admins',
      scope: 'global',
      security: true,
      managed: true,
      protected: false,
      canWrite: true,
    }
    api.on('GET /api/v1/directories/{directory}/groups', () => [
      200,
      { groups: [group], truncated: false },
    ])
    let members = [{ dn: 'CN=alice,OU=Lab', name: 'alice', kind: 'user' }]
    api.on('GET /api/v1/directories/{directory}/groups/{name}', () => [200, { ...group, members }])
    let change: unknown
    api.on('POST /api/v1/directories/{directory}/groups/{name}/members', (b) => {
      change = b
      members = [...members, { dn: 'CN=bob,OU=Lab', name: 'bob', kind: 'user' }]
      return [200, { ...group, members }]
    })
    const w = await mountPage('?tab=groups')
    expect(w.text()).toContain('lab-admins')
    expect(w.text()).toContain('Global')
    await w
      .findAll('button')
      .find((b) => buttonName(b) === 'Members of lab-admins')!
      .trigger('click')
    await flushPromises()
    const dialog = w.get('dialog[open]')
    expect(dialog.text()).toContain('alice')
    await dialog.get('input').setValue('bob')
    const forms = dialog.findAll('form')
    await forms[forms.length - 1]!.trigger('submit')
    await flushPromises()
    expect(change).toEqual({ add: ['bob'], remove: [] })
    expect(w.get('dialog[open]').text()).toContain('bob')
  })
})
