// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { defineComponent, h } from 'vue'

const fake = vi.hoisted(() => ({ api: undefined as unknown }))
vi.mock('@/api/client', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/client')>()),
  api: new Proxy({}, { get: (_, prop) => (fake.api as Record<string | symbol, unknown>)[prop] }),
}))

import ProjectsView from '../projects/ProjectsView.vue'
import ProjectView from '../projects/ProjectView.vue'
import GuestProjects from '@/components/projects/GuestProjects.vue'
import { useSessionStore } from '@/stores/session'
import { useToastStore } from '@/stores/toast'
import { FakeApi, sessionInfo } from '@/__tests__/fakeApi'
import { mountWith, buttonName } from '@/__tests__/helpers'

let api: FakeApi
beforeEach(() => {
  api = new FakeApi()
  fake.api = api.client
})

interface NbObj {
  type: string
  id: number
  display: string
  detail: string
}

const Dummy = defineComponent({ render: () => h('div') })
const webshop = {
  name: 'webshop',
  title: 'Webshop',
  color: '2196f3',
  description: 'New shop',
  tag: 'project-webshop',
  canWrite: true,
}

function router() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/projects', name: 'projects', component: ProjectsView },
      { path: '/projects/:name', name: 'project', component: ProjectView },
      { path: '/proxmox/:cluster/:type/:vmid', name: 'proxmox-guest', component: Dummy },
    ],
  })
}

function lastToast(): string | undefined {
  const l = useToastStore().toasts
  return l[l.length - 1]?.message
}

describe('Projects', () => {
  it('lists the projects with their state', async () => {
    api.on('GET /api/v1/projects', () => [
      200,
      {
        projects: [webshop],
        state: { errors: ['x.yaml: color "nope"'], orphaned: ['project-old'] },
      },
    ])
    const r = router()
    await r.push('/projects')
    const w = mountWith(ProjectsView, { global: { plugins: [r] } })
    await flushPromises()
    expect(w.text()).toContain('Webshop')
    expect(w.text()).toContain('project-webshop')
    expect(w.text()).toContain('Invalid project file: x.yaml: color "nope"')
    expect(w.text()).toContain('The NetBox tag project-old has no project file.')
  })

  it('shows a project and adds and removes NetBox objects', async () => {
    let page = {
      ...webshop,
      guests: [
        {
          cluster: 'lab',
          type: 'qemu',
          vmid: 101,
          name: 'web',
          node: 'pve1',
          status: 'running',
          visible: true,
        },
        {
          cluster: 'lab',
          type: 'qemu',
          vmid: 102,
          name: 'db',
          node: '',
          status: '',
          visible: false,
        },
      ],
      groups: [
        {
          directory: 'lab',
          name: 'shop-admins',
          visible: true,
          description: 'Shop administrators',
          members: [{ dn: 'CN=alice', name: 'alice', displayName: 'Alice Example', kind: 'user' }],
        },
        { directory: 'lab', name: 'secret', visible: false },
      ],
      netbox: {
        vm: [] as NbObj[],
        ip: [{ type: 'ip', id: 5, display: '192.0.2.10/24', detail: 'web.example.org' }],
        iprange: [] as NbObj[],
        prefix: [] as NbObj[],
        vlan: [] as NbObj[],
        interface: [] as NbObj[],
      },
      errors: [],
    }
    api.on('GET /api/v1/projects/{name}', () => [200, page])
    api.on('GET /api/v1/projects/{name}/search', () => [
      200,
      { objects: [{ type: 'prefix', id: 3, display: '192.0.2.0/24' }] },
    ])
    let added = ''
    api.on('POST /api/v1/projects/{name}/objects', (b) => {
      added = JSON.stringify(b)
      page = {
        ...page,
        netbox: {
          ...page.netbox,
          prefix: [{ type: 'prefix', id: 3, display: '192.0.2.0/24', detail: '' }],
        },
      }
      return [200, { type: 'prefix', id: 3, display: '192.0.2.0/24' }]
    })
    let removed = ''
    api.on('DELETE /api/v1/projects/{name}/objects/{type}/{id}', (_, p) => {
      removed = `${String(p?.path?.type)}/${String(p?.path?.id)}`
      return [200, { type: 'ip', id: 5, display: '192.0.2.10/24' }]
    })
    const r = router()
    await r.push('/projects/webshop')
    const w = mountWith(ProjectView, { attachTo: document.body, global: { plugins: [r] } })
    await flushPromises()
    expect(w.get('h1').text()).toBe('Webshop')
    expect(w.findAll('a').some((a) => a.text() === 'web')).toBe(true)
    expect(w.findAll('a').some((a) => a.text() === 'db')).toBe(false)
    expect(w.text()).toContain('Active Directory groups')
    expect(w.text()).toContain('Alice Example (alice)')
    expect(w.text()).toContain('Members not visible to you')
    expect(w.text()).toContain('web.example.org')

    await w.get('select').setValue('prefix')
    const inputs = w.findAll('input')
    await inputs[inputs.length - 1]!.setValue('192.0')
    await w.get('form').trigger('submit')
    await flushPromises()
    const results = w.get('[data-testid="project-results"]')
    await results
      .findAll('button')
      .find((b) => buttonName(b) === 'Add')!
      .trigger('click')
    await flushPromises()
    expect(added).toBe('{"type":"prefix","id":3}')
    expect(lastToast()).toBe('192.0.2.0/24 was added to the project.')
    expect(w.get('[data-testid="project-results"]').text()).toContain('already in the project')

    await w
      .findAll('button')
      .find((b) => buttonName(b) === 'Remove')!
      .trigger('click')
    await flushPromises()
    expect(removed).toBe('ip/5')
    w.unmount()
  })

  it('changes the projects of a guest', async () => {
    api.on('GET /api/v1/projects', () => [
      200,
      {
        projects: [
          webshop,
          {
            ...webshop,
            name: 'intranet',
            title: 'Intranet',
            tag: 'project-intranet',
            canWrite: false,
          },
        ],
      },
    ])
    api.on('GET /api/v1/netbox', () => [200, { name: 'netbox', writeBack: true, clusters: [] }])
    let body: unknown
    api.on('PUT /api/v1/proxmox/clusters/{cluster}/guests/{type}/{vmid}/projects', (b) => {
      body = b
      return [200, { projects: [], netbox: { status: 'ok' } }]
    })
    const r = router()
    const w = mountWith(GuestProjects, {
      attachTo: document.body,
      global: { plugins: [r] },
      props: {
        cluster: 'lab',
        type: 'qemu',
        vmid: 101,
        tags: ['prod', 'project-webshop', 'project-intranet'],
        canWrite: true,
      },
    })
    useSessionStore().info = sessionInfo({ permissions: [], roles: ['test'] })
    await flushPromises()
    expect(w.text()).toContain('Webshop')
    expect(w.text()).toContain('Intranet')
    await w
      .findAll('button')
      .find((b) => buttonName(b) === 'Change projects')!
      .trigger('click')
    const boxes = w.get('dialog').findAll('input[type="checkbox"]')
    // Intranet is shown because it is assigned, but the user may not change it.
    expect(boxes).toHaveLength(2)
    expect(boxes[1]!.attributes('disabled')).toBeDefined()
    await boxes[0]!.setValue(false)
    await w.get('dialog form').trigger('submit')
    await flushPromises()
    expect(body).toEqual({ projects: ['intranet'] })
    expect(w.emitted('changed')).toHaveLength(1)
    w.unmount()
  })
})
