// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'

import App from '../App.vue'
import { createAppI18n, detectLocale, type Locale } from '../i18n'

function mountApp(locale: Locale) {
  const router = createRouter({ history: createMemoryHistory(), routes: [] })
  return mount(App, { global: { plugins: [router, createAppI18n(locale)] } })
}

describe('App', () => {
  it('renders in English', () => {
    expect(mountApp('en').text()).toContain('Dashboard for central data center services')
  })

  it('renders in German', () => {
    expect(mountApp('de').text()).toContain('Dashboard für zentrale Rechenzentrumsdienste')
  })
})

describe('detectLocale', () => {
  it('picks the first supported browser language', () => {
    expect(detectLocale(['fr-FR', 'de-AT', 'en'])).toBe('de')
    expect(detectLocale(['en-GB'])).toBe('en')
  })

  it('falls back to English', () => {
    expect(detectLocale(['fr', 'es'])).toBe('en')
    expect(detectLocale([])).toBe('en')
  })
})
