<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { api, unwrap } from '@/api/client'
import { errorMessage } from '@/api/errors'
import LeasesPanel from '@/components/dhcp/LeasesPanel.vue'
import ReservationsPanel from '@/components/dhcp/ReservationsPanel.vue'
import SubnetsPanel from '@/components/dhcp/SubnetsPanel.vue'
import IconButton from '@/components/ui/IconButton.vue'
import BaseSelect from '@/components/ui/BaseSelect.vue'
import type { DhcpServer, DhcpSubnet } from '@/dhcp/dhcp'

const i18n = useI18n()
const { t } = i18n
const route = useRoute()
const router = useRouter()

type Tab = 'subnets' | 'leases' | 'reservations'
const TABS: Tab[] = ['subnets', 'leases', 'reservations']

const servers = ref<DhcpServer[]>([])
const subnets = ref<DhcpSubnet[] | null>(null)
const loading = ref(false)
const error = ref('')
const subnetError = ref('')

const serverName = computed(() => String(route.params.server ?? ''))
const server = computed(() => servers.value.find((s) => s.name === serverName.value))
const tab = computed<Tab>(() => {
  const q = String(route.query.tab ?? '')
  return (TABS as string[]).includes(q) ? (q as Tab) : 'subnets'
})
/** Subnet chosen in the subnet table, shown in the leases tab. */
const leaseSubnet = computed(() => String(route.query.subnet ?? ''))

const serverOptions = computed(() =>
  servers.value.map((s) => ({
    value: s.name,
    label: `${s.name} (${t(`dhcp.families.${s.family}`)})`,
  })),
)
const missing = computed(() => {
  const c = server.value?.capabilities
  if (!c || server.value?.status !== 'ok') return []
  const out: string[] = []
  if (!c.leases) out.push(t('dhcp.missing.leases'))
  if (!c.reservations) out.push(t('dhcp.missing.reservations'))
  else if (!c.reservationWrite) out.push(t('dhcp.missing.hostDatabase'))
  if (!c.subnets) out.push(t('dhcp.missing.subnets'))
  if (!c.statistics) out.push(t('dhcp.missing.statistics'))
  return out
})

async function loadServers() {
  const res = await unwrap(api.GET('/api/v1/dhcp/servers'))
  servers.value = res.servers
  if (!serverName.value && res.servers.length > 0) {
    await router.replace({ name: 'dhcp', params: { server: res.servers[0]!.name } })
  }
}

async function loadSubnets() {
  if (!serverName.value || server.value?.status !== 'ok') {
    subnets.value = null
    return
  }
  try {
    const res = await unwrap(
      api.GET('/api/v1/dhcp/servers/{server}/subnets', {
        params: { path: { server: serverName.value } },
      }),
    )
    subnets.value = res.subnets
    subnetError.value = ''
  } catch (e) {
    subnets.value = null
    subnetError.value = errorMessage(i18n, e)
  }
}

async function refresh() {
  loading.value = true
  try {
    await loadServers()
    error.value = ''
    await loadSubnets()
  } catch (e) {
    error.value = errorMessage(i18n, e)
  } finally {
    loading.value = false
  }
}

function selectServer(name: string) {
  if (name !== serverName.value) void router.push({ name: 'dhcp', params: { server: name } })
}

function selectTab(next: Tab, query: Record<string, string> = {}) {
  void router.replace({ query: { ...query, tab: next } })
}

function showLeases(subnetId: number) {
  selectTab('leases', { subnet: String(subnetId) })
}

watch(serverName, () => {
  subnets.value = null
  void loadSubnets()
})

onMounted(refresh)
</script>

<template>
  <div class="flex flex-col gap-6">
    <div class="flex flex-col gap-3">
      <h1 class="text-2xl font-semibold">{{ t('nav.dhcp') }}</h1>
      <div class="flex flex-wrap items-end gap-3">
        <BaseSelect
          v-if="servers.length > 0"
          :model-value="serverName"
          :label="t('dhcp.server')"
          :options="serverOptions"
          @update:model-value="selectServer"
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
    <p v-else-if="servers.length === 0 && !loading" class="text-fg-muted">
      {{ t('dhcp.noServers') }}
    </p>

    <template v-if="server">
      <p
        v-if="server.status !== 'ok'"
        role="alert"
        class="rounded-md border border-danger px-3 py-2 text-sm text-danger"
      >
        {{ t('dhcp.unavailable', { error: server.error }) }}
      </p>
      <ul v-if="missing.length" class="list-inside list-disc text-sm text-fg-muted">
        <li v-for="m in missing" :key="m">{{ m }}</li>
      </ul>
    </template>

    <template v-if="server?.status === 'ok'">
      <div role="tablist" :aria-label="t('dhcp.views')" class="flex gap-1 border-b border-border">
        <button
          v-for="x in TABS"
          :id="`dhcp-tab-${x}`"
          :key="x"
          type="button"
          role="tab"
          :aria-selected="tab === x"
          :aria-controls="`dhcp-panel-${x}`"
          class="-mb-px rounded-t-md border px-3 py-2 text-sm font-medium focus-visible:outline-2 focus-visible:outline-focus"
          :class="
            tab === x
              ? 'border-border border-b-surface bg-surface text-fg'
              : 'border-transparent text-fg-muted hover:text-fg'
          "
          @click="selectTab(x)"
        >
          {{ t(`dhcp.tabs.${x}`) }}
        </button>
      </div>

      <p
        v-if="subnetError"
        role="alert"
        class="rounded-md border border-danger px-3 py-2 text-sm text-danger"
      >
        {{ subnetError }}
      </p>
      <div
        v-else-if="subnets"
        :id="`dhcp-panel-${tab}`"
        role="tabpanel"
        :aria-labelledby="`dhcp-tab-${tab}`"
      >
        <SubnetsPanel
          v-if="tab === 'subnets'"
          :server="server"
          :subnets="subnets"
          @show-leases="showLeases"
        />
        <LeasesPanel
          v-else-if="tab === 'leases'"
          :key="`${server.name}-${leaseSubnet}`"
          :server="server"
          :subnets="subnets"
          :initial-subnet="leaseSubnet"
        />
        <ReservationsPanel v-else :key="server.name" :server="server" :subnets="subnets" />
      </div>
    </template>
  </div>
</template>
