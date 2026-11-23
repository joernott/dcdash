<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { api, unwrap } from '@/api/client'
import { errorMessage } from '@/api/errors'
import AliasesPanel from '@/components/firewalls/AliasesPanel.vue'
import HealthPanel from '@/components/firewalls/HealthPanel.vue'
import RulesPanel from '@/components/firewalls/RulesPanel.vue'
import VpnPanel from '@/components/firewalls/VpnPanel.vue'
import IconButton from '@/components/ui/IconButton.vue'
import BaseSelect from '@/components/ui/BaseSelect.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import { formatBytes, type FirewallInfo } from '@/firewalls/firewalls'

const i18n = useI18n()
const { t } = i18n
const route = useRoute()
const router = useRouter()

type Tab = 'health' | 'vpn' | 'aliases' | 'rules'
const TABS: Tab[] = ['health', 'vpn', 'aliases', 'rules']

const firewalls = ref<FirewallInfo[]>([])
const loading = ref(false)
const error = ref('')

const name = computed(() => String(route.params.firewall ?? ''))
const current = computed(() => firewalls.value.find((f) => f.name === name.value))
const tab = computed<Tab>(() => {
  const q = String(route.query.tab ?? '')
  return (TABS as string[]).includes(q) ? (q as Tab) : 'health'
})
const options = computed(() => firewalls.value.map((f) => ({ value: f.name, label: f.name })))

async function load() {
  loading.value = true
  try {
    firewalls.value = (await unwrap(api.GET('/api/v1/firewalls'))).firewalls
    error.value = ''
    if (!name.value && firewalls.value.length > 0) {
      await router.replace({
        name: 'firewalls',
        params: { firewall: firewalls.value[0]!.name },
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
    void router.push({ name: 'firewalls', params: { firewall: next }, query: route.query })
}

function selectTab(next: Tab) {
  void router.replace({ query: { tab: next } })
}

onMounted(load)
</script>

<template>
  <div class="flex flex-col gap-6">
    <div class="flex flex-col gap-3">
      <h1 class="text-2xl font-semibold">{{ t('nav.firewalls') }}</h1>
      <div class="flex flex-wrap items-end gap-3">
        <BaseSelect
          v-if="firewalls.length > 0"
          :model-value="name"
          :label="t('firewalls.firewall')"
          :options="options"
          @update:model-value="select"
        />
        <IconButton
          icon="refresh"
          :label="t('home.refresh')"
          size="md"
          variant="secondary"
          :loading="loading"
          @click="load"
        />
      </div>
    </div>

    <p
      v-if="error"
      role="alert"
      class="rounded-md border border-danger px-3 py-2 text-sm text-danger"
    >
      {{ error }}
    </p>
    <p v-else-if="firewalls.length === 0 && !loading" class="text-fg-muted">
      {{ t('firewalls.none') }}
    </p>

    <section
      v-if="current"
      aria-labelledby="firewall-info"
      class="flex flex-col gap-2 rounded-md border border-border p-4"
    >
      <div class="flex flex-wrap items-center gap-3">
        <h2 id="firewall-info" class="text-lg font-semibold">{{ current.name }}</h2>
        <StatusBadge
          :status="current.status === 'ok' ? 'ok' : 'error'"
          :label="current.status === 'ok' ? t('home.ok') : t('home.error')"
        />
        <a :href="current.url" target="_blank" rel="noopener" class="text-sm text-primary underline"
          >https</a
        >
        <a :href="`ssh://${current.host}`" class="text-sm text-primary underline">ssh</a>
      </div>
      <p v-if="current.status !== 'ok'" role="alert" class="text-sm text-danger">
        {{ t('firewalls.unavailable', { error: current.error ?? '' }) }}
      </p>
      <dl v-else class="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 text-sm">
        <dt class="text-fg-muted">{{ t('firewalls.info.version') }}</dt>
        <dd>{{ current.product }} {{ current.version }}</dd>
        <template v-if="current.uptime">
          <dt class="text-fg-muted">{{ t('firewalls.info.uptime') }}</dt>
          <dd>{{ current.uptime }}</dd>
        </template>
        <template v-if="current.memoryTotal">
          <dt class="text-fg-muted">{{ t('firewalls.info.memory') }}</dt>
          <dd>
            {{
              t('firewalls.info.memoryOf', {
                used: formatBytes(current.memoryUsed ?? 0),
                total: formatBytes(current.memoryTotal),
              })
            }}
          </dd>
        </template>
      </dl>
    </section>

    <template v-if="current?.status === 'ok'">
      <div
        role="tablist"
        :aria-label="t('firewalls.views')"
        class="flex gap-1 border-b border-border"
      >
        <button
          v-for="x in TABS"
          :id="`fw-tab-${x}`"
          :key="x"
          type="button"
          role="tab"
          :aria-selected="tab === x"
          :aria-controls="`fw-panel-${x}`"
          class="-mb-px rounded-t-md border px-3 py-2 text-sm font-medium focus-visible:outline-2 focus-visible:outline-focus"
          :class="
            tab === x
              ? 'border-border border-b-surface bg-surface text-fg'
              : 'border-transparent text-fg-muted hover:text-fg'
          "
          @click="selectTab(x)"
        >
          {{ t(`firewalls.tabs.${x}`) }}
        </button>
      </div>
      <div :id="`fw-panel-${tab}`" role="tabpanel" :aria-labelledby="`fw-tab-${tab}`">
        <HealthPanel v-if="tab === 'health'" :firewall="current.name" />
        <VpnPanel v-else-if="tab === 'vpn'" :firewall="current.name" />
        <AliasesPanel v-else-if="tab === 'aliases'" :firewall="current.name" />
        <RulesPanel v-else :firewall="current.name" />
      </div>
    </template>
  </div>
</template>
