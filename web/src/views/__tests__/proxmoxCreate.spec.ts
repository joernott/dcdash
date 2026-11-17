// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'
import { defineComponent, h } from 'vue'

const fake = vi.hoisted(() => ({ api: undefined as unknown }))
vi.mock('@/api/client', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/client')>()),
  api: new Proxy({}, { get: (_, prop) => (fake.api as Record<string | symbol, unknown>)[prop] }),
}))

import CreateGuestView from '../proxmox/CreateGuestView.vue'
import GuestView from '../proxmox/GuestView.vue'
import ProxmoxView from '../proxmox/ProxmoxView.vue'
import { options } from '@/proxmox/__tests__/fixtures'
import { useSessionStore } from '@/stores/session'
import { useToastStore } from '@/stores/toast'
import { FakeApi, sessionInfo, apiError } from '@/__tests__/fakeApi'
import { mountWith } from '@/__tests__/helpers'

let api: FakeApi
beforeEach(() => {
  api = new FakeApi()
  fake.api = api.client
})
afterEach(() => {
  vi.useRealTimers()
})

const Dummy = defineComponent({ render: () => h('div') })

function setup(provisioning = true): Router {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/proxmox/:cluster?', name: 'proxmox', component: Dummy },
      { path: '/proxmox/:cluster/new', name: 'proxmox-create', component: Dummy },
      { path: '/proxmox/:cluster/:type/:vmid', name: 'proxmox-guest', component: Dummy },
      { path: '/proxmox/:cluster/:type/:vmid/console', name: 'proxmox-console', component: Dummy },
      { path: '/proxmox/:cluster/nodes/:node/shell', name: 'proxmox-shell', component: Dummy },
    ],
  })
  api.on('GET /api/v1/proxmox/clusters', () => [
    200,
    { clusters: [{ name: 'lab', terminals: false, provisioning }] },
  ])
  api.on('GET /api/v1/proxmox/clusters/{cluster}/provisioning', (_, params) => [
    200,
    { ...options, node: String(params?.query?.node ?? 'pve1') },
  ])
  return router
}

function mount(router: Router, component: object, permissions: string[]) {
  const w = mountWith(component, { attachTo: document.body, global: { plugins: [router] } })
  useSessionStore().info = sessionInfo({ permissions, roles: ['test'] })
  return w
}

async function next(w: VueWrapper) {
  await w.get('form').trigger('submit')
  await flushPromises()
}

function job(state: string, steps: [string, string][]) {
  return {
    id: 'j1',
    cluster: 'lab',
    type: 'qemu',
    vmid: 5000,
    node: 'pve2',
    name: 'app1',
    user: 'alice',
    state,
    steps: steps.map(([name, s]) => ({ name, state: s })),
    created: '2026-11-17T10:00:00Z',
  }
}

