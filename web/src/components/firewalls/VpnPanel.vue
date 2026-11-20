<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { api, unwrap, type Schemas } from '@/api/client'
import { errorMessage } from '@/api/errors'
import BaseButton from '@/components/ui/BaseButton.vue'
import DataTable from '@/components/ui/DataTable.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import { ageSeconds, formatAge, formatBytes, type Peer, type Tunnel } from '@/firewalls/firewalls'
import { useToastStore } from '@/stores/toast'

/** WireGuard peers (enable, disable) and IPsec tunnels (restart). */
const props = defineProps<{ firewall: string }>()

const i18n = useI18n()
const { t } = i18n
const toast = useToastStore()

const vpn = ref<Schemas['FirewallVPN'] | null>(null)
const loading = ref(false)
const error = ref('')
const busy = ref('')

const peerColumns = computed(() => [
  { key: 'name', label: t('firewalls.vpn.peer') },
  { key: 'status', label: t('firewalls.health.status') },
  { key: 'handshake', label: t('firewalls.vpn.handshake') },
  { key: 'traffic', label: t('firewalls.vpn.traffic') },
  { key: 'allowedIps', label: t('firewalls.vpn.allowedIps') },
  { key: 'actions', label: t('firewalls.actions'), class: 'text-right' },
])
const tunnelColumns = computed(() => [
  { key: 'name', label: t('firewalls.vpn.tunnel') },
  { key: 'status', label: t('firewalls.health.status') },
  { key: 'remoteAddrs', label: t('firewalls.vpn.remote') },
  { key: 'children', label: t('firewalls.vpn.children') },
  { key: 'actions', label: t('firewalls.actions'), class: 'text-right' },
])

function peerState(p: Peer): 'ok' | 'warning' | 'error' | 'unknown' {
  if (!p.enabled) return 'unknown'
  return p.status === 'online' ? 'ok' : p.status === 'stale' ? 'warning' : 'error'
}
function peerLabel(p: Peer): string {
  if (!p.enabled) return t('firewalls.vpn.disabled')
  return t(`firewalls.vpn.states.${p.status || 'offline'}`)
}
function handshake(p: Peer): string {
  const s = ageSeconds(p.latestHandshake)
  return s === null ? t('firewalls.vpn.never') : t('firewalls.vpn.ago', { age: formatAge(s) })
}

async function load() {
  loading.value = true
  try {
    vpn.value = await unwrap(
      api.GET('/api/v1/firewalls/{firewall}/vpn', {
        params: { path: { firewall: props.firewall } },
      }),
    )
    error.value = ''
  } catch (e) {
    vpn.value = null
    error.value = errorMessage(i18n, e)
  } finally {
    loading.value = false
  }
}

async function setPeer(p: Peer, enabled: boolean) {
  busy.value = p.uuid
  try {
    await unwrap(
      api.PUT('/api/v1/firewalls/{firewall}/wireguard/{uuid}', {
        params: { path: { firewall: props.firewall, uuid: p.uuid } },
        body: { enabled },
      }),
    )
    toast.success(
      t(enabled ? 'firewalls.vpn.enabledPeer' : 'firewalls.vpn.disabledPeer', { peer: p.name }),
    )
    await load()
  } catch (e) {
    toast.error(errorMessage(i18n, e))
  } finally {
    busy.value = ''
  }
}

async function restart(tn: Tunnel) {
  busy.value = tn.name
  try {
    await unwrap(
      api.POST('/api/v1/firewalls/{firewall}/ipsec/{name}/restart', {
        params: { path: { firewall: props.firewall, name: tn.name } },
      }),
    )
    toast.success(t('firewalls.vpn.restarted', { tunnel: tn.description || tn.name }))
    // Connecting is asynchronous: reload after a moment.
    setTimeout(() => void load(), 3000)
  } catch (e) {
    toast.error(errorMessage(i18n, e))
  } finally {
    busy.value = ''
  }
}

watch(() => props.firewall, load)
onMounted(load)
</script>

