// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'

const fake = vi.hoisted(() => ({ api: undefined as unknown }))
vi.mock('@/api/client', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/client')>()),
  api: new Proxy({}, { get: (_, prop) => (fake.api as Record<string | symbol, unknown>)[prop] }),
}))

import FirewallsView from '../firewalls/FirewallsView.vue'
import { useSessionStore } from '@/stores/session'
import { useToastStore } from '@/stores/toast'
import { FakeApi, sessionInfo } from '@/__tests__/fakeApi'
import { mountWith } from '@/__tests__/helpers'

let api: FakeApi
beforeEach(() => {
  api = new FakeApi()
  fake.api = api.client
})

function lastToast(): string | undefined {
  const l = useToastStore().toasts
  return l[l.length - 1]?.message
}

const firewalls = [
  {
    name: 'fw1',
    type: 'opnsense',
    url: 'https://fw1.example.org',
    host: 'fw1.example.org',
    status: 'ok',
    product: 'OPNsense',
    version: '26.1.6',
    uptime: '3 days, 04:05:06',
    memoryUsed: 2147483648,
    memoryTotal: 8589934592,
  },
]

const rule = {
  uuid: 'r-1',
  enabled: true,
  sequence: 10,
  action: 'pass',
  quick: true,
  interface: 'lan',
  direction: 'in',
  ipProtocol: 'inet',
  protocol: 'TCP',
  source: 'any',
  sourceNot: false,
  sourcePort: '',
  destination: 'webservers',
  destinationNot: false,
  destinationPort: '443',
  log: false,
  description: 'web',
  legacy: false,
  canWrite: true,
}

async function setup(path: string) {
  const router: Router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/firewalls/:firewall?', name: 'firewalls', component: FirewallsView }],
  })
  api.on('GET /api/v1/firewalls', () => [200, { firewalls }])
  api.on('GET /api/v1/firewalls/{firewall}/health', () => [
    200,
    {
      canOperate: true,
      gateways: [
        { name: 'WAN2', address: '203.0.113.1', status: 'down', detail: 'down' },
        {
          name: 'WAN_DHCP',
          address: '198.51.100.1',
          status: 'online',
          detail: 'none',
          delay: '1.2 ms',
          loss: '0.0 %',
        },
      ],
      services: [
        {
          id: 'unbound',
          name: 'unbound',
          description: 'Unbound DNS',
          running: true,
          locked: false,
        },
      ],
    },
  ])
  api.on('GET /api/v1/firewalls/{firewall}/vpn', () => [
    200,
    {
      peers: [
        {
          uuid: 'p-1',
          name: 'laptop-alice',
          enabled: true,
          publicKey: 'pubA=',
          allowedIps: '10.10.0.2/32',
          latestHandshake: new Date(Date.now() - 60_000).toISOString(),
          rxBytes: 1536,
          txBytes: 2048,
          status: 'online',
          canOperate: true,
        },
      ],
      tunnels: [
        {
          name: 'con1',
          description: 'Site B',
          connected: false,
          bytesIn: 0,
          bytesOut: 0,
          children: [],
          canOperate: true,
        },
      ],
      errors: {},
    },
  ])
  api.on('GET /api/v1/firewalls/{firewall}/rules', () => [
    200,
    {
      canCreate: true,
      rules: [
        { ...rule, uuid: '', legacy: true, canWrite: false, description: 'Default allow LAN' },
        rule,
      ],
    },
  ])
  await router.push(path)
  const w = mountWith(FirewallsView, { attachTo: document.body, global: { plugins: [router] } })
  useSessionStore().info = sessionInfo({ permissions: ['firewall:*:*:admin'], roles: ['test'] })
  await flushPromises()
  return { w, router }
}

