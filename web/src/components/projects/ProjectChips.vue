<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { RouterLink } from 'vue-router'

import { useProjectsStore } from '@/stores/projects'

/** Shows projects as colored chips, linked to their pages. */
const props = defineProps<{ names: string[] }>()
const store = useProjectsStore()
</script>

<template>
  <ul class="flex flex-wrap gap-1.5">
    <li v-for="n in props.names" :key="n">
      <RouterLink
        :to="{ name: 'project', params: { name: n } }"
        class="inline-flex items-center gap-1.5 rounded-full border border-border px-2 py-0.5 text-xs hover:bg-surface-sunken"
      >
        <span
          class="size-2.5 rounded-full"
          aria-hidden="true"
          :style="{ backgroundColor: `#${store.byName(n)?.color ?? '9e9e9e'}` }"
        />
        {{ store.byName(n)?.title ?? n }}
      </RouterLink>
    </li>
  </ul>
</template>
