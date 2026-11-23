<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import UsageBar from '@/components/proxmox/UsageBar.vue'
import IconButton from '@/components/ui/IconButton.vue'
import DataTable from '@/components/ui/DataTable.vue'
import {
  formatCount,
  pdUtilization,
  utilization,
  type DhcpServer,
  type DhcpSubnet,
} from '@/dhcp/dhcp'

/** The subnets of a server with pools and utilization. */
const props = defineProps<{ server: DhcpServer; subnets: DhcpSubnet[] }>()
const emit = defineEmits<{ showLeases: [subnetId: number] }>()

const { t, locale } = useI18n()

const columns = computed(() => [
  { key: 'prefix', label: t('dhcp.subnet') },
  { key: 'pools', label: t('dhcp.pools') },
  { key: 'usage', label: t('dhcp.utilization'), class: 'w-64' },
  ...(props.server.capabilities.leases
    ? [{ key: 'actions', label: t('dhcp.actionsColumn'), class: 'w-px whitespace-nowrap' }]
    : []),
])

function count(n: number): string {
  return formatCount(n, locale.value)
}
</script>

<template>
  <section aria-labelledby="dhcp-subnets" class="flex flex-col gap-3">
    <h2 id="dhcp-subnets" class="sr-only">{{ t('dhcp.tabs.subnets') }}</h2>
    <DataTable
      :columns="columns"
      :rows="props.subnets"
      :row-key="(s: DhcpSubnet) => s.id"
      :caption="t('dhcp.tabs.subnets')"
      :empty="t('dhcp.noSubnets')"
    >
      <template #cell-prefix="{ row }">
        <span class="font-mono">{{ (row as DhcpSubnet).prefix }}</span>
        <span class="block text-xs text-fg-muted">
          {{ t('dhcp.subnetId', { id: (row as DhcpSubnet).id }) }}
          <template v-if="(row as DhcpSubnet).sharedNetwork">
            · {{ t('dhcp.sharedNetwork', { name: (row as DhcpSubnet).sharedNetwork }) }}
          </template>
        </span>
      </template>
      <template #cell-pools="{ row }">
        <ul class="font-mono text-xs">
          <li v-for="p in (row as DhcpSubnet).pools" :key="p">{{ p }}</li>
          <li v-for="p in (row as DhcpSubnet).pdPools ?? []" :key="p">{{ p }}</li>
        </ul>
        <span
          v-if="!(row as DhcpSubnet).pools.length && !(row as DhcpSubnet).pdPools?.length"
          class="text-fg-muted"
          >–</span
        >
      </template>
      <template #cell-usage="{ row }">
        <div class="flex flex-col gap-2">
          <UsageBar
            :label="t('dhcp.addresses')"
            :ratio="utilization(row as DhcpSubnet)"
            :detail="
              (row as DhcpSubnet).stats
                ? t('dhcp.usedOf', {
                    used: count((row as DhcpSubnet).stats!.assigned),
                    total: count((row as DhcpSubnet).stats!.total),
                  })
                : undefined
            "
          />
          <UsageBar
            v-if="pdUtilization(row as DhcpSubnet) !== undefined"
            :label="t('dhcp.prefixes')"
            :ratio="pdUtilization(row as DhcpSubnet)"
            :detail="
              t('dhcp.usedOf', {
                used: count((row as DhcpSubnet).stats!.assignedPd ?? 0),
                total: count((row as DhcpSubnet).stats!.totalPd ?? 0),
              })
            "
          />
          <span v-if="(row as DhcpSubnet).stats?.declined" class="text-xs text-warning">
            {{ t('dhcp.declined', { n: (row as DhcpSubnet).stats!.declined }) }}
          </span>
        </div>
      </template>
      <template #cell-actions="{ row }">
        <IconButton
          icon="leases"
          :label="t('dhcp.showLeases')"
          size="sm"
          variant="secondary"
          @click="emit('showLeases', (row as DhcpSubnet).id)"
        />
      </template>
    </DataTable>
  </section>
</template>
