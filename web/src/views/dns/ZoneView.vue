<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, onMounted, ref, useTemplateRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'

import { api, ApiError, unwrap, type Schemas } from '@/api/client'
import { errorMessage } from '@/api/errors'
import RecordDialog from '@/components/dns/RecordDialog.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import BaseInput from '@/components/ui/BaseInput.vue'
import BaseModal from '@/components/ui/BaseModal.vue'
import BaseSelect from '@/components/ui/BaseSelect.vue'
import DataTable from '@/components/ui/DataTable.vue'
import {
  filterRecords,
  relativeName,
  zonePermission,
  type Capabilities,
  type DnsRecord,
  type RecordFilter,
} from '@/dns/records'
import { useSessionStore } from '@/stores/session'
import { useToastStore } from '@/stores/toast'

const i18n = useI18n()
const { t } = i18n
const route = useRoute()
const session = useSessionStore()
const toast = useToastStore()
const dialog = useTemplateRef<InstanceType<typeof RecordDialog>>('dialog')

const provider = computed(() => String(route.params.provider))
const zone = computed(() => String(route.params.zone))
const recordSet = ref<Schemas['DNSRecordSet'] | null>(null)
const capabilities = ref<Capabilities>({
  proxied: false,
  comments: false,
  autoTtl: false,
  zoneCreate: false,
})
const loading = ref(false)
const error = ref('')
const filter = ref<RecordFilter>({ text: '', type: '' })

const canWrite = computed(() => session.can(zonePermission(provider.value, zone.value, 'write')))
const records = computed(() => filterRecords(recordSet.value?.records ?? [], filter.value))
const typeOptions = computed(() => [
  { value: '', label: t('dns.allTypes') },
  ...[...new Set((recordSet.value?.records ?? []).map((r) => r.type))]
    .sort()
    .map((x) => ({ value: x, label: x })),
])
const columns = computed(() => [
  { key: 'name', label: t('dns.name') },
  { key: 'type', label: t('dns.type'), class: 'w-20' },
  { key: 'content', label: t('dns.content') },
  { key: 'ttl', label: t('dns.ttl'), class: 'w-24' },
  ...(canWrite.value ? [{ key: 'actions', label: t('dns.actionsColumn') }] : []),
])

function ttlLabel(r: DnsRecord): string {
  return r.ttl === 1 && capabilities.value.autoTtl ? t('dns.ttlAuto') : String(r.ttl)
}

async function load() {
  loading.value = true
  try {
    const path = { provider: provider.value, zone: zone.value }
    const [rs, pl] = await Promise.all([
      unwrap(
        api.GET('/api/v1/dns/providers/{provider}/zones/{zone}/records', { params: { path } }),
      ),
      unwrap(api.GET('/api/v1/dns/providers')),
    ])
    recordSet.value = rs
    const p = pl.providers.find((x) => x.name === provider.value)
    if (p) capabilities.value = p.capabilities
    error.value = ''
  } catch (e) {
    error.value = errorMessage(i18n, e)
  } finally {
    loading.value = false
  }
}

function saved(r: DnsRecord) {
  toast.success(t('dns.recordSaved', { name: relativeName(r.name, zone.value), type: r.type }))
  void load()
}

// Delete a record after confirmation.
const deleting = ref<DnsRecord | null>(null)
const deleteError = ref('')
const busy = ref(false)
const deleteOpen = computed({
  get: () => deleting.value !== null,
  set: (v: boolean) => {
    if (!v) deleting.value = null
  },
})

function askDelete(r: DnsRecord) {
  deleteError.value = ''
  deleting.value = r
}

async function removeRecord() {
  const r = deleting.value
  if (!r) return
  busy.value = true
  try {
    await unwrap(
      api.DELETE('/api/v1/dns/providers/{provider}/zones/{zone}/records/{id}', {
        params: {
          path: { provider: provider.value, zone: zone.value, id: r.id },
          query: { version: r.version },
        },
      }),
    )
    deleting.value = null
    toast.success(t('dns.recordDeleted', { name: relativeName(r.name, zone.value), type: r.type }))
    await load()
  } catch (e) {
    deleteError.value = errorMessage(i18n, e)
    // A concurrent change: reload, so the next attempt uses the new version.
    if (e instanceof ApiError && e.code === 'conflict') void load()
  } finally {
    busy.value = false
  }
}

