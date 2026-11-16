<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { api, unwrap, type Schemas } from '@/api/client'
import { errorMessage } from '@/api/errors'
import BaseButton from '@/components/ui/BaseButton.vue'
import BaseInput from '@/components/ui/BaseInput.vue'
import BaseModal from '@/components/ui/BaseModal.vue'
import BaseSelect from '@/components/ui/BaseSelect.vue'
import DataTable from '@/components/ui/DataTable.vue'
import { zonePermission, type DnsZone } from '@/dns/records'
import { useSessionStore } from '@/stores/session'
import { useToastStore } from '@/stores/toast'

type Provider = Schemas['DNSProviderList']['providers'][number]

const i18n = useI18n()
const { t } = i18n
const route = useRoute()
const router = useRouter()
const session = useSessionStore()
const toast = useToastStore()

const providers = ref<Provider[]>([])
const zones = ref<DnsZone[] | null>(null)
const loading = ref(false)
const error = ref('')
const search = ref('')

const providerName = computed(() => String(route.params.provider ?? ''))
const provider = computed(() => providers.value.find((p) => p.name === providerName.value))
const providerOptions = computed(() =>
  providers.value.map((p) => ({ value: p.name, label: `${p.name} (${t(`dns.kinds.${p.kind}`)})` })),
)
const shownZones = computed(() => {
  const q = search.value.trim().toLowerCase()
  return (zones.value ?? []).filter((z) => !q || z.name.includes(q))
})
const canCreate = computed(
  () =>
    !!provider.value?.capabilities.zoneCreate && session.canAny('dns', 'admin', providerName.value),
)
const canDeleteAny = computed(() => session.canAny('dns', 'admin', providerName.value))
const columns = computed(() => [
  { key: 'name', label: t('dns.zone') },
  { key: 'status', label: t('dns.status') },
  { key: 'actions', label: t('dns.actionsColumn') },
])

async function loadProviders() {
  const res = await unwrap(api.GET('/api/v1/dns/providers'))
  providers.value = res.providers
  if (!providerName.value && res.providers.length > 0) {
    await router.replace({ name: 'dns', params: { provider: res.providers[0]!.name } })
  }
}

async function loadZones() {
  if (!providerName.value) return
  loading.value = true
  try {
    const res = await unwrap(
      api.GET('/api/v1/dns/providers/{provider}/zones', {
        params: { path: { provider: providerName.value } },
      }),
    )
    zones.value = res.zones
    error.value = ''
  } catch (e) {
    zones.value = null
    error.value = errorMessage(i18n, e)
  } finally {
    loading.value = false
  }
}

function selectProvider(p: string) {
  if (p !== providerName.value) void router.push({ name: 'dns', params: { provider: p } })
}

// Create zone.
const createOpen = ref(false)
const newZone = ref('')
const createError = ref('')
const busy = ref(false)

function openCreate() {
  newZone.value = ''
  createError.value = ''
  createOpen.value = true
}

async function createZone() {
  busy.value = true
  try {
    const z = await unwrap(
      api.POST('/api/v1/dns/providers/{provider}/zones', {
        params: { path: { provider: providerName.value } },
        body: { name: newZone.value.trim() },
      }),
    )
    createOpen.value = false
    toast.success(t('dns.zoneCreated', { zone: z.name }))
    if (z.nameServers?.length) {
      toast.show('info', t('dns.delegateTo', { zone: z.name, ns: z.nameServers.join(', ') }), 0)
    }
    await loadZones()
  } catch (e) {
    createError.value = errorMessage(i18n, e)
  } finally {
    busy.value = false
  }
}

// Delete zone: the user types the zone name to confirm.
const deleteZone = ref<DnsZone | null>(null)
const confirmName = ref('')
const deleteError = ref('')
const deleteOpen = computed({
  get: () => deleteZone.value !== null,
  set: (v: boolean) => {
    if (!v) deleteZone.value = null
  },
})

