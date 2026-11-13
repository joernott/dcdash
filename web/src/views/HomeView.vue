<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { api, unwrap, type Schemas } from '@/api/client'
import { errorMessage } from '@/api/errors'
import BaseButton from '@/components/ui/BaseButton.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import { useSessionStore } from '@/stores/session'

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

// Tiles grouped by kind: dcdash itself first, then the modules.
const groups = computed(() => {
  const byKind = new Map<string, Component[]>()
  for (const c of report.value?.components ?? []) {
    byKind.set(c.kind, [...(byKind.get(c.kind) ?? []), c])
  }
  return [...byKind.entries()].sort(([a], [b]) =>
    a === 'system' ? -1 : b === 'system' ? 1 : a.localeCompare(b),
  )
})

const failing = computed(
  () => report.value?.components.filter((c) => c.status !== 'ok').length ?? 0,
)

function label(key: string, fallback: string) {
  return te(key) ? t(key) : fallback
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
      <BaseButton variant="secondary" size="sm" :loading="loading" @click="load">
        {{ t('home.refresh') }}
      </BaseButton>
    </div>

    <p
      v-if="error"
      role="alert"
      class="rounded-md border border-danger px-3 py-2 text-sm text-danger"
    >
      {{ error }}
    </p>

    <section v-for="[kind, components] in groups" :key="kind" :aria-labelledby="`kind-${kind}`">
      <h2 :id="`kind-${kind}`" class="mb-3 text-lg font-semibold">
        {{ label(`home.kinds.${kind}`, kind) }}
      </h2>
      <ul class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        <li
          v-for="c in components"
          :key="c.name"
          class="rounded-lg border bg-surface-raised p-4"
          :class="c.status === 'ok' ? 'border-border' : 'border-danger'"
        >
          <div class="mb-2 flex items-center justify-between gap-2">
            <h3 class="font-medium">{{ label(`home.components.${c.name}`, c.name) }}</h3>
            <StatusBadge
              :status="c.status === 'ok' ? 'ok' : 'error'"
              :label="c.status === 'ok' ? t('home.ok') : t('home.error')"
            />
          </div>
          <p
            class="text-sm break-words"
            :class="c.status === 'ok' ? 'text-fg-muted' : 'text-danger'"
          >
            {{ c.detail || t('home.noDetail') }}
          </p>
          <p class="mt-2 text-xs text-fg-muted">
            {{ t('home.latency', { ms: n(c.latencyMs, 'integer') }) }}
          </p>
        </li>
      </ul>
    </section>
  </div>
</template>
