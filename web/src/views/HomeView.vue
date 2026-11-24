<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { api, unwrap, type Schemas } from '@/api/client'
import { errorMessage } from '@/api/errors'
import IconButton from '@/components/ui/IconButton.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import { useSessionStore } from '@/stores/session'
import { downloadRdp, systemOf, systemStyle, type System } from '@/systems'

type Report = Schemas['StatusReport']
type Component = Schemas['StatusComponent']

/** Refresh interval of the tiles while the page is open. */
const REFRESH_MS = 30_000

const session = useSessionStore()
const i18n = useI18n()
const { t, te, d, n } = i18n

const report = ref<Report | null>(null)
const error = ref('')
const loading = ref(false)
let timer: ReturnType<typeof setInterval> | undefined

async function load() {
  loading.value = true
  try {
    report.value = await unwrap(api.GET('/api/v1/status'))
    error.value = ''
  } catch (e) {
    error.value = errorMessage(i18n, e)
  } finally {
    loading.value = false
  }
}

// All tiles: dcdash itself first, then the modules (by kind), each in configuration order.
const tiles = computed(() => {
  const all = [...(report.value?.components ?? [])]
  const rank = (k: string) => (k === 'system' ? '' : k)
  return all
    .map((c, i) => ({ c, i }))
    .sort((a, b) => rank(a.c.kind).localeCompare(rank(b.c.kind)) || a.i - b.i)
    .map(({ c }) => ({ ...c, system: systemOf(c) }))
})

/** Name of the kind of system on a tile, so that the color is not the only cue. */
function systemLabel(c: { kind: string; system: System | null }) {
  return c.system ? t(`home.systems.${c.system}`) : label(`home.kinds.${c.kind}`, c.kind)
}

const failing = computed(
  () => report.value?.components.filter((c) => c.status !== 'ok').length ?? 0,
)

function label(key: string, fallback: string) {
  return te(key) ? t(key) : fallback
}

// Parameters that hold times (RFC 3339) are shown in the user's locale.
const TIME_PARAMS = new Set(['until', 'at'])

/**
 * The detail of a component in the user's language: the translation of
 * detailCode if there is one, otherwise the English detail from the server
 * (e.g. an error message of a backend). A "count" parameter selects the plural.
 */
function detailText(c: Component): string {
  const key = `home.details.${c.detailCode}`
  if (!c.detailCode || !te(key)) return c.detail || t('home.noDetail')
  const params: Record<string, string> = {}
  for (const [k, v] of Object.entries(c.detailParams ?? {})) {
    params[k] = TIME_PARAMS.has(k) ? d(new Date(v), 'short') : v
  }
  return params.count === undefined ? t(key, params) : t(key, params, Number(params.count))
}

onMounted(() => {
  void load()
  timer = setInterval(() => {
    if (document.visibilityState !== 'hidden') void load()
  }, REFRESH_MS)
})
onUnmounted(() => clearInterval(timer))
</script>

<template>
  <div class="flex flex-col gap-6">
    <div class="flex flex-wrap items-end justify-between gap-4">
      <div>
        <h1 class="text-2xl font-semibold">
          {{
            t('home.welcome', {
              name: session.info?.user.displayName || session.info?.user.username,
            })
          }}
        </h1>
        <p class="text-sm text-fg-muted" role="status">
          <template v-if="report">
            {{ failing === 0 ? t('home.allOk') : t('home.failing', { n: failing }, failing) }}
            · {{ t('home.checkedAt', { time: d(new Date(report.checkedAt), 'time') }) }}
          </template>
        </p>
      </div>
      <IconButton
        icon="refresh"
        :label="t('home.refresh')"
        variant="secondary"
        size="sm"
        :loading="loading"
        @click="load"
      />
    </div>

    <p
      v-if="error"
      role="alert"
      class="rounded-md border border-danger px-3 py-2 text-sm text-danger"
    >
      {{ error }}
    </p>

    <ul
      class="grid grid-cols-1 gap-3 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-4 xl:grid-cols-6"
      :aria-label="t('home.tiles')"
    >
      <li
        v-for="c in tiles"
        :key="`${c.kind}/${c.name}`"
        class="flex min-w-0 flex-col rounded-lg border p-3"
        :class="[
          c.system ? 'sys-tint' : 'bg-surface-raised',
          c.status === 'ok' ? (c.system ? '' : 'border-border') : 'border-2 border-danger',
        ]"
        :style="systemStyle(c.system)"
        :data-system="c.system ?? 'dcdash'"
      >
        <!-- Heading, links and status in one line; the links wrap below a long name. -->
        <div class="mb-1 flex flex-wrap items-center justify-between gap-x-2 gap-y-1">
          <h2 class="min-w-0 font-medium break-words" :class="{ 'sys-text': c.system }">
            {{ label(`home.components.${c.name}`, c.name) }}
          </h2>
          <div class="ml-auto flex shrink-0 items-center gap-1">
            <a
              v-if="c.links?.ui"
              :href="c.links.ui"
              target="_blank"
              rel="noopener noreferrer"
              class="link-button"
              :title="t('home.links.uiLabel', { name: c.name })"
              >{{ t('home.links.ui')
              }}<span class="sr-only">: {{ t('home.links.uiLabel', { name: c.name }) }}</span></a
            >
            <a
              v-if="c.links?.ssh"
              :href="c.links.ssh"
              class="link-button"
              :title="t('home.links.sshLabel', { name: c.name })"
              >{{ t('home.links.ssh')
              }}<span class="sr-only">: {{ t('home.links.sshLabel', { name: c.name }) }}</span></a
            >
            <button
              v-if="c.links?.rdp"
              type="button"
              class="link-button"
              :title="t('home.links.rdpLabel', { name: c.name })"
              @click="downloadRdp(c.name, c.links.rdp)"
            >
              {{ t('home.links.rdp')
              }}<span class="sr-only">: {{ t('home.links.rdpLabel', { name: c.name }) }}</span>
            </button>
            <StatusBadge
              :status="c.status === 'ok' ? 'ok' : 'error'"
              :label="c.status === 'ok' ? t('home.ok') : t('home.error')"
            />
          </div>
        </div>
        <p class="text-sm break-words" :class="c.status === 'ok' ? 'text-fg-muted' : 'text-danger'">
          {{ detailText(c) }}
        </p>
        <p class="mt-auto pt-2 text-xs text-fg-muted">
          {{ systemLabel(c) }} · {{ t('home.latency', { ms: n(c.latencyMs, 'integer') }) }}
        </p>
      </li>
    </ul>
  </div>
</template>
