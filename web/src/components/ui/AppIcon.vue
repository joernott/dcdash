<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed } from 'vue'

import { icons, type IconName } from './icons'

/** An icon in the current text color; decorative, the surrounding element names it. */
const props = defineProps<{ name: IconName }>()

const icon = computed(() => icons[props.name])
</script>

<template>
  <svg
    :viewBox="`0 0 ${icon.width} ${icon.height}`"
    class="size-4 shrink-0"
    fill="currentColor"
    aria-hidden="true"
    focusable="false"
    :data-icon="props.name"
  >
    <path v-for="(d, i) in icon.paths" :key="i" :d="d" />
  </svg>
</template>
