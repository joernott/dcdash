<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'

import { api, unwrap } from '@/api/client'
import { errorMessage } from '@/api/errors'
import BaseButton from '@/components/ui/BaseButton.vue'
import BaseInput from '@/components/ui/BaseInput.vue'
import BaseModal from '@/components/ui/BaseModal.vue'
import type { Guest } from '@/proxmox/guests'
import { useProxmoxTasksStore } from '@/stores/proxmoxTasks'
import { netboxFailure } from '@/netbox/netbox'
import { useToastStore } from '@/stores/toast'

/**
 * Deletes a stopped guest after the user typed its name, then follows the
 * Proxmox task. Open it with ask(); "deleted" is emitted when the task
 * succeeded.
 */
const emit = defineEmits<{ deleted: [] }>()

const i18n = useI18n()
const { t } = i18n
const id = useId()
const toast = useToastStore()
const tasks = useProxmoxTasksStore()

const open = ref(false)
const busy = ref(false)
const cluster = ref('')
const guest = ref<Pick<Guest, 'type' | 'vmid' | 'name'> | null>(null)
const confirm = ref('')
const purge = ref(true)
const error = ref('')

/** What the user types: the name, or the ID of a guest without a name. */
const expected = computed(() => guest.value?.name || String(guest.value?.vmid ?? ''))

function ask(c: string, g: Pick<Guest, 'type' | 'vmid' | 'name'>) {
  cluster.value = c
  guest.value = g
  confirm.value = ''
  purge.value = true
  error.value = ''
  open.value = true
}

async function remove() {
  const g = guest.value
  if (!g || confirm.value.trim() !== expected.value) return
  busy.value = true
  error.value = ''
  const label = t('proxmox.taskLabel', {
    name: `${g.name} (${g.vmid})`,
    action: t('proxmox.delete.action'),
  })
  try {
    const ref = await unwrap(
      api.DELETE('/api/v1/proxmox/clusters/{cluster}/guests/{type}/{vmid}', {
        params: { path: { cluster: cluster.value, type: g.type, vmid: g.vmid } },
        body: { confirm: confirm.value.trim(), purge: purge.value },
      }),
    )
    open.value = false
    toast.info(t('proxmox.taskStarted', { task: label }))
    const nb = netboxFailure(ref.netbox, t)
    if (nb) toast.error(nb)
    void tasks.track(cluster.value, ref.upid, label, (task) => {
      if (task.state === 'ok') {
        toast.success(t('proxmox.delete.done', { name: g.name || g.vmid }))
        emit('deleted')
      } else if (task.state === 'failed') {
        toast.error(t('proxmox.taskFailed', { task: label, message: task.message }))
      } else {
        toast.error(t('proxmox.taskUnknown', { task: label }))
      }
    })
  } catch (e) {
    error.value = errorMessage(i18n, e)
  } finally {
    busy.value = false
  }
}

defineExpose({ ask })
</script>

<template>
  <BaseModal v-model:open="open" :title="t('proxmox.delete.title')">
    <form :id="`${id}-form`" class="flex flex-col gap-3" @submit.prevent="remove">
      <p>{{ t('proxmox.delete.warning', { name: expected, vmid: guest?.vmid }) }}</p>
      <BaseInput
        v-model="confirm"
        :label="t('proxmox.delete.confirm', { name: expected })"
        :error="error"
        autocomplete="off"
      />
      <label class="flex items-start gap-2 text-sm">
        <input v-model="purge" type="checkbox" class="mt-0.5 size-4" />
        <span>
          {{ t('proxmox.delete.purge') }}
          <span class="block text-fg-muted">{{ t('proxmox.delete.purgeHint') }}</span>
        </span>
      </label>
    </form>
    <template #footer>
      <BaseButton variant="secondary" @click="open = false">{{ t('common.cancel') }}</BaseButton>
      <BaseButton
        variant="danger"
        type="submit"
        :form="`${id}-form`"
        :loading="busy"
        :disabled="confirm.trim() !== expected"
      >
        {{ t('proxmox.delete.action') }}
      </BaseButton>
    </template>
  </BaseModal>
</template>
