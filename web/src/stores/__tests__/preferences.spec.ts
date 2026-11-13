// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

const GET = vi.fn<(...a: unknown[]) => unknown>()
const PUT = vi.fn<(...a: unknown[]) => unknown>()
vi.mock('@/api/client', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/client')>()),
  api: { GET: (...a: unknown[]) => GET(...a), PUT: (...a: unknown[]) => PUT(...a) },
}))

import { readFileSync } from 'node:fs'
import { join } from 'node:path'

import { initialLocale, usePreferencesStore } from '../preferences'
import { memoryStorage } from '@/__tests__/helpers'

let systemDark = false
const listeners: (() => void)[] = []

function ok(data: unknown) {
  return Promise.resolve({ data, response: new Response(null, { status: 200 }) })
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.stubGlobal('localStorage', memoryStorage())
  document.documentElement.className = ''
  systemDark = false
  listeners.length = 0
  GET.mockReset()
  PUT.mockReset()
  vi.stubGlobal('matchMedia', () => ({
    get matches() {
      return systemDark
    },
    addEventListener: (_: string, fn: () => void) => listeners.push(fn),
  }))
})

const dark = () => document.documentElement.classList.contains('dark')

describe('preferences store', () => {
  it('follows the system theme and its changes', () => {
    const store = usePreferencesStore()
    store.init(() => {})
    expect(dark()).toBe(false)
    systemDark = true
    listeners.forEach((fn) => fn())
    expect(dark()).toBe(true)
  })

  it('applies and caches the theme and language without saving before login', async () => {
    const locales: string[] = []
    const store = usePreferencesStore()
    store.init((l) => locales.push(l))
    await store.setTheme('dark')
    await store.setLanguage('de')
    expect(dark()).toBe(true)
    expect(locales[locales.length - 1]).toBe('de')
    expect(document.documentElement.lang).toBe('de')
    expect(localStorage.getItem('dcdash.theme')).toBe('dark')
    expect(initialLocale()).toBe('de')
    expect(PUT).not.toHaveBeenCalled()

    // The next page load starts with the cached values.
    setActivePinia(createPinia())
    expect(usePreferencesStore().theme).toBe('dark')
  })

  it('loads the server preferences after login and saves changes', async () => {
    GET.mockReturnValue(ok({ language: 'de', theme: 'light' }))
    PUT.mockImplementation((...a: unknown[]) => ok((a[1] as { body: unknown }).body))
    systemDark = true
    const locales: string[] = []
    const store = usePreferencesStore()
    store.init((l) => locales.push(l))
    expect(dark()).toBe(true)

    await store.load()
    expect(dark()).toBe(false)
    expect(locales[locales.length - 1]).toBe('de')
    expect(store.synced).toBe(true)

    await store.setTheme('system')
    expect(PUT).toHaveBeenCalledWith('/api/v1/me/preferences', {
      body: { language: 'de', theme: 'system' },
    })
    expect(dark()).toBe(true)

    store.reset()
    await store.setTheme('dark')
    expect(PUT).toHaveBeenCalledTimes(1)
  })

  it('uses the browser language when none is chosen', () => {
    vi.stubGlobal('navigator', { languages: ['de-DE', 'en'] })
    const locales: string[] = []
    usePreferencesStore().init((l) => locales.push(l))
    expect(locales).toEqual(['de'])
  })
})

describe('theme-init.js', () => {
  // The script runs before the app; it must agree with the store.
  const script = readFileSync(join(process.cwd(), 'public/theme-init.js'), 'utf8')
  const run = () => new Function(script)()

  it.each([
    ['dark', false, true],
    ['light', true, false],
    ['system', true, true],
    ['', false, false],
  ])('theme %j with system dark %s gives dark=%s', (theme, sysDark, want) => {
    if (theme) localStorage.setItem('dcdash.theme', theme)
    systemDark = sysDark
    run()
    expect(dark()).toBe(want)
  })
})