watch([provider, zone], load)
onMounted(load)
</script>

<template>
  <div class="flex flex-col gap-6">
    <nav :aria-label="t('dns.breadcrumb')" class="text-sm">
      <RouterLink :to="{ name: 'dns', params: { provider } }" class="text-primary underline">
        {{ t('nav.dns') }} · {{ provider }}
      </RouterLink>
    </nav>
    <div class="flex flex-wrap items-end justify-between gap-4">
      <h1 class="text-2xl font-semibold">{{ zone }}</h1>
      <div class="flex flex-wrap items-end gap-3">
        <BaseButton variant="secondary" :loading="loading" @click="load">
          {{ t('home.refresh') }}
        </BaseButton>
        <BaseButton
          v-if="canWrite && recordSet"
          variant="primary"
          @click="dialog?.create(recordSet.version)"
        >
          {{ t('dns.addRecord') }}
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

    <section v-if="recordSet" aria-labelledby="dns-records" class="flex flex-col gap-3">
      <h2 id="dns-records" class="text-lg font-semibold">{{ t('dns.records') }}</h2>
      <form class="flex flex-wrap items-end gap-3" role="search" @submit.prevent>
        <BaseInput v-model="filter.text" :label="t('dns.searchRecords')" type="search" />
        <BaseSelect v-model="filter.type" :label="t('dns.type')" :options="typeOptions" />
      </form>
      <p class="text-sm text-fg-muted" aria-live="polite">
        {{ t('dns.shown', { n: records.length, total: recordSet.records.length }) }}
      </p>
      <DataTable
        :columns="columns"
        :rows="records"
        :row-key="(r: DnsRecord) => r.id"
        :caption="t('dns.records')"
        :empty="t('dns.noRecords')"
      >
        <template #cell-name="{ row }">
          <span class="font-mono text-sm">{{ relativeName((row as DnsRecord).name, zone) }}</span>
        </template>
        <template #cell-content="{ row }">
          <span class="font-mono text-sm break-all">{{ (row as DnsRecord).content }}</span>
          <span
            v-if="(row as DnsRecord).proxied"
            class="ml-2 rounded bg-surface-sunken px-1.5 text-xs text-fg-muted"
            >{{ t('dns.proxiedBadge') }}</span
          >
          <span v-if="(row as DnsRecord).comment" class="block text-xs text-fg-muted">
            {{ (row as DnsRecord).comment }}
          </span>
        </template>
        <template #cell-ttl="{ row }">{{ ttlLabel(row as DnsRecord) }}</template>
        <template #cell-actions="{ row }">
          <div
            v-if="!(row as DnsRecord).readOnly"
            class="flex flex-wrap gap-1.5"
            role="group"
            :aria-label="t('dns.actionsFor', { name: relativeName((row as DnsRecord).name, zone) })"
          >
            <BaseButton size="sm" variant="secondary" @click="dialog?.edit(row as DnsRecord)">
              {{ t('dns.edit') }}
            </BaseButton>
            <BaseButton size="sm" variant="danger" @click="askDelete(row as DnsRecord)">
              {{ t('dns.delete') }}
            </BaseButton>
          </div>
          <span v-else class="text-xs text-fg-muted">{{ t('dns.readOnly') }}</span>
        </template>
      </DataTable>
    </section>

    <RecordDialog
      v-if="canWrite"
      ref="dialog"
      :provider="provider"
      :zone="zone"
      :capabilities="capabilities"
      @saved="saved"
    />

    <BaseModal v-if="canWrite" v-model:open="deleteOpen" :title="t('dns.deleteRecord')">
      <p>
        {{
          t('dns.deleteRecordQuestion', {
            name: deleting ? relativeName(deleting.name, zone) : '',
            type: deleting?.type,
          })
        }}
      </p>
      <p v-if="deleting" class="mt-2 font-mono text-sm break-all text-fg-muted">
        {{ deleting.content }}
      </p>
      <p v-if="deleteError" role="alert" class="mt-2 text-sm text-danger">{{ deleteError }}</p>
      <template #footer>
        <BaseButton variant="secondary" @click="deleteOpen = false">{{
          t('common.cancel')
        }}</BaseButton>
        <BaseButton variant="danger" :loading="busy" @click="removeRecord">
          {{ t('dns.delete') }}
        </BaseButton>
      </template>
    </BaseModal>
  </div>
</template>
