// SPDX-License-Identifier: GPL-3.0-or-later
import { mount, type ComponentMountingOptions } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import type { Component } from 'vue'

import { createAppI18n, type Locale } from '@/i18n'

/** Mounts a component with i18n and a fresh Pinia store. */
export function mountWith<C extends Component>(
  component: C,
  options: ComponentMountingOptions<C> & { locale?: Locale } = {},
) {
  const pinia = createPinia()
  setActivePinia(pinia)
  const { locale = 'en', global = {}, ...rest } = options
  return mount(component, {
    ...rest,
    global: { ...global, plugins: [...(global.plugins ?? []), pinia, createAppI18n(locale)] },
  } as ComponentMountingOptions<C>)
}

/**
 * An in-memory localStorage. Node 25+ defines its own (disabled) localStorage
 * global, which hides the one of jsdom, so tests install this one.
 */
export function memoryStorage(): Storage {
  const data = new Map<string, string>()
  return {
    get length() {
      return data.size
    },
    clear: () => data.clear(),
    getItem: (k) => data.get(k) ?? null,
    key: (i) => [...data.keys()][i] ?? null,
    removeItem: (k) => void data.delete(k),
    setItem: (k, v) => void data.set(k, String(v)),
  }
}