<template>
  <div class="flex flex-col gap-4">
    <div>
      <BaseButton variant="secondary" :loading="loading" @click="load">
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
    <template v-if="vpn">
      <h2 class="text-lg font-semibold">WireGuard</h2>
      <p v-if="vpn.errors.wireguard" class="text-sm text-warning">
        {{ t('firewalls.vpn.partError', { error: vpn.errors.wireguard }) }}
      </p>
      <DataTable
        :columns="peerColumns"
        :rows="vpn.peers"
        :row-key="(p: Peer) => p.uuid"
        caption="WireGuard"
        :empty="t('firewalls.vpn.noPeers')"
      >
        <template #cell-status="{ row }">
          <StatusBadge :status="peerState(row as Peer)" :label="peerLabel(row as Peer)" />
        </template>
        <template #cell-handshake="{ row }">{{ handshake(row as Peer) }}</template>
        <template #cell-traffic="{ row }">
          <span class="text-xs">
            ↓ {{ formatBytes((row as Peer).rxBytes) }} · ↑ {{ formatBytes((row as Peer).txBytes) }}
          </span>
        </template>
        <template #cell-allowedIps="{ row }">
          <span class="font-mono text-xs">{{ (row as Peer).allowedIps }}</span>
        </template>
        <template #cell-actions="{ row }">
          <div class="flex justify-end">
            <BaseButton
              v-if="(row as Peer).canOperate"
              size="sm"
              :variant="(row as Peer).enabled ? 'danger' : 'secondary'"
              :loading="busy === (row as Peer).uuid"
              :aria-label="
                t(
                  (row as Peer).enabled
                    ? 'firewalls.vpn.disableLabel'
                    : 'firewalls.vpn.enableLabel',
                  {
                    peer: (row as Peer).name,
                  },
                )
              "
              @click="setPeer(row as Peer, !(row as Peer).enabled)"
            >
              {{ (row as Peer).enabled ? t('firewalls.vpn.disable') : t('firewalls.vpn.enable') }}
            </BaseButton>
          </div>
        </template>
      </DataTable>
      <h2 class="text-lg font-semibold">IPsec</h2>
      <p v-if="vpn.errors.ipsec" class="text-sm text-warning">
        {{ t('firewalls.vpn.partError', { error: vpn.errors.ipsec }) }}
      </p>
      <DataTable
        :columns="tunnelColumns"
        :rows="vpn.tunnels"
        :row-key="(tn: Tunnel) => tn.name"
        caption="IPsec"
        :empty="t('firewalls.vpn.noTunnels')"
      >
        <template #cell-name="{ row }">
          <span class="font-medium">{{ (row as Tunnel).description || (row as Tunnel).name }}</span>
          <div class="font-mono text-xs text-fg-muted">{{ (row as Tunnel).name }}</div>
        </template>
        <template #cell-status="{ row }">
          <StatusBadge
            :status="(row as Tunnel).connected ? 'ok' : 'error'"
            :label="
              (row as Tunnel).connected
                ? t('firewalls.vpn.connected')
                : t('firewalls.vpn.notConnected')
            "
          />
        </template>
        <template #cell-children="{ row }">
          <div v-for="c in (row as Tunnel).children" :key="c.name" class="text-xs">
            <span class="font-mono">{{ c.localTs }} ⇄ {{ c.remoteTs }}</span>
            <span class="text-fg-muted"> {{ c.state || t('firewalls.vpn.notInstalled') }}</span>
          </div>
        </template>
        <template #cell-actions="{ row }">
          <div class="flex justify-end">
            <BaseButton
              v-if="(row as Tunnel).canOperate"
              size="sm"
              variant="secondary"
              :loading="busy === (row as Tunnel).name"
              :aria-label="t('firewalls.vpn.restartLabel', { tunnel: (row as Tunnel).name })"
              @click="restart(row as Tunnel)"
            >
              {{ t('firewalls.vpn.restart') }}
            </BaseButton>
          </div>
        </template>
      </DataTable>
    </template>
  </div>
</template>
