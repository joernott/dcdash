<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'

import BaseButton from '@/components/ui/BaseButton.vue'

/** Shows new recovery codes once, with a copy button. */
const props = defineProps<{ codes: string[] }>()
const { t } = useI18n()
const copied = ref(false)

async function copy() {
  try {
    await navigator.clipboard.writeText(props.codes.join('\n') + '\n')
    copied.value = true
  } catch {
    copied.value = false
  }
}
</script>

<template>
  <div class="flex flex-col gap-3">
    <p class="text-sm">{{ t('recovery.intro') }}</p>
    <ul
      class="grid grid-cols-2 gap-x-6 gap-y-1 rounded-md border border-border bg-surface-sunken p-4 font-mono text-sm"
      :aria-label="t('recovery.listLabel')"
    >
      <li v-for="code in props.codes" :key="code">{{ code }}</li>
    </ul>
    <div class="flex items-center gap-3">
      <BaseButton variant="secondary" size="sm" @click="copy">{{ t('recovery.copy') }}</BaseButton>
      <span v-if="copied" role="status" class="text-sm text-success">{{
        t('recovery.copied')
      }}</span>
    </div>
  </div>
</template>
