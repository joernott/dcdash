<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { api, unwrap, type Schemas } from '@/api/client'
import { errorMessage } from '@/api/errors'
import AppIcon from '@/components/ui/AppIcon.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import IconButton from '@/components/ui/IconButton.vue'
import BaseInput from '@/components/ui/BaseInput.vue'
import BaseModal from '@/components/ui/BaseModal.vue'
import BaseSelect from '@/components/ui/BaseSelect.vue'
import DataTable from '@/components/ui/DataTable.vue'
import { zonePermission, type DnsZone } from '@/dns/records'
import { systemOf, systemStyle } from '@/systems'
import { useSessionStore } from '@/stores/session'
import { useToastStore } from '@/stores/toast'

type Provider = Schemas['DNSProviderList']['providers'][number]

// The zones of a provider: null until loaded or if loading failed.
interface ProviderZones {
  zones: DnsZone[] | null
  loading: boolean
  error: string
}

const i18n = useI18n()
const { t } = i18n
const route = useRoute()
const router = useRouter()
const session = useSessionStore()
const toast = useToastStore()

const providers = ref<Provider[]>([])
const state = reactive<Record<string, ProviderZones>>({})
const error = ref('')
const loadingProviders = ref(true)
const search = ref('')

// The route's provider is an optional filter; without it, all providers are shown.
const filter = computed(() => String(route.params.provider ?? ''))
const shown = computed(() =>
  filter.value ? providers.value.filter((p) => p.name === filter.value) : providers.value,
)
const filterOptions = computed(() => [
  { value: '', label: t('dns.allProviders') },
  ...providers.value.map((p) => ({ value: p.name, label: providerLabel(p) })),
])
const loading = computed(() => shown.value.some((p) => state[p.name]?.loading))
const columns = computed(() => [
  { key: 'name', label: t('dns.zone') },
  { key: 'status', label: t('dns.status') },
  { key: 'actions', label: t('dns.actionsColumn'), class: 'w-px whitespace-nowrap' },
])

function providerLabel(p: Provider) {
  return `${p.name} (${t(`dns.kinds.${p.kind}`)})`
}

// The provider's system (bind, Cloudflare, directory): its zone rows have its color.
function zoneSystem(p: Provider) {
  return systemOf({ kind: 'dns', type: p.kind })
}

function zonesOf(p: Provider) {
  const q = search.value.trim().toLowerCase()
  return (state[p.name]?.zones ?? []).filter((z) => !q || z.name.includes(q))
}

function canCreate(p: Provider) {
  return !!p.capabilities.zoneCreate && session.canAny('dns', 'admin', p.name)
}

function canDelete(p: Provider, z: DnsZone) {
  return !!p.capabilities.zoneCreate && session.can(zonePermission(p.name, z.name, 'admin'))
}

async function loadZones(name: string) {
  state[name] ??= { zones: null, loading: false, error: '' }
  // The reactive proxy, not the plain object the assignment returns.
  const s = state[name]!
  s.loading = true
  try {
    const res = await unwrap(
      api.GET('/api/v1/dns/providers/{provider}/zones', { params: { path: { provider: name } } }),
    )
    s.zones = res.zones
    s.error = ''
  } catch (e) {
    s.zones = null
    s.error = errorMessage(i18n, e)
  } finally {
    s.loading = false
  }
}

// Loads the zones of all shown providers in parallel.
async function loadShown() {
  await Promise.all(shown.value.map((p) => loadZones(p.name)))
}

function selectFilter(p: string) {
  if (p !== filter.value) {
    void router.push({ name: 'dns', params: p ? { provider: p } : {} })
  }
}

// Create zone.
const createFor = ref<Provider | null>(null)
const newZone = ref('')
const createError = ref('')
const busy = ref(false)
const createOpen = computed({
  get: () => createFor.value !== null,
  set: (v: boolean) => {
    if (!v) createFor.value = null
  },
})

