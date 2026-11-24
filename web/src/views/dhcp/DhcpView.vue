<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, nextTick, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { api, unwrap } from '@/api/client'
import { errorMessage } from '@/api/errors'
import LeasesList from '@/components/dhcp/LeasesList.vue'
import ReservationsPanel from '@/components/dhcp/ReservationsPanel.vue'
import SubnetsPanel from '@/components/dhcp/SubnetsPanel.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import BaseInput from '@/components/ui/BaseInput.vue'
import BaseSelect from '@/components/ui/BaseSelect.vue'
import IconButton from '@/components/ui/IconButton.vue'
import LinkButtons from '@/components/ui/LinkButtons.vue'
import type { DhcpLease, DhcpServer, DhcpSubnet, SearchKey } from '@/dhcp/dhcp'

/**
 * All DHCP servers with their subnets, like the DNS page. The server in the route is an
 * optional filter. The lease search (top right) and the leases button of a subnet show their
 * leases above the servers.
 */
const i18n = useI18n()
const { t } = i18n
const route = useRoute()
const router = useRouter()

// The subnets of a server: null until loaded or if loading failed.
interface ServerSubnets {
  subnets: DhcpSubnet[] | null
  loading: boolean
  error: string
}

type LeaseQuery = { subnet?: number; by?: SearchKey; q?: string }

// The leases of one server in the lease view.
interface LeasePart {
  server: DhcpServer
  query: LeaseQuery
  leases: DhcpLease[] | null
  truncated: boolean
  loading: boolean
  error: string
}

const servers = ref<DhcpServer[]>([])
const state = reactive<Record<string, ServerSubnets>>({})
const reservationsOpen = reactive<Record<string, boolean>>({})
const loadingServers = ref(false)
const error = ref('')

const filter = computed(() => String(route.params.server ?? ''))
const shown = computed(() =>
  filter.value ? servers.value.filter((s) => s.name === filter.value) : servers.value,
)
const filterOptions = computed(() => [
  { value: '', label: t('dhcp.allServers') },
  ...servers.value.map((s) => ({ value: s.name, label: serverLabel(s) })),
])
const loading = computed(
  () => loadingServers.value || shown.value.some((s) => state[s.name]?.loading),
)

function serverLabel(s: DhcpServer) {
  return `${s.name} (${t(`dhcp.families.${s.family}`)})`
}

function usable(s: DhcpServer) {
  return s.status === 'ok'
}

function missing(s: DhcpServer): string[] {
  const c = s.capabilities
  if (!usable(s)) return []
  const out: string[] = []
  if (!c.leases) out.push(t('dhcp.missing.leases'))
  if (!c.reservations) out.push(t('dhcp.missing.reservations'))
  else if (!c.reservationWrite) out.push(t('dhcp.missing.hostDatabase'))
  if (!c.subnets) out.push(t('dhcp.missing.subnets'))
  if (!c.statistics) out.push(t('dhcp.missing.statistics'))
  return out
}

async function loadSubnets(s: DhcpServer) {
  if (!usable(s)) return
  state[s.name] ??= { subnets: null, loading: false, error: '' }
  // The reactive proxy, not the plain object the assignment returns.
  const st = state[s.name]!
  st.loading = true
  try {
    const res = await unwrap(
      api.GET('/api/v1/dhcp/servers/{server}/subnets', { params: { path: { server: s.name } } }),
    )
    st.subnets = res.subnets
    st.error = ''
  } catch (e) {
    st.subnets = null
    st.error = errorMessage(i18n, e)
  } finally {
    st.loading = false
  }
}

async function refresh() {
  loadingServers.value = true
  try {
    servers.value = (await unwrap(api.GET('/api/v1/dhcp/servers'))).servers
    error.value = ''
  } catch (e) {
    error.value = errorMessage(i18n, e)
    return
  } finally {
    loadingServers.value = false
  }
  await Promise.all([
    ...shown.value.map(loadSubnets),
    ...(leaseView.value?.parts.map(loadPart) ?? []),
  ])
}

