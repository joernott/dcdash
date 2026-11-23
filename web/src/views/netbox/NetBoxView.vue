<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute, useRouter } from 'vue-router'

import { api, ApiError, unwrap, type Schemas } from '@/api/client'
import { errorMessage } from '@/api/errors'
import AppIcon from '@/components/ui/AppIcon.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import IconButton from '@/components/ui/IconButton.vue'
import BaseModal from '@/components/ui/BaseModal.vue'
import BaseSelect from '@/components/ui/BaseSelect.vue'
import DataTable from '@/components/ui/DataTable.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import { netboxFailure, type NetBoxInfo } from '@/netbox/netbox'
import { useSessionStore } from '@/stores/session'
import { useToastStore } from '@/stores/toast'

type GuestDiff = Schemas['NetBoxGuestDiff']
type AddressDiff = Schemas['NetBoxAddressDiff']

const i18n = useI18n()
const { t } = i18n
const route = useRoute()
const router = useRouter()
const session = useSessionStore()
const toast = useToastStore()

const info = ref<NetBoxInfo | null>(null)
const notConfigured = ref(false)
const guests = ref<GuestDiff[]>([])
const loading = ref(false)
const error = ref('')
const onlyDifferences = ref(true)
const busy = ref('')

const cluster = computed(() => String(route.params.cluster ?? ''))
const clusterOptions = computed(() =>
  (info.value?.clusters ?? []).map((c) => ({ value: c, label: c })),
)
const canRead = computed(() => !!info.value && session.can(`netbox:${info.value.name}:sync:read`))
const canWrite = computed(() => !!info.value && session.can(`netbox:${info.value.name}:sync:write`))
const shown = computed(() =>
  onlyDifferences.value ? guests.value.filter((g) => g.state !== 'ok') : guests.value,
)
const counts = computed(() => {
  const c: Record<string, number> = { ok: 0, missing: 0, extra: 0, differs: 0 }
  for (const g of guests.value) c[g.state] = (c[g.state] ?? 0) + 1
  return c
})

const columns = computed(() => [
  { key: 'name', label: t('netbox.columns.guest') },
  { key: 'state', label: t('netbox.columns.state') },
  { key: 'differences', label: t('netbox.columns.differences') },
  { key: 'actions', label: t('netbox.columns.actions'), class: 'w-px whitespace-nowrap' },
])

const badge: Record<GuestDiff['state'], 'ok' | 'warning' | 'error' | 'unknown'> = {
  ok: 'ok',
  differs: 'warning',
  missing: 'error',
  extra: 'unknown',
}

async function loadInfo() {
  try {
    info.value = await unwrap(api.GET('/api/v1/netbox'))
    if (!cluster.value && info.value.clusters.length > 0) {
      await router.replace({ name: 'netbox', params: { cluster: info.value.clusters[0] } })
    }
  } catch (e) {
    if (e instanceof ApiError && e.status === 404) notConfigured.value = true
    else error.value = errorMessage(i18n, e)
  }
}

async function load() {
  if (!cluster.value || !canRead.value) return
  loading.value = true
  try {
    const res = await unwrap(
      api.GET('/api/v1/netbox/compare/guests/{cluster}', {
        params: { path: { cluster: cluster.value } },
      }),
    )
    guests.value = res.guests
    error.value = ''
  } catch (e) {
    guests.value = []
    error.value = errorMessage(i18n, e)
  } finally {
    loading.value = false
  }
}

function rowKey(g: GuestDiff): string {
  return g.type ? `${g.type}/${g.vmid}` : `netbox/${g.netboxId}`
}

function report(result: Schemas['NetBoxResult'], success: string) {
  const failed = netboxFailure(result, t)
  if (failed) toast.error(failed)
  else toast.success(success)
}

async function apply(g: GuestDiff) {
  if (!g.type || !g.vmid) return
  busy.value = rowKey(g)
  try {
    const res = await unwrap(
      api.POST('/api/v1/netbox/compare/guests/{cluster}/{type}/{vmid}', {
        params: { path: { cluster: cluster.value, type: g.type, vmid: g.vmid } },
      }),
    )
    report(res.netbox, t('netbox.applied', { name: g.name }))
    await load()
  } catch (e) {
    toast.error(errorMessage(i18n, e))
  } finally {
    busy.value = ''
  }
}

