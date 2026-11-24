<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, onMounted, ref, useTemplateRef } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute, useRouter } from 'vue-router'

import { api, unwrap } from '@/api/client'
import { errorMessage } from '@/api/errors'
import MacSearchPanel from '@/components/switches/MacSearchPanel.vue'
import OverviewPanel from '@/components/switches/OverviewPanel.vue'
import PortsPanel from '@/components/switches/PortsPanel.vue'
import VlansPanel from '@/components/switches/VlansPanel.vue'
import BaseSelect from '@/components/ui/BaseSelect.vue'
import IconButton from '@/components/ui/IconButton.vue'
import LinkButtons from '@/components/ui/LinkButtons.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import { uptimeDays, type SwitchInfo } from '@/switches/switches'

/**
 * /switches: all switches like their front panels, with a filter and the device search.
 * /switches/<name>: one switch with its ports and VLANs (?tab=vlans).
 */
const i18n = useI18n()
const { t } = i18n
const route = useRoute()
const router = useRouter()

type Tab = 'ports' | 'vlans'
const TABS: Tab[] = ['ports', 'vlans']

const switches = ref<SwitchInfo[]>([])
const loading = ref(false)
const error = ref('')
const loaded = ref(false)
const overview = useTemplateRef<InstanceType<typeof OverviewPanel>>('overview')
const panel = useTemplateRef<{ load: () => Promise<void> }>('panel')

const name = computed(() => String(route.params.switch ?? ''))
const current = computed(() => switches.value.find((s) => s.name === name.value))
const tab = computed<Tab>(() => (route.query.tab === 'vlans' ? 'vlans' : 'ports'))
const names = computed(() => switches.value.filter((s) => s.status === 'ok').map((s) => s.name))
const days = computed(() => uptimeDays(current.value?.booted))

// The filter of the overview: one switch or all ('').
const filter = computed({
  get: () => String(route.query.switch ?? ''),
  set: (v: string) => void router.replace({ query: v ? { switch: v } : {} }),
})
const filterOptions = computed(() => [
  { value: '', label: t('switches.all') },
  ...switches.value.map((s) => ({
    value: s.name,
    label: s.hostname ? `${s.name} (${s.hostname})` : s.name,
  })),
])
const shown = computed(() =>
  filter.value ? switches.value.filter((s) => s.name === filter.value) : switches.value,
)

async function load() {
  loading.value = true
  try {
    switches.value = (await unwrap(api.GET('/api/v1/switches'))).switches
    error.value = ''
  } catch (e) {
    error.value = errorMessage(i18n, e)
  } finally {
    loading.value = false
    loaded.value = true
  }
}

/** Reloads the switches and what the page shows of them. */
async function refresh() {
  await load()
  await (name.value ? panel.value?.load() : overview.value?.load())
}

function selectTab(next: Tab) {
  void router.replace({ query: { tab: next } })
}

function sshUrl(host: string) {
  return `ssh://${host}`
}

onMounted(load)
</script>

<template>
  <div class="flex flex-col gap-6">
    <RouterLink
      v-if="name"
      :to="{ name: 'switches' }"
      class="-mb-4 self-start text-sm text-primary hover:underline"
      >← {{ t('switches.back') }}</RouterLink
    >
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div class="flex flex-wrap items-center gap-3">
        <h1 class="text-2xl font-semibold">{{ name || t('nav.switches') }}</h1>
        <template v-if="current">
          <span v-if="current.hostname && current.hostname !== name" class="text-fg-muted">{{
            current.hostname
          }}</span>
          <StatusBadge
            :status="current.status === 'ok' ? 'ok' : 'error'"
            :label="current.status === 'ok' ? t('home.ok') : t('home.error')"
          />
          <LinkButtons :name="current.name" :ui="current.url" :ssh="sshUrl(current.host)" />
        </template>
      </div>
      <IconButton
        icon="refresh"
        :label="t('home.refresh')"
        size="md"
        variant="secondary"
        :loading="loading"
        @click="refresh"
      />
    </div>

    <p
      v-if="error"
      role="alert"
      class="rounded-md border border-danger px-3 py-2 text-sm text-danger"
    >
      {{ error }}
    </p>
    <p v-else-if="loaded && switches.length === 0" class="text-fg-muted">
      {{ t('switches.none') }}
    </p>

    <!-- Overview: every switch, the dropdown only filters. -->
    <template v-else-if="!name && switches.length > 0">
      <MacSearchPanel>
        <template #before>
          <BaseSelect v-model="filter" :label="t('switches.switch')" :options="filterOptions" />
        </template>
      </MacSearchPanel>
      <OverviewPanel ref="overview" :switches="shown" />
    </template>

    <!-- Details of one switch. -->
    <template v-else-if="name && loaded && !error">
      <p v-if="!current" role="alert" class="text-danger">
        {{ t('switches.notFound', { name }) }}
      </p>
      <p v-else-if="current.status !== 'ok'" role="alert" class="text-sm text-danger">
        {{ t('switches.unavailable', { error: current.error ?? '' }) }}
      </p>
      <template v-else>
        <dl
          class="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 rounded-md border border-border p-4 text-sm"
        >
          <dt class="text-fg-muted">{{ t('switches.info.model') }}</dt>
          <dd>{{ current.model }}</dd>
          <dt class="text-fg-muted">{{ t('switches.info.firmware') }}</dt>
          <dd>AOS-CX {{ current.firmware }} ({{ current.apiVersion }})</dd>
          <template v-for="m in current.members" :key="m.name">
            <dt class="text-fg-muted">{{ t('switches.info.member', { n: m.name }) }}</dt>
            <dd>
              {{ m.product }}, {{ t('switches.info.serial') }} {{ m.serial }}, {{ m.partNumber }}
            </dd>
          </template>
          <template v-if="days !== null">
            <dt class="text-fg-muted">{{ t('switches.info.uptime') }}</dt>
            <dd>{{ t('switches.info.days', { n: days }, days) }}</dd>
          </template>
        </dl>
        <div>
          <div
            role="tablist"
            :aria-label="t('switches.views')"
            class="flex gap-1 border-b border-border"
          >
            <button
              v-for="x in TABS"
              :id="`switch-tab-${x}`"
              :key="x"
              type="button"
              role="tab"
              :aria-selected="tab === x"
              :aria-controls="`switch-panel-${x}`"
              class="-mb-px rounded-t-md border px-3 py-2 text-sm font-medium focus-visible:outline-2 focus-visible:outline-focus"
              :class="
                tab === x
                  ? 'border-border border-b-surface bg-surface text-fg'
                  : 'border-transparent text-fg-muted hover:text-fg'
              "
              @click="selectTab(x)"
            >
              {{ t(`switches.tabs.${x}`) }}
            </button>
          </div>
          <div
            :id="`switch-panel-${tab}`"
            role="tabpanel"
            :aria-labelledby="`switch-tab-${tab}`"
            class="pt-4"
          >
            <PortsPanel v-if="tab === 'ports'" ref="panel" :switch-name="current.name" />
            <VlansPanel v-else ref="panel" :switch-name="current.name" :switches="names" />
          </div>
        </div>
      </template>
    </template>
  </div>
</template>
