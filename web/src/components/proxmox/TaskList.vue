<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import StatusBadge from '@/components/ui/StatusBadge.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import { useProxmoxTasksStore, type TaskState } from '@/stores/proxmoxTasks'

/** The tasks started in this browser tab, newest first, with their logs. */
const props = defineProps<{ cluster: string }>()
const { t, d } = useI18n()
const store = useProxmoxTasksStore()

const tasks = computed(() => store.tasks.filter((x) => x.cluster === props.cluster))
const badge: Record<TaskState, 'ok' | 'warning' | 'error' | 'unknown'> = {
  running: 'warning',
  ok: 'ok',
  failed: 'error',
  unknown: 'unknown',
}
</script>

<template>
  <section v-if="tasks.length > 0" aria-labelledby="proxmox-tasks" class="flex flex-col gap-3">
    <div class="flex items-center justify-between gap-3">
      <h2 id="proxmox-tasks" class="text-lg font-semibold">{{ t('proxmox.tasks') }}</h2>
      <BaseButton size="sm" variant="ghost" @click="store.clear()">
        {{ t('proxmox.clearTasks') }}
      </BaseButton>
    </div>
    <ul class="flex flex-col gap-2">
      <li
        v-for="task in tasks"
        :key="task.upid"
        class="rounded-md border border-border bg-surface-raised p-3 text-sm"
      >
        <div class="flex flex-wrap items-center gap-2">
          <StatusBadge :status="badge[task.state]" :label="t(`proxmox.taskStates.${task.state}`)" />
          <span class="font-medium">{{ task.label }}</span>
          <span class="text-fg-muted">{{ d(task.startedAt, 'time') }}</span>
        </div>
        <p v-if="task.message" class="mt-1 text-danger">{{ task.message }}</p>
        <details v-if="task.log.length > 0" class="mt-1">
          <summary class="cursor-pointer text-fg-muted">{{ t('proxmox.taskLog') }}</summary>
          <pre
            class="mt-1 max-h-48 overflow-auto rounded bg-surface-sunken p-2 font-mono text-xs"
            >{{ task.log.join('\n') }}</pre>
        </details>
      </li>
    </ul>
  </section>
</template>
