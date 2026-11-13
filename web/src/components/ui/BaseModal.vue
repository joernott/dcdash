<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { useId, useTemplateRef, watch, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'

const open = defineModel<boolean>('open', { default: false })
const props = defineProps<{ title: string }>()
const { t } = useI18n()
const id = useId()
const dialog = useTemplateRef<HTMLDialogElement>('dialog')

// The native <dialog> provides the backdrop, focus handling and Escape to close.
function sync(value: boolean) {
  const d = dialog.value
  if (!d) return
  if (value && !d.open) {
    if (typeof d.showModal === 'function') d.showModal()
    else d.setAttribute('open', '')
  } else if (!value && d.open) {
    if (typeof d.close === 'function') d.close()
    else d.removeAttribute('open')
  }
}
watch(open, sync)
onMounted(() => sync(open.value))
</script>

<template>
  <dialog
    ref="dialog"
    :aria-labelledby="`${id}-title`"
    class="m-auto w-full max-w-lg rounded-lg border border-border bg-surface-raised p-0 text-fg shadow-xl backdrop:bg-black/50"
    @close="open = false"
  >
    <div class="flex items-center justify-between border-b border-border px-5 py-3">
      <h2 :id="`${id}-title`" class="text-lg font-semibold">{{ props.title }}</h2>
      <button
        type="button"
        class="rounded p-1 text-fg-muted hover:bg-surface-sunken"
        :aria-label="t('common.close')"
        @click="open = false"
      >
        <span aria-hidden="true">✕</span>
      </button>
    </div>
    <div class="px-5 py-4">
      <slot />
    </div>
    <div v-if="$slots.footer" class="flex justify-end gap-2 border-t border-border px-5 py-3">
      <slot name="footer" />
    </div>
  </dialog>
</template>
