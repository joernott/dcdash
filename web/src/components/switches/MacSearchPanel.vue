<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'

import { api, unwrap, type Schemas } from '@/api/client'
import { errorMessage } from '@/api/errors'
import IconButton from '@/components/ui/IconButton.vue'
import BaseInput from '@/components/ui/BaseInput.vue'
import DataTable from '@/components/ui/DataTable.vue'
import type { MacResult } from '@/switches/switches'

/**
 * Finds a MAC address on all switches; access ports first. The slot "before" puts controls of
 * the page on the line of the search field.
 */
const i18n = useI18n()
const { t } = i18n

const query = ref('')
const result = ref<Schemas['SwitchMACSearch'] | null>(null)
const busy = ref(false)
const error = ref('')

const columns = computed(() => [
  { key: 'mac', label: t('switches.mac.mac') },
  { key: 'switch', label: t('switches.mac.switch') },
  { key: 'port', label: t('switches.columns.port') },
  { key: 'vlan', label: 'VLAN' },
  { key: 'where', label: t('switches.mac.where') },
])

async function search() {
  busy.value = true
  error.value = ''
  try {
    result.value = await unwrap(
      api.GET('/api/v1/switches/macs', { params: { query: { q: query.value } } }),
    )
  } catch (e) {
    result.value = null
    error.value = errorMessage(i18n, e)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="flex flex-col gap-3">
    <!-- Controls of the page (slot "before") on the same line as the search. -->
    <div class="flex flex-wrap items-start gap-x-6 gap-y-3">
      <slot name="before" />
      <form class="flex items-start gap-3" role="search" @submit.prevent="search">
        <BaseInput
          v-model="query"
          :label="t('switches.mac.query')"
          :hint="t('switches.mac.queryHint')"
          placeholder="bc:24:11:12:34:56"
          inputmode="search"
        />
        <!-- Aligned with the input below the label. -->
        <div class="pt-6">
          <IconButton
            icon="search"
            :label="t('switches.mac.search')"
            size="md"
            type="submit"
            :loading="busy"
            :disabled="!query.trim()"
          />
        </div>
      </form>
    </div>
    <p
      v-if="error"
      role="alert"
      class="rounded-md border border-danger px-3 py-2 text-sm text-danger"
    >
      {{ error }}
    </p>
    <template v-if="result">
      <p v-for="e in result.errors" :key="e.switch" class="text-sm text-warning">
        {{ t('switches.mac.switchFailed', { switch: e.switch, error: e.error }) }}
      </p>
      <DataTable
        :columns="columns"
        :rows="result.results"
        :row-key="(r: MacResult) => `${r.switch}|${r.port}|${r.vlan}|${r.mac}`"
        :caption="t('switches.tabs.macs')"
        :empty="t('switches.mac.none')"
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
    </template>
  </div>
</template>