function askDelete(z: DnsZone) {
  confirmName.value = ''
  deleteError.value = ''
  deleteZone.value = z
}

async function removeZone() {
  const z = deleteZone.value
  if (!z) return
  busy.value = true
  try {
    await unwrap(
      api.DELETE('/api/v1/dns/providers/{provider}/zones/{zone}', {
        params: {
          path: { provider: providerName.value, zone: z.name },
          query: { confirm: confirmName.value.trim() },
        },
      }),
    )
    deleteZone.value = null
    toast.success(t('dns.zoneDeleted', { zone: z.name }))
    await loadZones()
  } catch (e) {
    deleteError.value = errorMessage(i18n, e)
  } finally {
    busy.value = false
  }
}

watch(providerName, () => {
  zones.value = null
  void loadZones()
})

onMounted(async () => {
  try {
    await loadProviders()
  } catch (e) {
    error.value = errorMessage(i18n, e)
    return
  }
  await loadZones()
})
</script>

<template>
  <div class="flex flex-col gap-6">
    <div class="flex flex-wrap items-end justify-between gap-4">
      <h1 class="text-2xl font-semibold">{{ t('nav.dns') }}</h1>
      <div class="flex flex-wrap items-end gap-3">
        <BaseSelect
          v-if="providers.length > 1"
          :model-value="providerName"
          :label="t('dns.provider')"
          :options="providerOptions"
          @update:model-value="selectProvider"
        />
        <BaseButton variant="secondary" :loading="loading" @click="loadZones">
          {{ t('home.refresh') }}
        </BaseButton>
        <BaseButton v-if="canCreate" variant="primary" @click="openCreate">
          {{ t('dns.createZone') }}
        </BaseButton>
      </div>
    </div>

    <p
      v-if="error"
      role="alert"
      class="rounded-md border border-danger px-3 py-2 text-sm text-danger"
    >
      {{ error }}
    </p>
    <p v-else-if="providers.length === 0 && !loading" class="text-fg-muted">
      {{ t('dns.noProviders') }}
    </p>

    <section v-if="zones" aria-labelledby="dns-zones" class="flex flex-col gap-3">
      <h2 id="dns-zones" class="text-lg font-semibold">{{ t('dns.zones') }}</h2>
      <form class="flex flex-wrap items-end gap-3" role="search" @submit.prevent>
        <BaseInput v-model="search" :label="t('dns.searchZones')" type="search" />
      </form>
      <DataTable
        :columns="columns"
        :rows="shownZones"
        :row-key="(z: DnsZone) => z.name"
        :caption="t('dns.zones')"
        :empty="t('dns.noZones')"
      >
        <template #cell-name="{ row }">
          <RouterLink
            :to="{
              name: 'dns-zone',
              params: { provider: providerName, zone: (row as DnsZone).name },
            }"
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
          <BaseButton
            v-if="
              provider?.capabilities.zoneCreate &&
              session.can(zonePermission(providerName, (row as DnsZone).name, 'admin'))
            "
            size="sm"
            variant="danger"
            @click="askDelete(row as DnsZone)"
          >
            {{ t('dns.deleteZone') }}
          </BaseButton>
        </template>
      </DataTable>
    </section>

    <BaseModal v-if="canCreate" v-model:open="createOpen" :title="t('dns.createZone')">
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

    <BaseModal v-if="canDeleteAny" v-model:open="deleteOpen" :title="t('dns.deleteZone')">
      <form id="dns-delete-zone" class="flex flex-col gap-3" @submit.prevent="removeZone">
        <p>{{ t('dns.deleteZoneWarning', { zone: deleteZone?.name }) }}</p>
        <BaseInput
          v-model="confirmName"
          :label="t('dns.confirmZone', { zone: deleteZone?.name })"
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
          :disabled="confirmName.trim().toLowerCase() !== deleteZone?.name"
        >
          {{ t('dns.deleteZone') }}
        </BaseButton>
      </template>
    </BaseModal>
  </div>
</template>
