<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { api, unwrap } from '@/api/client'
import { errorMessage } from '@/api/errors'
import BaseButton from '@/components/ui/BaseButton.vue'
import BaseInput from '@/components/ui/BaseInput.vue'
import BaseModal from '@/components/ui/BaseModal.vue'
import BaseSelect from '@/components/ui/BaseSelect.vue'
import { netboxFailure } from '@/netbox/netbox'
import { useToastStore } from '@/stores/toast'
import { formatVlans, parseVlans, type SwitchPort, type SwitchVlan } from '@/switches/switches'

/** Changes a port: description, enabled, access or trunk with VLANs. Open it with edit(). */
const props = defineProps<{ switchName: string; vlans: SwitchVlan[] }>()
const emit = defineEmits<{ saved: [port: SwitchPort] }>()

const i18n = useI18n()
const { t } = i18n
const toast = useToastStore()

const open = ref(false)
const busy = ref(false)
const error = ref('')
const port = ref<SwitchPort | null>(null)
const form = ref({
  description: '',
  enabled: true,
  mode: 'access',
  vlan: '1',
  trunk: '',
  allVlans: true,
})
const confirmUplink = ref(false)

const vlanOptions = computed(() =>
  props.vlans.map((v) => ({ value: String(v.id), label: `${v.id} ${v.name}` })),
)
const modeOptions = computed(() => [
  { value: 'access', label: t('switches.modes.access') },
  { value: 'trunk', label: t('switches.modes.trunk') },
])
const trunkVlans = computed(() => (form.value.allVlans ? [] : parseVlans(form.value.trunk)))
const unknownVlans = computed(() =>
  (trunkVlans.value ?? []).filter((id) => !props.vlans.some((v) => v.id === id)),
)
const vlanEditable = computed(() => port.value?.canWrite && !port.value.lag)
const invalid = computed(
  () =>
    form.value.mode === 'trunk' &&
    !form.value.allVlans &&
    (trunkVlans.value === null || trunkVlans.value.length === 0 || unknownVlans.value.length > 0),
)

/** Opens the dialog for a port. */
function edit(p: SwitchPort) {
  port.value = p
  form.value = {
    description: p.description,
    enabled: p.enabled,
    mode: p.mode,
    vlan: String(p.vlan),
    trunk: formatVlans(p.trunkVlans),
    allVlans: p.mode === 'access' || p.allVlans,
  }
  error.value = ''
  busy.value = false
  confirmUplink.value = false
  open.value = true
}

/** Only the fields that changed are sent. */
function changes() {
  const p = port.value!
  const body: Record<string, unknown> = {}
  if (form.value.enabled !== p.enabled) body.enabled = form.value.enabled
  if (!p.canWrite) return body
  if (form.value.description.trim() !== p.description)
    body.description = form.value.description.trim()
  if (!vlanEditable.value) return body
  if (form.value.mode !== p.mode) body.mode = form.value.mode
  if (Number(form.value.vlan) !== p.vlan || body.mode) body.vlan = Number(form.value.vlan)
  if (form.value.mode === 'trunk') {
    const next = trunkVlans.value ?? []
    const prev = p.allVlans ? [] : p.trunkVlans
    if (body.mode || next.join() !== prev.join()) body.trunkVlans = next
  }
  return body
}

async function save() {
  const p = port.value!
  const body = changes()
  if (Object.keys(body).length === 0) {
    open.value = false
    return
  }
  if (p.uplink && !confirmUplink.value) {
    error.value = t('switches.port.confirmUplinkFirst')
    return
  }
  busy.value = true
  error.value = ''
  try {
    const res = await unwrap(
      api.PUT('/api/v1/switches/{switch}/ports/{port}', {
        params: { path: { switch: props.switchName, port: p.name } },
        body,
      }),
    )
    open.value = false
    if (res.saved) toast.success(t('switches.port.saved', { port: p.name }))
    else toast.error(t('switches.notSaved', { error: res.saveError ?? '' }))
    const nb = netboxFailure(res.netbox, t)
    if (nb) toast.error(nb)
    emit('saved', res.port)
  } catch (e) {
    error.value = errorMessage(i18n, e)
  } finally {
    busy.value = false
  }
}

defineExpose({ edit })
</script>

<template>
  <BaseModal v-model:open="open" :title="t('switches.port.title', { port: port?.name ?? '' })">
    <form v-if="port" id="switch-port" class="flex flex-col gap-3" @submit.prevent="save">
      <label class="flex items-center gap-2 text-sm">
        <input v-model="form.enabled" type="checkbox" class="size-4" :disabled="!port.canOperate" />
        {{ t('switches.port.enabled') }}
      </label>
      <BaseInput
        v-model="form.description"
        :label="t('switches.port.description')"
        :disabled="!port.canWrite"
        :hint="t('switches.port.descriptionHint')"
      />
      <p v-if="port.lag" class="text-sm text-fg-muted">
        {{ t('switches.port.inLag', { lag: port.lag }) }}
      </p>
      <template v-if="vlanEditable">
        <BaseSelect v-model="form.mode" :label="t('switches.port.mode')" :options="modeOptions" />
        <BaseSelect
          v-model="form.vlan"
          :label="
            form.mode === 'trunk' ? t('switches.port.nativeVlan') : t('switches.port.accessVlan')
          "
          :options="vlanOptions"
        />
        <template v-if="form.mode === 'trunk'">
          <label class="flex items-center gap-2 text-sm">
            <input v-model="form.allVlans" type="checkbox" class="size-4" />
            {{ t('switches.port.allVlans') }}
          </label>
          <BaseInput
            v-if="!form.allVlans"
            v-model="form.trunk"
            :label="t('switches.port.trunkVlans')"
            :hint="t('switches.port.trunkHint')"
            placeholder="10, 20-25"
          />
          <p v-if="!form.allVlans && trunkVlans === null" class="text-sm text-danger">
            {{ t('switches.port.trunkInvalid') }}
          </p>
          <p v-else-if="unknownVlans.length" class="text-sm text-danger">
            {{ t('switches.port.trunkUnknown', { vlans: formatVlans(unknownVlans) }) }}
          </p>
        </template>
      </template>
      <div
        v-if="port.uplink"
        class="flex flex-col gap-2 rounded-md border border-warning px-3 py-2 text-sm"
      >
        <p class="text-warning">{{ t('switches.port.uplinkWarning') }}</p>
        <label class="flex items-center gap-2">
          <input v-model="confirmUplink" type="checkbox" class="size-4" />
          {{ t('switches.port.confirmUplink') }}
        </label>
      </div>
      <p class="text-sm text-fg-muted">{{ t('switches.savedHint') }}</p>
      <p v-if="error" role="alert" class="text-sm text-danger">{{ error }}</p>
    </form>
    <template #footer>
      <BaseButton variant="secondary" @click="open = false">{{ t('common.cancel') }}</BaseButton>
      <BaseButton
        variant="primary"
        type="submit"
        form="switch-port"
        :loading="busy"
        :disabled="invalid || (port?.uplink && !confirmUplink)"
      >
        {{ t('common.save') }}
      </BaseButton>
    </template>
  </BaseModal>
</template>
