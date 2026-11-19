<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { api, unwrap } from '@/api/client'
import { errorMessage } from '@/api/errors'
import ProjectPicker from '@/components/projects/ProjectPicker.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import BaseModal from '@/components/ui/BaseModal.vue'
import { useToastStore } from '@/stores/toast'
import type { SwitchPort } from '@/switches/switches'

/**
 * The projects of a switch port: the project tags of its NetBox interface, changed through
 * the project API (type "interface"). Open it with edit().
 */
const emit = defineEmits<{ saved: [] }>()

const i18n = useI18n()
const { t } = i18n
const toast = useToastStore()

const open = ref(false)
const busy = ref(false)
const error = ref('')
const port = ref<SwitchPort | null>(null)
const selected = ref<string[]>([])

function edit(p: SwitchPort) {
  port.value = p
  selected.value = [...(p.netbox?.projects ?? [])]
  error.value = ''
  open.value = true
}

async function save() {
  const p = port.value!
  const id = p.netbox!.id
  const before = p.netbox!.projects
  busy.value = true
  error.value = ''
  try {
    for (const name of selected.value.filter((n) => !before.includes(n))) {
      await unwrap(
        api.POST('/api/v1/projects/{name}/objects', {
          params: { path: { name } },
          body: { type: 'interface', id },
        }),
      )
    }
    for (const name of before.filter((n) => !selected.value.includes(n))) {
      await unwrap(
        api.DELETE('/api/v1/projects/{name}/objects/{type}/{id}', {
          params: { path: { name, type: 'interface', id } },
        }),
      )
    }
    open.value = false
    toast.success(t('switches.projectsSaved', { port: p.name }))
    emit('saved')
  } catch (e) {
    error.value = errorMessage(i18n, e)
    emit('saved')
  } finally {
    busy.value = false
  }
}

defineExpose({ edit })
</script>

<template>
  <BaseModal v-model:open="open" :title="t('switches.projectsTitle', { port: port?.name ?? '' })">
    <form id="port-projects" class="flex flex-col gap-4" @submit.prevent="save">
      <ProjectPicker v-model="selected" />
      <p v-if="error" role="alert" class="text-sm text-danger">{{ error }}</p>
    </form>
    <template #footer>
      <BaseButton variant="secondary" @click="open = false">{{ t('common.cancel') }}</BaseButton>
      <BaseButton type="submit" form="port-projects" :loading="busy">
        {{ t('common.save') }}
      </BaseButton>
    </template>
  </BaseModal>
</template>
