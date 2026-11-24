<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, onMounted, ref, useTemplateRef } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute, useRouter } from 'vue-router'

import { api, unwrap } from '@/api/client'
import { errorMessage } from '@/api/errors'
import AliasesPanel from '@/components/firewalls/AliasesPanel.vue'
import HealthPanel from '@/components/firewalls/HealthPanel.vue'
import RulesPanel from '@/components/firewalls/RulesPanel.vue'
import VpnPanel from '@/components/firewalls/VpnPanel.vue'
import BaseSelect from '@/components/ui/BaseSelect.vue'
import IconButton from '@/components/ui/IconButton.vue'
import LinkButtons from '@/components/ui/LinkButtons.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import { formatBytes, type FirewallInfo } from '@/firewalls/firewalls'
import { systemStyle } from '@/systems'

/**
 * /firewalls: every firewall with its version, uptime and memory; the field filters them.
 * /firewalls/<name>: one firewall with the tabs health, VPN, aliases and rules (?tab=).
 */
const i18n = useI18n()
const { t } = i18n
const route = useRoute()
const router = useRouter()

type Tab = 'health' | 'vpn' | 'aliases' | 'rules'
const TABS: Tab[] = ['health', 'vpn', 'aliases', 'rules']

const firewalls = ref<FirewallInfo[]>([])
const loading = ref(false)
const loaded = ref(false)
const error = ref('')
const panel = useTemplateRef<{ load: () => Promise<void> }>('panel')

const name = computed(() => String(route.params.firewall ?? ''))
const current = computed(() => firewalls.value.find((f) => f.name === name.value))
const tab = computed<Tab>(() => {
  const q = String(route.query.tab ?? '')
  return (TABS as string[]).includes(q) ? (q as Tab) : 'health'
})

// The filter of the overview: one firewall or all ('').
const filter = computed({
  get: () => String(route.query.firewall ?? ''),
  set: (v: string) => void router.replace({ query: v ? { firewall: v } : {} }),
})
const filterOptions = computed(() => [
  { value: '', label: t('firewalls.all') },
  ...firewalls.value.map((f) => ({ value: f.name, label: f.name })),
])
const shown = computed(() =>
  filter.value ? firewalls.value.filter((f) => f.name === filter.value) : firewalls.value,
)

async function load() {
  loading.value = true
  try {
    firewalls.value = (await unwrap(api.GET('/api/v1/firewalls'))).firewalls
    error.value = ''
  } catch (e) {
    error.value = errorMessage(i18n, e)
  } finally {
    loading.value = false
    loaded.value = true
  }
}

/** Reloads the firewalls and, on the details page, the open tab. */
async function refresh() {
  await load()
  await panel.value?.load()
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
      :to="{ name: 'firewalls' }"
      class="-mb-4 self-start text-sm text-primary hover:underline"
      >← {{ t('firewalls.back') }}</RouterLink
    >
    <!-- The heading and, on the right, the filter and refresh. -->
    <div class="flex flex-wrap items-end justify-between gap-4">
      <div class="flex flex-wrap items-center gap-3">
        <h1 class="text-2xl font-semibold">{{ name || t('nav.firewalls') }}</h1>
        <template v-if="current">
          <StatusBadge
            :status="current.status === 'ok' ? 'ok' : 'error'"
            :label="current.status === 'ok' ? t('home.ok') : t('home.error')"
          />
          <LinkButtons :name="current.name" :ui="current.url" :ssh="sshUrl(current.host)" />
        </template>
      </div>
      <div class="flex flex-wrap items-end gap-3">
        <BaseSelect
          v-if="!name && firewalls.length > 1"
          v-model="filter"
          :label="t('firewalls.firewall')"
          :options="filterOptions"
        />
        <IconButton
          icon="refresh"
          :label="t('home.refresh')"
          size="md"
          variant="secondary"
          :loading="loading"
          @click="refresh"
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
    <p v-else-if="loaded && firewalls.length === 0" class="text-fg-muted">
      {{ t('firewalls.none') }}
    </p>

    <!-- Overview: every firewall; the name opens its details. -->
    <ul v-else-if="!name" class="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
      <li
        v-for="f in shown"
        :key="f.name"
        class="sys-tint flex flex-col gap-2 rounded-md border p-4"
        :class="f.status === 'ok' ? '' : 'border-danger'"
        :style="systemStyle('firewall')"
      >
        <section :aria-labelledby="`firewall-${f.name}`" class="flex flex-col gap-2">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <h2 :id="`firewall-${f.name}`" class="text-lg font-semibold">
              <RouterLink
                :to="{ name: 'firewalls', params: { firewall: f.name } }"
                class="text-primary underline"
                >{{ f.name }}</RouterLink
              >
            </h2>
            <div class="flex flex-wrap items-center gap-2">
              <LinkButtons :name="f.name" :ui="f.url" :ssh="sshUrl(f.host)" />
              <StatusBadge
                :status="f.status === 'ok' ? 'ok' : 'error'"
                :label="f.status === 'ok' ? t('home.ok') : t('home.error')"
              />
            </div>
          </div>
          <p v-if="f.status !== 'ok'" role="alert" class="text-sm text-danger">
            {{ t('firewalls.unavailable', { error: f.error ?? '' }) }}
          </p>
          <dl v-else class="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 text-sm">
            <dt class="text-fg-muted">{{ t('firewalls.info.version') }}</dt>
            <dd>{{ f.product }} {{ f.version }}</dd>
            <template v-if="f.uptime">
              <dt class="text-fg-muted">{{ t('firewalls.info.uptime') }}</dt>
              <dd>{{ f.uptime }}</dd>
            </template>
            <template v-if="f.memoryTotal">
              <dt class="text-fg-muted">{{ t('firewalls.info.memory') }}</dt>
              <dd>
                {{
                  t('firewalls.info.memoryOf', {
                    used: formatBytes(f.memoryUsed ?? 0),
                    total: formatBytes(f.memoryTotal),
                  })
                }}
              </dd>
            </template>
          </dl>
        </section>
      </li>
    </ul>

    <!-- Details of one firewall. -->
    <template v-else-if="loaded && !error">
      <p v-if="!current" role="alert" class="text-danger">
        {{ t('firewalls.notFound', { name }) }}
      </p>
      <p v-else-if="current.status !== 'ok'" role="alert" class="text-sm text-danger">
        {{ t('firewalls.unavailable', { error: current.error ?? '' }) }}
      </p>
      <template v-else>
        <dl
          class="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 rounded-md border border-border p-4 text-sm"
        >
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
        <div>
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
          <div
            :id="`fw-panel-${tab}`"
            role="tabpanel"
            :aria-labelledby="`fw-tab-${tab}`"
            class="pt-4"
          >
            <HealthPanel v-if="tab === 'health'" ref="panel" :firewall="current.name" />
            <VpnPanel v-else-if="tab === 'vpn'" ref="panel" :firewall="current.name" />
            <AliasesPanel v-else-if="tab === 'aliases'" ref="panel" :firewall="current.name" />
            <RulesPanel v-else ref="panel" :firewall="current.name" />
          </div>
        </div>
      </template>
    </template>
  </div>
</template>
