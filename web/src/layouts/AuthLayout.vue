<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { onMounted, ref, useSlots } from 'vue'
import { useI18n } from 'vue-i18n'

import { api, unwrap } from '@/api/client'
import PreferenceSwitchers from '@/components/PreferenceSwitchers.vue'

/**
 * Layout of the pages without a session (login, enrollment, password change): a bar with the
 * name, version and display settings, below it the page's tile and an optional second tile
 * (slot "aside").
 */
const props = defineProps<{ title: string }>()
const slots = useSlots()
const { t } = useI18n()

const version = ref('')
onMounted(async () => {
  try {
    version.value = (await unwrap(api.GET('/api/v1/version'))).version
  } catch {
    // Without the version the bar shows only the name.
  }
})
</script>

<template>
  <div class="flex min-h-screen flex-col bg-surface-sunken">
    <header class="border-b border-border bg-surface-raised">
      <div
        class="mx-auto flex max-w-4xl flex-wrap items-center justify-between gap-x-6 gap-y-2 px-4 py-2"
      >
        <p class="text-lg font-semibold text-primary">
          {{ t('app.title') }}
          <span
            v-if="version"
            class="ml-1 text-sm font-normal text-fg-muted"
            data-testid="version"
            >{{ version }}</span
          >
        </p>
        <div role="group" :aria-label="t('preferences.title')">
          <PreferenceSwitchers compact />
        </div>
      </div>
    </header>
    <main
      :class="[
        'mx-auto grid w-full items-start gap-6 px-4 py-10',
        slots.aside ? 'max-w-md md:max-w-4xl md:grid-cols-2' : 'max-w-md',
      ]"
    >
      <section class="rounded-lg border border-border bg-surface p-8 shadow-sm">
        <h1 class="mb-6 text-2xl font-semibold">{{ props.title }}</h1>
        <slot />
      </section>
      <section v-if="slots.aside" class="rounded-lg border border-border bg-surface p-8 shadow-sm">
        <slot name="aside" />
      </section>
    </main>
  </div>
</template>
