// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'

const fake = vi.hoisted(() => ({ api: undefined as unknown }))
vi.mock('@/api/client', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/client')>()),
  api: new Proxy({}, { get: (_, prop) => (fake.api as Record<string | symbol, unknown>)[prop] }),
}))

import DocsView from '../DocsView.vue'
import { FakeApi } from '@/__tests__/fakeApi'
import { mountWith } from '@/__tests__/helpers'

let api: FakeApi
beforeEach(() => {
  api = new FakeApi()
  fake.api = api.client
  // jsdom has no layout: scrolling is a no-op.
  Element.prototype.scrollIntoView = vi.fn<() => void>()
  window.scrollTo = vi.fn<() => void>()
  api.on('GET /api/v1/docs', () => [
    200,
    {
      pages: [
        { name: 'user-guide', title: 'User guide' },
        { name: 'security', title: 'Security' },
      ],
    },
  ])
  api.on('GET /api/v1/docs/{page}', (_, params) => {
    const page = (params?.path as { page?: string } | undefined)?.page
    if (page === 'user-guide') {
      return [
        200,
        {
          name: 'user-guide',
          title: 'User guide',
          html:
            '<h1 id="doc-user-guide">User guide</h1>' +
            '<p><a href="/docs/security#doc-ldap">LDAP</a> ' +
            '<a href="#doc-user-guide">top</a> ' +
            '<a href="https://github.com/x" target="_blank" rel="noopener noreferrer">GitHub</a></p>',
        },
      ]
    }
    if (page === 'security') {
      return [200, { name: 'security', title: 'Security', html: '<h2 id="doc-ldap">LDAP</h2>' }]
    }
    return [404, { error: { code: 'not_found', message: 'no such documentation page' } }]
  })
})

async function setup(path: string, locale: 'en' | 'de' = 'en') {
  const router: Router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/docs/:page?', name: 'docs', component: DocsView }],
  })
  await router.push(path)
  const w = mountWith(DocsView, { locale, attachTo: document.body, global: { plugins: [router] } })
  await flushPromises()
  return { router, w }
}

describe('DocsView', () => {
  it('opens the first page and lists all pages', async () => {
    const { router, w } = await setup('/docs')
    await flushPromises()
    expect(router.currentRoute.value.params.page).toBe('user-guide')
    expect(w.get('article').text()).toContain('User guide')
    const nav = w.get('[data-testid="docs-nav"]')
    expect(nav.findAll('a').map((a) => a.text())).toEqual(['User guide', 'Security'])
    expect(nav.get('[aria-current="page"]').text()).toBe('User guide')
    expect(w.text()).not.toContain('only available in English')
    w.unmount()
  })

  it('follows links within the documentation with the router', async () => {
    const { router, w } = await setup('/docs/user-guide')

    // External links are left to the browser (new tab).
    const ev = new MouseEvent('click', { bubbles: true, cancelable: true })
    w.get('a[target="_blank"]').element.dispatchEvent(ev)
    expect(ev.defaultPrevented).toBe(false)

    await w.get('a[href="/docs/security#doc-ldap"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/docs/security#doc-ldap')
    expect(w.get('article').html()).toContain('id="doc-ldap"')
    w.unmount()
  })

  it('says that the documentation is in English and reports unknown pages', async () => {
    const { w } = await setup('/docs/nope', 'de')
    expect(w.text()).toContain('nur auf Englisch verfügbar')
    expect(w.find('[role="alert"]').exists()).toBe(true)
    expect(w.find('article').exists()).toBe(false)
    w.unmount()
  })
})
