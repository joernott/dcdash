<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { api, unwrap, type Schemas } from '@/api/client'
import { errorMessage } from '@/api/errors'
import BaseButton from '@/components/ui/BaseButton.vue'
import BaseInput from '@/components/ui/BaseInput.vue'
import BaseSelect from '@/components/ui/BaseSelect.vue'
import DataTable from '@/components/ui/DataTable.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

type Entry = Schemas['AuditEntry']

const PAGE_SIZE = 50

const i18n = useI18n()
const { t, d } = i18n

const filter = reactive({ actor: '', action: '', target: '', result: '', since: '', until: '' })
const entries = ref<Entry[]>([])
const nextBefore = ref<number | null>(null)
const loading = ref(false)
const error = ref('')

const resultOptions = computed(() => [
  { value: '', label: t('audit.allResults') },
  { value: 'success', label: t('audit.results.success') },
  { value: 'failure', label: t('audit.results.failure') },
  { value: 'denied', label: t('audit.results.denied') },
])

const columns = computed(() => [
  { key: 'time', label: t('audit.time'), class: 'whitespace-nowrap' },
  { key: 'actor', label: t('audit.actor') },
  { key: 'action', label: t('audit.action') },
  { key: 'target', label: t('audit.target') },
  { key: 'result', label: t('audit.result') },
  { key: 'details', label: t('audit.details') },
])

/** datetime-local values are local time; the API expects RFC 3339. */
function toISO(local: string): string | undefined {
  if (!local) return undefined
  const date = new Date(local)
  return Number.isNaN(date.getTime()) ? undefined : date.toISOString()
}

async function load(more = false) {
  loading.value = true
  error.value = ''
  try {
    const page = await unwrap(
      api.GET('/api/v1/audit', {
        params: {
          query: {
            actor: filter.actor.trim() || undefined,
            action: filter.action.trim() || undefined,
            target: filter.target.trim() || undefined,
            result: (filter.result || undefined) as Entry['result'] | undefined,
            since: toISO(filter.since),
            until: toISO(filter.until),
            before: more ? (nextBefore.value ?? undefined) : undefined,
            limit: PAGE_SIZE,
          },
        },
      }),
    )
    entries.value = more ? [...entries.value, ...page.entries] : page.entries
    nextBefore.value = page.nextBefore ?? null
  } catch (e) {
    error.value = errorMessage(i18n, e)
  } finally {
    loading.value = false
  }
}

function reset() {
  Object.assign(filter, { actor: '', action: '', target: '', result: '', since: '', until: '' })
  void load()
}

function badge(result: Entry['result']) {
  return result === 'success' ? 'ok' : result === 'denied' ? 'warning' : 'error'
}

function details(e: Entry): string {
  return Object.entries(e.details ?? {})
    .map(([k, v]) => `${k}=${typeof v === 'string' ? v : JSON.stringify(v)}`)
    .join(', ')
}

onMounted(() => load())
</script>

<template>
  <div class="flex flex-col gap-4">
    <h1 class="text-2xl font-semibold">{{ t('nav.audit') }}</h1>

    <form
      class="grid gap-3 rounded-lg border border-border bg-surface-raised p-4 sm:grid-cols-2 lg:grid-cols-3"
      :aria-label="t('audit.filter')"
      @submit.prevent="load()"
    >
      <BaseInput v-model="filter.actor" :label="t('audit.actor')" />
      <BaseInput v-model="filter.action" :label="t('audit.action')" :hint="t('audit.actionHint')" />
      <BaseInput v-model="filter.target" :label="t('audit.target')" />
      <BaseSelect v-model="filter.result" :label="t('audit.result')" :options="resultOptions" />
      <BaseInput v-model="filter.since" type="datetime-local" :label="t('audit.since')" />
      <BaseInput v-model="filter.until" type="datetime-local" :label="t('audit.until')" />
      <div class="flex gap-2 sm:col-span-2 lg:col-span-3">
        <BaseButton type="submit" :loading="loading">{{ t('audit.apply') }}</BaseButton>
        <BaseButton variant="ghost" @click="reset">{{ t('audit.reset') }}</BaseButton>
      </div>
    </form>

    <p
      v-if="error"
      role="alert"
      class="rounded-md border border-danger px-3 py-2 text-sm text-danger"
    >
      {{ error }}
    </p>

    <DataTable
      :columns="columns"
      :rows="entries"
      :row-key="(e: Entry) => e.id"
      :loading="loading && entries.length === 0"
      :caption="t('nav.audit')"
    >
      <template #cell-time="{ row }">
        <time :datetime="(row as Entry).time">{{ d(new Date((row as Entry).time), 'long') }}</time>
      </template>
      <template #cell-actor="{ row }">
        {{ (row as Entry).actor || '–' }}
        <span class="block text-xs text-fg-muted">
          {{ (row as Entry).actorSource
          }}<template v-if="(row as Entry).clientIp">, {{ (row as Entry).clientIp }}</template>
        </span>
      </template>
      <template #cell-action="{ row }">
        <code class="text-xs">{{ (row as Entry).action }}</code>
      </template>
      <template #cell-result="{ row }">
        <StatusBadge
          :status="badge((row as Entry).result)"
          :label="t(`audit.results.${(row as Entry).result}`)"
        />
      </template>
      <template #cell-details="{ row }">
        <span class="text-xs break-all text-fg-muted">{{ details(row as Entry) }}</span>
      </template>
    </DataTable>

    <div v-if="nextBefore !== null" class="flex justify-center">
      <BaseButton variant="secondary" :loading="loading" @click="load(true)">{{
        t('audit.more')
      }}</BaseButton>
    </div>
  </div>
</template>
