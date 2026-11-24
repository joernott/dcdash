<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { api, unwrap } from '@/api/client'
import { errorMessage } from '@/api/errors'
import AppIcon from '@/components/ui/AppIcon.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import BaseModal from '@/components/ui/BaseModal.vue'
import DataTable from '@/components/ui/DataTable.vue'
import IconButton from '@/components/ui/IconButton.vue'
import { leaseAddress, leaseClient, type DhcpLease, type DhcpServer } from '@/dhcp/dhcp'
import { useToastStore } from '@/stores/toast'

/** Leases of one server (a search result or the leases of a subnet), with deletion. */
const props = defineProps<{
  server: DhcpServer
  leases: DhcpLease[]
  truncated: boolean
  caption: string
}>()
const emit = defineEmits<{ deleted: [] }>()

const i18n = useI18n()
const { t, d } = i18n
const toast = useToastStore()

const canDeleteAny = computed(() => props.leases.some((l) => l.canDelete))
const columns = computed(() => [
  { key: 'address', label: t('dhcp.address') },
  { key: 'client', label: props.server.family === 'v6' ? t('dhcp.duid') : t('dhcp.mac') },
  { key: 'hostname', label: t('dhcp.hostname') },
  { key: 'subnet', label: t('dhcp.subnet') },
  { key: 'state', label: t('dhcp.state') },
  { key: 'expires', label: t('dhcp.expires') },
  ...(canDeleteAny.value
    ? [{ key: 'actions', label: t('dhcp.actionsColumn'), class: 'w-px whitespace-nowrap' }]
    : []),
])

// Delete a lease.
const deleting = ref<DhcpLease | null>(null)
const deleteError = ref('')
const busy = ref(false)
const deleteOpen = computed({
  get: () => deleting.value !== null,
  set: (v: boolean) => {
    if (!v) deleting.value = null
  },
})

function askDelete(l: DhcpLease) {
  deleteError.value = ''
  deleting.value = l
}

async function remove() {
  const l = deleting.value
  if (!l) return
  busy.value = true
  try {
    const res = await unwrap(
      api.DELETE('/api/v1/dhcp/servers/{server}/leases/{ip}', {
        params: {
          path: { server: props.server.name, ip: l.ipAddress },
          query: l.type ? { type: l.type } : {},
        },
      }),
    )
    deleting.value = null
    toast.success(t('dhcp.leaseDeleted', { address: leaseAddress(l) }))
    for (const p of res.partners) {
      if (p.error)
        toast.show('error', t('dhcp.partnerFailed', { server: p.server, error: p.error }), 0)
    }
    emit('deleted')
  } catch (e) {
    deleteError.value = errorMessage(i18n, e)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="flex flex-col gap-2">
    <p class="text-sm text-fg-muted" aria-live="polite">
      {{ t('dhcp.leaseCount', { n: props.leases.length }, props.leases.length) }}
      <template v-if="props.truncated"> · {{ t('dhcp.truncated') }}</template>
    </p>
    <DataTable
      system="kea"
      :columns="columns"
      :rows="props.leases"
      :row-key="(l: DhcpLease) => `${l.ipAddress}-${l.type ?? ''}`"
      :caption="props.caption"
      :empty="t('dhcp.noLeases')"
    >
      <template #cell-address="{ row }">
        <span class="font-mono">{{ leaseAddress(row as DhcpLease) }}</span>
        <span v-if="(row as DhcpLease).type === 'IA_PD'" class="block text-xs text-fg-muted">
          {{ t('dhcp.delegatedPrefix') }}
        </span>
      </template>
      <template #cell-client="{ row }">
        <span class="font-mono text-xs break-all">{{ leaseClient(row as DhcpLease) || '–' }}</span>
      </template>
      <template #cell-hostname="{ row }">{{ (row as DhcpLease).hostname || '–' }}</template>
      <template #cell-subnet="{ row }">
        <span class="font-mono text-xs">{{ (row as DhcpLease).subnet }}</span>
      </template>
      <template #cell-state="{ row }">{{
        t(`dhcp.leaseStates.${(row as DhcpLease).state}`)
      }}</template>
      <template #cell-expires="{ row }">
        {{ d(new Date((row as DhcpLease).expires), 'short') }}
      </template>
      <template #cell-actions="{ row }">
        <IconButton
          v-if="(row as DhcpLease).canDelete"
          icon="delete"
          size="sm"
          variant="danger"
          :label="t('dhcp.deleteLeaseFor', { address: leaseAddress(row as DhcpLease) })"
          @click="askDelete(row as DhcpLease)"
        />
      </template>
    </DataTable>

    <BaseModal v-if="canDeleteAny" v-model:open="deleteOpen" :title="t('dhcp.deleteLease')">
      <p>
        {{ t('dhcp.deleteLeaseQuestion', { address: deleting ? leaseAddress(deleting) : '' }) }}
      </p>
      <p class="mt-2 text-sm text-fg-muted">{{ t('dhcp.deleteLeaseHint') }}</p>
      <p v-if="props.server.partners?.length" class="mt-2 text-sm text-fg-muted">
        {{ t('dhcp.deleteLeasePartners', { partners: props.server.partners.join(', ') }) }}
      </p>
      <p v-if="deleteError" role="alert" class="mt-2 text-sm text-danger">{{ deleteError }}</p>
      <template #footer>
        <BaseButton variant="secondary" @click="deleteOpen = false">{{
          t('common.cancel')
        }}</BaseButton>
        <BaseButton variant="danger" :loading="busy" @click="remove">
          <AppIcon name="delete" />{{ t('dhcp.delete') }}</BaseButton
        >
      </template>
    </BaseModal>
  </div>
</template>
