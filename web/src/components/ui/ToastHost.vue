<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import IconButton from './IconButton.vue'
import { useToastStore } from '@/stores/toast'

/**
 * The messages at the bottom right. Errors stay, also across pages, until closed; the others
 * disappear on their own and can be closed earlier.
 */
const store = useToastStore()
const { t } = useI18n()

const kinds = {
  success: 'border-success',
  error: 'border-danger',
  info: 'border-primary',
}
</script>

<template>
  <div
    class="pointer-events-none fixed inset-x-0 bottom-4 z-50 flex flex-col items-center gap-2 px-4 sm:items-end"
    aria-live="polite"
  >
    <div
      v-for="toast in store.toasts"
      :key="toast.id"
      :role="toast.kind === 'error' ? 'alert' : 'status'"
      class="pointer-events-auto flex w-full max-w-sm items-start gap-3 rounded-md border-l-4 bg-surface-raised px-4 py-3 text-fg shadow-lg ring-1 ring-border"
      :class="kinds[toast.kind]"
    >
      <p class="flex-1 self-center text-sm">{{ toast.message }}</p>
      <IconButton
        icon="close"
        :label="t('common.closeMessage')"
        class="shrink-0"
        @click="store.dismiss(toast.id)"
      />
    </div>
  </div>
</template>
