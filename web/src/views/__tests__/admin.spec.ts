// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'

const fake = vi.hoisted(() => ({ api: undefined as unknown }))
vi.mock('@/api/client', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/client')>()),
  api: new Proxy({}, { get: (_, prop) => (fake.api as Record<string | symbol, unknown>)[prop] }),
}))

import AuditView from '../admin/AuditView.vue'
import SessionsView from '../admin/SessionsView.vue'
import { useSessionStore } from '@/stores/session'
import { useToastStore } from '@/stores/toast'
import { FakeApi, sessionInfo, type Init } from '@/__tests__/fakeApi'
import { mountWith } from '@/__tests__/helpers'

let api: FakeApi
beforeEach(() => {
  api = new FakeApi()
  fake.api = api.client
})

function entry(id: number, over: Record<string, unknown> = {}) {
  return {
    id,
    time: '2026-11-13T10:00:00Z',
    actor: 'alice',
    actorSource: 'ldap',
    clientIp: '192.0.2.1',
    action: 'auth.login',
    target: '',
    result: 'success',
    details: { stage: 'authenticated' },
    requestId: 'r',
    ...over,
  }
}

describe('AuditView', () => {
  it('filters and loads older entries', async () => {
    const queries: unknown[] = []
    api.client.GET = (_path: string, init?: Init) => {
      queries.push(init?.params?.query)
      const before = init?.params?.query?.before as number | undefined
      const body = before
        ? { entries: [entry(1, { result: 'denied', action: 'authz.denied' })], nextBefore: null }
        : { entries: [entry(3), entry(2)], nextBefore: 2 }
      return Promise.resolve({ data: body, response: new Response(null, { status: 200 }) })
    }
    const w = mountWith(AuditView, { locale: 'de' })
    await flushPromises()
    expect(w.findAll('tbody tr')).toHaveLength(2)
    expect(w.get('tbody').text()).toContain('stage=authenticated')

    await w
      .findAll('button')
      .find((b) => b.text() === 'Ältere Einträge laden')!
      .trigger('click')
    await flushPromises()
    expect(w.findAll('tbody tr')).toHaveLength(3)
    expect(w.get('tbody').text()).toContain('Abgelehnt')
    expect(w.findAll('button').some((b) => b.text() === 'Ältere Einträge laden')).toBe(false)

    const inputs = w.findAll('input')
    await inputs[0]!.setValue('bob')
    await inputs[1]!.setValue('auth.*')
    await w.get('select').setValue('failure')
    await inputs[3]!.setValue('2026-11-01T00:00')
    await w.get('form').trigger('submit')
    await flushPromises()
    const last = queries[queries.length - 1] as Record<string, unknown>
    expect(last).toMatchObject({ actor: 'bob', action: 'auth.*', result: 'failure', limit: 50 })
    expect(last.since).toMatch(/^2026-1[01]-\d\dT\d\d:00:00\.000Z$/)
    expect(last.before).toBeUndefined()
  })
})

describe('SessionsView', () => {
  const list = {
    sessions: [
      {
        id: 's1',
        username: 'alice',
        displayName: 'Alice',
        source: 'ldap',
        stage: 'authenticated',
        createdAt: '2026-11-13T09:00:00Z',
        lastSeenAt: '2026-11-13T10:00:00Z',
        expiresAt: '2026-11-13T17:00:00Z',
        remoteAddr: '192.0.2.1',
        userAgent: 'Firefox',
        current: true,
      },
      {
        id: 's2',
        username: 'bob',
        displayName: '',
        source: 'ldap',
        stage: 'totp',
        createdAt: '2026-11-13T09:30:00Z',
        lastSeenAt: '2026-11-13T09:30:00Z',
        expiresAt: '2026-11-13T17:30:00Z',
        remoteAddr: '192.0.2.2',
        userAgent: 'curl',
        current: false,
      },
    ],
  }

  async function mountAs(permissions: string[], roles: string[]) {
    api.on('GET /api/v1/admin/sessions', () => [200, list])
    const w = mountWith(SessionsView, { attachTo: document.body })
    useSessionStore().info = sessionInfo({ permissions, roles })
    await flushPromises()
    return w
  }

  it('lists sessions read-only without write permission', async () => {
    const w = await mountAs(['system:core:sessions:read'], ['viewer'])
    expect(w.findAll('tbody tr')).toHaveLength(2)
    expect(w.get('tbody').text()).toContain('Waiting for code')
    expect(w.findAll('button').some((b) => b.text() === 'Terminate')).toBe(false)
    expect(w.text()).not.toContain('Reset authenticator app')
    w.unmount()
  })

  it('terminates a session after confirmation', async () => {
    const deleted: string[] = []
    api.client.DELETE = (path: string, init?: Init) => {
      deleted.push(`${path} ${String(init?.params?.path?.id)}`)
      return Promise.resolve({ data: undefined, response: new Response(null, { status: 204 }) })
    }
    const w = await mountAs(['system:core:sessions:write'], ['ops'])
    const buttons = w.findAll('tbody button')
    await buttons[1]!.trigger('click')
    expect(w.get('dialog').text()).toContain('Terminate this session of bob?')
    expect(deleted).toEqual([])
    await w
      .findAll('dialog button')
      .find((b) => b.text() === 'Confirm')!
      .trigger('click')
    await flushPromises()
    expect(deleted).toEqual(['/api/v1/admin/sessions/{id} s2'])
    w.unmount()
  })

  it('lets root reset a TOTP enrollment', async () => {
    const deleted: unknown[] = []
    api.client.DELETE = (path: string, init?: Init) => {
      deleted.push([path, init?.params?.path])
      return Promise.resolve({ data: undefined, response: new Response(null, { status: 204 }) })
    }
    const w = await mountAs(['*:*:*:admin'], ['root'])
    const form = w.findAll('form')[1]!
    await form.get('input').setValue('bob')
    await form.trigger('submit')
    await w
      .findAll('dialog button')
      .find((b) => b.text() === 'Confirm')!
      .trigger('click')
    await flushPromises()
    expect(deleted).toEqual([['/api/v1/admin/users/{username}/totp', { username: 'bob' }]])
    expect(useToastStore().toasts.map((x) => x.message)).toContain(
      'The authenticator app of bob was reset.',
    )
    w.unmount()
  })
})