// Deleting a VM that exists only in NetBox, after confirmation.
const deleting = ref<GuestDiff | null>(null)
const deleteOpen = computed({
  get: () => deleting.value !== null,
  set: (v: boolean) => {
    if (!v) deleting.value = null
  },
})

async function removeVM() {
  const g = deleting.value
  if (!g?.netboxId) return
  busy.value = rowKey(g)
  try {
    const res = await unwrap(
      api.DELETE('/api/v1/netbox/compare/guests/{cluster}/vms/{id}', {
        params: { path: { cluster: cluster.value, id: g.netboxId } },
      }),
    )
    deleting.value = null
    report(res.netbox, t('netbox.deleted', { name: g.name }))
    await load()
  } catch (e) {
    toast.error(errorMessage(i18n, e))
  } finally {
    busy.value = ''
  }
}

// Addresses are compared on request: it reads all reservations and records.
const addresses = ref<AddressDiff[] | null>(null)
const addressErrors = ref<string[]>([])
const addressesLoading = ref(false)
const addressError = ref('')
const addressColumns = computed(() => [
  { key: 'address', label: t('netbox.columns.address') },
  { key: 'source', label: t('netbox.columns.source') },
  { key: 'state', label: t('netbox.columns.state') },
])

async function compareAddresses() {
  addressesLoading.value = true
  try {
    const res = await unwrap(api.GET('/api/v1/netbox/compare/addresses'))
    addresses.value = res.addresses
    addressErrors.value = res.errors
    addressError.value = ''
  } catch (e) {
    addressError.value = errorMessage(i18n, e)
  } finally {
    addressesLoading.value = false
  }
}

function sourceLabel(a: AddressDiff): string {
  return a.source === 'dns'
    ? t('netbox.sources.dns', { name: a.name ?? '' })
    : t('netbox.sources.dhcp', { server: a.source.replace(/^dhcp:/, '') })
}

onMounted(loadInfo)
watch([cluster, canRead], () => void load(), { immediate: true })
</script>

