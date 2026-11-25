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

import NetBoxView from '../netbox/NetBoxView.vue'
import { useSessionStore } from '@/stores/session'
import { useToastStore } from '@/stores/toast'
import { FakeApi, sessionInfo, apiError } from '@/__tests__/fakeApi'
import { mountWith, buttonName } from '@/__tests__/helpers'

let api: FakeApi
beforeEach(() => {
  api = new FakeApi()
  fake.api = api.client
})

function lastToast(): string | undefined {
  const l = useToastStore().toasts
  return l[l.length - 1]?.message
}

const Dummy = defineComponent({ render: () => h('div') })

const comparison = {
  cluster: 'lab',
  netboxCluster: 'Lab',
  guests: [
    { name: 'old', state: 'extra', differences: [], netboxId: 7, tagged: true },
    { name: 'manual', state: 'extra', differences: [], netboxId: 8, tagged: false },
    {
      type: 'qemu',
      vmid: 5000,
      name: 'app1',
      state: 'differs',
      differences: [{ field: 'memory', actual: '8192', netbox: '2048' }],
      netboxId: 1,
      tagged: true,
    },
    { type: 'qemu', vmid: 5001, name: 'db1', state: 'missing', differences: [], tagged: false },
    {
      type: 'lxc',
      vmid: 5002,
      name: 'ct1',
      state: 'ok',
      differences: [],
      netboxId: 3,
      tagged: true,
    },
  ],
}

const nodes = [
  {
    kind: 'node',
    name: 'pve1',
    device: 'pve1',
    state: 'differs',
    differences: [{ field: 'cluster', actual: 'Lab', netbox: 'Other' }],
    netboxId: 4,
    updatable: true,
  },
  {
    kind: 'node',
    name: 'pve2',
    device: 'pve2',
    state: 'missing',
    differences: [],
    updatable: false,
  },
  { kind: 'node', name: 'pve3', device: 'pve3', state: 'ok', differences: [], updatable: false },
]
const switchDevices = [
  {
    kind: 'switch',
    name: 'sw1',
    device: 'core-1',
    state: 'differs',
    differences: [{ field: 'model', actual: '6000 24G (R8N88A)', netbox: '6100' }],
    updatable: false,
  },
]

async function setup(permissions: string[], configured = true, clusters = ['lab']) {
  const router: Router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/netbox/:cluster?', name: 'netbox', component: NetBoxView },
      { path: '/proxmox/:cluster/:type/:vmid', name: 'proxmox-guest', component: Dummy },
    ],
  })
  api.on('GET /api/v1/netbox', () =>
    configured
      ? [200, { name: 'netbox', writeBack: true, clusters }]
      : [404, apiError('not_found')],
  )
  api.on('GET /api/v1/netbox/compare/guests/{cluster}', () => [200, comparison])
  api.on('GET /api/v1/netbox/compare/nodes/{cluster}', () => [200, { devices: nodes }])
  api.on('GET /api/v1/netbox/compare/switches', () => [200, { devices: switchDevices }])
  await router.push('/netbox')
  const w = mountWith(NetBoxView, { attachTo: document.body, global: { plugins: [router] } })
  useSessionStore().info = sessionInfo({ permissions, roles: ['test'] })
  await flushPromises()
  return { w, router }
}

