<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { api, unwrap, type Schemas } from '@/api/client'
import { errorMessage } from '@/api/errors'
import BaseButton from '@/components/ui/BaseButton.vue'
import IconButton from '@/components/ui/IconButton.vue'
import BaseModal from '@/components/ui/BaseModal.vue'
import DataTable from '@/components/ui/DataTable.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import type { FirewallService, Gateway } from '@/firewalls/firewalls'
import { useToastStore } from '@/stores/toast'

/** Gateways and services of a firewall; services can be restarted. */
const props = defineProps<{ firewall: string }>()

const i18n = useI18n()
const { t } = i18n
const toast = useToastStore()

const health = ref<Schemas['FirewallHealth'] | null>(null)
const loading = ref(false)
const error = ref('')
const restarting = ref<FirewallService | null>(null)
const confirmOpen = ref(false)
const busy = ref(false)

const gwColumns = computed(() => [
  { key: 'name', label: t('firewalls.health.gateway') },
  { key: 'address', label: t('firewalls.health.address') },
  { key: 'status', label: t('firewalls.health.status') },
  { key: 'quality', label: t('firewalls.health.quality') },
])
const svcColumns = computed(() => [
  { key: 'name', label: t('firewalls.health.service') },
  { key: 'description', label: t('firewalls.health.description') },
  { key: 'running', label: t('firewalls.health.status') },
  { key: 'actions', label: t('firewalls.actions'), class: 'text-right' },
])
const badge = { online: 'ok', degraded: 'warning', down: 'error' } as const

async function load() {
  loading.value = true
  try {
    health.value = await unwrap(
      api.GET('/api/v1/firewalls/{firewall}/health', {
        params: { path: { firewall: props.firewall } },
      }),
    )
    error.value = ''
  } catch (e) {
    health.value = null
    error.value = errorMessage(i18n, e)
  } finally {
    loading.value = false
  }
}

function askRestart(s: FirewallService) {
  restarting.value = s
  confirmOpen.value = true
}

async function restart() {
  const s = restarting.value!
  busy.value = true
  try {
    await unwrap(
      api.POST('/api/v1/firewalls/{firewall}/services/restart', {
        params: { path: { firewall: props.firewall } },
        body: { id: s.id },
      }),
    )
    toast.success(t('firewalls.health.restarted', { service: s.name }))
    confirmOpen.value = false
    await load()
  } catch (e) {
    toast.error(errorMessage(i18n, e))
  } finally {
    busy.value = false
  }
}

watch(() => props.firewall, load)
onMounted(load)
defineExpose({ load })
</script>

<template>
  <div class="flex flex-col gap-4">
    <p
      v-if="error"
      role="alert"
      class="rounded-md border border-danger px-3 py-2 text-sm text-danger"
    >
      {{ error }}
    </p>
    <template v-if="health">
      <h2 class="text-lg font-semibold">{{ t('firewalls.health.gateways') }}</h2>
      <DataTable
        system="firewall"
        :columns="gwColumns"
        :rows="health.gateways"
        :row-key="(g: Gateway) => g.name"
        :caption="t('firewalls.health.gateways')"
      >
        <template #cell-status="{ row }">
          <StatusBadge
            :status="badge[(row as Gateway).status]"
            :label="t(`firewalls.gatewayStates.${(row as Gateway).status}`)"
          />
        </template>
        <template #cell-quality="{ row }">
          <span v-if="(row as Gateway).delay" class="text-xs">
            {{ (row as Gateway).delay }} · {{ (row as Gateway).loss }}
          </span>
          <span v-else class="text-xs text-fg-muted">{{ t('firewalls.health.notMonitored') }}</span>
        </template>
      </DataTable>
      <h2 class="text-lg font-semibold">{{ t('firewalls.health.services') }}</h2>
      <DataTable
        system="firewall"
        :columns="svcColumns"
        :rows="health.services"
        :row-key="(s: FirewallService) => s.id"
        :caption="t('firewalls.health.services')"
      >
        <template #cell-running="{ row }">
          <StatusBadge
            :status="(row as FirewallService).running ? 'ok' : 'error'"
            :label="
              (row as FirewallService).running
                ? t('firewalls.health.running')
                : t('firewalls.health.stopped')
            "
          />
        </template>
        <template #cell-actions="{ row }">
          <div class="flex justify-end">
            <IconButton
              icon="reboot"
              v-if="health.canOperate"
              size="sm"
              variant="secondary"
              :label="
                t('firewalls.health.restartLabel', { service: (row as FirewallService).name })
              "
              @click="askRestart(row as FirewallService)"
            />
          </div>
        </template>
      </DataTable>
    </template>
    <BaseModal
      v-model:open="confirmOpen"
      :title="t('firewalls.health.restartTitle', { service: restarting?.name ?? '' })"
    >
      <p>{{ t('firewalls.health.restartText', { firewall: props.firewall }) }}</p>
      <template #footer>
        <BaseButton variant="secondary" @click="confirmOpen = false">
          {{ t('common.cancel') }}
        </BaseButton>
        <BaseButton variant="primary" :loading="busy" @click="restart">
          {{ t('firewalls.health.restart') }}
        </BaseButton>
      </template>
    </BaseModal>
  </div>
</template>
