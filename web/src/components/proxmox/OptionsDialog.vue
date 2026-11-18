<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'

import { errorMessage } from '@/api/errors'
import BaseButton from '@/components/ui/BaseButton.vue'
import BaseInput from '@/components/ui/BaseInput.vue'
import BaseModal from '@/components/ui/BaseModal.vue'
import BaseSelect from '@/components/ui/BaseSelect.vue'
import {
  OPTION_KINDS,
  OS_TYPES,
  optionChanges,
  optionsForm,
  validateOptions,
  type Hardware,
  type OptionKey,
} from '@/proxmox/hardware'

/** Edits the options of a VM. run() performs the change and throws on errors. */
const props = defineProps<{
  run: (changes: { set: Record<string, string>; delete: string[] }) => Promise<void>
}>()

const i18n = useI18n()
const { t } = i18n
const id = useId()

const open = ref(false)
const busy = ref(false)
const error = ref('')
const hw = ref<Hardware | null>(null)
const form = ref({} as Record<OptionKey, string>)

const errors = computed(() =>
  hw.value
    ? validateOptions(form.value, hw.value.limits)
    : ({} as ReturnType<typeof validateOptions>),
)
const changes = computed(() =>
  hw.value ? optionChanges(hw.value, form.value) : { set: {}, delete: [] },
)
const changed = computed(
  () => Object.keys(changes.value.set).length > 0 || changes.value.delete.length > 0,
)

function edit(h: Hardware) {
  hw.value = h
  form.value = optionsForm(h)
  error.value = ''
  open.value = true
}

function errorText(k: OptionKey): string {
  const e = errors.value[k]
  return e ? t(e.key, e.params ?? {}) : ''
}

function hint(k: OptionKey): string {
  const key = `proxmox.hardware.hints.${k}`
  return i18n.te(key)
    ? t(key, { cores: hw.value?.limits.cores, memory: hw.value?.limits.memory })
    : ''
}

const osOptions = computed(() => [
  { value: '', label: t('proxmox.hardware.default') },
  ...OS_TYPES.map((o) => ({ value: o, label: t(`proxmox.hardware.osTypes.${o}`) })),
])

async function save() {
  if (Object.keys(errors.value).length > 0 || !changed.value) return
  busy.value = true
  error.value = ''
  try {
    await props.run(changes.value)
    open.value = false
  } catch (e) {
    error.value = errorMessage(i18n, e)
  } finally {
    busy.value = false
  }
}

defineExpose({ edit })
</script>

<template>
  <BaseModal v-model:open="open" :title="t('proxmox.hardware.editOptions')">
    <form :id="`${id}-form`" class="flex flex-col gap-3" @submit.prevent="save">
      <p
        v-if="hw?.running"
        class="rounded-md border border-border bg-surface-sunken px-3 py-2 text-sm text-fg-muted"
      >
        {{ t('proxmox.hardware.runningHint') }}
      </p>
      <template v-for="k in Object.keys(OPTION_KINDS) as OptionKey[]" :key="k">
        <label v-if="OPTION_KINDS[k] === 'flag'" class="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            class="size-4"
            :checked="form[k] === '1'"
            @change="form[k] = ($event.target as HTMLInputElement).checked ? '1' : '0'"
          />
          {{ t(`proxmox.hardware.options.${k}`) }}
        </label>
        <BaseSelect
          v-else-if="OPTION_KINDS[k] === 'ostype'"
          v-model="form[k]"
          :label="t(`proxmox.hardware.options.${k}`)"
          :options="osOptions"
        />
        <div v-else-if="OPTION_KINDS[k] === 'textarea'" class="flex flex-col gap-1">
          <label :for="`${id}-${k}`" class="text-sm font-medium">
            {{ t(`proxmox.hardware.options.${k}`) }}
          </label>
          <textarea
            :id="`${id}-${k}`"
            v-model="form[k]"
            rows="3"
            class="rounded-md border border-border bg-surface px-3 py-2 text-fg"
          />
        </div>
        <BaseInput
          v-else
          v-model="form[k]"
          :label="t(`proxmox.hardware.options.${k}`)"
          :inputmode="OPTION_KINDS[k] === 'number' ? 'numeric' : 'text'"
          :error="errorText(k)"
          :hint="hint(k)"
        />
      </template>
      <p v-if="error" role="alert" class="text-sm text-danger">{{ error }}</p>
    </form>
    <template #footer>
      <BaseButton variant="secondary" @click="open = false">{{ t('common.cancel') }}</BaseButton>
      <BaseButton
        type="submit"
        :form="`${id}-form`"
        :loading="busy"
        :disabled="!changed || Object.keys(errors).length > 0"
      >
        {{ t('common.save') }}
      </BaseButton>
    </template>
  </BaseModal>
</template>
