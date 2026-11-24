<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { useId } from 'vue'

export interface SelectOption {
  value: string
  label: string
}

const model = defineModel<string>({ default: '' })
/** compact: the label beside a smaller select, for bars. */
const props = defineProps<{
  label: string
  options: SelectOption[]
  disabled?: boolean
  compact?: boolean
}>()
const id = useId()
</script>

<template>
  <div :class="props.compact ? 'flex items-center gap-2' : 'flex flex-col gap-1'">
    <label :for="id" :class="props.compact ? 'text-sm text-fg-muted' : 'text-sm font-medium'">{{
      props.label
    }}</label>
    <select
      :id="id"
      v-model="model"
      :disabled="props.disabled"
      :class="[
        'rounded-md border border-border bg-surface text-fg disabled:opacity-60',
        props.compact ? 'px-2 py-1 text-sm' : 'px-3 py-2',
      ]"
    >
      <option v-for="o in props.options" :key="o.value" :value="o.value">{{ o.label }}</option>
    </select>
  </div>
</template>
