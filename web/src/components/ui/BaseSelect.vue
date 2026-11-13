<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { useId } from 'vue'

export interface SelectOption {
  value: string
  label: string
}

const model = defineModel<string>({ default: '' })
const props = defineProps<{ label: string; options: SelectOption[] }>()
const id = useId()
</script>

<template>
  <div class="flex flex-col gap-1">
    <label :for="id" class="text-sm font-medium">{{ props.label }}</label>
    <select
      :id="id"
      v-model="model"
      class="rounded-md border border-border bg-surface px-3 py-2 text-fg"
    >
      <option v-for="o in props.options" :key="o.value" :value="o.value">{{ o.label }}</option>
    </select>
  </div>
</template>
