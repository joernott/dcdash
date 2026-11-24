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
    {
      name: 'pve-a',
      kind: 'proxmox',
      status: 'ok',
      detail: '3 nodes',
      latencyMs: 12,
      links: { ui: 'https://pve-a.example.com:8006', ssh: 'ssh://root@pve-a.example.com' },
    },
    { name: 'database', kind: 'system', status: 'ok', detail: '', latencyMs: 1 },
    {
      name: 'directory',
      kind: 'system',
      status: 'error',
      detail: 'connection refused',
      latencyMs: 5000,
      links: { rdp: 'dc1.example.com' },
    },
  ],
}

describe('HomeView', () => {
  it('shows a colored tile per component, dcdash first, with links', async () => {
    api.on('GET /api/v1/status', () => [200, report])
    const w = mountWith(HomeView, { locale: 'de' })
    await flushPromises()
    expect(w.findAll('h2').map((h) => h.text())).toEqual([
      'Datenbank',
      'Benutzerverzeichnis',
      'pve-a',
    ])
    expect(w.get('[role="status"]').text()).toContain('Eine Komponente hat ein Problem.')
    const tiles = w.findAll('li')
    const directory = tiles.find((li) => li.text().includes('Benutzerverzeichnis'))!
    expect(directory.text()).toContain('connection refused')
    expect(directory.text()).toContain('5.000 ms')
    expect(directory.classes()).toContain('border-danger')
    expect(directory.classes()).not.toContain('sys-tint')
    expect(
      directory.get('button[title="Remotedesktop-Datei für directory herunterladen"]').text(),
    ).toBe('RDP: Remotedesktop-Datei für directory herunterladen')

    const pve = tiles.find((li) => li.text().includes('pve-a'))!
    expect(pve.classes()).toContain('sys-tint')
    expect(pve.attributes('data-system')).toBe('proxmox')
    expect(pve.attributes('style')).toContain('--sys: var(--dc-sys-proxmox)')
    expect(pve.text()).toMatch(/Proxmox · \D*12 ms/)
    expect(pve.get('a[title="Weboberfläche von pve-a öffnen"]').attributes()).toMatchObject({
      href: 'https://pve-a.example.com:8006',
      target: '_blank',
    })
    expect(pve.get('a[title="Mit SSH zu pve-a verbinden"]').attributes('href')).toBe(
      'ssh://root@pve-a.example.com',
    )
    expect(pve.find('button[title^="Remotedesktop"]').exists()).toBe(false)
    // Heading, links and status share one header row.
    const header = pve.get('h2').element.parentElement!
    expect(header.querySelector('a[href^="ssh://"]')).not.toBeNull()
    expect(header.textContent).toContain('OK')
  })

  it('translates coded details', async () => {
    api.on('GET /api/v1/status', () => [
      200,
      {
        checkedAt: '2026-11-13T10:00:00Z',
        components: [
          {
            name: 'certificate',
            kind: 'system',
            status: 'ok',
            detail: 'valid until 2027-02-01T12:00:00Z',
            detailCode: 'certificate.valid',
            detailParams: { until: '2027-02-01T12:00:00Z' },
            latencyMs: 0,
          },
          {
            name: 'cf',
            kind: 'dns',
            type: 'cloudflare',
            status: 'ok',
            detail: 'Cloudflare, 1 zones',
            detailCode: 'dns.cloudflare',
            detailParams: { count: '1' },
            latencyMs: 3,
          },
          {
            name: 'kea',
            kind: 'dhcp',
            status: 'error',
            detail: 'HA partner kea-b not in touch',
            detailCode: 'dhcp.partnerOutOfTouch',
            detailParams: { partner: 'kea-b' },
            latencyMs: 3,
          },
          {
            name: 'directory',
            kind: 'system',
            status: 'ok',
            detail: 'connected to dc1:636; not reachable: ...',
            detailCode: 'directory.degraded',
            detailParams: { server: 'dc1:636', down: 'dc2:636' },
            latencyMs: 3,
          },
          {
            name: 'directory2',
            kind: 'system',
            status: 'ok',
            detail: 'connected to dc1:636',
            detailCode: 'directory.connected',
            detailParams: { server: 'dc1:636', count: '2' },
            latencyMs: 3,
          },
          {
            name: 'future',
            kind: 'dhcp',
            status: 'ok',
            detail: 'English fallback',
            detailCode: 'unknown.code',
            latencyMs: 3,
          },
        ],
      },
    ])
    const w = mountWith(HomeView, { locale: 'de' })
    await flushPromises()
    const text = w.text()
    expect(text).toMatch(/Gültig bis 01\.02\.2027, \d\d:00/)
    expect(text).toContain('Cloudflare, eine Zone')
    expect(text).toContain('Keine Verbindung zum HA-Partner kea-b')
    expect(text).toContain('English fallback')
    expect(text).toContain('Verbunden mit dc1:636; nicht erreichbar: dc2:636')
    expect(text).toContain('Verbunden mit dc1:636, alle 2 Server erreichbar')
    // Every DNS tile has the DNS color; the bottom line still names Cloudflare.
    const cf = w.findAll('li').find((li) => li.text().includes('Cloudflare, eine Zone'))!
    expect(cf.attributes('data-system')).toBe('dns')
    expect(cf.attributes('style')).toContain('--sys: var(--dc-sys-dns)')
    expect(cf.text()).toMatch(/Cloudflare · /)
  })

  it('shows errors and refreshes', async () => {
    api.on('GET /api/v1/status', () => [500, { error: { code: 'internal_error', message: 'x' } }])
    const w = mountWith(HomeView)
    await flushPromises()
    expect(w.get('[role="alert"]').text()).toContain('internal error')
    api.on('GET /api/v1/status', () => [200, report])
    await w.get('button[aria-label="Refresh"], button:not([aria-label])').trigger('click')
    await flushPromises()
    expect(w.find('[role="alert"]').exists()).toBe(false)
    expect(w.get('[role="status"]').text()).toContain('One component has a problem.')
  })
})