function selectFilter(name: string) {
  if (name !== filter.value) {
    void router.push({ name: 'dhcp', params: name ? { server: name } : {} })
  }
}

// Changing the filter loads the servers whose subnets were not loaded yet.
watch(filter, () => {
  for (const s of shown.value) {
    if (!state[s.name]) void loadSubnets(s)
  }
})

// The lease view: a search on the shown servers or the leases of a subnet.
const leaseView = ref<{ title: string; parts: LeasePart[] } | null>(null)
const leaseHeading = ref<HTMLElement | null>(null)

/** Servers whose leases can be searched. */
const searchable = computed(() => shown.value.filter((s) => usable(s) && s.capabilities.leases))
/** The search keys of any searchable server. */
const keyOptions = computed(() => {
  const keys = new Set<SearchKey>()
  for (const s of searchable.value) for (const k of s.searchKeys) keys.add(k)
  return [...keys].map((k) => ({ value: k, label: t(`dhcp.searchKeys.${k}`) }))
})
const by = ref<SearchKey>('ip')
const query = ref('')
watch(keyOptions, (opts) => {
  if (opts.length && !opts.some((o) => o.value === by.value)) by.value = opts[0]!.value
})

async function loadPart(p: LeasePart) {
  p.loading = true
  try {
    const res = await unwrap(
      api.GET('/api/v1/dhcp/servers/{server}/leases', {
        params: { path: { server: p.server.name }, query: p.query },
      }),
    )
    p.leases = res.leases
    p.truncated = res.truncated
    p.error = ''
  } catch (e) {
    p.leases = null
    p.error = errorMessage(i18n, e)
  } finally {
    p.loading = false
  }
}

async function showLeaseView(title: string, servers: DhcpServer[], q: LeaseQuery) {
  leaseView.value = {
    title,
    parts: servers.map((server) => ({
      server,
      query: q,
      leases: null,
      truncated: false,
      loading: false,
      error: '',
    })),
  }
  await nextTick()
  leaseHeading.value?.scrollIntoView?.({ block: 'nearest' })
  // The reactive proxies of the parts.
  await Promise.all(leaseView.value.parts.map(loadPart))
}

function search() {
  const q = query.value.trim()
  if (!q) return
  const targets = searchable.value.filter((s) => s.searchKeys.includes(by.value))
  void showLeaseView(t('dhcp.searchResults', { q }), targets, { by: by.value, q })
}

function showSubnetLeases(s: DhcpServer, subnet: DhcpSubnet) {
  void showLeaseView(t('dhcp.leasesOf', { subnet: subnet.prefix, server: s.name }), [s], {
    subnet: subnet.id,
  })
}

onMounted(refresh)
</script>

