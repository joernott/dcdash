// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'

const fake = vi.hoisted(() => ({ api: undefined as unknown }))
vi.mock('@/api/client', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/client')>()),
  api: new Proxy({}, { get: (_, prop) => (fake.api as Record<string | symbol, unknown>)[prop] }),
}))

import SwitchesView from '../switches/SwitchesView.vue'
import { useSessionStore } from '@/stores/session'
import { useToastStore } from '@/stores/toast'
import { FakeApi, sessionInfo, apiError } from '@/__tests__/fakeApi'
import { mountWith } from '@/__tests__/helpers'

let api: FakeApi
beforeEach(() => {
  api = new FakeApi()
  fake.api = api.client
})

function last<T>(l: T[]): T | undefined {
  return l[l.length - 1]
}

function lastToast(): string | undefined {
  const l = useToastStore().toasts
  return l[l.length - 1]?.message
}

const switches = [
  {
    name: 'sw1',
    type: 'aoscx',
    status: 'ok',
    hostname: 'sw-test',
    model: '6000 24G 4SFP Swch',
    platform: '6000',
    firmware: 'PL.10.08.1010',
    apiVersion: 'v10.08',
    booted: '2026-11-01T00:00:00Z',
    members: [{ name: '1', product: '6000 24G 4SFP Swch', serial: 'CN1', partNumber: 'R8N88A' }],
  },
  { name: 'sw2', type: 'aoscx', status: 'unavailable', error: 'switch not reachable', members: [] },
]

function port(name: string, extra: Record<string, unknown> = {}) {
  return {
    name,
    type: 'system',
    description: '',
    enabled: true,
    adminUp: true,
    linkUp: true,
    speedMbps: 1000,
    mode: 'access',
    vlan: 10,
    trunkVlans: [],
    allVlans: false,
    neighbors: [],
    uplink: false,
    canOperate: true,
    canWrite: true,
    ...extra,
  }
}

const ports = {
  switch: 'sw1',
  ports: [
    port('1/1/1', {
      mode: 'trunk',
      vlan: 1,
      allVlans: true,
      uplink: true,
      description: 'uplink',
      neighbors: [
        {
          chassisId: 'c',
          portId: '1/1/49',
          systemName: 'core-sw',
          capabilities: ['Bridge'],
          bridge: true,
        },
      ],
    }),
    port('1/1/2', { description: 'app1' }),
    port('1/1/4', { enabled: false, linkUp: false, speedMbps: 0 }),
  ],
  vlans: [
    { id: 1, name: 'DEFAULT_VLAN_1', type: 'default', adminUp: true, operUp: true, canWrite: true },
    { id: 10, name: 'servers', type: 'static', adminUp: true, operUp: true, canWrite: true },
    { id: 4000, name: 'test', type: 'static', adminUp: true, operUp: true, canWrite: true },
  ],
}

async function setup(path = '/switches', list: unknown[] = switches) {
  const router: Router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/switches/:switch?', name: 'switches', component: SwitchesView }],
  })
  api.on('GET /api/v1/switches', () => [200, { switches: list }])
  api.on('GET /api/v1/switches/{switch}/vlans', () => [
    200,
    {
      switch: 'sw1',
      vlans: ports.vlans,
      usage: { '1': ['1/1/1'], '10': ['1/1/2'] },
      canCreate: true,
    },
  ])
  api.on('GET /api/v1/switches/{switch}/ports', () => [200, ports])
  api.on('GET /api/v1/projects', () => [200, { projects: [] }])
  api.on('GET /api/v1/netbox', () => [404, apiError('not_found')])
  await router.push(path)
  const w = mountWith(SwitchesView, { attachTo: document.body, global: { plugins: [router] } })
  useSessionStore().info = sessionInfo({ permissions: ['switch:*:*:admin'], roles: ['test'] })
  await flushPromises()
  return { w, router }
}

