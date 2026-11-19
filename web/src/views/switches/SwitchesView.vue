<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { api, unwrap } from '@/api/client'
import { errorMessage } from '@/api/errors'
import MacSearchPanel from '@/components/switches/MacSearchPanel.vue'
import OverviewPanel from '@/components/switches/OverviewPanel.vue'
import PortsPanel from '@/components/switches/PortsPanel.vue'
import VlansPanel from '@/components/switches/VlansPanel.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import BaseSelect from '@/components/ui/BaseSelect.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import { uptimeDays, type SwitchInfo } from '@/switches/switches'

const i18n = useI18n()
const { t } = i18n
const route = useRoute()
const router = useRouter()

type Tab = 'overview' | 'ports' | 'vlans' | 'macs'
const TABS: Tab[] = ['overview', 'ports', 'vlans', 'macs']

const switches = ref<SwitchInfo[]>([])
const loading = ref(false)
const error = ref('')

const name = computed(() => String(route.params.switch ?? ''))
const current = computed(() => switches.value.find((s) => s.name === name.value))
const tab = computed<Tab>(() => {
  const q = String(route.query.tab ?? '')
  return (TABS as string[]).includes(q) ? (q as Tab) : 'overview'
})
const options = computed(() =>
  switches.value.map((s) => ({
    value: s.name,
    label: s.hostname ? `${s.name} (${s.hostname})` : s.name,
  })),
)
const names = computed(() => switches.value.filter((s) => s.status === 'ok').map((s) => s.name))
const days = computed(() => uptimeDays(current.value?.booted))

async function load() {
  loading.value = true
  try {
    switches.value = (await unwrap(api.GET('/api/v1/switches'))).switches
    error.value = ''
    if (!name.value && switches.value.length > 0) {
      await router.replace({
        name: 'switches',
        params: { switch: switches.value[0]!.name },
        query: route.query,
      })
    }
  } catch (e) {
    error.value = errorMessage(i18n, e)
  } finally {
    loading.value = false
  }
}

function select(next: string) {
  if (next !== name.value)
    void router.push({ name: 'switches', params: { switch: next }, query: route.query })
}

function selectTab(next: Tab) {
  void router.replace({ query: { tab: next } })
}

onMounted(load)
</script>

<template>
  <div class="flex flex-col gap-6">
    <div class="flex flex-col gap-3">
      <h1 class="text-2xl font-semibold">{{ t('nav.switches') }}</h1>
      <div class="flex flex-wrap items-end gap-3">
        <BaseSelect
          v-if="switches.length > 0"
          :model-value="name"
          :label="t('switches.switch')"
          :options="options"
          @update:model-value="select"
        />
        <BaseButton variant="secondary" :loading="loading" @click="load">
          {{ t('home.refresh') }}
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
    <p v-else-if="switches.length === 0 && !loading" class="text-fg-muted">
      {{ t('switches.none') }}
    </p>

    <section
      v-if="current && (tab === 'ports' || tab === 'vlans')"
      aria-labelledby="switch-info"
      class="flex flex-col gap-2 rounded-md border border-border p-4"
    >
      <div class="flex flex-wrap items-center gap-3">
        <h2 id="switch-info" class="text-lg font-semibold">
          {{ current.hostname || current.name }}
        </h2>
        <StatusBadge
          :status="current.status === 'ok' ? 'ok' : 'error'"
          :label="current.status === 'ok' ? t('home.ok') : t('home.error')"
        />
      </div>
      <p v-if="current.status !== 'ok'" role="alert" class="text-sm text-danger">
        {{ t('switches.unavailable', { error: current.error ?? '' }) }}
      </p>
      <dl v-else class="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 text-sm">
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
    </section>

    <template v-if="switches.length > 0">
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
      <div :id="`switch-panel-${tab}`" role="tabpanel" :aria-labelledby="`switch-tab-${tab}`">
        <OverviewPanel v-if="tab === 'overview'" :switches="switches" />
        <MacSearchPanel v-else-if="tab === 'macs'" />
        <template v-else-if="current?.status === 'ok'">
          <PortsPanel v-if="tab === 'ports'" :switch-name="current.name" />
          <VlansPanel v-else :switch-name="current.name" :switches="names" />
        </template>
      </div>
    </template>
  </div>
</template>
