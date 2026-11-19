<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, onMounted, useId } from 'vue'
import { useI18n } from 'vue-i18n'

import { useProjectsStore } from '@/stores/projects'

/**
 * Selects projects (names). Projects the user may not assign are shown, if
 * already selected, but can't be changed.
 */
const model = defineModel<string[]>({ default: () => [] })
const props = withDefaults(defineProps<{ label?: string }>(), { label: '' })
const { t } = useI18n()
const store = useProjectsStore()
const id = useId()

const shown = computed(() =>
  store.projects.filter((p) => p.canWrite || model.value.includes(p.name)),
)

function toggle(name: string, on: boolean) {
  const next = model.value.filter((n) => n !== name)
  if (on) next.push(name)
  model.value = next.sort()
}

onMounted(() => void store.load())
</script>

<template>
  <fieldset v-if="shown.length > 0" class="flex flex-col gap-2" :aria-describedby="`${id}-hint`">
    <legend class="text-sm font-medium">{{ props.label || t('projects.label') }}</legend>
    <p :id="`${id}-hint`" class="text-sm text-fg-muted">{{ t('projects.pickerHint') }}</p>
    <div class="flex flex-wrap gap-x-4 gap-y-2">
      <label v-for="p in shown" :key="p.name" class="flex items-center gap-2 text-sm">
        <input
          type="checkbox"
          class="size-4"
          :checked="model.includes(p.name)"
          :disabled="!p.canWrite"
          @change="toggle(p.name, ($event.target as HTMLInputElement).checked)"
        />
        <span
          class="size-2.5 rounded-full"
          aria-hidden="true"
          :style="{ backgroundColor: `#${p.color}` }"
        />
        {{ p.title }}
      </label>
    </div>
  </fieldset>
</template>