describe('SwitchesView', () => {
  it('shows the inventory and the ports and changes a port', async () => {
    const { w, router } = await setup()
    expect(router.currentRoute.value.params.switch).toBe('sw1')
    expect(w.text()).toContain('AOS-CX PL.10.08.1010 (v10.08)')
    expect(w.text()).toContain('serial number CN1')
    const rows = () => w.get('tbody').findAll('tr')
    expect(rows()).toHaveLength(3)
    expect(rows()[0]!.text()).toContain('uplink')
    expect(rows()[0]!.text()).toContain('1 + all')
    expect(rows()[0]!.text()).toContain('core-sw')
    expect(rows()[2]!.text()).toContain('Disabled')

    // The filter matches descriptions.
    await w.get('input[inputmode="search"]').setValue('app')
    expect(rows()).toHaveLength(1)
    await w.get('input[inputmode="search"]').setValue('')

    let sent: unknown
    api.on('PUT /api/v1/switches/{switch}/ports/{port}', (body, p) => {
      sent = { body, port: p?.path?.port }
      return [
        200,
        {
          saved: true,
          port: port('1/1/2', { description: 'app2', mode: 'trunk', trunkVlans: [10, 4000] }),
        },
      ]
    })
    await w.get('button[aria-label="Change port 1/1/2"]').trigger('click')
    const dialog = w.get('dialog')
    await dialog.findAll('input')[1]!.setValue('app2')
    await dialog.findAll('select')[0]!.setValue('trunk')
    await dialog.get('input[type="checkbox"]:not([disabled])').setValue(true)
    const allBox = dialog
      .findAll('input[type="checkbox"]')
      .find((c) => c.element.parentElement?.textContent?.includes('Allow all'))!
    await allBox.setValue(false)
    await last(dialog.findAll('input'))!.setValue('10, 4000')
    await dialog.get('form').trigger('submit')
    await flushPromises()
    expect(sent).toEqual({
      body: { description: 'app2', mode: 'trunk', vlan: 10, trunkVlans: [10, 4000] },
      port: '1/1/2',
    })
    expect(lastToast()).toBe('Port 1/1/2 was changed.')
    expect(rows()[1]!.text()).toContain('10 + 4000')
    w.unmount()
  })

  it('asks before changing an uplink and reports a failed save', async () => {
    const { w } = await setup('/switches/sw1')
    api.on('PUT /api/v1/switches/{switch}/ports/{port}', () => [
      200,
      {
        saved: false,
        saveError: 'switch not reachable',
        port: port('1/1/1', { enabled: false, uplink: true }),
      },
    ])
    await w.get('button[aria-label="Change port 1/1/1"]').trigger('click')
    const dialog = w.get('dialog')
    expect(dialog.text()).toContain('This port leads to another switch')
    const save = dialog.findAll('button').find((b) => b.text() === 'Save')!
    expect(save.attributes('disabled')).toBeDefined()
    await dialog.findAll('input[type="checkbox"]')[0]!.setValue(false)
    await last(dialog.findAll('input[type="checkbox"]'))!.setValue(true)
    await dialog.get('form').trigger('submit')
    await flushPromises()
    expect(api.calls.filter((c) => c.route.startsWith('PUT')).map((c) => c.body)).toEqual([
      { enabled: false },
    ])
    expect(lastToast()).toBe(
      'The change is active, but it could not be saved to the startup configuration: switch not reachable',
    )
    w.unmount()
  })

  it('manages VLANs on several switches', async () => {
    const twoOk = [switches[0], { ...switches[0], name: 'sw3', hostname: 'sw-three' }]
    const { w } = await setup('/switches/sw1?tab=vlans', twoOk)
    const rows = () => w.get('tbody').findAll('tr')
    expect(rows()).toHaveLength(3)
    // Only unused VLANs other than VLAN 1 can be deleted.
    expect(
      w
        .get('tbody')
        .findAll('button')
        .filter((b) => b.text() === 'Delete'),
    ).toHaveLength(1)

    const created: string[] = []
    api.on('POST /api/v1/switches/{switch}/vlans', (body, p) => {
      created.push(`${String(p?.path?.switch)}:${JSON.stringify(body)}`)
      return p?.path?.switch === 'sw3'
        ? [409, apiError('exists')]
        : [
            201,
            {
              saved: true,
              vlan: { id: 4001, name: 'lab', type: 'static', adminUp: true, operUp: true },
            },
          ]
    })
    await w
      .findAll('button')
      .find((b) => b.text() === 'Create VLAN')!
      .trigger('click')
    const dialog = w.findAll('dialog').find((d) => d.text().includes('Create a VLAN'))!
    await dialog.findAll('input')[0]!.setValue('4001')
    await dialog.findAll('input')[1]!.setValue('lab')
    await dialog.findAll('input[type="checkbox"]')[1]!.setValue(true)
    await dialog.get('form').trigger('submit')
    await flushPromises()
    expect(created).toEqual(['sw1:{"name":"lab","id":4001}', 'sw3:{"name":"lab","id":4001}'])
    expect(dialog.text()).toContain('sw1: done')
    expect(dialog.text()).toContain('sw3: This entry exists already.')
    w.unmount()
  })

  it('finds a MAC address', async () => {
    const { w } = await setup('/switches/sw1?tab=macs')
    api.on('GET /api/v1/switches/macs', () => [
      200,
      {
        query: 'bc2411',
        results: [
          {
            switch: 'sw1',
            mac: 'bc:24:11:00:00:01',
            vlan: 10,
            port: '1/1/2',
            portDescription: 'app1',
            uplink: false,
            from: 'dynamic',
          },
          {
            switch: 'sw3',
            mac: 'bc:24:11:00:00:01',
            vlan: 10,
            port: '1/1/49',
            uplink: true,
            from: 'dynamic',
          },
        ],
        errors: [{ switch: 'sw2', error: 'switch not reachable' }],
      },
    ])
    await w.get('form[role="search"] input').setValue('bc:24:11')
    await w.get('form[role="search"]').trigger('submit')
    await flushPromises()
    const rows = w.get('tbody').findAll('tr')
    expect(rows[0]!.text()).toContain('Connected here')
    expect(rows[1]!.text()).toContain('Behind another switch')
    expect(w.text()).toContain('sw2 could not be searched: switch not reachable')
    w.unmount()
  })
})
