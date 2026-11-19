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

async function setup(permissions: string[], configured = true) {
  const router: Router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/netbox/:cluster?', name: 'netbox', component: NetBoxView },
      { path: '/proxmox/:cluster/:type/:vmid', name: 'proxmox-guest', component: Dummy },
    ],
  })
  api.on('GET /api/v1/netbox', () =>
    configured
      ? [200, { name: 'netbox', writeBack: true, clusters: ['lab'] }]
      : [404, apiError('not_found')],
  )
  api.on('GET /api/v1/netbox/compare/guests/{cluster}', () => [200, comparison])
  await router.push('/netbox')
  const w = mountWith(NetBoxView, { attachTo: document.body, global: { plugins: [router] } })
  useSessionStore().info = sessionInfo({ permissions, roles: ['test'] })
  await flushPromises()
  return { w, router }
}

describe('NetBoxView', () => {
  it('shows the differences and updates NetBox', async () => {
    const { w, router } = await setup(['netbox:netbox:sync:write'])
    expect(router.currentRoute.value.params.cluster).toBe('lab')
    expect(w.get('[data-testid="netbox-counts"]').text()).toBe(
      '1 match, 1 differ, 1 missing in NetBox, 2 only in NetBox',
    )
    const rows = () => w.findAll('tbody tr')
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
        .filter((b) => b.text() === text)
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
      .find((b) => b.text() === 'Delete from NetBox')!
      .trigger('click')
    await flushPromises()
    expect(deleted).toBe(7)
    expect(lastToast()).toBe('old was deleted from NetBox.')
    w.unmount()
  })

  it('compares addresses on request', async () => {
    const { w } = await setup(['netbox:netbox:sync:read'])
    expect(w.findAll('button').some((b) => b.text() === 'Update NetBox')).toBe(false)
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
      .find((b) => b.text() === 'Compare')!
      .trigger('click')
    await flushPromises()
    expect(w.text()).toContain('DHCP reservation (kea4)')
    expect(w.text()).toContain('DNS name in NetBox: old.example.org')
    expect(w.text()).toContain('Could not be read: DNS: bind: timeout')
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
