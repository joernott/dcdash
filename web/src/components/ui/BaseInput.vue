<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { useId, useTemplateRef } from 'vue'

const model = defineModel<string>({ default: '' })
const props = withDefaults(
  defineProps<{
    label: string
    type?: string
    error?: string
    hint?: string
    autocomplete?: string
    required?: boolean
    inputmode?: 'text' | 'numeric' | 'email' | 'search'
    placeholder?: string
  }>(),
  {
    type: 'text',
    error: '',
    hint: '',
    autocomplete: 'off',
    required: false,
    inputmode: 'text',
    placeholder: '',
  },
)

const id = useId()
const input = useTemplateRef<HTMLInputElement>('input')
defineExpose({ focus: () => input.value?.focus() })
</script>

<template>
  <div class="flex flex-col gap-1">
    <label :for="id" class="text-sm font-medium">{{ props.label }}</label>
    <input
      :id="id"
      ref="input"
      v-model="model"
      :type="props.type"
      :autocomplete="props.autocomplete"
      :required="props.required"
      :inputmode="props.inputmode"
      :placeholder="props.placeholder"
      :aria-invalid="props.error ? true : undefined"
      :aria-describedby="props.error || props.hint ? `${id}-desc` : undefined"
      class="rounded-md border bg-surface px-3 py-2 text-fg placeholder:text-fg-muted"
      :class="props.error ? 'border-danger' : 'border-border'"
    />
    <p
      v-if="props.error || props.hint"
      :id="`${id}-desc`"
      class="text-sm"
      :class="props.error ? 'text-danger' : 'text-fg-muted'"
    >
      {{ props.error || props.hint }}
    </p>
  </div>
</template>
