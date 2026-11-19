<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { api, unwrap } from '@/api/client'
import { errorMessage } from '@/api/errors'
import BaseButton from '@/components/ui/BaseButton.vue'
import BaseInput from '@/components/ui/BaseInput.vue'
import BaseModal from '@/components/ui/BaseModal.vue'
import { netboxFailure } from '@/netbox/netbox'
import { useToastStore } from '@/stores/toast'
import type { SwitchVlan } from '@/switches/switches'

/**
 * Creates a VLAN on one or several switches, or renames a VLAN. Open it with create() or
 * rename().
 */
const props = defineProps<{ switchName: string; switches: string[] }>()
const emit = defineEmits<{ saved: [] }>()

const i18n = useI18n()
const { t } = i18n
const toast = useToastStore()

const open = ref(false)
const busy = ref(false)
const renaming = ref<SwitchVlan | null>(null)
const id = ref('')
const name = ref('')
const targets = ref<string[]>([])
/** Outcome per switch when creating on several switches. */
const results = ref<{ switch: string; ok: boolean; message: string }[]>([])

const vid = computed(() => (/^\d{1,4}$/.test(id.value.trim()) ? Number(id.value) : NaN))
const invalid = computed(
  () =>
    !name.value.trim() ||
    (!renaming.value && (!(vid.value >= 2 && vid.value <= 4094) || targets.value.length === 0)),
)
const title = computed(() =>
  renaming.value
    ? t('switches.vlan.renameTitle', { id: renaming.value.id })
    : t('switches.vlan.createTitle'),
)

function create() {
  renaming.value = null
  id.value = ''
  name.value = ''
  targets.value = [props.switchName]
  results.value = []
  open.value = true
}

function rename(v: SwitchVlan) {
  renaming.value = v
  name.value = v.name
  results.value = []
  open.value = true
}

function toggle(sw: string, on: boolean) {
  targets.value = on ? [...targets.value, sw] : targets.value.filter((x) => x !== sw)
}

async function save() {
  busy.value = true
  results.value = []
  const body = { name: name.value.trim() }
  const list = renaming.value
    ? [props.switchName]
    : props.switches.filter((s) => targets.value.includes(s))
  for (const sw of list) {
    try {
      const res = renaming.value
        ? await unwrap(
            api.PUT('/api/v1/switches/{switch}/vlans/{id}', {
              params: { path: { switch: sw, id: renaming.value.id } },
              body,
            }),
          )
        : await unwrap(
            api.POST('/api/v1/switches/{switch}/vlans', {
              params: { path: { switch: sw } },
              body: { ...body, id: vid.value },
            }),
          )
      const problems = [
        res.saved ? '' : t('switches.notSaved', { error: res.saveError ?? '' }),
        netboxFailure(res.netbox, t) ?? '',
      ].filter((x) => x)
      results.value.push({ switch: sw, ok: problems.length === 0, message: problems.join(' ') })
    } catch (e) {
      results.value.push({ switch: sw, ok: false, message: errorMessage(i18n, e) })
    }
  }
  busy.value = false
  emit('saved')
  if (results.value.every((r) => r.ok)) {
    open.value = false
    toast.success(t('switches.vlan.saved', { id: renaming.value?.id ?? vid.value }))
  }
}

defineExpose({ create, rename })
</script>

<template>
  <BaseModal v-model:open="open" :title="title">
    <form id="switch-vlan" class="flex flex-col gap-3" @submit.prevent="save">
      <BaseInput
        v-if="!renaming"
        v-model="id"
        :label="t('switches.vlan.id')"
        :hint="t('switches.vlan.idHint')"
        inputmode="numeric"
        required
      />
      <BaseInput
        v-model="name"
        :label="t('switches.vlan.name')"
        :hint="t('switches.vlan.nameHint')"
        required
      />
      <fieldset v-if="!renaming && props.switches.length > 1" class="flex flex-col gap-1">
        <legend class="mb-1 text-sm font-medium">{{ t('switches.vlan.onSwitches') }}</legend>
        <label v-for="sw in props.switches" :key="sw" class="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            class="size-4"
            :checked="targets.includes(sw)"
            @change="toggle(sw, ($event.target as HTMLInputElement).checked)"
          />
          {{ sw }}
        </label>
      </fieldset>
      <p class="text-sm text-fg-muted">{{ t('switches.savedHint') }}</p>
      <ul v-if="results.length" class="flex flex-col gap-1 text-sm" role="status">
        <li v-for="r in results" :key="r.switch" :class="r.ok ? 'text-success' : 'text-danger'">
          {{ r.switch }}: {{ r.ok ? t('switches.vlan.done') : r.message }}
        </li>
      </ul>
    </form>
    <template #footer>
      <BaseButton variant="secondary" @click="open = false">{{ t('common.close') }}</BaseButton>
      <BaseButton
        variant="primary"
        type="submit"
        form="switch-vlan"
        :loading="busy"
        :disabled="invalid"
      >
        {{ t('common.save') }}
      </BaseButton>
    </template>
  </BaseModal>
</template>
