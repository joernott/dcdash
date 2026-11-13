// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, it, expect } from 'vitest'
import { h } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'

import App from '../App.vue'
import { detectLocale } from '../i18n'
import { mountWith } from './helpers'

describe('App', () => {
  it('renders the route and the toast area', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/', component: { render: () => h('p', 'page') } }],
    })
    await router.push('/')
    const w = mountWith(App, { global: { plugins: [router] } })
    expect(w.text()).toContain('page')
    expect(w.find('[aria-live="polite"]').exists()).toBe(true)
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
