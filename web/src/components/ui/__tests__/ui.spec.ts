// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, it, expect, vi, afterEach } from 'vitest'
import { nextTick } from 'vue'

import { mountWith } from '@/__tests__/helpers'
import BaseButton from '../BaseButton.vue'
import BaseInput from '../BaseInput.vue'
import BaseModal from '../BaseModal.vue'
import DataTable from '../DataTable.vue'
import ToastHost from '../ToastHost.vue'
import { useToastStore } from '@/stores/toast'

afterEach(() => vi.useRealTimers())

describe('BaseButton', () => {
  it('is disabled and busy while loading', () => {
    const w = mountWith(BaseButton, { props: { loading: true }, slots: { default: 'Go' } })
    const b = w.get('button')
    expect(b.attributes('disabled')).toBeDefined()
    expect(b.attributes('aria-busy')).toBe('true')
    expect(b.attributes('type')).toBe('button')
  })

  it('uses theme tokens, not fixed colors', () => {
    for (const variant of ['primary', 'secondary', 'danger', 'ghost'] as const) {
      const cls = mountWith(BaseButton, { props: { variant } }).get('button').classes().join(' ')
      expect(cls).not.toMatch(/(slate|gray|blue|red|green)-\d/)
    }
  })
})

describe('BaseInput', () => {
  it('links label, value and error', async () => {
    const w = mountWith(BaseInput, { props: { label: 'User', modelValue: 'a', error: 'Required' } })
    const input = w.get('input')
    expect(w.get('label').attributes('for')).toBe(input.attributes('id'))
    expect(input.attributes('aria-invalid')).toBe('true')
    const desc = w.get(`#${input.attributes('aria-describedby')}`)
    expect(desc.text()).toBe('Required')
    await input.setValue('bob')
    expect(w.emitted('update:modelValue')?.[0]).toEqual(['bob'])
  })
})

describe('DataTable', () => {
  const columns = [
    { key: 'name', label: 'Name' },
    { key: 'n', label: 'Count' },
  ]
  it('renders rows, slots and the empty state', () => {
    const w = mountWith(DataTable, {
      props: {
        columns,
        rows: [{ name: 'a', n: 1 }],
        rowKey: (r: object) => (r as { name: string }).name,
      },
      slots: { 'cell-n': '<b>custom</b>' },
    })
    expect(w.findAll('th').map((th) => th.text())).toEqual(['Name', 'Count'])
    expect(w.get('tbody').text()).toContain('a')
    expect(w.get('tbody b').text()).toBe('custom')

    const empty = mountWith(DataTable, {
      props: { columns, rows: [], rowKey: () => 1 },
      locale: 'de',
    })
    expect(empty.get('tbody').text()).toBe('Keine Einträge')
  })
})

describe('BaseModal', () => {
  it('opens and closes', async () => {
    const w = mountWith(BaseModal, {
      props: { title: 'Confirm', open: false },
      slots: { default: 'Body', footer: '<button>OK</button>' },
      attachTo: document.body,
    })
    const dialog = w.get('dialog')
    expect(dialog.attributes('open')).toBeUndefined()
    await w.setProps({ open: true })
    await nextTick()
    expect((dialog.element as HTMLDialogElement).open).toBe(true)
    expect(dialog.attributes('aria-labelledby')).toBe(w.get('h2').attributes('id'))
    await w.get('button[aria-label="Close"]').trigger('click')
    const events = w.emitted('update:open') ?? []
    expect(events[events.length - 1]).toEqual([false])
    w.unmount()
  })
})

describe('toasts', () => {
  it('shows, auto-dismisses info and keeps errors', async () => {
    vi.useFakeTimers()
    const w = mountWith(ToastHost)
    const store = useToastStore()
    store.success('Saved')
    store.error('Failed')
    await nextTick()
    expect(w.findAll('[role="status"]')).toHaveLength(1)
    expect(w.get('[role="alert"]').text()).toContain('Failed')
    vi.advanceTimersByTime(6000)
    await nextTick()
    expect(w.findAll('[role="status"]')).toHaveLength(0)
    await w.get('[role="alert"] button').trigger('click')
    expect(store.toasts).toHaveLength(0)
  })
})
