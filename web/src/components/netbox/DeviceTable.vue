<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Schemas } from '@/api/client'
import BaseButton from '@/components/ui/BaseButton.vue'
import DataTable from '@/components/ui/DataTable.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

type DeviceDiff = Schemas['NetBoxDeviceDiff']

/** Proxmox nodes or switches compared with their NetBox devices, with the update button. */
const props = defineProps<{
  devices: DeviceDiff[]
  caption: string
  loading: boolean
  canWrite: boolean
  /** The name of the device being updated. */
  busy: string
}>()
const emit = defineEmits<{ apply: [device: DeviceDiff] }>()
const { t } = useI18n()

const columns = computed(() => [
  { key: 'name', label: t('netbox.columns.device') },
  { key: 'state', label: t('netbox.columns.state') },
  { key: 'differences', label: t('netbox.columns.differences') },
  { key: 'actions', label: t('netbox.columns.actions'), class: 'w-px whitespace-nowrap' },
])

const badge: Record<DeviceDiff['state'], 'ok' | 'warning' | 'error' | 'unknown'> = {
  ok: 'ok',
  differs: 'warning',
  missing: 'error',
  unavailable: 'unknown',
}
</script>

<template>
  <DataTable
    system="netbox"
    :columns="columns"
    :rows="props.devices"
    :row-key="(d: DeviceDiff) => `${d.kind}/${d.name}`"
    :caption="props.caption"
    :loading="props.loading"
    :empty="t('netbox.noDifferences')"
  >
    <template #cell-name="{ row }">
      <span>{{ (row as DeviceDiff).name }}</span>
      <span
        v-if="(row as DeviceDiff).device !== (row as DeviceDiff).name"
        class="block text-xs text-fg-muted"
      >
        {{ t('netbox.inNetBoxAs', { device: (row as DeviceDiff).device }) }}
      </span>
    </template>
    <template #cell-state="{ row }">
      <StatusBadge
        :status="badge[(row as DeviceDiff).state]"
        :label="t(`netbox.states.${(row as DeviceDiff).state}`)"
      />
      <span v-if="(row as DeviceDiff).error" class="block text-xs text-fg-muted">
        {{ (row as DeviceDiff).error }}
      </span>
    </template>
    <template #cell-differences="{ row }">
      <dl v-if="(row as DeviceDiff).differences.length" class="grid gap-1 text-sm">
        <div v-for="d in (row as DeviceDiff).differences" :key="d.field">
          <dt class="font-medium">{{ t(`netbox.fields.${d.field}`) }}</dt>
          <dd class="font-mono text-xs">
            {{ t('netbox.actual') }}: {{ d.actual || '–' }}<br />
            {{ t('netbox.inNetBox') }}: {{ d.netbox || '–' }}
          </dd>
        </div>
      </dl>
      <span v-else class="text-fg-muted">–</span>
    </template>
    <template #cell-actions="{ row }">
      <template v-if="props.canWrite">
        <BaseButton
          v-if="(row as DeviceDiff).updatable"
          size="sm"
          variant="secondary"
          :loading="props.busy === (row as DeviceDiff).name"
          :aria-label="`${t('netbox.apply')}: ${(row as DeviceDiff).name}`"
          @click="emit('apply', row as DeviceDiff)"
        >
          {{ t('netbox.apply') }}
        </BaseButton>
        <span v-else-if="(row as DeviceDiff).state === 'differs'" class="text-xs text-fg-muted">
          {{ t('netbox.notUpdatable') }}
        </span>
      </template>
    </template>
  </DataTable>
</template>
