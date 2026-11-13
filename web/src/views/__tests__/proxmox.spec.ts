// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'
import { defineComponent, h } from 'vue'

const fake = vi.hoisted(() => ({ api: undefined as unknown }))
vi.mock('@/api/client', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/client')>()),
  api: new Proxy({}, { get: (_, prop) => (fake.api as Record<string | symbol, unknown>)[prop] }),
}))

import ProxmoxView from '../proxmox/ProxmoxView.vue'
import GuestView from '../proxmox/GuestView.vue'
import { useSessionStore } from '@/stores/session'
import { useToastStore } from '@/stores/toast'
import { useProxmoxTasksStore } from '@/stores/proxmoxTasks'
import { FakeApi, sessionInfo, apiError, type Init } from '@/__tests__/fakeApi'
import { mountWith } from '@/__tests__/helpers'

let api: FakeApi
beforeEach(() => {
  api = new FakeApi()
  fake.api = api.client
})
afterEach(() => {
  vi.useRealTimers()
})

function guest(over: Record<string, unknown>) {
  return {
    type: 'qemu',
    vmid: 101,
    name: 'web',
    node: 'pve1',
    status: 'running',
    template: false,
    tags: [],
    pool: '',
    lock: '',
    haState: '',
    cpu: 0.05,
    maxCpu: 2,
    mem: 1 * GIB,
    maxMem: 2 * GIB,
    disk: 0,
    maxDisk: 32 * GIB,
    uptime: 3600,
    ...over,
  }
}

const GIB = 2 ** 30

const inventory = {
  nodes: [
    {
      name: 'pve1',
      status: 'online',
      cpu: 0.12,
      maxCpu: 16,
      mem: 8 * GIB,
      maxMem: 64 * GIB,
      disk: 10 * GIB,
      maxDisk: 100 * GIB,
      uptime: 90000,
    },
  ],
  guests: [
    guest({ tags: ['prod', 'web'] }),
    guest({ vmid: 102, name: 'db', node: 'pve2', status: 'stopped', cpu: 0, mem: 0, uptime: 0 }),
    guest({ type: 'lxc', vmid: 200, name: 'proxy' }),
  ],
  storage: [],
}

const Dummy = defineComponent({ render: () => h('div') })

async function setup(terminals = true): Promise<Router> {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/proxmox/:cluster?', name: 'proxmox', component: Dummy },
      { path: '/proxmox/:cluster/:type/:vmid', name: 'proxmox-guest', component: Dummy },
      { path: '/proxmox/:cluster/:type/:vmid/console', name: 'proxmox-console', component: Dummy },
      { path: '/proxmox/:cluster/nodes/:node/shell', name: 'proxmox-shell', component: Dummy },
    ],
  })
  api.on('GET /api/v1/proxmox/clusters', () => [
    200,
    { clusters: [{ name: 'lab', terminals }] },
  ])
  api.on('GET /api/v1/proxmox/clusters/{cluster}/resources', () => [200, inventory])
  return router
}

function mountView(router: Router, permissions: string[], component = ProxmoxView) {
  const w = mountWith(component, { attachTo: document.body, global: { plugins: [router] } })
  useSessionStore().info = sessionInfo({ permissions, roles: ['test'] })
  return w
}

