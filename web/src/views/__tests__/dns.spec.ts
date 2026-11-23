// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'
import { defineComponent, h } from 'vue'

const fake = vi.hoisted(() => ({ api: undefined as unknown }))
vi.mock('@/api/client', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/client')>()),
  api: new Proxy({}, { get: (_, prop) => (fake.api as Record<string | symbol, unknown>)[prop] }),
}))

import DnsView from '../dns/DnsView.vue'
import ZoneView from '../dns/ZoneView.vue'
import { useSessionStore } from '@/stores/session'
import { useToastStore } from '@/stores/toast'
import { FakeApi, sessionInfo, apiError } from '@/__tests__/fakeApi'
import { mountWith, buttonName } from '@/__tests__/helpers'

let api: FakeApi
beforeEach(() => {
  api = new FakeApi()
  fake.api = api.client
})

const Dummy = defineComponent({ render: () => h('div') })

const providers = {
  providers: [
    {
      name: 'cf',
      kind: 'cloudflare',
      capabilities: { proxied: true, comments: true, autoTtl: true, zoneCreate: true },
    },
    {
      name: 'bind',
      kind: 'bind',
      capabilities: { proxied: false, comments: false, autoTtl: false, zoneCreate: false },
    },
  ],
}

const records = {
  version: '',
  records: [
    {
      id: 'soa',
      name: 'example.org',
      type: 'SOA',
      ttl: 3600,
      content: 'ns1.example.org. hostmaster.example.org. 1 3600 900 1209600 300',
      readOnly: true,
      version: 'v1',
    },
    {
      id: 'r1',
      name: 'www.example.org',
      type: 'A',
      ttl: 1,
      content: '192.0.2.1',
      proxied: true,
      version: 'v1',
    },
    {
      id: 'r2',
      name: 'example.org',
      type: 'MX',
      ttl: 300,
      content: '10 mail.example.org.',
      version: 'v1',
    },
  ],
}

async function setup(): Promise<Router> {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/dns/:provider?', name: 'dns', component: Dummy },
      { path: '/dns/:provider/:zone', name: 'dns-zone', component: Dummy },
    ],
  })
  api.on('GET /api/v1/dns/providers', () => [200, providers])
  api.on('GET /api/v1/dns/providers/{provider}/zones', () => [
    200,
    {
      zones: [
        { name: 'example.org', status: 'active' },
        { name: 'new.example', status: 'pending', nameServers: ['ada.ns.cloudflare.com'] },
      ],
    },
  ])
  api.on('GET /api/v1/dns/providers/{provider}/zones/{zone}/records', () => [200, records])
  return router
}

function mountView(
  router: Router,
  permissions: string[],
  component: typeof DnsView,
  locale = 'en',
) {
  const w = mountWith(component, {
    locale: locale as 'en' | 'de',
    attachTo: document.body,
    global: { plugins: [router] },
  })
  useSessionStore().info = sessionInfo({ permissions, roles: ['test'] })
  return w
}

describe('DnsView', () => {
  it('lists the zones and deletes a zone after typing its name', async () => {
    const router = await setup()
    await router.push('/dns')
    let deleted = false
    api.on('DELETE /api/v1/dns/providers/{provider}/zones/{zone}', () => {
      deleted = true
      return [204, null]
    })
    const w = mountView(router, ['dns:*:*:admin'], DnsView)
    await flushPromises()
    expect(router.currentRoute.value.params.provider).toBe('cf')
    await flushPromises()
    expect(w.findAll('tbody tr')).toHaveLength(2)
    // The rows have the Cloudflare color.
    expect(w.get('tbody tr').classes()).toContain('sys-tint')
    expect(w.get('tbody tr').attributes('style')).toContain('--sys: var(--dc-sys-cloudflare)')
    expect(w.text()).toContain('Delegate the zone at the registrar to: ada.ns.cloudflare.com')
    expect(w.text()).toContain('Create zone')

    await w.findAll('tbody button')[0]!.trigger('click')
    const dialog = w.findAll('dialog')[1]!
    const submit = dialog.findAll('button').find((b) => buttonName(b) === 'Delete zone')!
    expect(submit.attributes('disabled')).toBeDefined()
    await dialog.get('input').setValue('example.org')
    expect(submit.attributes('disabled')).toBeUndefined()
    await dialog.get('form').trigger('submit')
    await flushPromises()
    expect(deleted).toBe(true)
    expect(useToastStore().toasts.map((t) => t.message)).toContain('Zone example.org deleted.')
    w.unmount()
  })

  it('shows no zone actions to readers', async () => {
    const router = await setup()
    await router.push('/dns/cf')
    const w = mountView(router, ['dns:cf:*:read'], DnsView)
    await flushPromises()
    expect(w.findAll('tbody tr')).toHaveLength(2)
    expect(w.text()).not.toContain('Create zone')
    expect(w.findAll('tbody button')).toHaveLength(0)
    w.unmount()
  })
})

