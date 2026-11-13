<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, useTemplateRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { api, unwrap, type Schemas } from '@/api/client'
import { errorMessage } from '@/api/errors'
import GuestActions from '@/components/proxmox/GuestActions.vue'
import PowerActionDialog from '@/components/proxmox/PowerActionDialog.vue'
import TaskList from '@/components/proxmox/TaskList.vue'
import UsageBar from '@/components/proxmox/UsageBar.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import BaseInput from '@/components/ui/BaseInput.vue'
import BaseSelect from '@/components/ui/BaseSelect.vue'
import DataTable from '@/components/ui/DataTable.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import {
  filterGuests,
  formatBytes,
  nodeShellPermission,
  ratio,
  statusStyle,
  uptimeParts,
  type Guest,
  type GuestFilter,
} from '@/proxmox/guests'
import { useSessionStore } from '@/stores/session'

type Inventory = Schemas['ProxmoxInventory']

/** Reload interval of the inventory. */
const REFRESH_MS = 15_000

const i18n = useI18n()
const { t, n, locale } = i18n
const route = useRoute()
const router = useRouter()
const session = useSessionStore()
const dialog = useTemplateRef<InstanceType<typeof PowerActionDialog>>('dialog')

const clusters = ref<string[]>([])
const inventory = ref<Inventory | null>(null)
const loading = ref(false)
const error = ref('')
const filter = ref<GuestFilter>({ text: '', type: '', status: '', node: '' })
let timer: ReturnType<typeof setInterval> | undefined

const cluster = computed(() => String(route.params.cluster ?? ''))
const guests = computed(() => filterGuests(inventory.value?.guests ?? [], filter.value))

const columns = computed(() => [
  { key: 'vmid', label: t('proxmox.vmid'), class: 'w-16' },
  { key: 'name', label: t('proxmox.name') },
  { key: 'type', label: t('proxmox.type') },
  { key: 'node', label: t('proxmox.node') },
  { key: 'status', label: t('proxmox.status') },
  { key: 'usage', label: t('proxmox.usage'), class: 'min-w-40' },
  { key: 'actions', label: t('proxmox.actionsColumn') },
])

const typeOptions = computed(() => [
  { value: '', label: t('proxmox.all') },
  { value: 'qemu', label: t('proxmox.types.qemu') },
  { value: 'lxc', label: t('proxmox.types.lxc') },
])
const statusOptions = computed(() => [
  { value: '', label: t('proxmox.all') },
  ...[...new Set((inventory.value?.guests ?? []).map((g) => g.status))]
    .sort()
    .map((s) => ({ value: s, label: statusLabel(s) })),
])
const nodeOptions = computed(() => [
  { value: '', label: t('proxmox.all') },
  ...[...new Set((inventory.value?.guests ?? []).map((g) => g.node))]
    .sort()
    .map((s) => ({ value: s, label: s })),
])
const clusterOptions = computed(() => clusters.value.map((c) => ({ value: c, label: c })))

function statusLabel(s: string): string {
  const key = `proxmox.states.${s}`
  return i18n.te(key) ? t(key) : s
}

function bytes(v: number): string {
  return formatBytes(v, locale.value)
}

function uptime(seconds: number): string {
  const u = uptimeParts(seconds)
  return t('proxmox.uptimeValue', u)
}

async function loadClusters() {
  const res = await unwrap(api.GET('/api/v1/proxmox/clusters'))
  clusters.value = res.clusters.map((c) => c.name)
  if (!cluster.value && clusters.value.length > 0) {
    await router.replace({ name: 'proxmox', params: { cluster: clusters.value[0] } })
  }
}

async function load() {
  if (!cluster.value) return
  loading.value = true
  try {
    inventory.value = await unwrap(
      api.GET('/api/v1/proxmox/clusters/{cluster}/resources', {
        params: { path: { cluster: cluster.value } },
      }),
    )
    error.value = ''
  } catch (e) {
    error.value = errorMessage(i18n, e)
  } finally {
    loading.value = false
  }
}

function selectCluster(c: string) {
  if (c !== cluster.value) void router.push({ name: 'proxmox', params: { cluster: c } })
}

function act(g: Guest, action: Parameters<NonNullable<typeof dialog.value>['ask']>[2]) {
  dialog.value?.ask(cluster.value, g, action)
}

watch(cluster, () => {
  inventory.value = null
  void load()
})

onMounted(async () => {
  try {
    await loadClusters()
  } catch (e) {
    error.value = errorMessage(i18n, e)
  }
  await load()
  timer = setInterval(() => {
    if (!document.hidden) void load()
  }, REFRESH_MS)
})
onBeforeUnmount(() => clearInterval(timer))
</script>