function openCreate(p: Provider) {
  newZone.value = ''
  createError.value = ''
  createFor.value = p
}

async function createZone() {
  const p = createFor.value
  if (!p) return
  busy.value = true
  try {
    const z = await unwrap(
      api.POST('/api/v1/dns/providers/{provider}/zones', {
        params: { path: { provider: p.name } },
        body: { name: newZone.value.trim() },
      }),
    )
    createFor.value = null
    toast.success(t('dns.zoneCreated', { zone: z.name }))
    if (z.nameServers?.length) {
      toast.show('info', t('dns.delegateTo', { zone: z.name, ns: z.nameServers.join(', ') }), 0)
    }
    await loadZones(p.name)
  } catch (e) {
    createError.value = errorMessage(i18n, e)
  } finally {
    busy.value = false
  }
}

// Delete zone: the user types the zone name to confirm.
const deleteTarget = ref<{ provider: string; zone: DnsZone } | null>(null)
const confirmName = ref('')
const deleteError = ref('')
const deleteOpen = computed({
  get: () => deleteTarget.value !== null,
  set: (v: boolean) => {
    if (!v) deleteTarget.value = null
  },
})

function askDelete(p: Provider, z: DnsZone) {
  confirmName.value = ''
  deleteError.value = ''
  deleteTarget.value = { provider: p.name, zone: z }
}

async function removeZone() {
  const target = deleteTarget.value
  if (!target) return
  busy.value = true
  try {
    await unwrap(
      api.DELETE('/api/v1/dns/providers/{provider}/zones/{zone}', {
        params: {
          path: { provider: target.provider, zone: target.zone.name },
          query: { confirm: confirmName.value.trim() },
        },
      }),
    )
    deleteTarget.value = null
    toast.success(t('dns.zoneDeleted', { zone: target.zone.name }))
    await loadZones(target.provider)
  } catch (e) {
    deleteError.value = errorMessage(i18n, e)
  } finally {
    busy.value = false
  }
}

// Changing the filter loads the providers whose zones were not loaded yet.
watch(filter, () => {
  for (const p of shown.value) {
    if (!state[p.name]) void loadZones(p.name)
  }
})

onMounted(async () => {
  try {
    providers.value = (await unwrap(api.GET('/api/v1/dns/providers'))).providers
  } catch (e) {
    error.value = errorMessage(i18n, e)
    return
  } finally {
    loadingProviders.value = false
  }
  await loadShown()
})
</script>

