<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'

import { api, unwrap } from '@/api/client'
import { errorMessage } from '@/api/errors'
import LinkButtons from '@/components/ui/LinkButtons.vue'
import {
  OVERVIEW_COLUMNS,
  overviewLayout,
  portState,
  type SwitchInfo,
  type SwitchPort,
} from '@/switches/switches'
import { systemStyle } from '@/systems'

/**
 * All switches at a glance, laid out like their front panels: per switch one row for the odd
 * and one for the even ports, the SFP ports at the end. Each port shows an arrow (up or down)
 * and its VLAN.
 */
const props = defineProps<{ switches: SwitchInfo[] }>()

const i18n = useI18n()
const { t } = i18n

const ports = ref<Record<string, SwitchPort[]>>({})
const errors = ref<Record<string, string>>({})
const loading = ref(false)

const blocks = computed(() =>
  props.switches.map((s) => ({
    sw: s,
    rows: ports.value[s.name] ? overviewLayout(ports.value[s.name]!) : [],
    error:
      s.status === 'ok'
        ? errors.value[s.name]
        : t('switches.unavailable', { error: s.error ?? '' }),
  })),
)

async function load() {
  loading.value = true
  await Promise.all(
    props.switches
      .filter((s) => s.status === 'ok')
      .map(async (s) => {
        try {
          const res = await unwrap(
            api.GET('/api/v1/switches/{switch}/ports', { params: { path: { switch: s.name } } }),
          )
          ports.value = { ...ports.value, [s.name]: res.ports }
          errors.value = { ...errors.value, [s.name]: '' }
        } catch (e) {
          errors.value = { ...errors.value, [s.name]: errorMessage(i18n, e) }
        }
      }),
  )
  loading.value = false
}

function label(p: SwitchPort): string {
  const state = t(`switches.states.${portState(p)}`)
  const vlan =
    p.mode === 'trunk'
      ? t('switches.overview.trunk', { vlan: p.vlan })
      : t('switches.overview.vlan', { vlan: p.vlan })
  return `${p.name}: ${state}, ${vlan}${p.description ? `, ${p.description}` : ''}`
}

watch(() => props.switches.map((s) => s.name).join(), load)
onMounted(load)

// sshUrl is the ssh:// link of a switch.
function sshUrl(host: string) {
  return `ssh://${host}`
}

defineExpose({ load })
</script>

<template>
  <div class="flex flex-col gap-3">
    <p class="text-sm text-fg-muted">{{ t('switches.overview.hint') }}</p>
    <!-- As wide as the front panels, in the switch colors. -->
    <div
      class="w-fit max-w-full overflow-x-auto rounded-md border border-border"
      :style="systemStyle('switch')"
    >
      <table class="text-center text-xs">
        <caption class="sr-only">
          {{
            t('switches.tabs.overview')
          }}
        </caption>
        <thead class="sr-only">
          <tr>
            <th scope="col">{{ t('switches.switch') }}</th>
            <th v-for="c in OVERVIEW_COLUMNS" :key="c" scope="col">
              {{
                c > 12
                  ? t('switches.overview.sfpColumn', { n: c - 12 })
                  : t('switches.overview.column', { n: c })
              }}
            </th>
          </tr>
        </thead>
        <tbody v-for="b in blocks" :key="b.sw.name" class="border-t border-border first:border-t-0">
          <tr v-if="b.rows.length === 0" class="sys-row">
            <th scope="row" class="px-3 py-2 text-left font-medium">{{ b.sw.name }}</th>
            <td :colspan="OVERVIEW_COLUMNS" class="px-3 py-2 text-left text-danger">
              {{ b.error || t('common.loading') }}
            </td>
          </tr>
          <tr v-for="(row, i) in b.rows" :key="i" class="sys-row">
            <th
              v-if="i === 0"
              scope="rowgroup"
              :rowspan="b.rows.length"
              class="border-r border-border px-3 py-1 text-left align-top font-medium"
            >
              <RouterLink
                :to="{ name: 'switches', params: { switch: b.sw.name } }"
                class="text-base font-semibold text-primary underline"
              >
                {{ b.sw.name }}
              </RouterLink>
              <LinkButtons
                class="mt-1 font-normal"
                :name="b.sw.name"
                :ui="b.sw.url"
                :ssh="sshUrl(b.sw.host)"
              />
            </th>
            <td
              v-for="(p, c) in row"
              :key="c"
              class="min-w-10 px-1 py-0.5"
              :class="c === 12 ? 'border-l border-border' : ''"
            >
              <template v-if="p">
                <span class="sr-only">{{ label(p) }}</span>
                <div aria-hidden="true" :title="label(p)">
                  <span
                    class="block text-base leading-none"
                    :class="portState(p) === 'up' ? 'text-success' : 'text-danger'"
                    >{{ portState(p) === 'up' ? '▲' : '▼' }}</span
                  >
                  <span class="font-mono" :class="p.mode === 'trunk' ? 'italic underline' : ''">
                    {{ p.vlan }}
                  </span>
                </div>
              </template>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <p class="text-xs text-fg-muted">{{ t('switches.overview.legend') }}</p>
  </div>
</template>
