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
  DISK_BUSES,
  diskGiB,
  supportsIOThread,
  supportsSSD,
  type DiskBus,
  type Hardware,
  type HardwareDisk,
} from '@/proxmox/hardware'

export type DiskChange =
  | { mode: 'create'; body: Schemas['ProxmoxDiskRequest'] }
  | { mode: 'edit'; disk: string; body: Schemas['ProxmoxDiskUpdate'] }
  | { mode: 'attach'; disk: string; bus: DiskBus }

/**
 * Creates a disk, changes one (grow, options) or attaches an unused one.
 * run() performs the change and throws on errors.
 */
const props = defineProps<{ run: (change: DiskChange) => Promise<void> }>()

const i18n = useI18n()
const { t } = i18n
const id = useId()

const open = ref(false)
const busy = ref(false)
const error = ref('')
const mode = ref<DiskChange['mode']>('create')
const hw = ref<Hardware | null>(null)
const disk = ref<HardwareDisk | null>(null)
const form = ref({
  bus: 'scsi' as DiskBus,
  storage: '',
  size: '',
  discard: true,
  ssd: false,
  iothread: true,
  noBackup: false,
})

const title = computed(() =>
  t(`proxmox.hardware.diskDialog.${mode.value}`, { disk: disk.value?.key }),
)
const bus = computed(() => (mode.value === 'edit' ? disk.value!.bus : form.value.bus))
const minGiB = computed(() => (mode.value === 'edit' && disk.value ? diskGiB(disk.value) : 0))

const sizeError = computed(() => {
  if (mode.value === 'attach' || !hw.value) return ''
  const v = form.value.size.trim()
  if (mode.value === 'edit' && v === '') return ''
  const n = Number(v)
  const max = hw.value.limits.disk
  if (!Number.isInteger(n) || n <= minGiB.value || n > max || (mode.value === 'create' && n < 1)) {
    return mode.value === 'edit'
      ? t('proxmox.hardware.errors.grow', { min: minGiB.value, max })
      : t('proxmox.hardware.errors.range', { min: 1, max })
  }
  return ''
})

const busOptions = computed(() => DISK_BUSES.map((b) => ({ value: b, label: b })))
const storageOptions = computed(() =>
  (hw.value?.storages ?? []).map((s) => ({ value: s, label: s })),
)

function reset(h: Hardware) {
  hw.value = h
  error.value = ''
  open.value = true
}

function create(h: Hardware) {
  mode.value = 'create'
  disk.value = null
  form.value = {
    bus: 'scsi',
    storage: h.storages[0] ?? '',
    size: '',
    discard: true,
    ssd: false,
    iothread: true,
    noBackup: false,
  }
  reset(h)
}

function edit(h: Hardware, d: HardwareDisk) {
  mode.value = 'edit'
  disk.value = d
  form.value = {
    bus: d.bus as DiskBus,
    storage: d.storage,
    size: '',
    discard: d.discard,
    ssd: d.ssd,
    iothread: d.iothread,
    noBackup: !d.backup,
  }
  reset(h)
}

function attach(h: Hardware, d: HardwareDisk) {
  mode.value = 'attach'
  disk.value = d
  form.value.bus = 'scsi'
  reset(h)
}

function change(): DiskChange {
  const f = form.value
  const ssd = supportsSSD(bus.value) && f.ssd
  const iothread = supportsIOThread(bus.value) && f.iothread
  switch (mode.value) {
    case 'create':
      return {
        mode: 'create',
        body: {
          bus: f.bus,
          storage: f.storage,
          sizeGiB: Number(f.size),
          discard: f.discard,
          ssd,
          iothread,
          noBackup: f.noBackup,
        },
      }
    case 'edit': {
      const d = disk.value!
      const body: Schemas['ProxmoxDiskUpdate'] = {}
      if (f.size.trim()) body.sizeGiB = Number(f.size)
      if (f.discard !== d.discard) body.discard = f.discard
      if (ssd !== d.ssd) body.ssd = ssd
      if (iothread !== d.iothread) body.iothread = iothread
      if (f.noBackup !== !d.backup) body.noBackup = f.noBackup
      return { mode: 'edit', disk: d.key, body }
    }
    default:
      return { mode: 'attach', disk: disk.value!.key, bus: f.bus }
  }
}

const unchanged = computed(() => {
  const c = change()
  return c.mode === 'edit' && Object.keys(c.body).length === 0
})

async function save() {
  if (sizeError.value || unchanged.value) return
  busy.value = true
  error.value = ''
  try {
    await props.run(change())
    open.value = false
  } catch (e) {
    error.value = errorMessage(i18n, e)
  } finally {
    busy.value = false
  }
}

defineExpose({ create, edit, attach })
</script>

<template>
  <BaseModal v-model:open="open" :title="title">
    <form :id="`${id}-form`" class="flex flex-col gap-3" @submit.prevent="save">
      <p v-if="disk" class="text-sm text-fg-muted">
        {{ t('proxmox.hardware.volume') }}: <span class="font-mono">{{ disk.volume }}</span>
        <template v-if="disk.size"> · {{ disk.size }}</template>
      </p>
      <BaseSelect
        v-if="mode !== 'edit'"
        v-model="form.bus"
        :label="t('proxmox.hardware.bus')"
        :options="busOptions"
      />
      <template v-if="mode !== 'attach'">
        <BaseSelect
          v-if="mode === 'create'"
          v-model="form.storage"
          :label="t('proxmox.hardware.storage')"
          :options="storageOptions"
        />
        <BaseInput
          v-model="form.size"
          :label="t(mode === 'edit' ? 'proxmox.hardware.newSize' : 'proxmox.hardware.sizeGiB')"
          inputmode="numeric"
          :required="mode === 'create'"
          :error="sizeError"
          :hint="mode === 'edit' ? t('proxmox.hardware.hints.grow') : ''"
        />
        <label class="flex items-center gap-2 text-sm">
          <input v-model="form.discard" type="checkbox" class="size-4" />
          {{ t('proxmox.hardware.discard') }}
        </label>
        <label v-if="supportsSSD(bus)" class="flex items-center gap-2 text-sm">
          <input v-model="form.ssd" type="checkbox" class="size-4" />
          {{ t('proxmox.hardware.ssd') }}
        </label>
        <label v-if="supportsIOThread(bus)" class="flex items-center gap-2 text-sm">
          <input v-model="form.iothread" type="checkbox" class="size-4" />
          {{ t('proxmox.hardware.iothread') }}
        </label>
        <label class="flex items-center gap-2 text-sm">
          <input v-model="form.noBackup" type="checkbox" class="size-4" />
          {{ t('proxmox.hardware.noBackup') }}
        </label>
      </template>
      <p v-if="error" role="alert" class="text-sm text-danger">{{ error }}</p>
    </form>
    <template #footer>
      <BaseButton variant="secondary" @click="open = false">{{ t('common.cancel') }}</BaseButton>
      <BaseButton
        type="submit"
        :form="`${id}-form`"
        :loading="busy"
        :disabled="!!sizeError || unchanged"
      >
        {{ t(`proxmox.hardware.diskDialog.submit.${mode}`) }}
      </BaseButton>
    </template>
  </BaseModal>
</template>
