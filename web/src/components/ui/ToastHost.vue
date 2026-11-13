<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import { useToastStore } from '@/stores/toast'

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
      <p class="flex-1 text-sm">{{ toast.message }}</p>
      <button
        type="button"
        class="text-fg-muted hover:text-fg"
        :aria-label="t('common.close')"
        @click="store.dismiss(toast.id)"
      >
        <span aria-hidden="true">✕</span>
      </button>
    </div>
  </div>
</template>
