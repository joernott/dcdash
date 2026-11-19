// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises, type VueWrapper } from '@vue/test-utils'

const fake = vi.hoisted(() => ({ api: undefined as unknown }))
vi.mock('@/api/client', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/client')>()),
  api: new Proxy({}, { get: (_, prop) => (fake.api as Record<string | symbol, unknown>)[prop] }),
}))

import HardwarePanel from '@/components/proxmox/HardwarePanel.vue'
import { hardware } from '@/proxmox/__tests__/fixtures'
import { FakeApi } from '@/__tests__/fakeApi'
import { mountWith } from '@/__tests__/helpers'
import { useToastStore } from '@/stores/toast'

const base = 'GET /api/v1/proxmox/clusters/{cluster}/guests/{type}/{vmid}/hardware'

let api: FakeApi
beforeEach(() => {
  api = new FakeApi()
  fake.api = api.client
  api.on(base, () => [200, hardware()])
})

function mount(canWrite = true, canAdmin = true, locale: 'en' | 'de' = 'en') {
  return mountWith(HardwarePanel, {
    locale,
    attachTo: document.body,
    props: { cluster: 'lab', vmid: 101, name: 'web', canWrite, canAdmin },
  })
}

function dialog(w: VueWrapper, title: string) {
  return w.findAll('dialog').find((d) => d.find('h2').text() === title)!
}

function button(w: VueWrapper, scope: string, text: string) {
  return w
    .findAll(`${scope} button`)
    .filter((b) => !b.element.closest('dialog') || scope === 'dialog')
    .find((b) => b.text() === text)!
}

describe('HardwarePanel', () => {
  it('shows options, disks and network devices with pending changes', async () => {
    const w = mount()
    await flushPromises()
    const text = w.text()
    expect(text).toContain('Cores')
    expect(text).toContain('local-lvm:vm-101-disk-0')
    expect(text).toContain('Discard, IO thread')
    expect(text).toContain('BC:24:11:00:00:01')
    expect(text).toContain('VLAN 100, firewall')
    expect(w.find('[data-testid="pending-hint"]').exists()).toBe(true)
    // The unused disk can be attached, the attached one detached.
    const tables = w.findAll('table')
    expect(tables[1]!.text()).toContain('Attach')
    expect(tables[1]!.text()).toContain('Detach')
    w.unmount()
  })

  it('has no buttons without write permission', async () => {
    const w = mount(false, false)
    await flushPromises()
    expect(w.findAll('button').filter((b) => !b.element.closest('dialog'))).toHaveLength(0)
    w.unmount()
  })

  it('sends changed options with the digest', async () => {
    let body: unknown
    api.on('POST /api/v1/proxmox/clusters/{cluster}/guests/{type}/{vmid}/hardware/options', (b) => {
      body = b
      return [200, { node: 'pve1', done: true, upid: 'UPID:x' }]
    })
    const w = mount()
    await flushPromises()
    await button(w, '', 'Edit options').trigger('click')
    const d = dialog(w, 'Edit options')
    const label = d.findAll('label').find((l) => l.text() === 'Memory (MiB)')!
    await d.get(`[id="${label.attributes('for')}"]`).setValue('4096')
    await d.get('form').trigger('submit')
    await flushPromises()
    expect(body).toEqual({ digest: 'd1', set: { memory: '4096' }, delete: [] })
    expect(useToastStore().toasts.map((x) => x.message)).toContain('web: Change options done.')
    w.unmount()
  })

  it('adds a disk within the limits', async () => {
    let body: unknown
    api.on('POST /api/v1/proxmox/clusters/{cluster}/guests/{type}/{vmid}/hardware/disks', (b) => {
      body = b
      return [200, { node: 'pve1', done: true, key: 'scsi1' }]
    })
    const w = mount()
    await flushPromises()
    await button(w, '', 'Add disk').trigger('click')
    const d = dialog(w, 'Add disk')
    const size = d.findAll('input').find((i) => i.attributes('inputmode') === 'numeric')!
    await size.setValue('500')
    expect(d.text()).toContain('Between 1 and 100.')
    await size.setValue('20')
    await d.get('form').trigger('submit')
    await flushPromises()
    expect(body).toMatchObject({
      digest: 'd1',
      bus: 'scsi',
      storage: 'local-lvm',
      sizeGiB: 20,
      discard: true,
      iothread: true,
    })
    w.unmount()
  })

  it('deletes a disk only after typing its name', async () => {
    let body: unknown
    api.on(
      'DELETE /api/v1/proxmox/clusters/{cluster}/guests/{type}/{vmid}/hardware/disks/{disk}',
      (b) => {
        body = b
        return [200, { node: 'pve1', done: true }]
      },
    )
    const w = mount()
    await flushPromises()
    const row = w
      .findAll('tr')
      .find((r) => r.find('td').exists() && r.get('td').text().startsWith('unused0'))!
    await row
      .findAll('button')
      .find((b) => b.text() === 'Delete')!
      .trigger('click')
    const d = dialog(w, 'Delete disk')
    const submit = d.findAll('button').find((b) => b.text() === 'Delete')!
    expect(submit.attributes('disabled')).toBeDefined()
    await d.get('input').setValue('unused0')
    expect(submit.attributes('disabled')).toBeUndefined()
    await d.get('form').trigger('submit')
    await flushPromises()
    expect(body).toEqual({ digest: 'd1', confirm: 'unused0' })
    w.unmount()
  })

  it('reports a configuration changed in the meantime', async () => {
    api.on(
      'DELETE /api/v1/proxmox/clusters/{cluster}/guests/{type}/{vmid}/hardware/networks/{net}',
      () => [409, { error: { code: 'config_changed', message: 'changed' } }],
    )
    const w = mount(true, true, 'de')
    await flushPromises()
    const row = w
      .findAll('tr')
      .find((r) => r.find('td').exists() && r.get('td').text().startsWith('net0'))!
    await row
      .findAll('button')
      .find((b) => b.text() === 'Entfernen')!
      .trigger('click')
    const d = dialog(w, 'Netzwerkgerät entfernen')
    await d.get('form').trigger('submit')
    await flushPromises()
    expect(d.get('[role="alert"]').text()).toContain('zwischenzeitlich geändert')
    w.unmount()
  })
})
