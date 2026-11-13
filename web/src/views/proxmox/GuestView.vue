<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, useTemplateRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'

import { api, unwrap, type Schemas } from '@/api/client'
import { errorMessage } from '@/api/errors'
import GuestActions from '@/components/proxmox/GuestActions.vue'
import PowerActionDialog from '@/components/proxmox/PowerActionDialog.vue'
import TaskList from '@/components/proxmox/TaskList.vue'
import UsageBar from '@/components/proxmox/UsageBar.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import { formatBytes, ratio, statusStyle, uptimeParts, type PowerAction } from '@/proxmox/guests'

type Detail = Schemas['ProxmoxGuestDetail']

const REFRESH_MS = 10_000

const i18n = useI18n()
const { t, locale } = i18n
const route = useRoute()
const dialog = useTemplateRef<InstanceType<typeof PowerActionDialog>>('dialog')

const detail = ref<Detail | null>(null)
const error = ref('')
let timer: ReturnType<typeof setInterval> | undefined

const cluster = computed(() => String(route.params.cluster))
const type = computed(() => String(route.params.type) as 'qemu' | 'lxc')
const vmid = computed(() => Number(route.params.vmid))
const guest = computed(() => detail.value?.guest)

/** Configuration entries sorted by key, values as text. */
const config = computed(() =>
  Object.entries(detail.value?.config ?? {})
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([key, value]) => ({
      key,
      value: typeof value === 'object' ? JSON.stringify(value) : String(value),
    })),
)

function statusLabel(s: string): string {
  const key = `proxmox.states.${s}`
  return i18n.te(key) ? t(key) : s
}

function bytes(v: number): string {
  return formatBytes(v, locale.value)
}

async function load() {
  try {
    detail.value = await unwrap(
      api.GET('/api/v1/proxmox/clusters/{cluster}/guests/{type}/{vmid}', {
        params: { path: { cluster: cluster.value, type: type.value, vmid: vmid.value } },
      }),
    )
    error.value = ''
  } catch (e) {
    error.value = errorMessage(i18n, e)
  }
}

function act(a: PowerAction) {
  if (guest.value) dialog.value?.ask(cluster.value, guest.value, a)
}

watch([cluster, type, vmid], () => {
  detail.value = null
  void load()
})
onMounted(() => {
  void load()
  timer = setInterval(() => {
    if (!document.hidden) void load()
  }, REFRESH_MS)
})
onBeforeUnmount(() => clearInterval(timer))
</script>

<template>
  <div class="flex flex-col gap-6">
    <nav :aria-label="t('proxmox.breadcrumb')" class="text-sm">
      <RouterLink :to="{ name: 'proxmox', params: { cluster } }" class="text-primary underline">
        {{ t('nav.proxmox') }} · {{ cluster }}
      </RouterLink>
    </nav>

    <p
      v-if="error"
      role="alert"
      class="rounded-md border border-danger px-3 py-2 text-sm text-danger"
    >
      {{ error }}
    </p>

    <template v-if="guest">
      <div class="flex flex-wrap items-center justify-between gap-4">
        <div class="flex flex-wrap items-center gap-3">
          <h1 class="text-2xl font-semibold">{{ guest.name || `#${guest.vmid}` }}</h1>
          <StatusBadge :status="statusStyle(guest.status)" :label="statusLabel(guest.status)" />
        </div>
        <GuestActions :cluster="cluster" :guest="guest" size="md" @action="act" />
      </div>

      <dl class="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-[max-content_1fr]">
        <dt class="text-fg-muted">{{ t('proxmox.vmid') }}</dt>
        <dd>{{ guest.vmid }}</dd>
        <dt class="text-fg-muted">{{ t('proxmox.type') }}</dt>
        <dd>{{ t(`proxmox.types.${guest.type}`) }}</dd>
        <dt class="text-fg-muted">{{ t('proxmox.node') }}</dt>
        <dd>{{ guest.node }}</dd>
        <template v-if="guest.status === 'running'">
          <dt class="text-fg-muted">{{ t('proxmox.uptime') }}</dt>
          <dd>{{ t('proxmox.uptimeValue', uptimeParts(guest.uptime)) }}</dd>
        </template>
        <template v-if="guest.tags.length">
          <dt class="text-fg-muted">{{ t('proxmox.tags') }}</dt>
          <dd>{{ guest.tags.join(', ') }}</dd>
        </template>
        <template v-if="guest.pool">
          <dt class="text-fg-muted">{{ t('proxmox.pool') }}</dt>
          <dd>{{ guest.pool }}</dd>
        </template>
        <template v-if="guest.haState">
          <dt class="text-fg-muted">{{ t('proxmox.ha') }}</dt>
          <dd>{{ guest.haState }}</dd>
        </template>
        <template v-if="guest.lock">
          <dt class="text-fg-muted">{{ t('proxmox.lock') }}</dt>
          <dd>{{ guest.lock }}</dd>
        </template>
      </dl>

      <section
        v-if="guest.status === 'running'"
        aria-labelledby="guest-usage"
        class="grid max-w-3xl gap-4 sm:grid-cols-2"
      >
        <h2 id="guest-usage" class="sr-only">{{ t('proxmox.usage') }}</h2>
        <UsageBar
          :label="t('proxmox.cpu')"
          :ratio="guest.cpu"
          :detail="t('proxmox.cores', { n: guest.maxCpu })"
        />
        <UsageBar
          :label="t('proxmox.memory')"
          :ratio="ratio(guest.mem, guest.maxMem)"
          :detail="`${bytes(guest.mem)} / ${bytes(guest.maxMem)}`"
        />
      </section>

      <section aria-labelledby="guest-config" class="flex flex-col gap-3">
        <h2 id="guest-config" class="text-lg font-semibold">{{ t('proxmox.configuration') }}</h2>
        <div class="overflow-x-auto rounded-md border border-border">
          <table class="w-full text-left text-sm">
            <caption class="sr-only">
              {{
                t('proxmox.configuration')
              }}
            </caption>
            <tbody>
              <tr v-for="c in config" :key="c.key" class="border-t border-border first:border-t-0">
                <th scope="row" class="w-48 px-3 py-1.5 font-mono font-normal text-fg-muted">
                  {{ c.key }}
                </th>
                <td class="px-3 py-1.5 font-mono break-all whitespace-pre-wrap">{{ c.value }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </template>
    <p v-else-if="!error" class="text-fg-muted">{{ t('common.loading') }}</p>

    <TaskList :cluster="cluster" />
    <PowerActionDialog ref="dialog" @done="load" />
  </div>
</template>
