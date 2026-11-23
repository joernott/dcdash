<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import AppIcon from '@/components/ui/AppIcon.vue'
import IconButton from '@/components/ui/IconButton.vue'
import type { IconName } from '@/components/ui/icons'
import { availableActions, guestPermission, type Guest, type PowerAction } from '@/proxmox/guests'
import { useProxmoxClustersStore } from '@/stores/proxmoxClusters'
import { useSessionStore } from '@/stores/session'

/** Buttons for the power actions and the console the user may use on a guest. */
const props = withDefaults(
  defineProps<{
    cluster: string
    guest: Pick<Guest, 'type' | 'vmid' | 'name' | 'status' | 'template' | 'lock'>
    size?: 'sm' | 'md'
  }>(),
  { size: 'sm' },
)
const emit = defineEmits<{ action: [action: PowerAction] }>()

const { t } = useI18n()
const session = useSessionStore()
const clusters = useProxmoxClustersStore()

// Start and resume: green power icon; shut down: red power icon.
const ACTION_ICONS: Record<PowerAction, { icon: IconName; tone?: 'success' | 'danger' }> = {
  start: { icon: 'power', tone: 'success' },
  resume: { icon: 'power', tone: 'success' },
  shutdown: { icon: 'power', tone: 'danger' },
  reboot: { icon: 'reboot' },
  suspend: { icon: 'suspend' },
  stop: { icon: 'stop' },
}

const actions = computed(() =>
  session.can(guestPermission(props.cluster, props.guest.type, props.guest.vmid, 'operate'))
    ? availableActions(props.guest)
    : [],
)
const canConsole = computed(
  () =>
    props.guest.status === 'running' &&
    // Terminals need a console user; VNC works with the API token.
    (props.guest.type === 'qemu' || clusters.terminals(props.cluster)) &&
    session.can(guestPermission(props.cluster, props.guest.type, props.guest.vmid, 'console')),
)
</script>

<template>
  <div
    class="flex gap-1.5"
    :class="{ 'flex-wrap': props.size !== 'sm' }"
    role="group"
    :aria-label="t('proxmox.actionsFor', { name: props.guest.name })"
  >
    <IconButton
      v-for="a in actions"
      :key="a"
      :icon="ACTION_ICONS[a].icon"
      :tone="ACTION_ICONS[a].tone"
      :label="t(`proxmox.actions.${a}`)"
      :size="props.size"
      @click="emit('action', a)"
    />
    <RouterLink
      v-if="canConsole"
      :to="{
        name: 'proxmox-console',
        params: { cluster: props.cluster, type: props.guest.type, vmid: props.guest.vmid },
      }"
      class="inline-flex items-center rounded-md border border-border bg-surface-raised font-medium text-fg hover:bg-surface-sunken"
      :class="props.size === 'sm' ? 'px-2.5 py-1.5 text-sm' : 'px-4 py-2'"
      :aria-label="t('proxmox.console')"
      :title="t('proxmox.console')"
    >
      <AppIcon name="console" />
    </RouterLink>
  </div>
</template>
