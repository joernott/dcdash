<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, onMounted, ref, useTemplateRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { api, unwrap, type Schemas } from '@/api/client'
import { errorMessage } from '@/api/errors'
import VlanDialog from '@/components/switches/VlanDialog.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import BaseModal from '@/components/ui/BaseModal.vue'
import DataTable from '@/components/ui/DataTable.vue'
import { netboxFailure } from '@/netbox/netbox'
import { useToastStore } from '@/stores/toast'
import type { SwitchVlan } from '@/switches/switches'

const props = defineProps<{ switchName: string; switches: string[] }>()

const i18n = useI18n()
const { t } = i18n
const toast = useToastStore()

const list = ref<Schemas['SwitchVLANList'] | null>(null)
const loading = ref(false)
const error = ref('')
const dialog = useTemplateRef<InstanceType<typeof VlanDialog>>('dialog')
const deleting = ref<SwitchVlan | null>(null)
const deleteOpen = ref(false)
const deleteBusy = ref(false)
const deleteError = ref('')

const columns = computed(() => [
  { key: 'id', label: t('switches.vlan.id') },
  { key: 'name', label: t('switches.vlan.name') },
  { key: 'usage', label: t('switches.vlan.usedBy') },
  { key: 'actions', label: t('switches.columns.actions'), class: 'text-right' },
])
const canCreate = computed(() => list.value?.canCreate ?? false)

function usage(v: SwitchVlan): string[] {
  return list.value?.usage[String(v.id)] ?? []
}

async function load() {
  loading.value = true
  try {
    list.value = await unwrap(
      api.GET('/api/v1/switches/{switch}/vlans', {
        params: { path: { switch: props.switchName } },
      }),
    )
    error.value = ''
  } catch (e) {
    list.value = null
    error.value = errorMessage(i18n, e)
  } finally {
    loading.value = false
  }
}

function askDelete(v: SwitchVlan) {
  deleting.value = v
  deleteError.value = ''
  deleteOpen.value = true
}

async function remove() {
  const v = deleting.value!
  deleteBusy.value = true
  try {
    const res = await unwrap(
      api.DELETE('/api/v1/switches/{switch}/vlans/{id}', {
        params: { path: { switch: props.switchName, id: v.id } },
      }),
    )
    deleteOpen.value = false
    if (res.saved) toast.success(t('switches.vlan.deleted', { id: v.id }))
    else toast.error(t('switches.notSaved', { error: res.saveError ?? '' }))
    const nb = netboxFailure(res.netbox, t)
    if (nb) toast.error(nb)
    await load()
  } catch (e) {
    deleteError.value = errorMessage(i18n, e)
  } finally {
    deleteBusy.value = false
  }
}

watch(() => props.switchName, load)
onMounted(load)
</script>

<template>
  <div class="flex flex-col gap-3">
    <div class="flex flex-wrap gap-3">
      <BaseButton v-if="canCreate" @click="dialog?.create()">{{
        t('switches.vlan.create')
      }}</BaseButton>
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
    <DataTable
      :columns="columns"
      :rows="list?.vlans ?? []"
      :row-key="(v: SwitchVlan) => v.id"
      :caption="t('switches.tabs.vlans')"
      :loading="loading && !list"
    >
      <template #cell-usage="{ row }">
        <span class="font-mono text-xs">{{ usage(row as SwitchVlan).join(', ') }}</span>
      </template>
      <template #cell-actions="{ row }">
        <div v-if="(row as SwitchVlan).canWrite" class="flex justify-end gap-2">
          <BaseButton
            size="sm"
            variant="secondary"
            :aria-label="t('switches.vlan.renameLabel', { id: (row as SwitchVlan).id })"
            @click="dialog?.rename(row as SwitchVlan)"
          >
            {{ t('switches.vlan.rename') }}
          </BaseButton>
          <BaseButton
            v-if="(row as SwitchVlan).id !== 1 && usage(row as SwitchVlan).length === 0"
            size="sm"
            variant="danger"
            :aria-label="t('switches.vlan.deleteLabel', { id: (row as SwitchVlan).id })"
            @click="askDelete(row as SwitchVlan)"
          >
            {{ t('switches.vlan.delete') }}
          </BaseButton>
        </div>
      </template>
    </DataTable>
    <VlanDialog
      ref="dialog"
      :switch-name="props.switchName"
      :switches="props.switches"
      @saved="load"
    />
    <BaseModal
      v-model:open="deleteOpen"
      :title="t('switches.vlan.deleteTitle', { id: deleting?.id ?? 0, name: deleting?.name ?? '' })"
    >
      <p>{{ t('switches.vlan.deleteText', { switch: props.switchName }) }}</p>
      <p v-if="deleteError" role="alert" class="mt-2 text-sm text-danger">{{ deleteError }}</p>
      <template #footer>
        <BaseButton variant="secondary" @click="deleteOpen = false">{{
          t('common.cancel')
        }}</BaseButton>
        <BaseButton variant="danger" :loading="deleteBusy" @click="remove">
          {{ t('switches.vlan.delete') }}
        </BaseButton>
      </template>
    </BaseModal>
  </div>
</template>