describe('ProxmoxView', () => {
  it('selects the first cluster and shows nodes and guests', async () => {
    const router = await setup()
    await router.push('/proxmox')
    const w = mountView(router, ['proxmox:lab:*:read'])
    await flushPromises()
    expect(router.currentRoute.value.params.cluster).toBe('lab')
    await flushPromises()
    expect(w.text()).toContain('pve1')
    expect(w.findAll('tbody tr')).toHaveLength(3)
    // Read only: no action buttons, no console, no shell.
    expect(w.findAll('tbody button')).toHaveLength(0)
    expect(w.text()).not.toContain('Console')
    expect(w.text()).not.toContain('Shell')
    expect(w.get('[role="meter"][aria-label="Memory"]').attributes('aria-valuenow')).toBe('13')

    await w.get('input[type="search"]').setValue('PROD')
    expect(w.findAll('tbody tr')).toHaveLength(1)
    await w.get('input[type="search"]').setValue('')
    const selects = w.findAll('select')
    await selects[0]!.setValue('lxc')
    expect(w.findAll('tbody tr')).toHaveLength(1)
    expect(w.text()).toContain('1 of 3 shown')
    w.unmount()
  })

  it('shows the actions the user may run and runs one after confirmation', async () => {
    vi.useFakeTimers()
    const router = await setup()
    await router.push('/proxmox/lab')
    let posted = ''
    api.client.POST = (path: string, init?: Init) => {
      posted = `${path} ${JSON.stringify(init?.params?.path)}`
      return Promise.resolve({
        data: { upid: 'UPID:pve2:1:2:3:qmstart:102:x:', node: 'pve2' },
        response: new Response(null, { status: 202 }),
      })
    }
    let polls = 0
    api.on('GET /api/v1/proxmox/clusters/{cluster}/tasks/{upid}', () => {
      polls++
      return [
        200,
        {
          upid: 'UPID:pve2:1:2:3:qmstart:102:x:',
          node: 'pve2',
          type: 'qmstart',
          id: '102',
          user: 'x',
          running: polls < 2,
          exitStatus: polls < 2 ? '' : 'OK',
          ok: polls >= 2,
          startTime: '2026-11-13T10:00:00Z',
          log: ['TASK OK'],
        },
      ]
    })
    const w = mountView(router, [
      'proxmox:lab:qemu/*:operate',
      'proxmox:lab:lxc/200:console',
      'proxmox:lab:node/pve1:admin',
    ])
    await flushPromises()
    const rows = w.findAll('tbody tr')
    expect(rows[0]!.findAll('button').map((b) => b.text())).toEqual([
      'Shut down',
      'Reboot',
      'Suspend',
      'Stop',
    ])
    expect(rows[1]!.findAll('button').map((b) => b.text())).toEqual(['Start'])
    // console includes operate (action hierarchy); shell needs admin on the node.
    expect(rows[2]!.findAll('button')).toHaveLength(4)
    expect(rows[2]!.text()).toContain('Console')
    expect(w.text()).toContain('Shell')

    await rows[1]!.get('button').trigger('click')
    expect(w.get('dialog').text()).toContain('Start db (102)?')
    await w
      .findAll('dialog button')
      .find((b) => b.text() === 'Start')!
      .trigger('click')
    await flushPromises()
    expect(posted).toContain('"action":"start"')
    expect(posted).toContain('"vmid":102')

    await vi.advanceTimersByTimeAsync(2500)
    await flushPromises()
    const tasks = useProxmoxTasksStore().tasks
    expect(tasks[0]!.state).toBe('ok')
    expect(useToastStore().toasts.map((t) => t.message)).toContain('Done: db (102): Start')
    expect(w.text()).toContain('TASK OK')
    w.unmount()
  })

  it('hides terminals and node shells without a console user', async () => {
    const router = await setup(false)
    await router.push('/proxmox/lab')
    const w = mountView(router, ['proxmox:lab:*:admin'])
    await flushPromises()
    const rows = w.findAll('tbody tr')
    // VNC works with the API token, terminals need the console user.
    expect(rows[0]!.text()).toContain('Console')
    expect(rows[2]!.text()).not.toContain('Console')
    expect(w.text()).not.toContain('Shell')
    w.unmount()
  })

  it('shows backend errors', async () => {
    const router = await setup()
    api.on('GET /api/v1/proxmox/clusters/{cluster}/resources', () => [
      502,
      apiError('upstream_unavailable'),
    ])
    await router.push('/proxmox/lab')
    const w = mountView(router, ['proxmox:lab:*:read'])
    await flushPromises()
    expect(w.get('[role="alert"]').text()).toContain('cannot be reached')
    w.unmount()
  })
})

describe('GuestView', () => {
  it('shows status and configuration in German', async () => {
    const router = await setup()
    api.on('GET /api/v1/proxmox/clusters/{cluster}/guests/{type}/{vmid}', () => [
      200,
      {
        guest: inventory.guests[0],
        status: { status: 'running' },
        config: { name: 'web', cores: 2, net0: 'virtio=BC:24:11:00:00:01,bridge=vmbr0' },
      },
    ])
    await router.push('/proxmox/lab/qemu/101')
    const w = mountWith(GuestView, {
      locale: 'de',
      attachTo: document.body,
      global: { plugins: [router] },
    })
    useSessionStore().info = sessionInfo({ permissions: ['proxmox:lab:*:read'], roles: ['x'] })
    await flushPromises()
    expect(w.get('h1').text()).toBe('web')
    expect(w.text()).toContain('Läuft')
    expect(w.text()).toContain('Virtuelle Maschine')
    expect(w.text()).toContain('bridge=vmbr0')
    expect(w.findAll('[role="group"] button')).toHaveLength(0)
    w.unmount()
  })
})
