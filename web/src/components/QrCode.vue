<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed } from 'vue'
import { encode } from 'uqr'

/**
 * Renders text as a QR code (SVG). It is always dark on a white background,
 * also in dark mode, because many scanner apps cannot read inverted codes.
 */
const props = defineProps<{ value: string; label: string }>()

const qr = computed(() => {
  const { data, size } = encode(props.value, { ecc: 'M', border: 2 })
  let path = ''
  data.forEach((row, y) =>
    row.forEach((dark, x) => {
      if (dark) path += `M${x} ${y}h1v1h-1z`
    }),
  )
  return { path, size }
})
</script>

<template>
  <svg
    role="img"
    :aria-label="props.label"
    :viewBox="`0 0 ${qr.size} ${qr.size}`"
    shape-rendering="crispEdges"
    class="h-auto w-56 max-w-full rounded-md bg-white"
  >
    <path :d="qr.path" fill="#000" />
  </svg>
</template>
