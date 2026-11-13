<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
/** A colored status label; the color is never the only signal (the text is always shown). */
const props = defineProps<{ status: 'ok' | 'warning' | 'error' | 'unknown'; label: string }>()

const styles = {
  ok: 'border-success text-success',
  warning: 'border-warning text-warning',
  error: 'border-danger text-danger',
  unknown: 'border-border text-fg-muted',
}
</script>

<template>
  <span
    class="inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-xs font-medium"
    :class="styles[props.status]"
  >
    <span class="size-2 rounded-full bg-current" aria-hidden="true" />
    {{ props.label }}
  </span>
</template>