describe('NetBoxView', () => {
  it('shows the differences and updates NetBox', async () => {
    const { w, router } = await setup(['netbox:netbox:sync:write'])
    // No redirect to the first cluster: all clusters, each with its section.
    expect(router.currentRoute.value.fullPath).toBe('/netbox')
    expect(w.get('section h2').text()).toBe('Virtual machines and containers of lab')
    // Refresh and the address comparison on the line of the heading.
    const top = w.get('h1').element.parentElement!
    expect(top.querySelector('button[aria-label="Refresh"]')).not.toBeNull()
    expect(top.textContent).toContain('Compare DNS and DHCP')
    expect(w.get('[data-testid="netbox-counts"]').text()).toBe(
      '1 match, 1 differ, 1 missing in NetBox, 2 only in NetBox',
    )
    const rows = () => w.findAll('table')[0]!.findAll('tbody tr')
    // Only differences: ct1 matches and is hidden.
    expect(rows()).toHaveLength(4)
    expect(w.text()).not.toContain('ct1')
    expect(w.text()).toContain('Memory (MiB)')
    expect(w.text()).toContain('Actual: 8192')
    await w.get('input[type="checkbox"]').setValue(false)
    expect(rows()).toHaveLength(5)

    // Update buttons for guests in Proxmox, delete only for tagged VMs that exist only in NetBox.
    const buttons = (text: string) =>
      w
        .get('tbody')
        .findAll('button')
        .filter((b) => buttonName(b) === text)
    expect(buttons('Update NetBox')).toHaveLength(2)
    expect(buttons('Delete from NetBox')).toHaveLength(1)

    let applied = ''
    api.on('POST /api/v1/netbox/compare/guests/{cluster}/{type}/{vmid}', (_, p) => {
      applied = `${String(p?.path?.type)}/${String(p?.path?.vmid)}`
      return [200, { netbox: { status: 'failed', error: 'NetBox: down' } }]
    })
    await buttons('Update NetBox')[0]!.trigger('click')
    await flushPromises()
    expect(applied).toBe('qemu/5000')
    expect(lastToast()).toBe('The change was made, but NetBox could not be updated: NetBox: down')

    let deleted = 0
    api.on('DELETE /api/v1/netbox/compare/guests/{cluster}/vms/{id}', (_, p) => {
      deleted = Number(p?.path?.id)
      return [200, { netbox: { status: 'ok' } }]
    })
    await buttons('Delete from NetBox')[0]!.trigger('click')
    await flushPromises()
    const dialog = w.get('dialog')
    expect(dialog.text()).toContain('old exists only in NetBox')
    await dialog
      .findAll('button')
      .find((b) => buttonName(b) === 'Delete from NetBox')!
      .trigger('click')
    await flushPromises()
    expect(deleted).toBe(7)
    expect(lastToast()).toBe('old was deleted from NetBox.')
    w.unmount()
  })

  it('compares addresses on request', async () => {
    const { w } = await setup(['netbox:netbox:sync:read'])
    expect(w.findAll('button').some((b) => buttonName(b) === 'Update NetBox')).toBe(false)
    api.on('GET /api/v1/netbox/compare/addresses', () => [
      200,
      {
        addresses: [
          { address: '203.0.113.9', source: 'dhcp:kea4', state: 'missing' },
          {
            address: '192.0.2.10',
            source: 'dns',
            name: 'printer.example.org',
            state: 'differs',
            netbox: 'old.example.org',
          },
        ],
        errors: ['DNS: bind: timeout'],
      },
    ])
    await w
      .findAll('button')
      .find((b) => buttonName(b) === 'Compare DNS and DHCP')!
      .trigger('click')
    await flushPromises()
    expect(w.text()).toContain('DHCP reservation (kea4)')
    expect(w.text()).toContain('DNS name in NetBox: old.example.org')
    expect(w.text()).toContain('Could not be read: DNS: bind: timeout')
    w.unmount()
  })

  it('shows all clusters, the field filters them', async () => {
    const { w, router } = await setup(['netbox:netbox:sync:read'], true, ['lab', 'prod'])
    expect(w.findAll('section h2').map((h) => h.text())).toEqual([
      'Virtual machines and containers of lab',
      'Virtual machines and containers of prod',
      'Switches',
    ])
    await w.get('select').setValue('prod')
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/netbox/prod')
    expect(w.findAll('section h2').map((h) => h.text())).toEqual([
      'Virtual machines and containers of prod',
      'Switches',
    ])
    w.unmount()
  })

  it('compares the nodes and switches with their NetBox devices', async () => {
    const { w } = await setup(['netbox:netbox:sync:write'])
    const tables = w.findAll('table')
    const captions = tables.map((t) => t.get('caption').text())
    expect(captions).toEqual([
      'Virtual machines and containers of lab',
      'Physical machines of lab',
      'Switches',
    ])
    // Only differences: pve3 matches and is hidden.
    const nodeRows = tables[1]!.findAll('tbody tr')
    expect(nodeRows.map((r) => r.text())).toEqual([
      expect.stringContaining('pve1'),
      expect.stringContaining('Missing in NetBox'),
    ])
    expect(nodeRows[0]!.text()).toContain('ClusterActual: Lab')
    // The model can't be updated by dcdash; the NetBox device name is shown.
    const sw = tables[2]!.get('tbody tr')
    expect(sw.text()).toContain('NetBox: core-1')
    expect(sw.text()).toContain('Change this in NetBox')
    expect(sw.findAll('button')).toHaveLength(0)

    let applied = ''
    api.on('POST /api/v1/netbox/compare/nodes/{cluster}/{node}', (_, p) => {
      applied = `${String(p?.path?.cluster)}/${String(p?.path?.node)}`
      return [200, { netbox: { status: 'ok' } }]
    })
    await nodeRows[0]!
      .findAll('button')
      .find((b) => buttonName(b) === 'Update NetBox: pve1')!
      .trigger('click')
    await flushPromises()
    expect(applied).toBe('lab/pve1')
    expect(lastToast()).toBe('NetBox was updated for pve1.')
    w.unmount()
  })

  it('explains missing permissions and configuration', async () => {
    let { w } = await setup(['netbox:netbox:ipam:read'])
    expect(w.text()).toContain('You do not have permission to compare')
    expect(api.calls.some((c) => c.route.startsWith('GET /api/v1/netbox/compare'))).toBe(false)
    w.unmount()
    ;({ w } = await setup(['netbox:netbox:sync:read'], false))
    expect(w.text()).toContain('NetBox is not configured.')
    w.unmount()
  })
})