<template>
  <div class="flex flex-col gap-6">
    <div class="flex flex-wrap items-end justify-between gap-4">
      <h1 class="text-2xl font-semibold">{{ t('nav.proxmox') }}</h1>
      <div class="flex flex-wrap items-end gap-3">
        <BaseSelect
          v-if="clusters.length > 1"
          :model-value="cluster"
          :label="t('proxmox.cluster')"
          :options="clusterOptions"
          @update:model-value="selectCluster"
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
    <p v-else-if="clusters.length === 0 && !loading" class="text-fg-muted">
      {{ t('proxmox.noClusters') }}
    </p>

    <section v-if="inventory" aria-labelledby="proxmox-nodes" class="flex flex-col gap-3">
      <h2 id="proxmox-nodes" class="text-lg font-semibold">{{ t('proxmox.nodes') }}</h2>
      <p v-if="inventory.nodes.length === 0" class="text-sm text-fg-muted">
        {{ t('proxmox.noNodes') }}
      </p>
      <ul class="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
        <li
          v-for="node in inventory.nodes"
          :key="node.name"
          class="flex flex-col gap-3 rounded-lg border border-border bg-surface-raised p-4"
        >
          <div class="flex items-center justify-between gap-2">
            <h3 class="font-semibold">{{ node.name }}</h3>
            <StatusBadge :status="statusStyle(node.status)" :label="statusLabel(node.status)" />
          </div>
          <UsageBar
            :label="t('proxmox.cpu')"
            :ratio="node.maxCpu > 0 ? node.cpu : undefined"
            :detail="node.maxCpu > 0 ? t('proxmox.cores', { n: node.maxCpu }) : undefined"
          />
          <UsageBar
            :label="t('proxmox.memory')"
            :ratio="ratio(node.mem, node.maxMem)"
            :detail="node.maxMem > 0 ? `${bytes(node.mem)} / ${bytes(node.maxMem)}` : undefined"
          />
          <UsageBar
            :label="t('proxmox.disk')"
            :ratio="ratio(node.disk, node.maxDisk)"
            :detail="node.maxDisk > 0 ? `${bytes(node.disk)} / ${bytes(node.maxDisk)}` : undefined"
          />
          <div class="flex items-center justify-between gap-2 text-sm text-fg-muted">
            <span v-if="node.uptime > 0">{{ t('proxmox.uptime') }}: {{ uptime(node.uptime) }}</span>
            <RouterLink
              v-if="session.can(nodeShellPermission(cluster, node.name))"
              :to="{ name: 'proxmox-shell', params: { cluster, node: node.name } }"
              class="text-primary underline"
            >
              {{ t('proxmox.shell') }}
            </RouterLink>
          </div>
        </li>
      </ul>
    </section>

    <section v-if="inventory" aria-labelledby="proxmox-guests" class="flex flex-col gap-3">
      <h2 id="proxmox-guests" class="text-lg font-semibold">{{ t('proxmox.guests') }}</h2>
      <form class="flex flex-wrap items-end gap-3" role="search" @submit.prevent>
        <BaseInput v-model="filter.text" :label="t('proxmox.search')" type="search" />
        <BaseSelect v-model="filter.type" :label="t('proxmox.type')" :options="typeOptions" />
        <BaseSelect v-model="filter.status" :label="t('proxmox.status')" :options="statusOptions" />
        <BaseSelect v-model="filter.node" :label="t('proxmox.node')" :options="nodeOptions" />
      </form>
      <p class="text-sm text-fg-muted" aria-live="polite">
        {{ t('proxmox.shown', { n: guests.length, total: inventory.guests.length }) }}
      </p>
      <DataTable
        :columns="columns"
        :rows="guests"
        :row-key="(g: Guest) => `${g.type}/${g.vmid}`"
        :caption="t('proxmox.guests')"
        :empty="t('proxmox.noGuests')"
      >
        <template #cell-name="{ row }">
          <RouterLink
            :to="{
              name: 'proxmox-guest',
              params: { cluster, type: (row as Guest).type, vmid: (row as Guest).vmid },
            }"
            class="font-medium text-primary underline"
          >
            {{ (row as Guest).name || `#${(row as Guest).vmid}` }}
          </RouterLink>
          <span v-if="(row as Guest).template" class="ml-1 text-xs text-fg-muted">
            ({{ t('proxmox.template') }})
          </span>
          <span v-if="(row as Guest).tags.length" class="mt-0.5 flex flex-wrap gap-1">
            <span
              v-for="tag in (row as Guest).tags"
              :key="tag"
              class="rounded bg-surface-sunken px-1.5 text-xs text-fg-muted"
              >{{ tag }}</span
            >
          </span>
        </template>
        <template #cell-type="{ row }">{{ t(`proxmox.types.${(row as Guest).type}`) }}</template>
        <template #cell-status="{ row }">
          <StatusBadge
            :status="statusStyle((row as Guest).status)"
            :label="statusLabel((row as Guest).status)"
          />
        </template>
        <template #cell-usage="{ row }">
          <div v-if="(row as Guest).status === 'running'" class="flex flex-col gap-1">
            <span class="text-xs tabular-nums">
              {{ t('proxmox.cpu') }} {{ n((row as Guest).cpu, 'percent') }} ·
              {{ t('proxmox.memory') }} {{ bytes((row as Guest).mem) }} /
              {{ bytes((row as Guest).maxMem) }}
            </span>
          </div>
          <span v-else class="text-fg-muted">–</span>
        </template>
        <template #cell-actions="{ row }">
          <GuestActions
            :cluster="cluster"
            :guest="row as Guest"
            @action="(a) => act(row as Guest, a)"
          />
        </template>
      </DataTable>
    </section>

    <TaskList v-if="cluster" :cluster="cluster" />
    <PowerActionDialog ref="dialog" @done="load" />
  </div>
</template>