<template>
  <div class="flex flex-col gap-6">
    <!-- The heading and, on the right, filter, lease search and refresh. -->
    <div class="flex flex-wrap items-end justify-between gap-4">
      <h1 class="text-2xl font-semibold">{{ t('nav.dhcp') }}</h1>
      <form class="flex flex-wrap items-end gap-3" role="search" @submit.prevent="search">
        <BaseSelect
          v-if="servers.length > 1"
          :model-value="filter"
          :label="t('dhcp.server')"
          :options="filterOptions"
          @update:model-value="selectFilter"
        />
        <template v-if="keyOptions.length">
          <BaseSelect v-model="by" :label="t('dhcp.searchBy')" :options="keyOptions" />
          <BaseInput v-model="query" :label="t('dhcp.searchValue')" type="search" />
          <IconButton
            icon="search"
            :label="t('dhcp.search')"
            size="md"
            type="submit"
            variant="primary"
            :disabled="!query.trim()"
          />
        </template>
        <IconButton
          icon="refresh"
          :label="t('home.refresh')"
          size="md"
          variant="secondary"
          :loading="loading"
          @click="refresh"
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
    <p v-else-if="servers.length === 0 && !loadingServers" class="text-fg-muted">
      {{ t('dhcp.noServers') }}
    </p>

    <!-- Leases: the search result or the leases of a subnet. -->
    <section
      v-if="leaseView"
      aria-labelledby="dhcp-leases"
      class="flex flex-col gap-3 rounded-md border border-border p-4"
    >
      <div class="flex items-center justify-between gap-3">
        <h2 id="dhcp-leases" ref="leaseHeading" class="text-lg font-semibold">
          {{ leaseView.title }}
        </h2>
        <IconButton icon="close" :label="t('dhcp.closeLeases')" @click="leaseView = null" />
      </div>
      <p v-if="leaseView.parts.length === 0" class="text-sm text-fg-muted">
        {{ t('dhcp.noLeases') }}
      </p>
      <div v-for="p in leaseView.parts" :key="p.server.name" class="flex flex-col gap-2">
        <h3 v-if="leaseView.parts.length > 1" class="font-semibold">
          {{ serverLabel(p.server) }}
        </h3>
        <p
          v-if="p.error"
          role="alert"
          class="rounded-md border border-danger px-3 py-2 text-sm text-danger"
        >
          {{ p.error }}
        </p>
        <LeasesList
          v-else-if="p.leases"
          :server="p.server"
          :leases="p.leases"
          :truncated="p.truncated"
          :caption="
            leaseView.parts.length > 1 ? `${leaseView.title} (${p.server.name})` : leaseView.title
          "
          @deleted="loadPart(p)"
        />
        <p v-else class="text-sm text-fg-muted">{{ t('common.loading') }}</p>
      </div>
    </section>

    <section
      v-for="s in shown"
      :key="s.name"
      :aria-labelledby="`dhcp-server-${s.name}`"
      class="flex flex-col gap-3"
    >
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div class="flex flex-wrap items-center gap-3">
          <h2 :id="`dhcp-server-${s.name}`" class="text-lg font-semibold">
            {{ serverLabel(s) }}
          </h2>
          <LinkButtons
            v-if="s.links"
            :name="s.name"
            :ui="s.links.ui"
            :ssh="s.links.ssh"
            :rdp="s.links.rdp"
          />
        </div>
        <BaseButton
          v-if="usable(s) && s.capabilities.reservations"
          variant="secondary"
          size="sm"
          :aria-expanded="!!reservationsOpen[s.name]"
          :aria-controls="`dhcp-reservations-panel-${s.name}`"
          @click="reservationsOpen[s.name] = !reservationsOpen[s.name]"
        >
          {{ t('dhcp.tabs.reservations') }}
        </BaseButton>
      </div>
      <p
        v-if="!usable(s)"
        role="alert"
        class="rounded-md border border-danger px-3 py-2 text-sm text-danger"
      >
        {{ t('dhcp.unavailable', { error: s.error }) }}
      </p>
      <template v-else>
        <ul v-if="missing(s).length" class="list-inside list-disc text-sm text-fg-muted">
          <li v-for="m in missing(s)" :key="m">{{ m }}</li>
        </ul>
        <p
          v-if="state[s.name]?.error"
          role="alert"
          class="rounded-md border border-danger px-3 py-2 text-sm text-danger"
        >
          {{ state[s.name]!.error }}
        </p>
        <SubnetsPanel
          v-else-if="state[s.name]?.subnets"
          :server="s"
          :subnets="state[s.name]!.subnets!"
          @show-leases="(sub: DhcpSubnet) => showSubnetLeases(s, sub)"
        />
        <p v-else class="text-sm text-fg-muted">{{ t('common.loading') }}</p>
        <div
          v-if="reservationsOpen[s.name] && state[s.name]?.subnets"
          :id="`dhcp-reservations-panel-${s.name}`"
        >
          <ReservationsPanel :server="s" :subnets="state[s.name]!.subnets!" />
        </div>
      </template>
    </section>
  </div>
</template>
