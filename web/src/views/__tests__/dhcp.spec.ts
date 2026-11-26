// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'

const fake = vi.hoisted(() => ({ api: undefined as unknown }))
vi.mock('@/api/client', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/client')>()),
  api: new Proxy({}, { get: (_, prop) => (fake.api as Record<string | symbol, unknown>)[prop] }),
}))

import DhcpView from '../dhcp/DhcpView.vue'
import { useSessionStore } from '@/stores/session'
import { useToastStore } from '@/stores/toast'
import { FakeApi, sessionInfo, apiError } from '@/__tests__/fakeApi'
import { mountWith, buttonName } from '@/__tests__/helpers'

let api: FakeApi
beforeEach(() => {
  api = new FakeApi()
  fake.api = api.client
})

const caps = {
  leases: true,
  leaseDelete: true,
  reservations: true,
  reservationWrite: true,
  subnets: true,
  statistics: true,
  ha: false,
}

const servers = {
  servers: [
    {
      name: 'kea4',
      family: 'v4',
      haGroup: 'site1',
      partners: ['kea4-b'],
      status: 'ok',
      version: '3.0.4',
      uptime: 90061,
      capabilities: caps,
      hostDatabase: true,
      ha: [
        {
          mode: 'hot-standby',
          role: 'primary',
          state: 'hot-standby',
          partner: 'kea4-b',
          partnerState: 'hot-standby',
          inTouch: true,
        },
      ],
      searchKeys: ['ip', 'hw-address', 'client-id', 'hostname'],
      links: { ssh: 'ssh://root@kea4' },
    },
    {
      name: 'kea6',
      family: 'v6',
      status: 'unavailable',
      error: 'server not reachable',
      capabilities: { ...caps, leases: false },
      hostDatabase: false,
      ha: [],
      searchKeys: ['ip', 'duid', 'hostname'],
    },
  ],
}

const subnets = {
  subnets: [
    {
      id: 1,
      prefix: '192.0.2.0/24',
      pools: ['192.0.2.100-192.0.2.199'],
      stats: { subnetId: 1, total: 100, assigned: 80, declined: 2 },
    },
    { id: 2, prefix: '198.51.100.0/24', pools: [] },
  ],
}

const lease = {
  ipAddress: '192.0.2.150',
  hwAddress: '11:22:33:44:55:66',
  hostname: 'laptop',
  subnetId: 1,
  subnet: '192.0.2.0/24',
  state: 'assigned',
  validLifetime: 3600,
  expires: '2026-11-16T12:00:00Z',
  canDelete: true,
}

const reservations = {
  reservations: [
    {
      id: 'cfg',
      version: 'v1',
      subnetId: 1,
      subnet: '192.0.2.0/24',
      identifierType: 'hw-address',
      identifier: 'aa:bb:cc:00:00:01',
      ipAddresses: ['192.0.2.10'],
      hostname: 'printer',
      source: 'config',
      readOnly: true,
    },
    {
      id: 'db1',
      version: 'v1',
      subnetId: 1,
      subnet: '192.0.2.0/24',
      identifierType: 'hw-address',
      identifier: 'aa:bb:cc:00:00:02',
      ipAddresses: ['192.0.2.11'],
      hostname: 'scanner',
      source: 'database',
      readOnly: false,
    },
  ],
}

async function setup(path: string): Promise<Router> {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/dhcp/:server?', name: 'dhcp', component: DhcpView }],
  })
  api.on('GET /api/v1/dhcp/servers', () => [200, servers])
  api.on('GET /api/v1/dhcp/servers/{server}/subnets', () => [200, subnets])
  api.on('GET /api/v1/dhcp/servers/{server}/reservations', () => [200, reservations])
  await router.push(path)
  return router
}

function mountView(router: Router, permissions: string[], locale: 'en' | 'de' = 'en') {
  const w = mountWith(DhcpView, { locale, attachTo: document.body, global: { plugins: [router] } })
  useSessionStore().info = sessionInfo({ permissions, roles: ['test'] })
  return w
}