<template>
  <div class="flex flex-col gap-6">
    <div class="flex flex-col gap-3">
      <h1 class="text-2xl font-semibold">{{ t('nav.netbox') }}</h1>
      <p v-if="notConfigured" class="text-fg-muted">{{ t('netbox.notConfigured') }}</p>
      <p v-else-if="info && !canRead" class="text-fg-muted">{{ t('netbox.noCompare') }}</p>
      <div v-if="info && canRead && clusterOptions.length > 0" class="max-w-xs">
        <BaseSelect
          :model-value="cluster"
          :label="t('netbox.cluster')"
          :options="clusterOptions"
          @update:model-value="
            (v: string) => router.push({ name: 'netbox', params: { cluster: v } })
          "
        />
      </div>
      <p v-if="info && canRead && clusterOptions.length === 0" class="text-fg-muted">
        {{ t('netbox.noClusters') }}
      </p>
      <p v-if="error" role="alert" class="text-danger">{{ error }}</p>
    </div>

    <section v-if="canRead && cluster" aria-labelledby="netbox-guests" class="flex flex-col gap-3">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <h2 id="netbox-guests" class="text-lg font-semibold">{{ t('netbox.guests') }}</h2>
        <div class="flex flex-wrap items-center gap-4">
          <p class="text-sm text-fg-muted" data-testid="netbox-counts">
            {{
              t('netbox.counts', {
                ok: counts.ok,
                differs: counts.differs,
                missing: counts.missing,
                extra: counts.extra,
              })
            }}
          </p>
          <label class="flex items-center gap-2 text-sm">
            <input v-model="onlyDifferences" type="checkbox" class="size-4" />
            {{ t('netbox.onlyDifferences') }}
          </label>
          <BaseButton variant="secondary" size="sm" :loading="loading" @click="load">
            {{ t('netbox.refresh') }}
          </BaseButton>
        </div>
      </div>
      <DataTable
        :columns="columns"
        :rows="shown"
        :row-key="rowKey"
        :caption="t('netbox.guests')"
        :loading="loading"
        :empty="t('netbox.noDifferences')"
      >
        <template #cell-name="{ row }">
          <RouterLink
            v-if="(row as GuestDiff).type"
            class="text-primary underline"
            :to="{
              name: 'proxmox-guest',
              params: { cluster, type: (row as GuestDiff).type, vmid: (row as GuestDiff).vmid },
            }"
          >
            {{ (row as GuestDiff).name }}
          </RouterLink>
          <span v-else>{{ (row as GuestDiff).name }}</span>
          <span class="block text-xs text-fg-muted">
            {{
              (row as GuestDiff).type
                ? `${(row as GuestDiff).type} ${(row as GuestDiff).vmid}`
                : t('netbox.onlyInNetBox')
            }}
          </span>
        </template>
        <template #cell-state="{ row }">
          <StatusBadge
            :status="badge[(row as GuestDiff).state]"
            :label="t(`netbox.states.${(row as GuestDiff).state}`)"
          />
        </template>
        <template #cell-differences="{ row }">
          <dl v-if="(row as GuestDiff).differences.length" class="grid gap-1 text-sm">
            <div v-for="d in (row as GuestDiff).differences" :key="d.field">
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
          <div v-if="canWrite" class="flex gap-2">
            <BaseButton
              v-if="(row as GuestDiff).type && (row as GuestDiff).state !== 'ok'"
              size="sm"
              variant="secondary"
              :loading="busy === rowKey(row as GuestDiff)"
              @click="apply(row as GuestDiff)"
            >
              {{ t('netbox.apply') }}
            </BaseButton>
            <IconButton
              icon="delete"
              :label="t('netbox.delete')"
              v-if="(row as GuestDiff).state === 'extra' && (row as GuestDiff).tagged"
              size="sm"
              variant="danger"
              @click="deleting = row as GuestDiff"
            />
          </div>
        </template>
      </DataTable>
    </section>

    <section v-if="canRead" aria-labelledby="netbox-addresses" class="flex flex-col gap-3">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <h2 id="netbox-addresses" class="text-lg font-semibold">{{ t('netbox.addresses') }}</h2>
        <BaseButton
          variant="secondary"
          size="sm"
          :loading="addressesLoading"
          @click="compareAddresses"
        >
          {{ t('netbox.compareAddresses') }}
        </BaseButton>
      </div>
      <p class="text-sm text-fg-muted">{{ t('netbox.addressesHint') }}</p>
      <p v-if="addressError" role="alert" class="text-danger">{{ addressError }}</p>
      <ul v-if="addressErrors.length" role="alert" class="text-sm text-warning">
        <li v-for="e in addressErrors" :key="e">{{ t('netbox.sourceError', { error: e }) }}</li>
      </ul>
      <DataTable
        v-if="addresses !== null"
        :columns="addressColumns"
        :rows="addresses"
        :row-key="(a: AddressDiff) => `${a.source}/${a.address}/${a.name ?? ''}`"
        :caption="t('netbox.addresses')"
        :empty="t('netbox.noDifferences')"
      >
        <template #cell-address="{ row }">
          <span class="font-mono">{{ (row as AddressDiff).address }}</span>
        </template>
        <template #cell-source="{ row }">{{ sourceLabel(row as AddressDiff) }}</template>
        <template #cell-state="{ row }">
          <template v-if="(row as AddressDiff).state === 'missing'">
            {{ t('netbox.addressMissing') }}
          </template>
          <template v-else>
            {{ t('netbox.dnsNameDiffers', { name: (row as AddressDiff).netbox || '–' }) }}
          </template>
        </template>
      </DataTable>
    </section>

    <BaseModal v-model:open="deleteOpen" :title="t('netbox.deleteTitle')">
      <p>{{ t('netbox.deleteText', { name: deleting?.name ?? '' }) }}</p>
      <template #footer>
        <BaseButton variant="secondary" @click="deleting = null">{{
          t('common.cancel')
        }}</BaseButton>
        <BaseButton variant="danger" :loading="busy !== ''" @click="removeVM">
          <AppIcon name="delete" />{{ t('netbox.delete') }}</BaseButton
        >
      </template>
    </BaseModal>
  </div>
</template>
