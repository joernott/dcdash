<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { api, unwrap } from '@/api/client'
import { errorMessage } from '@/api/errors'
import AppIcon from '@/components/ui/AppIcon.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import IconButton from '@/components/ui/IconButton.vue'
import BaseInput from '@/components/ui/BaseInput.vue'
import BaseModal from '@/components/ui/BaseModal.vue'
import BaseSelect from '@/components/ui/BaseSelect.vue'
import DataTable from '@/components/ui/DataTable.vue'
import {
  leaseAddress,
  leaseClient,
  type DhcpLease,
  type DhcpServer,
  type DhcpSubnet,
  type SearchKey,
} from '@/dhcp/dhcp'
import { useToastStore } from '@/stores/toast'

/** Lease search and the leases of a subnet, with deletion. */
const props = defineProps<{ server: DhcpServer; subnets: DhcpSubnet[]; initialSubnet: string }>()

const i18n = useI18n()
const { t, d } = i18n
const toast = useToastStore()

const leases = ref<DhcpLease[] | null>(null)
const truncated = ref(false)
const loading = ref(false)
const error = ref('')
const subnet = ref(props.initialSubnet)
const by = ref<SearchKey>(props.server.searchKeys[0] ?? 'ip')
const query = ref('')

const subnetOptions = computed(() => [
  { value: '', label: t('dhcp.chooseSubnet') },
  ...props.subnets.map((s) => ({ value: String(s.id), label: s.prefix })),
])
const keyOptions = computed(() =>
  props.server.searchKeys.map((k) => ({ value: k, label: t(`dhcp.searchKeys.${k}`) })),
)
const canDeleteAny = computed(() => (leases.value ?? []).some((l) => l.canDelete))
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

async function load(params: { subnet?: number; by?: SearchKey; q?: string }) {
  loading.value = true
  error.value = ''
  try {
    const res = await unwrap(
      api.GET('/api/v1/dhcp/servers/{server}/leases', {
        params: { path: { server: props.server.name }, query: params },
      }),
    )
    leases.value = res.leases
    truncated.value = res.truncated
  } catch (e) {
    leases.value = null
    error.value = errorMessage(i18n, e)
  } finally {
    loading.value = false
  }
}

/** The last query, repeated after a deletion. */
let last: Parameters<typeof load>[0] | null = null

function search() {
  if (!query.value.trim()) return
  subnet.value = ''
  last = { by: by.value, q: query.value.trim() }
  void load(last)
}

function showSubnet(id: string) {
  subnet.value = id
  if (!id) return
  query.value = ''
  last = { subnet: Number(id) }
  void load(last)
}

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
    if (last) await load(last)
  } catch (e) {
    deleteError.value = errorMessage(i18n, e)
  } finally {
    busy.value = false
  }
}

onMounted(() => {
  if (subnet.value) showSubnet(subnet.value)
})
</script>

<template>
  <section aria-labelledby="dhcp-leases" class="flex flex-col gap-3">
    <h2 id="dhcp-leases" class="sr-only">{{ t('dhcp.tabs.leases') }}</h2>
    <div class="flex flex-wrap items-end gap-6">
      <form class="flex flex-wrap items-end gap-3" role="search" @submit.prevent="search">
        <BaseSelect v-model="by" :label="t('dhcp.searchBy')" :options="keyOptions" />
        <BaseInput v-model="query" :label="t('dhcp.searchValue')" type="search" />
        <IconButton
          icon="search"
          :label="t('dhcp.search')"
          size="md"
          type="submit"
          variant="primary"
          :loading="loading"
          :disabled="!query.trim()"
        />
      </form>
      <BaseSelect
        :model-value="subnet"
        :label="t('dhcp.subnetLeases')"
        :options="subnetOptions"
        @update:model-value="showSubnet"
      />
    </div>

    <p
      v-if="error"
      role="alert"
      class="rounded-md border border-danger px-3 py-2 text-sm text-danger"
    >
      {{ error }}
    </p>
    <template v-else-if="leases">
      <p class="text-sm text-fg-muted" aria-live="polite">
        {{ t('dhcp.leaseCount', { n: leases.length }, leases.length) }}
        <template v-if="truncated"> · {{ t('dhcp.truncated') }}</template>
      </p>
      <DataTable
        :columns="columns"
        :rows="leases"
        :row-key="(l: DhcpLease) => `${l.ipAddress}-${l.type ?? ''}`"
        :caption="t('dhcp.tabs.leases')"
        :empty="t('dhcp.noLeases')"
      >
        <template #cell-address="{ row }">
          <span class="font-mono">{{ leaseAddress(row as DhcpLease) }}</span>
          <span v-if="(row as DhcpLease).type === 'IA_PD'" class="block text-xs text-fg-muted">
            {{ t('dhcp.delegatedPrefix') }}
          </span>
        </template>
        <template #cell-client="{ row }">
          <span class="font-mono text-xs break-all">{{
            leaseClient(row as DhcpLease) || '–'
          }}</span>
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
            icon="delete"
            v-if="(row as DhcpLease).canDelete"
            size="sm"
            variant="danger"
            :label="t('dhcp.deleteLeaseFor', { address: leaseAddress(row as DhcpLease) })"
            @click="askDelete(row as DhcpLease)"
          />
        </template>
      </DataTable>
    </template>
    <p v-else-if="!loading" class="text-sm text-fg-muted">{{ t('dhcp.leasesHint') }}</p>

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
  </section>
</template>
