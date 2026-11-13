// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { createMemoryHistory, type Router } from 'vue-router'

const fake = vi.hoisted(() => ({ api: undefined as unknown }))
vi.mock('@/api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/client')>()
  return {
    ...actual,
    api: new Proxy({}, { get: (_, prop) => (fake.api as Record<string | symbol, unknown>)[prop] }),
  }
})

import App from '@/App.vue'
import { createAppRouter } from '@/router'
import { useSessionStore } from '@/stores/session'
import { FakeApi, apiError, sessionInfo } from '@/__tests__/fakeApi'
import { memoryStorage, mountWith } from '@/__tests__/helpers'

let api: FakeApi
let router: Router

beforeEach(() => {
  api = new FakeApi()
  fake.api = api.client
  vi.stubGlobal('localStorage', memoryStorage())
  api.on('GET /api/v1/me/preferences', () => [200, { language: '', theme: 'system' }])
})

async function mountAt(path: string, locale: 'en' | 'de' = 'en') {
  router = createAppRouter(createMemoryHistory())
  const w = mountWith(App, { global: { plugins: [router] }, locale, attachTo: document.body })
  await router.push(path)
  await flushPromises()
  return w
}

describe('route guards', () => {
  it('sends anonymous users to the login page with a redirect', async () => {
    api.on('GET /api/v1/auth/me', () => [401, apiError('not_authenticated')])
    await mountAt('/admin/audit')
    expect(router.currentRoute.value.name).toBe('login')
    expect(router.currentRoute.value.query.redirect).toBe('/admin/audit')
  })

  it('blocks pages without permission', async () => {
    api.on('GET /api/v1/auth/me', () => [200, sessionInfo({ roles: [], permissions: [] })])
    const w = await mountAt('/admin/audit')
    expect(router.currentRoute.value.name).toBe('forbidden')
    // The navigation hides what the user may not use.
    expect(w.find('nav').text()).not.toContain('Audit log')
  })

  it('shows permitted pages and navigation entries', async () => {
    api.on('GET /api/v1/auth/me', () => [
      200,
      sessionInfo({ permissions: ['system:core:audit:read'], roles: ['auditor'] }),
    ])
    const w = await mountAt('/admin/audit')
    expect(router.currentRoute.value.name).toBe('audit')
    expect(w.find('nav').text()).toContain('Audit log')
    expect(w.find('nav').text()).not.toContain('Sessions')
  })

  it('sends users who must enroll to the enrollment page', async () => {
    api.on('GET /api/v1/auth/me', () => [200, sessionInfo({ stage: 'totp_enroll' })])
    api.on('POST /api/v1/auth/totp/enroll', () => [200, { secret: 'ABC', uri: 'otpauth://totp/x' }])
    await mountAt('/')
    expect(router.currentRoute.value.name).toBe('enroll')
  })
})

describe('login', () => {
  it('logs in with password and TOTP code and returns to the requested page', async () => {
    api.on('GET /api/v1/auth/me', () => [401, apiError('not_authenticated')])
    api.on('POST /api/v1/auth/login', (body) =>
      (body as { password: string }).password === 'secret'
        ? [200, sessionInfo({ stage: 'totp' })]
        : [401, apiError('invalid_credentials')],
    )
    api.on('POST /api/v1/auth/totp', (body) =>
      (body as { code?: string }).code === '123456'
        ? [200, sessionInfo()]
        : [401, apiError('invalid_code')],
    )
    const w = await mountAt('/login?redirect=/account', 'de')

    const [user, pass] = w.findAll('input')
    await user!.setValue('alice')
    await pass!.setValue('wrong')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(w.get('[role="alert"]').text()).toBe('Benutzername oder Passwort ist falsch.')
    expect((pass!.element as HTMLInputElement).value).toBe('')

    await pass!.setValue('secret')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(w.get('h1').text()).toBe('Bestätigungscode')

    await w.get('input').setValue('000000')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(w.get('[role="alert"]').text()).toContain('Code ist falsch')

    await w.get('input').setValue('123456')
    await w.get('form').trigger('submit')
    await flushPromises()
    await vi.waitFor(() => expect(router.currentRoute.value.fullPath).toBe('/account'))
    expect(useSessionStore().authenticated).toBe(true)
    // The password is never kept in the request log beyond the login call.
    expect(
      api.calls.filter((c) => c.route === 'POST /api/v1/auth/totp').map((c) => c.body),
    ).toEqual([{ code: '000000' }, { code: '123456' }])
  })

  it('accepts a recovery code', async () => {
    api.on('GET /api/v1/auth/me', () => [200, sessionInfo({ stage: 'totp' })])
    api.on('POST /api/v1/auth/totp', () => [200, sessionInfo()])
    const w = await mountAt('/login')
    await w
      .findAll('button')
      .find((b) => b.text() === 'Use a recovery code')!
      .trigger('click')
    await w.get('input').setValue('abcd-efgh-jkmn-pqrs')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(api.calls.find((c) => c.route === 'POST /api/v1/auth/totp')?.body).toEqual({
      recoveryCode: 'abcd-efgh-jkmn-pqrs',
    })
    await vi.waitFor(() => expect(router.currentRoute.value.name).toBe('home'))
  })

  it('ignores external redirect targets', async () => {
    api.on('GET /api/v1/auth/me', () => [200, sessionInfo()])
    await mountAt('/login?redirect=//evil.example/x')
    expect(router.currentRoute.value.fullPath).toBe('/')
  })
})

describe('enrollment', () => {
  it('shows the QR code, confirms and shows the recovery codes once', async () => {
    api.on('GET /api/v1/auth/me', () => [200, sessionInfo({ stage: 'totp_enroll' })])
    api.on('POST /api/v1/auth/totp/enroll', () => [
      200,
      { secret: 'JBSWY3DPEHPK3PXP', uri: 'otpauth://totp/dcdash:alice?secret=JBSWY3DPEHPK3PXP' },
    ])
    api.on('POST /api/v1/auth/totp/enroll/confirm', () => [
      200,
      { recoveryCodes: ['aaaa-bbbb-cccc-dddd', 'eeee-ffff-gggg-hhhh'], session: sessionInfo() },
    ])
    const w = await mountAt('/enroll')
    expect(w.get('svg[role="img"] path').attributes('d')).toMatch(/^M\d/)
    await w
      .findAll('button')
      .find((b) => b.text().startsWith("Can't scan"))!
      .trigger('click')
    expect(w.get('[data-testid="secret"]').text()).toBe('JBSWY3DPEHPK3PXP')

    await w.get('input').setValue('123456')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(w.get('ul[aria-label="Recovery codes"]').text()).toContain('aaaa-bbbb-cccc-dddd')
    await w
      .findAll('button')
      .find((b) => b.text() === 'Continue')!
      .trigger('click')
    await flushPromises()
    await vi.waitFor(() => expect(router.currentRoute.value.name).toBe('home'))
  })
})

describe('session expiry', () => {
  it('logs out and keeps the user on the login page', async () => {
    api.on('GET /api/v1/auth/me', () => [200, sessionInfo()])
    api.on('POST /api/v1/auth/logout', () => [204, undefined])
    const w = await mountAt('/')
    await w
      .findAll('button')
      .find((b) => b.attributes('aria-haspopup'))!
      .trigger('click')
    await w
      .findAll('button')
      .find((b) => b.text() === 'Log out')!
      .trigger('click')
    await flushPromises()
    await vi.waitFor(() => expect(router.currentRoute.value.name).toBe('login'))
    expect(useSessionStore().info).toBeNull()
  })
})
