<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import AppIcon from './AppIcon.vue'
import BaseButton from './BaseButton.vue'
import type { IconName } from './icons'

/**
 * A button that shows only an icon. The label is its accessible name and its tooltip, so the
 * button says the same as the text button it replaces. tone colors the icon (start green,
 * shut down red).
 */
const props = withDefaults(
  defineProps<{
    icon: IconName
    label: string
    variant?: 'primary' | 'secondary' | 'danger' | 'ghost'
    tone?: 'success' | 'danger'
    size?: 'sm' | 'md'
    type?: 'button' | 'submit' | 'reset'
    loading?: boolean
    disabled?: boolean
  }>(),
  { variant: 'secondary', size: 'sm', type: 'button', loading: false, disabled: false },
)

const tones = { success: 'text-success', danger: 'text-danger' }
</script>

<template>
  <BaseButton
    :variant="props.variant"
    :size="props.size"
    :type="props.type"
    :loading="props.loading"
    :disabled="props.disabled"
    :aria-label="props.label"
    :title="props.label"
  >
    <AppIcon
      v-if="!props.loading"
      :name="props.icon"
      :class="props.tone ? tones[props.tone] : undefined"
    />
  </BaseButton>
</template>
