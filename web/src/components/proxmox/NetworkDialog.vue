<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Schemas } from '@/api/client'
import { errorMessage } from '@/api/errors'
import BaseButton from '@/components/ui/BaseButton.vue'
import BaseInput from '@/components/ui/BaseInput.vue'
import BaseModal from '@/components/ui/BaseModal.vue'
import BaseSelect from '@/components/ui/BaseSelect.vue'
import {
  NET_MODELS,
  vlanAllowed,
  type Hardware,
  type NetModel,
  type NetworkDevice,
} from '@/proxmox/hardware'

/**
 * Adds a network device or changes one (the MAC address stays). run()
 * performs the change with the device key (empty for a new device) and throws
 * on errors.
 */
const props = defineProps<{
  run: (key: string, body: Schemas['ProxmoxNetworkRequest']) => Promise<void>
}>()

const i18n = useI18n()
const { t } = i18n
const id = useId()

const open = ref(false)
const busy = ref(false)
const error = ref('')
const hw = ref<Hardware | null>(null)
const device = ref<NetworkDevice | null>(null)
const form = ref({
  model: 'virtio' as NetModel,
  bridge: '',
  vlan: '',
  firewall: false,
  linkDown: false,
  mac: '',
})

const vlanError = computed(() => {
  const v = form.value.vlan.trim()
  if (!v || !hw.value) return ''
  const n = Number(v)
  return Number.isInteger(n) && vlanAllowed(n, hw.value.vlans)
    ? ''
    : t('proxmox.create.errors.vlan', { vlans: hw.value.vlans.join(', ') || '–' })
})
const macError = computed(() => {
  const v = form.value.mac.trim()
  if (!v) return ''
  const ok =
    /^[0-9A-Fa-f]{2}(:[0-9A-Fa-f]{2}){5}$/.test(v) && (parseInt(v.slice(0, 2), 16) & 1) === 0
  return ok ? '' : t('proxmox.hardware.errors.mac')
})

const modelOptions = computed(() => NET_MODELS.map((m) => ({ value: m, label: m })))
const bridgeOptions = computed(() => (hw.value?.bridges ?? []).map((b) => ({ value: b, label: b })))

function create(h: Hardware) {
  hw.value = h
  device.value = null
  form.value = {
    model: 'virtio',
    bridge: h.bridges[0] ?? '',
    vlan: '',
    firewall: false,
    linkDown: false,
    mac: '',
  }
  error.value = ''
  open.value = true
}

function edit(h: Hardware, n: NetworkDevice) {
  hw.value = h
  device.value = n
  form.value = {
    model: n.model,
    bridge: n.bridge,
    vlan: n.vlan ? String(n.vlan) : '',
    firewall: n.firewall,
    linkDown: n.linkDown,
    mac: n.mac,
  }
  error.value = ''
  open.value = true
}

async function save() {
  if (vlanError.value || macError.value || !form.value.bridge) return
  busy.value = true
  error.value = ''
  const f = form.value
  try {
    await props.run(device.value?.key ?? '', {
      model: f.model,
      bridge: f.bridge,
      vlan: f.vlan.trim() ? Number(f.vlan) : undefined,
      firewall: f.firewall,
      linkDown: f.linkDown,
      mac: device.value ? undefined : f.mac.trim() || undefined,
    })
    open.value = false
  } catch (e) {
    error.value = errorMessage(i18n, e)
  } finally {
    busy.value = false
  }
}

defineExpose({ create, edit })
</script>

<template>
  <BaseModal
    v-model:open="open"
    :title="
      device
        ? t('proxmox.hardware.netDialog.edit', { net: device.key })
        : t('proxmox.hardware.netDialog.create')
    "
  >
    <form :id="`${id}-form`" class="flex flex-col gap-3" @submit.prevent="save">
      <BaseSelect
        v-model="form.model"
        :label="t('proxmox.hardware.model')"
        :options="modelOptions"
      />
      <BaseSelect
        v-model="form.bridge"
        :label="t('proxmox.create.bridge')"
        :options="bridgeOptions"
      />
      <BaseInput
        v-model="form.vlan"
        :label="t('proxmox.create.vlan')"
        inputmode="numeric"
        :error="vlanError"
      />
      <BaseInput
        v-model="form.mac"
        :label="t('proxmox.hardware.mac')"
        :disabled="!!device"
        :error="macError"
        :hint="device ? t('proxmox.hardware.hints.macKept') : t('proxmox.hardware.hints.macNew')"
      />
      <label class="flex items-center gap-2 text-sm">
        <input v-model="form.firewall" type="checkbox" class="size-4" />
        {{ t('proxmox.hardware.firewall') }}
      </label>
      <label class="flex items-center gap-2 text-sm">
        <input v-model="form.linkDown" type="checkbox" class="size-4" />
        {{ t('proxmox.hardware.linkDown') }}
      </label>
      <p v-if="error" role="alert" class="text-sm text-danger">{{ error }}</p>
    </form>
    <template #footer>
      <BaseButton variant="secondary" @click="open = false">{{ t('common.cancel') }}</BaseButton>
      <BaseButton
        type="submit"
        :form="`${id}-form`"
        :loading="busy"
        :disabled="!!vlanError || !!macError || !form.bridge"
      >
        {{ device ? t('common.save') : t('proxmox.hardware.addNetwork') }}
      </BaseButton>
    </template>
  </BaseModal>
</template>
