<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

/** A labelled usage meter (CPU, memory, disk). Unknown totals show a dash. */
const props = defineProps<{ label: string; ratio: number | undefined; detail?: string }>()
const { n } = useI18n()

const percent = computed(() => (props.ratio === undefined ? undefined : props.ratio))
const color = computed(() => {
  const r = props.ratio ?? 0
  return r >= 0.9 ? 'bg-danger' : r >= 0.75 ? 'bg-warning' : 'bg-primary'
})
</script>

<template>
  <div class="flex flex-col gap-1 text-sm">
    <div class="flex justify-between gap-2">
      <span class="text-fg-muted">{{ props.label }}</span>
      <span class="tabular-nums">
        <template v-if="percent !== undefined">{{ n(percent, 'percent') }}</template>
        <template v-else>–</template>
        <span v-if="props.detail" class="text-fg-muted"> · {{ props.detail }}</span>
      </span>
    </div>
    <div
      class="h-1.5 overflow-hidden rounded-full bg-surface-sunken"
      role="meter"
      :aria-label="props.label"
      aria-valuemin="0"
      aria-valuemax="100"
      :aria-valuenow="percent === undefined ? undefined : Math.round(percent * 100)"
    >
      <div
        v-if="percent !== undefined"
        class="h-full rounded-full"
        :class="color"
        :style="{ width: `${Math.round(percent * 100)}%` }"
      />
    </div>
  </div>
</template>
