<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'

import type { Schemas } from '@/api/client'
import DataTable from '@/components/ui/DataTable.vue'
import type { MacResult } from '@/switches/switches'

/** The result of a MAC address search on all switches; access ports first. */
const props = defineProps<{ result: Schemas['SwitchMACSearch'] }>()
const { t } = useI18n()

const columns = computed(() => [
  { key: 'mac', label: t('switches.mac.mac') },
  { key: 'switch', label: t('switches.mac.switch') },
  { key: 'port', label: t('switches.columns.port') },
  { key: 'vlan', label: 'VLAN' },
  { key: 'where', label: t('switches.mac.where') },
])
</script>

<template>
  <div class="flex flex-col gap-3">
    <p v-for="e in props.result.errors" :key="e.switch" class="text-sm text-warning">
      {{ t('switches.mac.switchFailed', { switch: e.switch, error: e.error }) }}
    </p>
    <DataTable
      :columns="columns"
      :rows="props.result.results"
      :row-key="(r: MacResult) => `${r.switch}|${r.port}|${r.vlan}|${r.mac}`"
      :caption="t('switches.tabs.macs')"
      :empty="t('switches.mac.none')"
      system="switch"
    >
      <template #cell-mac="{ row }">
        <span class="font-mono">{{ (row as MacResult).mac }}</span>
      </template>
      <template #cell-switch="{ row }">
        <RouterLink
          :to="{ name: 'switches', params: { switch: (row as MacResult).switch } }"
          class="text-primary underline"
        >
          {{ (row as MacResult).switch }}
        </RouterLink>
      </template>
      <template #cell-port="{ row }">
        <span class="font-mono">{{ (row as MacResult).port }}</span>
        <div v-if="(row as MacResult).portDescription" class="text-xs text-fg-muted">
          {{ (row as MacResult).portDescription }}
        </div>
      </template>
      <template #cell-where="{ row }">
        <span :class="(row as MacResult).uplink ? 'text-fg-muted' : 'font-medium text-success'">
          {{ (row as MacResult).uplink ? t('switches.mac.behind') : t('switches.mac.connected') }}
        </span>
      </template>
    </DataTable>
  </div>
</template>
