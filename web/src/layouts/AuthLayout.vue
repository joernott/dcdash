<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'

import PreferenceSwitchers from '@/components/PreferenceSwitchers.vue'

/** Centered card for the login and enrollment pages. */
const props = defineProps<{ title: string }>()
const { t } = useI18n()
const showPrefs = ref(false)
</script>

<template>
  <div class="flex min-h-screen flex-col items-center justify-center bg-surface-sunken px-4 py-10">
    <main class="w-full max-w-md rounded-lg border border-border bg-surface p-8 shadow-sm">
      <p class="text-sm font-semibold tracking-wide text-primary">{{ t('app.title') }}</p>
      <h1 class="mt-1 mb-6 text-2xl font-semibold">{{ props.title }}</h1>
      <slot />
    </main>
    <div class="mt-4 w-full max-w-md">
      <button
        type="button"
        class="text-sm text-fg-muted underline-offset-2 hover:underline"
        :aria-expanded="showPrefs"
        @click="showPrefs = !showPrefs"
      >
        {{ t('preferences.title') }}
      </button>
      <div v-if="showPrefs" class="mt-3 rounded-lg border border-border bg-surface p-4">
        <PreferenceSwitchers />
      </div>
    </div>
  </div>
</template>
