<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { api, unwrap } from '@/api/client'
import { errorMessage } from '@/api/errors'
import ProjectChips from '@/components/projects/ProjectChips.vue'
import ProjectPicker from '@/components/projects/ProjectPicker.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import IconButton from '@/components/ui/IconButton.vue'
import BaseModal from '@/components/ui/BaseModal.vue'
import { netboxFailure } from '@/netbox/netbox'
import { projectsOfTags, useProjectsStore } from '@/stores/projects'
import { useToastStore } from '@/stores/toast'

/** The projects of a guest (its tags project-<name>), with a dialog to change them. */
const props = defineProps<{
  cluster: string
  type: 'qemu' | 'lxc'
  vmid: number
  tags: string[]
  canWrite: boolean
}>()
const emit = defineEmits<{ changed: [] }>()

const i18n = useI18n()
const { t } = i18n
const store = useProjectsStore()
const toast = useToastStore()

const current = computed(() => projectsOfTags(props.tags))
const open = ref(false)
const selected = ref<string[]>([])
const busy = ref(false)
const error = ref('')
const editable = computed(() => props.canWrite && store.writable.length > 0)

function edit() {
  selected.value = [...current.value]
  error.value = ''
  open.value = true
}

async function save() {
  busy.value = true
  try {
    const res = await unwrap(
      api.PUT('/api/v1/proxmox/clusters/{cluster}/guests/{type}/{vmid}/projects', {
        params: { path: { cluster: props.cluster, type: props.type, vmid: props.vmid } },
        body: { projects: selected.value },
      }),
    )
    open.value = false
    toast.success(t('projects.guestSaved'))
    const nb = netboxFailure(res.netbox, t)
    if (nb) toast.error(nb)
    emit('changed')
  } catch (e) {
    error.value = errorMessage(i18n, e)
  } finally {
    busy.value = false
  }
}

onMounted(() => void store.load())
</script>

<template>
  <div class="flex flex-wrap items-center gap-3">
    <ProjectChips v-if="current.length" :names="current" />
    <span v-else class="text-fg-muted">{{ t('projects.noneAssigned') }}</span>
    <IconButton
      icon="edit"
      :label="t('projects.edit')"
      v-if="editable"
      size="sm"
      variant="secondary"
      @click="edit"
    />
    <BaseModal v-model:open="open" :title="t('projects.editTitle')">
      <form id="guest-projects" class="flex flex-col gap-4" @submit.prevent="save">
        <ProjectPicker v-model="selected" />
        <p v-if="error" role="alert" class="text-sm text-danger">{{ error }}</p>
      </form>
      <template #footer>
        <BaseButton variant="secondary" @click="open = false">{{ t('common.cancel') }}</BaseButton>
        <BaseButton type="submit" form="guest-projects" :loading="busy">
          {{ t('common.save') }}
        </BaseButton>
      </template>
    </BaseModal>
  </div>
</template>
