<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { api, unwrap } from '@/api/client'
import { errorMessage } from '@/api/errors'
import BaseButton from '@/components/ui/BaseButton.vue'
import BaseModal from '@/components/ui/BaseModal.vue'
import { DANGEROUS_ACTIONS, type Guest, type PowerAction } from '@/proxmox/guests'
import { useProxmoxTasksStore } from '@/stores/proxmoxTasks'
import { useToastStore } from '@/stores/toast'

/**
 * Confirms and runs a power action, then follows its Proxmox task. Open it
 * with ask(); "done" is emitted when the task has finished.
 */
const emit = defineEmits<{ done: [] }>()

const i18n = useI18n()
const { t } = i18n
const toast = useToastStore()
const tasks = useProxmoxTasksStore()

const open = ref(false)
const busy = ref(false)
const cluster = ref('')
const guest = ref<Pick<Guest, 'type' | 'vmid' | 'name'> | null>(null)
const action = ref<PowerAction>('start')

const name = computed(() => (guest.value ? `${guest.value.name} (${guest.value.vmid})` : ''))
const dangerous = computed(() => DANGEROUS_ACTIONS.includes(action.value))

function ask(c: string, g: Pick<Guest, 'type' | 'vmid' | 'name'>, a: PowerAction) {
  cluster.value = c
  guest.value = g
  action.value = a
  open.value = true
}

async function run() {
  const g = guest.value
  if (!g) return
  busy.value = true
  const label = t('proxmox.taskLabel', {
    name: name.value,
    action: t(`proxmox.actions.${action.value}`),
  })
  try {
    const ref = await unwrap(
      api.POST('/api/v1/proxmox/clusters/{cluster}/guests/{type}/{vmid}/actions/{action}', {
        params: {
          path: { cluster: cluster.value, type: g.type, vmid: g.vmid, action: action.value },
        },
      }),
    )
    open.value = false
    toast.info(t('proxmox.taskStarted', { task: label }))
    void tasks.track(cluster.value, ref.upid, label, (task) => {
      if (task.state === 'ok') toast.success(t('proxmox.taskOk', { task: label }))
      else if (task.state === 'failed')
        toast.error(t('proxmox.taskFailed', { task: label, message: task.message }))
      else toast.error(t('proxmox.taskUnknown', { task: label }))
      emit('done')
    })
  } catch (e) {
    toast.error(errorMessage(i18n, e))
  } finally {
    busy.value = false
  }
}

defineExpose({ ask })
</script>

<template>
  <BaseModal v-model:open="open" :title="t(`proxmox.actions.${action}`)">
    <p>{{ t(`proxmox.confirm.${action}`, { name }) }}</p>
    <template #footer>
      <BaseButton variant="ghost" @click="open = false">{{ t('common.cancel') }}</BaseButton>
      <BaseButton :variant="dangerous ? 'danger' : 'primary'" :loading="busy" @click="run">
        {{ t(`proxmox.actions.${action}`) }}
      </BaseButton>
    </template>
  </BaseModal>
</template>
