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
