// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'

const fake = vi.hoisted(() => ({ api: undefined as unknown }))
vi.mock('@/api/client', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/client')>()),
  api: new Proxy({}, { get: (_, prop) => (fake.api as Record<string | symbol, unknown>)[prop] }),
}))

import HomeView from '../HomeView.vue'
import { FakeApi } from '@/__tests__/fakeApi'
import { mountWith } from '@/__tests__/helpers'

let api: FakeApi
beforeEach(() => {
  api = new FakeApi()
  fake.api = api.client
})

const report = {
  checkedAt: '2026-11-13T10:00:00Z',
  components: [
    { name: 'pve-a', kind: 'proxmox', status: 'ok', detail: '3 nodes', latencyMs: 12 },
    { name: 'database', kind: 'system', status: 'ok', detail: '', latencyMs: 1 },
    {
      name: 'directory',
      kind: 'system',
      status: 'error',
      detail: 'connection refused',
      latencyMs: 5000,
    },
  ],
}

describe('HomeView', () => {
  it('shows a tile per component, grouped with dcdash first', async () => {
    api.on('GET /api/v1/status', () => [200, report])
    const w = mountWith(HomeView, { locale: 'de' })
    await flushPromises()
    expect(w.findAll('h2').map((h) => h.text())).toEqual(['dcdash', 'Proxmox'])
    expect(w.findAll('h3').map((h) => h.text())).toEqual([
      'Datenbank',
      'Benutzerverzeichnis',
      'pve-a',
    ])
    expect(w.get('[role="status"]').text()).toContain('Eine Komponente hat ein Problem.')
    const directory = w.findAll('li').find((li) => li.text().includes('Benutzerverzeichnis'))!
    expect(directory.text()).toContain('connection refused')
    expect(directory.text()).toContain('5.000 ms')
    expect(directory.classes()).toContain('border-danger')
  })

  it('shows errors and refreshes', async () => {
    api.on('GET /api/v1/status', () => [500, { error: { code: 'internal_error', message: 'x' } }])
    const w = mountWith(HomeView)
    await flushPromises()
    expect(w.get('[role="alert"]').text()).toContain('internal error')
    api.on('GET /api/v1/status', () => [200, report])
    await w.get('button').trigger('click')
    await flushPromises()
    expect(w.find('[role="alert"]').exists()).toBe(false)
    expect(w.get('[role="status"]').text()).toContain('One component has a problem.')
  })
})