describe('CreateGuestView', () => {
  it('walks through the steps, creates a clone and follows the job', async () => {
    vi.useFakeTimers()
    const router = setup()
    await router.push('/proxmox/lab/new')
    let body: Record<string, unknown> = {}
    api.on('POST /api/v1/proxmox/clusters/{cluster}/guests', (b) => {
      body = b as Record<string, unknown>
      return [
        202,
        job('running', [
          ['clone', 'running'],
          ['configure', 'pending'],
        ]),
      ]
    })
    let polls = 0
    api.on('GET /api/v1/proxmox/clusters/{cluster}/jobs/{id}', () => {
      polls++
      return [
        200,
        job('done', [
          ['clone', 'done'],
          ['configure', 'done'],
        ]),
      ]
    })
    const w = mount(router, CreateGuestView, ['proxmox:lab:qemu/5*:admin'])
    await flushPromises()
    // The template is on pve2: the options of pve2 are loaded.
    expect(api.calls.filter((c) => c.route.endsWith('/provisioning'))).toHaveLength(2)
    expect(w.text()).toContain('Virtual machine from a template')
    expect(w.get('[aria-current="step"]').text()).toContain('Source')

    // The name is required.
    await next(w)
    expect(w.text()).toContain('Letters, digits and hyphens')
    expect(w.get('[aria-current="step"]').text()).toContain('Source')
    await w
      .findAll('input')
      .find((i) => i.attributes('required') !== undefined)!
      .setValue('app1')
    await next(w)
    expect(w.get('[aria-current="step"]').text()).toContain('Resources')
    expect(w.text()).toContain('local-lvm (100 GiB free)')

    const inputs = () => w.findAll('input')
    await inputs()[0]!.setValue('9')
    await next(w)
    expect(w.text()).toContain('Between 1 and 8.')
    await inputs()[0]!.setValue('4')
    await next(w)
    expect(w.get('[aria-current="step"]').text()).toContain('Network')
    await w.get('textarea').setValue('ssh-ed25519 AAAAC3Nza user@host')
    await next(w)
    expect(w.get('[aria-current="step"]').text()).toContain('Review')
    expect(w.text()).toContain('pve2')
    expect(w.text()).toContain('1 SSH key')
    expect(w.text()).toContain('Proxmox pool dev')

    await next(w)
    expect(body).toMatchObject({
      type: 'qemu',
      source: 'template',
      template: 9000,
      full: true,
      name: 'app1',
      cores: 4,
      memory: 2048,
      disk: 0,
      storage: 'local-lvm',
      sshKeys: 'ssh-ed25519 AAAAC3Nza user@host',
      network: { bridge: 'vmbr0', ipv4: 'dhcp' },
      start: true,
    })
    expect(w.text()).toContain('app1 (5000)')
    expect(w.text()).toContain('Clone the template')
    expect(w.text()).toContain('In progress')

    await vi.advanceTimersByTimeAsync(1100)
    await flushPromises()
    expect(polls).toBe(1)
    expect(w.text()).toContain('Done')
    const open = w.findAll('a').find((a) => a.text() === 'Open')!
    expect(open.attributes('href')).toBe('/proxmox/lab/qemu/5000')
    // No more polling after the job ended.
    await vi.advanceTimersByTimeAsync(3000)
    expect(polls).toBe(1)
    w.unmount()
  })

  it('shows server errors and failed steps', async () => {
    const router = setup()
    await router.push('/proxmox/lab/new')
    let answer: [number, unknown] = [409, apiError('no_free_vmid')]
    api.on('POST /api/v1/proxmox/clusters/{cluster}/guests', () => answer)
    const w = mount(router, CreateGuestView, ['proxmox:lab:*:admin'])
    await flushPromises()
    // A container on pve1.
    await w.findAll('input[type="radio"]')[2]!.setValue(true)
    await w
      .findAll('input')
      .find((i) => i.attributes('required') !== undefined)!
      .setValue('ct1')
    await next(w)
    await next(w)
    // A container needs a password or SSH keys.
    await next(w)
    expect(w.text()).toContain('Set a password or SSH keys for root.')
    await w.get('input[type="password"]').setValue('rootpw')
    await next(w)
    await next(w)
    expect(w.get('[role="alert"]').text()).toBe(
      'There is no free ID in the range you may use. Please enter an ID.',
    )
    answer = [
      202,
      job('failed', [
        ['create', 'failed'],
        ['start', 'skipped'],
      ]),
    ]
    ;(answer[1] as { steps: { error?: string }[] }).steps[0]!.error = 'storage full'
    await next(w)
    expect(w.text()).toContain('storage full')
    expect(w.text()).toContain('Skipped')
    expect(w.text()).toContain('The creation failed.')
    w.unmount()
  })

  it('offers the create button only with provisioning and admin rights', async () => {
    api.on('GET /api/v1/proxmox/clusters/{cluster}/resources', () => [
      200,
      { nodes: [], guests: [], storage: [] },
    ])
    let router = setup()
    await router.push('/proxmox/lab')
    let w = mount(router, ProxmoxView, ['proxmox:lab:qemu/5*:admin'])
    await flushPromises()
    expect(w.findAll('a').some((a) => a.text() === 'Create')).toBe(true)
    w.unmount()

    router = setup()
    await router.push('/proxmox/lab')
    w = mount(router, ProxmoxView, ['proxmox:lab:*:write'])
    await flushPromises()
    expect(w.findAll('a').some((a) => a.text() === 'Create')).toBe(false)
    w.unmount()

    router = setup(false)
    await router.push('/proxmox/lab')
    w = mount(router, ProxmoxView, ['proxmox:lab:*:admin'])
    await flushPromises()
    expect(w.findAll('a').some((a) => a.text() === 'Create')).toBe(false)
    w.unmount()
  })
})

