<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, nextTick, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute, useRouter } from 'vue-router'

import { api, ApiError, unwrap, type Schemas } from '@/api/client'
import { errorMessage } from '@/api/errors'
import AppIcon from '@/components/ui/AppIcon.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import BaseModal from '@/components/ui/BaseModal.vue'
import BaseSelect from '@/components/ui/BaseSelect.vue'
import DataTable from '@/components/ui/DataTable.vue'
import IconButton from '@/components/ui/IconButton.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import { netboxFailure, type NetBoxInfo } from '@/netbox/netbox'
import { useSessionStore } from '@/stores/session'
import { useToastStore } from '@/stores/toast'

type GuestDiff = Schemas['NetBoxGuestDiff']
type AddressDiff = Schemas['NetBoxAddressDiff']

/**
 * NetBox compared with the connected systems: the guests of every mapped Proxmox cluster (the
 * cluster in the route is an optional filter) and, on request, the addresses of DNS and DHCP.
 */
const i18n = useI18n()
const { t } = i18n
const route = useRoute()
const router = useRouter()
const session = useSessionStore()
const toast = useToastStore()

// The comparison of a cluster: null until loaded or if loading failed.
interface ClusterGuests {
  guests: GuestDiff[] | null
  loading: boolean
  error: string
}

const info = ref<NetBoxInfo | null>(null)
const notConfigured = ref(false)
const state = reactive<Record<string, ClusterGuests>>({})
const error = ref('')
const onlyDifferences = ref(true)
const busy = ref('')

const filter = computed(() => String(route.params.cluster ?? ''))
const clusters = computed(() => info.value?.clusters ?? [])
const shown = computed(() =>
  filter.value ? clusters.value.filter((c) => c === filter.value) : clusters.value,
)
const filterOptions = computed(() => [
  { value: '', label: t('netbox.allClusters') },
  ...clusters.value.map((c) => ({ value: c, label: c })),
])
const canRead = computed(() => !!info.value && session.can(`netbox:${info.value.name}:sync:read`))
const canWrite = computed(() => !!info.value && session.can(`netbox:${info.value.name}:sync:write`))
const loading = computed(() => shown.value.some((c) => state[c]?.loading))

function rows(cluster: string) {
  const all = state[cluster]?.guests ?? []
  return onlyDifferences.value ? all.filter((g) => g.state !== 'ok') : all
}

function counts(cluster: string) {
  const c: Record<string, number> = { ok: 0, missing: 0, extra: 0, differs: 0 }
  for (const g of state[cluster]?.guests ?? []) c[g.state] = (c[g.state] ?? 0) + 1
  return c
}

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
  } catch (e) {
    if (e instanceof ApiError && e.status === 404) notConfigured.value = true
    else error.value = errorMessage(i18n, e)
  }
}

async function load(cluster: string) {
  if (!canRead.value) return
  state[cluster] ??= { guests: null, loading: false, error: '' }
  // The reactive proxy, not the plain object the assignment returns.
  const s = state[cluster]!
  s.loading = true
  try {
    const res = await unwrap(
      api.GET('/api/v1/netbox/compare/guests/{cluster}', { params: { path: { cluster } } }),
    )
    s.guests = res.guests
    s.error = ''
  } catch (e) {
    s.guests = null
    s.error = errorMessage(i18n, e)
  } finally {
    s.loading = false
  }
}

async function refresh() {
  await Promise.all(shown.value.map(load))
}

function selectFilter(c: string) {
  if (c !== filter.value) void router.push({ name: 'netbox', params: c ? { cluster: c } : {} })
}

function rowKey(g: GuestDiff): string {
  return g.type ? `${g.type}/${g.vmid}` : `netbox/${g.netboxId}`
}

function report(result: Schemas['NetBoxResult'], success: string) {
  const failed = netboxFailure(result, t)
  if (failed) toast.error(failed)
  else toast.success(success)
}

async function apply(cluster: string, g: GuestDiff) {
  if (!g.type || !g.vmid) return
  busy.value = `${cluster}/${rowKey(g)}`
  try {
    const res = await unwrap(
      api.POST('/api/v1/netbox/compare/guests/{cluster}/{type}/{vmid}', {
        params: { path: { cluster, type: g.type, vmid: g.vmid } },
      }),
    )
    report(res.netbox, t('netbox.applied', { name: g.name }))
    await load(cluster)
  } catch (e) {
    toast.error(errorMessage(i18n, e))
  } finally {
    busy.value = ''
  }
}

// Deleting a VM that exists only in NetBox, after confirmation.
const deleting = ref<{ cluster: string; guest: GuestDiff } | null>(null)
const deleteOpen = computed({
  get: () => deleting.value !== null,
  set: (v: boolean) => {
    if (!v) deleting.value = null
  },
})

async function removeVM() {
  const target = deleting.value
  if (!target?.guest.netboxId) return
  const { cluster, guest: g } = target
  busy.value = `${cluster}/${rowKey(g)}`
  try {
    const res = await unwrap(
      api.DELETE('/api/v1/netbox/compare/guests/{cluster}/vms/{id}', {
        params: { path: { cluster, id: g.netboxId! } },
      }),
    )
    deleting.value = null
    report(res.netbox, t('netbox.deleted', { name: g.name }))
    await load(cluster)
  } catch (e) {
    toast.error(errorMessage(i18n, e))
  } finally {
    busy.value = ''
  }
}

// Addresses are compared on request: it reads all reservations and records.
const addresses = ref<AddressDiff[] | null>(null)
const addressErrors = ref<string[]>([])
const addressesOpen = ref(false)
const addressesLoading = ref(false)
const addressError = ref('')
const addressHeading = ref<HTMLElement | null>(null)
const addressColumns = computed(() => [
  { key: 'address', label: t('netbox.columns.address') },
  { key: 'source', label: t('netbox.columns.source') },
  { key: 'state', label: t('netbox.columns.state') },
])