describe('DhcpView', () => {
  it('shows all servers with their subnets and filters them', async () => {
    const router = await setup('/dhcp')
    const w = mountView(router, ['dhcp:*:*:read'])
    await flushPromises()
    // No redirect to the first server, no tabs.
    expect(router.currentRoute.value.fullPath).toBe('/dhcp')
    expect(w.find('[role=tablist]').exists()).toBe(false)
    expect(w.findAll('section h2').map((h) => h.text())).toEqual(['kea4 (DHCPv4)', 'kea6 (DHCPv6)'])
    // The server status is shown on the overview page, not here.
    expect(w.text()).not.toContain('Kea 3.0.4')
    const kea4 = w.get('section[aria-labelledby="dhcp-server-kea4"]')
    const rows = kea4.findAll('tbody tr')
    expect(rows).toHaveLength(2)
    expect(rows[0]!.text()).toContain('80 of 100')
    expect(rows[0]!.text()).toContain('2 declined')
    expect(rows[0]!.get('[role=meter]').attributes('aria-valuenow')).toBe('80')
    expect(w.get('section[aria-labelledby="dhcp-server-kea6"]').text()).toContain(
      'server not reachable',
    )
    // The UI/SSH buttons next to the name.
    expect(kea4.get('a[href="ssh://root@kea4"]').text()).toMatch(/^SSH/)
    // Filter and search on the line of the heading.
    const form = w.get('h1').element.parentElement!.querySelector('form[role="search"]')!
    expect(form.querySelectorAll('select')).toHaveLength(2)
    const filter = w.get('form[role=search] select')
    expect((filter.element as HTMLSelectElement).value).toBe('')
    await filter.setValue('kea4')
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/dhcp/kea4')
    expect(w.findAll('section h2').map((h) => h.text())).toEqual(['kea4 (DHCPv4)'])
    w.unmount()
  })

  it('reports an unavailable server', async () => {
    const router = await setup('/dhcp/kea6')
    const w = mountView(router, ['dhcp:*:*:read'], 'de')
    await flushPromises()
    expect(w.text()).toContain(
      'Der Server kann gerade nicht verwendet werden: server not reachable',
    )
    expect(w.findAll('section')).toHaveLength(1)
    w.unmount()
  })

  it('searches leases and deletes one on both HA partners', async () => {
    const router = await setup('/dhcp')
    const queries: unknown[] = []
    api.on('GET /api/v1/dhcp/servers/{server}/leases', (_, params) => {
      queries.push({ server: params?.path?.server, ...params?.query })
      return [200, { leases: [lease], truncated: false }]
    })
    api.on('DELETE /api/v1/dhcp/servers/{server}/leases/{ip}', () => [
      200,
      { partners: [{ server: 'kea4-b', done: false, error: 'server not reachable' }] },
    ])
    const w = mountView(router, ['dhcp:kea4:*:operate'])
    await flushPromises()
    const form = w.get('form[role=search]')
    await form.findAll('select')[1]!.setValue('hw-address')
    await form.get('input').setValue('11:22:33:44:55:66')
    await form.trigger('submit')
    await flushPromises()
    // Only the usable servers with leases are searched.
    expect(queries).toEqual([{ server: 'kea4', by: 'hw-address', q: '11:22:33:44:55:66' }])
    const result = w.get('section[aria-labelledby="dhcp-leases"]')
    expect(result.get('h2').text()).toBe('Leases matching “11:22:33:44:55:66”')
    expect(result.text()).toContain('One lease')
    expect(result.findAll('tbody tr')).toHaveLength(1)

    await result.get('tbody button').trigger('click')
    const dialog = w.get('dialog[open]')
    expect(dialog.text()).toContain('Delete the lease of 192.0.2.150?')
    expect(dialog.text()).toContain('also deleted on the HA partner kea4-b')
    await dialog
      .findAll('button')
      .find((b) => buttonName(b) === 'Delete')!
      .trigger('click')
    await flushPromises()
    const toasts = useToastStore().toasts.map((t) => t.message)
    expect(toasts).toContain('Lease 192.0.2.150 deleted.')
    expect(toasts).toContain(
      'The lease could not be deleted on HA partner kea4-b: server not reachable',
    )
    // The search runs again after the deletion.
    expect(queries).toHaveLength(2)
    w.unmount()
  })

  it('shows the leases of a subnet from its button', async () => {
    const router = await setup('/dhcp')
    let query: Record<string, unknown> | undefined
    api.on('GET /api/v1/dhcp/servers/{server}/leases', (_, params) => {
      query = params?.query
      return [200, { leases: [{ ...lease, canDelete: false }], truncated: true }]
    })
    const w = mountView(router, ['dhcp:kea4:*:read'])
    await flushPromises()
    // No dropdown for the subnet: a button per subnet.
    expect(w.text()).not.toContain('Leases of a subnet')
    await w
      .findAll('tbody button')
      .find((b) => buttonName(b) === 'Show the leases of 192.0.2.0/24')!
      .trigger('click')
    await flushPromises()
    expect(query).toEqual({ subnet: 1 })
    const result = w.get('section[aria-labelledby="dhcp-leases"]')
    expect(result.get('h2').text()).toBe('Leases of 192.0.2.0/24 on kea4')
    expect(result.text()).toContain('only the first ones are shown')
    expect(result.findAll('tbody button')).toHaveLength(0)
    await result
      .findAll('button')
      .find((b) => buttonName(b) === 'Close the leases')!
      .trigger('click')
    expect(w.find('section[aria-labelledby="dhcp-leases"]').exists()).toBe(false)
    w.unmount()
  })

  it('adds, edits and deletes reservations', async () => {
    const router = await setup('/dhcp/kea4')
    let body: Record<string, unknown> = {}
    api.on('POST /api/v1/dhcp/servers/{server}/reservations', (b) => {
      body = b as Record<string, unknown>
      return [201, { ...reservations.reservations[1], id: 'db2', hostname: 'nas' }]
    })
    api.on('PUT /api/v1/dhcp/servers/{server}/reservations/{id}', () => [
      400,
      apiError('invalid_reservation'),
    ])
    api.on('DELETE /api/v1/dhcp/servers/{server}/reservations/{id}', () => [
      409,
      apiError('conflict'),
    ])
    const w = mountView(router, ['dhcp:kea4:192.0.2.0/24:write'])
    await flushPromises()
    const toggle = w.findAll('button').find((b) => buttonName(b) === 'Reservations')!
    expect(toggle.attributes('aria-expanded')).toBe('false')
    await toggle.trigger('click')
    await flushPromises()
    expect(toggle.attributes('aria-expanded')).toBe('true')
    const panel = () => w.get('#dhcp-reservations-panel-kea4')
    const rows = panel().findAll('tbody tr')
    expect(rows).toHaveLength(2)
    expect(rows[0]!.text()).toContain('read-only')
    expect(rows[0]!.text()).toContain('Configuration file')
    expect(rows[1]!.text()).toContain('Host database')

    await panel()
      .findAll('button')
      .find((b) => buttonName(b) === 'Add reservation')!
      .trigger('click')
    let dialog = w.get('dialog[open]')
    // Only the writable subnet is offered.
    expect(
      dialog
        .findAll('select')[0]!
        .findAll('option')
        .map((o) => o.text()),
    ).toEqual(['192.0.2.0/24'])
    const inputs = dialog.findAll('input')
    await inputs[0]!.setValue('AA:BB:CC:00:00:03')
    await inputs[1]!.setValue('192.0.2.12')
    await inputs[2]!.setValue('nas')
    await dialog.get('form').trigger('submit')
    await flushPromises()
    expect(body).toMatchObject({
      subnetId: 1,
      identifierType: 'hw-address',
      identifier: 'AA:BB:CC:00:00:03',
      ipAddresses: ['192.0.2.12'],
      hostname: 'nas',
    })
    expect(useToastStore().toasts.map((t) => t.message)).toContain('Reservation nas added.')

    await panel().findAll('tbody tr')[1]!.findAll('button')[0]!.trigger('click')
    dialog = w.get('dialog[open]')
    expect(dialog.findAll('input')[0]!.attributes('disabled')).toBeDefined()
    await dialog.get('form').trigger('submit')
    await flushPromises()
    expect(dialog.text()).toContain('Invalid reservation: invalid_reservation')
    await dialog
      .findAll('button')
      .find((b) => buttonName(b) === 'Cancel')!
      .trigger('click')

    const loads = api.calls.filter((c) => c.route.endsWith('/reservations')).length
    await panel().findAll('tbody tr')[1]!.findAll('button')[1]!.trigger('click')
    dialog = w.get('dialog[open]')
    expect(dialog.text()).toContain('Delete the reservation scanner?')
    await dialog
      .findAll('button')
      .find((b) => buttonName(b) === 'Delete')!
      .trigger('click')
    await flushPromises()
    expect(dialog.text()).toContain('The data was changed in the meantime.')
    expect(api.calls.filter((c) => c.route.endsWith('/reservations')).length).toBe(loads + 1)
    w.unmount()
  })

  it('shows reservations read-only to readers', async () => {
    const router = await setup('/dhcp/kea4')
    // The API marks all reservations read-only for readers.
    api.on('GET /api/v1/dhcp/servers/{server}/reservations', () => [
      200,
      { reservations: reservations.reservations.map((r) => ({ ...r, readOnly: true })) },
    ])
    const w = mountView(router, ['dhcp:kea4:*:read'])
    await flushPromises()
    await w
      .findAll('button')
      .find((b) => buttonName(b) === 'Reservations')!
      .trigger('click')
    await flushPromises()
    const panel = w.get('#dhcp-reservations-panel-kea4')
    expect(panel.findAll('tbody tr')).toHaveLength(2)
    expect(panel.text()).not.toContain('Add reservation')
    expect(panel.findAll('tbody button')).toHaveLength(0)
    w.unmount()
  })
})