<template>
  <div class="flex flex-col gap-6">
    <div class="flex flex-wrap items-end justify-between gap-4">
      <h1 class="text-2xl font-semibold">{{ t('nav.dns') }}</h1>
      <form class="flex flex-wrap items-end gap-3" role="search" @submit.prevent>
        <BaseSelect
          v-if="providers.length > 1"
          :model-value="filter"
          :label="t('dns.provider')"
          :options="filterOptions"
          @update:model-value="selectFilter"
        />
        <BaseInput v-model="search" :label="t('dns.searchZones')" type="search" />
        <IconButton
          icon="refresh"
          :label="t('home.refresh')"
          size="md"
          variant="secondary"
          :loading="loading"
          @click="loadShown"
        />
      </form>
    </div>

    <p
      v-if="error"
      role="alert"
      class="rounded-md border border-danger px-3 py-2 text-sm text-danger"
    >
      {{ error }}
    </p>
    <p v-else-if="providers.length === 0 && !loadingProviders" class="text-fg-muted">
      {{ t('dns.noProviders') }}
    </p>

    <section
      v-for="p in shown"
      :key="p.name"
      :aria-labelledby="`dns-provider-${p.name}`"
      class="flex flex-col gap-3"
    >
      <div class="flex flex-wrap items-center justify-between gap-3">
        <h2 :id="`dns-provider-${p.name}`" class="text-lg font-semibold">
          {{ providerLabel(p) }}
        </h2>
        <BaseButton
          v-if="canCreate(p)"
          variant="primary"
          size="sm"
          :aria-label="t('dns.createZoneIn', { provider: p.name })"
          @click="openCreate(p)"
        >
          <AppIcon name="add" />{{ t('dns.createZone') }}
        </BaseButton>
      </div>
      <p
        v-if="state[p.name]?.error"
        role="alert"
        class="rounded-md border border-danger px-3 py-2 text-sm text-danger"
      >
        {{ state[p.name]!.error }}
      </p>
      <DataTable
        v-else-if="state[p.name]?.zones"
        :columns="columns"
        :rows="zonesOf(p)"
        :row-key="(z: DnsZone) => z.name"
        :row-class="() => (zoneSystem(p) ? 'sys-tint' : 'even:bg-surface-raised')"
        :row-style="() => systemStyle(zoneSystem(p))"
        :caption="t('dns.zonesOf', { provider: p.name })"
        :empty="t('dns.noZones')"
      >
        <template #cell-name="{ row }">
          <RouterLink
            :to="{ name: 'dns-zone', params: { provider: p.name, zone: (row as DnsZone).name } }"
            class="font-medium text-primary underline"
          >
            {{ (row as DnsZone).name }}
          </RouterLink>
        </template>
        <template #cell-status="{ row }">
          <span>{{ (row as DnsZone).status || '–' }}</span>
          <span
            v-if="(row as DnsZone).status === 'pending' && (row as DnsZone).nameServers?.length"
            class="block text-xs text-fg-muted"
          >
            {{ t('dns.pendingHint', { ns: (row as DnsZone).nameServers!.join(', ') }) }}
          </span>
        </template>
        <template #cell-actions="{ row }">
          <IconButton
            v-if="canDelete(p, row as DnsZone)"
            icon="delete"
            :label="t('dns.deleteZone')"
            size="sm"
            variant="danger"
            @click="askDelete(p, row as DnsZone)"
          />
        </template>
      </DataTable>
      <p v-else class="text-sm text-fg-muted">{{ t('common.loading') }}</p>
    </section>

    <BaseModal
      v-if="shown.some(canCreate)"
      v-model:open="createOpen"
      :title="t('dns.createZoneIn', { provider: createFor?.name ?? '' })"
    >
      <form id="dns-create-zone" class="flex flex-col gap-3" @submit.prevent="createZone">
        <BaseInput
          v-model="newZone"
          :label="t('dns.zoneName')"
          :hint="t('dns.zoneNameHint')"
          :error="createError"
          required
        />
      </form>
      <template #footer>
        <BaseButton variant="secondary" @click="createOpen = false">{{
          t('common.cancel')
        }}</BaseButton>
        <BaseButton
          variant="primary"
          type="submit"
          form="dns-create-zone"
          :loading="busy"
          :disabled="!newZone.trim()"
        >
          {{ t('dns.create') }}
        </BaseButton>
      </template>
    </BaseModal>

    <BaseModal v-if="shown.some(canCreate)" v-model:open="deleteOpen" :title="t('dns.deleteZone')">
      <form id="dns-delete-zone" class="flex flex-col gap-3" @submit.prevent="removeZone">
        <p>{{ t('dns.deleteZoneWarning', { zone: deleteTarget?.zone.name }) }}</p>
        <BaseInput
          v-model="confirmName"
          :label="t('dns.confirmZone', { zone: deleteTarget?.zone.name })"
          :error="deleteError"
          autocomplete="off"
        />
      </form>
      <template #footer>
        <BaseButton variant="secondary" @click="deleteOpen = false">{{
          t('common.cancel')
        }}</BaseButton>
        <BaseButton
          variant="danger"
          type="submit"
          form="dns-delete-zone"
          :loading="busy"
          :disabled="confirmName.trim().toLowerCase() !== deleteTarget?.zone.name"
        >
          <AppIcon name="delete" />{{ t('dns.deleteZone') }}</BaseButton
        >
      </template>
    </BaseModal>
  </div>
</template>