describe('FirewallsView', () => {
  it('shows the health and restarts a service', async () => {
    const { w, router } = await setup('/firewalls')
    expect(router.currentRoute.value.params.firewall).toBe('fw1')
    expect(w.text()).toContain('OPNsense 26.1.6')
    expect(w.text()).toContain('2.0 GiB of 8.0 GiB')
    expect(w.get('a[href="ssh://fw1.example.org"]').text()).toBe('ssh')
    const gws = w.findAll('table')[0]!.findAll('tbody tr')
    expect(gws[0]!.text()).toContain('Down')
    expect(gws[1]!.text()).toContain('1.2 ms · 0.0 %')

    let restarted: unknown
    api.on('POST /api/v1/firewalls/{firewall}/services/restart', (body) => {
      restarted = body
      return [204, null]
    })
    await w.get('button[aria-label="Restart unbound"]').trigger('click')
    const dialog = w.findAll('dialog').find((d) => d.text().includes('Restart unbound?'))!
    await dialog
      .findAll('button')
      .find((b) => b.text() === 'Restart')!
      .trigger('click')
    await flushPromises()
    expect(restarted).toEqual({ id: 'unbound' })
    expect(lastToast()).toBe('unbound was restarted.')
    w.unmount()
  })

  it('disables a WireGuard peer and restarts a tunnel', async () => {
    const { w } = await setup('/firewalls/fw1?tab=vpn')
    expect(w.text()).toContain('1 min ago')
    expect(w.text()).toContain('↓ 1.5 KiB · ↑ 2.0 KiB')
    let body: unknown
    api.on('PUT /api/v1/firewalls/{firewall}/wireguard/{uuid}', (b) => {
      body = b
      return [200, {}]
    })
    await w.get('button[aria-label="Disable laptop-alice"]').trigger('click')
    await flushPromises()
    expect(body).toEqual({ enabled: false })
    expect(lastToast()).toBe('laptop-alice was disabled.')
    api.on('POST /api/v1/firewalls/{firewall}/ipsec/{name}/restart', () => [204, null])
    await w.get('button[aria-label="Restart tunnel con1"]').trigger('click')
    await flushPromises()
    expect(lastToast()).toBe('Site B is being restarted.')
    w.unmount()
  })

  it('applies rule changes and reports a rollback', async () => {
    const { w } = await setup('/firewalls/fw1?tab=rules')
    const rows = w.get('tbody').findAll('tr')
    expect(rows).toHaveLength(2)
    expect(rows[0]!.text()).toContain('read-only')
    expect(rows[0]!.findAll('button')).toHaveLength(0)
    expect(rows[1]!.text()).toContain('webservers:443')

    api.on('PUT /api/v1/firewalls/{firewall}/rules/{uuid}', () => [
      200,
      { kept: false, rolledBack: true },
    ])
    await w.get('button[aria-label="Change rule web"]').trigger('click')
    const dialog = w.findAll('dialog').find((d) => d.text().includes('Change the rule'))!
    await dialog.get('form').trigger('submit')
    await flushPromises()
    expect(lastToast()).toBe(
      'After the change, dcdash could not reach fw1. OPNsense restores the previous rules within a minute.',
    )
    const sent = api.calls.find((c) => c.route.startsWith('PUT'))!.body as Record<string, unknown>
    expect(sent.destinationPort).toBe('443')
    expect(sent).not.toHaveProperty('uuid')

    api.on('POST /api/v1/firewalls/{firewall}/rules', () => [
      200,
      { kept: true, rolledBack: false },
    ])
    await w
      .findAll('button')
      .find((b) => b.text() === 'Create rule')!
      .trigger('click')
    const create = w.findAll('dialog').find((d) => d.text().includes('Create a rule'))!
    await create.get('form').trigger('submit')
    await flushPromises()
    expect(lastToast()).toBe('The rules were applied.')
    w.unmount()
  })

  it('shows the rules read-only without savepoints', async () => {
    const { w, router } = await setup('/firewalls/fw1')
    api.on('GET /api/v1/firewalls/{firewall}/rules', () => [
      200,
      { canCreate: false, noSavepoint: true, rules: [{ ...rule, canWrite: false }] },
    ])
    await router.push('/firewalls/fw1?tab=rules')
    await flushPromises()
    expect(w.get('[role="note"]').text()).toContain('no savepoints (removed in 26.7)')
    expect(w.findAll('button').some((b) => b.text() === 'Create rule')).toBe(false)
    expect(w.get('tbody').findAll('button')).toHaveLength(0)
    w.unmount()
  })
})