/** Buttons outside the dialogs. */
function pageButtons(w: VueWrapper) {
  return w.findAll('button').filter((b) => !b.element.closest('dialog'))
}

describe('Deleting a guest', () => {
  function detail(status: string) {
    return {
      guest: {
        type: 'qemu',
        vmid: 5000,
        name: 'app1',
        node: 'pve2',
        status,
        template: false,
        tags: [],
        pool: '',
        lock: '',
        haState: '',
        cpu: 0,
        maxCpu: 2,
        mem: 0,
        maxMem: 2 ** 31,
        disk: 0,
        maxDisk: 0,
        uptime: 0,
      },
      status: { status },
      config: {},
    }
  }

  it('deletes a stopped guest after typing its name', async () => {
    vi.useFakeTimers()
    const router = setup()
    await router.push('/proxmox/lab/qemu/5000')
    api.on('GET /api/v1/proxmox/clusters/{cluster}/guests/{type}/{vmid}', () => [
      200,
      detail('stopped'),
    ])
    let body: unknown
    api.on('DELETE /api/v1/proxmox/clusters/{cluster}/guests/{type}/{vmid}', (b) => {
      body = b
      return [202, { upid: 'UPID:pve2:1:2:3:qmdestroy:5000:x:', node: 'pve2' }]
    })
    api.on('GET /api/v1/proxmox/clusters/{cluster}/tasks/{upid}', () => [
      200,
      {
        upid: 'UPID:pve2:1:2:3:qmdestroy:5000:x:',
        node: 'pve2',
        type: 'qmdestroy',
        id: '5000',
        user: 'x',
        running: false,
        exitStatus: 'OK',
        ok: true,
        startTime: '2026-11-17T10:00:00Z',
        log: [],
      },
    ])
    const w = mount(router, GuestView, ['proxmox:lab:qemu/5*:admin'])
    await flushPromises()
    const del = pageButtons(w).find((b) => b.text() === 'Delete')!
    expect(del.attributes('disabled')).toBeUndefined()
    await del.trigger('click')
    const dialog = w.findAll('dialog')[1]!
    expect(dialog.text()).toContain('app1 (5000) is deleted with all its disks.')
    const submit = dialog.findAll('button').find((b) => b.text() === 'Delete')!
    expect(submit.attributes('disabled')).toBeDefined()
    await dialog.get('input:not([type="checkbox"])').setValue('app1')
    expect(submit.attributes('disabled')).toBeUndefined()
    await dialog.get('form').trigger('submit')
    await flushPromises()
    expect(body).toEqual({ confirm: 'app1', purge: true })
    await vi.advanceTimersByTimeAsync(1500)
    await flushPromises()
    expect(useToastStore().toasts.map((t) => t.message)).toContain('app1 was deleted.')
    expect(router.currentRoute.value.name).toBe('proxmox')
    w.unmount()
  })

  it('asks to stop a running guest first and hides the button without admin', async () => {
    const router = setup()
    await router.push('/proxmox/lab/qemu/5000')
    api.on('GET /api/v1/proxmox/clusters/{cluster}/guests/{type}/{vmid}', () => [
      200,
      detail('running'),
    ])
    let w = mount(router, GuestView, ['proxmox:lab:*:admin'])
    await flushPromises()
    const del = pageButtons(w).find((b) => b.text() === 'Delete')!
    expect(del.attributes('disabled')).toBeDefined()
    expect(w.text()).toContain('Stop the guest to delete it.')
    w.unmount()

    w = mount(router, GuestView, ['proxmox:lab:*:console'])
    await flushPromises()
    expect(pageButtons(w).some((b) => b.text() === 'Delete')).toBe(false)
    w.unmount()
  })
})
