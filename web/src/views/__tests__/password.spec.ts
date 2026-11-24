// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { defineComponent, h } from 'vue'

const fake = vi.hoisted(() => ({ api: undefined as unknown }))
vi.mock('@/api/client', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/client')>()),
  api: new Proxy({}, { get: (_, prop) => (fake.api as Record<string | symbol, unknown>)[prop] }),
}))

import PasswordView from '../PasswordView.vue'
import { FakeApi, apiError } from '@/__tests__/fakeApi'
import { mountWith } from '@/__tests__/helpers'

let api: FakeApi
beforeEach(() => {
  api = new FakeApi()
  fake.api = api.client
})

const Dummy = defineComponent({ render: () => h('div') })
function router() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/password', name: 'password', component: Dummy },
      { path: '/login', name: 'login', component: Dummy },
    ],
  })
}

const policy = {
  minLength: 7,
  complexity: true,
  history: 24,
  minAge: 0,
  maxAge: 0,
  lockoutThreshold: 5,
  lockoutDuration: 1800,
}

async function mountPage(directories: unknown[], path = '/password') {
  api.on('GET /api/v1/password/directories', () => [200, { directories }])
  const r = router()
  await r.push(path)
  const w = mountWith(PasswordView, { attachTo: document.body, global: { plugins: [r] } })
  await flushPromises()
  return w
}

async function fill(w: Awaited<ReturnType<typeof mountPage>>, next: string, confirm = next) {
  const inputs = w.findAll('input')
  await inputs[0]!.setValue('alice')
  await inputs[1]!.setValue('Old-12345')
  await inputs[2]!.setValue(next)
  await inputs[3]!.setValue(confirm)
}

describe('PasswordView', () => {
  it('changes the password and shows the policy', async () => {
    const w = await mountPage([{ name: 'lab', domain: 'ad.test', policy }])
    expect(w.text()).toContain('ad.test')
    expect(w.text()).toContain('At least 7 characters')
    expect(w.text()).toContain('your last 24 passwords')
    let sent: unknown
    api.on('POST /api/v1/password/change', (b) => {
      sent = b
      return [204, null]
    })
    await fill(w, 'New-12345')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(sent).toEqual({
      directory: 'lab',
      username: 'alice',
      currentPassword: 'Old-12345',
      newPassword: 'New-12345',
    })
    expect(w.get('[role=status]').text()).toContain('Your password was changed')
    // The fields are cleared.
    expect((w.findAll('input')[1]!.element as HTMLInputElement).value).toBe('')
  })

  it('checks the new password before sending it', async () => {
    const w = await mountPage([{ name: 'lab', domain: 'ad.test', policy }])
    await fill(w, 'New-12345', 'New-99999')
    expect(w.text()).toContain("The new passwords don't match.")
    await fill(w, 'short')
    expect(w.text()).toContain('At least 7 characters')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(api.calls.some((c) => c.route.startsWith('POST'))).toBe(false)
  })

  it('explains errors', async () => {
    const w = await mountPage([
      { name: 'lab', domain: 'ad.test', policy },
      { name: 'corp', domain: 'corp.test' },
    ])
    expect(w.find('form select').exists()).toBe(true)
    for (const [code, text] of [
      ['invalid_credentials', 'The user name or the current password is wrong.'],
      ['password_policy_history', 'You used the new password before.'],
      ['too_many_attempts', 'Too many failed attempts.'],
    ] as const) {
      api.on('POST /api/v1/password/change', () => [401, apiError(code)])
      await fill(w, 'New-12345')
      await w.get('form').trigger('submit')
      await flushPromises()
      expect(w.get('[role=alert]').text()).toContain(text)
    }
  })

  it('preselects the domain chosen on the login page', async () => {
    const dirs = [
      { name: 'lab', domain: 'ad.test', policy },
      { name: 'corp', domain: 'corp.test' },
    ]
    let w = await mountPage(dirs, '/password?directory=corp')
    expect((w.get('form select').element as HTMLSelectElement).value).toBe('corp')
    w.unmount()
    // An unknown name falls back to the first domain.
    w = await mountPage(dirs, '/password?directory=nope')
    expect((w.get('form select').element as HTMLSelectElement).value).toBe('lab')
  })

  it('tells when no domain offers the change', async () => {
    api.on('GET /api/v1/password/directories', () => [404, apiError('not_found')])
    const r = router()
    await r.push('/password')
    const w = mountWith(PasswordView, { global: { plugins: [r] } })
    await flushPromises()
    expect(w.text()).toContain('No domain offers the password change here.')
    expect(w.find('form').exists()).toBe(false)
  })
})