async function compareAddresses() {
  addressesOpen.value = true
  addressesLoading.value = true
  await nextTick()
  addressHeading.value?.scrollIntoView?.({ block: 'nearest' })
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
// Loads the clusters shown (again after a permission change) whose guests weren't loaded yet.
watch(
  [shown, canRead],
  () => {
    for (const c of shown.value) if (!state[c]) void load(c)
  },
  { immediate: true },
)
</script>

<template>
  <div class="flex flex-col gap-6">
    <!-- The heading and, on the right, the cluster filter, refresh and the address comparison. -->
    <div class="flex flex-wrap items-end justify-between gap-4">
      <h1 class="text-2xl font-semibold">{{ t('nav.netbox') }}</h1>
      <div v-if="info && canRead" class="flex flex-wrap items-end gap-3">
        <BaseSelect
          v-if="clusters.length > 1"
          :model-value="filter"
          :label="t('netbox.cluster')"
          :options="filterOptions"
          @update:model-value="selectFilter"
        />
        <label class="flex items-center gap-2 pb-2 text-sm">
          <input v-model="onlyDifferences" type="checkbox" class="size-4" />
          {{ t('netbox.onlyDifferences') }}
        </label>
        <IconButton
          icon="refresh"
          :label="t('home.refresh')"
          size="md"
          variant="secondary"
          :loading="loading"
          @click="refresh"
        />
        <BaseButton variant="secondary" :loading="addressesLoading" @click="compareAddresses">
          {{ t('netbox.compareAddresses') }}
        </BaseButton>
      </div>
    </div>

    <p v-if="notConfigured" class="text-fg-muted">{{ t('netbox.notConfigured') }}</p>
    <p v-else-if="info && !canRead" class="text-fg-muted">{{ t('netbox.noCompare') }}</p>
    <p v-else-if="info && clusters.length === 0" class="text-fg-muted">
      {{ t('netbox.noClusters') }}
    </p>
    <p v-if="error" role="alert" class="text-danger">{{ error }}</p>

    <!-- The address comparison, on request. -->
    <section
      v-if="canRead && addressesOpen"
      aria-labelledby="netbox-addresses"
      class="flex flex-col gap-3 rounded-md border border-border p-4"
    >
      <div class="flex items-center justify-between gap-3">
        <h2 id="netbox-addresses" ref="addressHeading" class="text-lg font-semibold">
          {{ t('netbox.addresses') }}
        </h2>
        <IconButton
          icon="close"
          :label="t('netbox.closeAddresses')"
          @click="addressesOpen = false"
        />
      </div>
      <p class="text-sm text-fg-muted">{{ t('netbox.addressesHint') }}</p>
      <p v-if="addressError" role="alert" class="text-danger">{{ addressError }}</p>
      <ul v-if="addressErrors.length" role="alert" class="text-sm text-warning">
        <li v-for="e in addressErrors" :key="e">{{ t('netbox.sourceError', { error: e }) }}</li>
      </ul>
      <DataTable
        v-if="addresses !== null || addressesLoading"
        system="netbox"
        :columns="addressColumns"
        :rows="addresses ?? []"
        :row-key="(a: AddressDiff) => `${a.source}/${a.address}/${a.name ?? ''}`"
        :caption="t('netbox.addresses')"
        :loading="addressesLoading"
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

    <template v-if="canRead">
      <section
        v-for="c in shown"
        :key="c"
        :aria-labelledby="`netbox-guests-${c}`"
        class="flex flex-col gap-3"
      >
        <div class="flex flex-wrap items-center justify-between gap-3">
          <h2 :id="`netbox-guests-${c}`" class="text-lg font-semibold">
            {{ t('netbox.guestsOf', { cluster: c }) }}
          </h2>
          <p v-if="state[c]?.guests" class="text-sm text-fg-muted" data-testid="netbox-counts">
            {{
              t('netbox.counts', {
                ok: counts(c).ok,
                differs: counts(c).differs,
                missing: counts(c).missing,
                extra: counts(c).extra,
              })
            }}
          </p>
        </div>
        <p v-if="state[c]?.error" role="alert" class="text-danger">{{ state[c]!.error }}</p>
        <DataTable
          v-else
          system="netbox"
          :columns="columns"
          :rows="rows(c)"
          :row-key="rowKey"
          :caption="t('netbox.guestsOf', { cluster: c })"
          :loading="!state[c]?.guests"
          :empty="t('netbox.noDifferences')"
        >
          <template #cell-name="{ row }">
            <RouterLink
              v-if="(row as GuestDiff).type"
              class="text-primary underline"
              :to="{
                name: 'proxmox-guest',
                params: {
                  cluster: c,
                  type: (row as GuestDiff).type,
                  vmid: (row as GuestDiff).vmid,
                },
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
                :loading="busy === `${c}/${rowKey(row as GuestDiff)}`"
                @click="apply(c, row as GuestDiff)"
              >
                {{ t('netbox.apply') }}
              </BaseButton>
              <IconButton
                v-if="(row as GuestDiff).state === 'extra' && (row as GuestDiff).tagged"
                icon="delete"
                :label="t('netbox.delete')"
                size="sm"
                variant="danger"
                @click="deleting = { cluster: c, guest: row as GuestDiff }"
              />
            </div>
          </template>
        </DataTable>
      </section>
    </template>

    <BaseModal v-model:open="deleteOpen" :title="t('netbox.deleteTitle')">
      <p>{{ t('netbox.deleteText', { name: deleting?.guest.name ?? '' }) }}</p>
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