describe('ZoneView', () => {
  it('lists records and adds one', async () => {
    const router = await setup()
    await router.push('/dns/cf/example.org')
    let body: Record<string, unknown> = {}
    api.on('POST /api/v1/dns/providers/{provider}/zones/{zone}/records', (b) => {
      body = b as Record<string, unknown>
      return [201, { ...records.records[1], id: 'r3', name: 'api.example.org' }]
    })
    const w = mountView(router, ['dns:cf:example.org:write'], ZoneView)
    await flushPromises()
    const rows = w.findAll('tbody tr')
    expect(rows).toHaveLength(3)
    expect(rows[1]!.attributes('style')).toContain('--sys: var(--dc-sys-cloudflare)')
    expect(rows[0]!.text()).toContain('read-only')
    expect(rows[1]!.text()).toContain('www')
    expect(rows[1]!.text()).toContain('proxied')
    expect(rows[1]!.text()).toContain('Automatic')
    expect(rows[2]!.text()).toContain('@')

    await w
      .findAll('button')
      .find((b) => buttonName(b) === 'Add record')!
      .trigger('click')
    const dialog = w.findAll('dialog')[0]!
    const inputs = dialog.findAll('input')
    await inputs[0]!.setValue('api')
    await inputs[1]!.setValue('192.0.2.9')
    await dialog.get('input[type="checkbox"]').setValue(true)
    await dialog.get('form').trigger('submit')
    await flushPromises()
    expect(body).toMatchObject({
      name: 'api',
      type: 'A',
      ttl: 0,
      content: '192.0.2.9',
      proxied: true,
      version: '',
    })
    expect(useToastStore().toasts.map((t) => t.message)).toContain('api (A) saved.')
    w.unmount()
  })

  it('reports a conflict on delete in German', async () => {
    const router = await setup()
    await router.push('/dns/cf/example.org')
    api.on('DELETE /api/v1/dns/providers/{provider}/zones/{zone}/records/{id}', () => [
      409,
      apiError('conflict'),
    ])
    const w = mountView(router, ['dns:cf:*:write'], ZoneView, 'de')
    await flushPromises()
    const loads = api.calls.filter((c) => c.route.endsWith('/records')).length
    await w
      .findAll('tbody tr')[2]!
      .findAll('button')
      .find((b) => buttonName(b) === 'Löschen')!
      .trigger('click')
    const dialog = w.findAll('dialog')[1]!
    expect(dialog.text()).toContain('Den MX-Eintrag @ löschen?')
    await dialog
      .findAll('button')
      .find((b) => buttonName(b) === 'Löschen')!
      .trigger('click')
    await flushPromises()
    expect(dialog.text()).toContain('Die Daten wurden inzwischen geändert.')
    expect(api.calls.filter((c) => c.route.endsWith('/records')).length).toBe(loads + 1)
    w.unmount()
  })

  it('is read-only for readers', async () => {
    const router = await setup()
    await router.push('/dns/cf/example.org')
    const w = mountView(router, ['dns:cf:*:read'], ZoneView)
    await flushPromises()
    expect(w.findAll('tbody tr')).toHaveLength(3)
    expect(w.text()).not.toContain('Add record')
    expect(w.findAll('tbody button')).toHaveLength(0)
    w.unmount()
  })
})
