<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, onMounted, ref, useTemplateRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { api, unwrap, type Schemas } from '@/api/client'
import { errorMessage } from '@/api/errors'
import ProjectChips from '@/components/projects/ProjectChips.vue'
import PortDialog from '@/components/switches/PortDialog.vue'
import PortProjectsDialog from '@/components/switches/PortProjectsDialog.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import BaseInput from '@/components/ui/BaseInput.vue'
import DataTable from '@/components/ui/DataTable.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import { useProjectsStore } from '@/stores/projects'
import {
  formatSpeed,
  portMatches,
  portState,
  portVlans,
  type SwitchPort,
} from '@/switches/switches'

const props = defineProps<{ switchName: string }>()

const i18n = useI18n()
const { t } = i18n
const projects = useProjectsStore()

const list = ref<Schemas['SwitchPortList'] | null>(null)
const loading = ref(false)
const error = ref('')
const filter = ref('')
const dialog = useTemplateRef<InstanceType<typeof PortDialog>>('dialog')
const projectDialog = useTemplateRef<InstanceType<typeof PortProjectsDialog>>('projectDialog')

const withProjects = computed(
  () => projects.projects.length > 0 && (list.value?.ports.some((p) => p.netbox) ?? false),
)
const columns = computed(() => [
  { key: 'name', label: t('switches.columns.port') },
  { key: 'state', label: t('switches.columns.state') },
  { key: 'vlans', label: t('switches.columns.vlans') },
  { key: 'description', label: t('switches.columns.description') },
  { key: 'neighbor', label: t('switches.columns.neighbor') },
  ...(withProjects.value ? [{ key: 'projects', label: t('switches.columns.projects') }] : []),
  { key: 'actions', label: t('switches.columns.actions'), class: 'text-right' },
])
const rows = computed(() => (list.value?.ports ?? []).filter((p) => portMatches(p, filter.value)))

const badge = { up: 'ok', down: 'unknown', disabled: 'warning' } as const

async function load() {
  loading.value = true
  try {
    list.value = await unwrap(
      api.GET('/api/v1/switches/{switch}/ports', {
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

function saved(p: SwitchPort) {
  if (!list.value) return
  const i = list.value.ports.findIndex((x) => x.name === p.name)
  // Keep the NetBox data, which the change response doesn't carry.
  if (i >= 0) list.value.ports[i] = { ...p, netbox: list.value.ports[i]!.netbox }
}

watch(() => props.switchName, load)
onMounted(() => {
  void load()
  void projects.load()
})
defineExpose({ load })
</script>

<template>
  <div class="flex flex-col gap-3">
    <div class="flex flex-wrap items-end gap-3">
      <BaseInput
        v-model="filter"
        :label="t('switches.filter')"
        :placeholder="t('switches.filterHint')"
        inputmode="search"
      />
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
    <p v-if="list?.netboxError" class="text-sm text-warning">
      {{ t('switches.netboxError', { error: list.netboxError }) }}
    </p>
    <DataTable
      :columns="columns"
      :rows="rows"
      :row-key="(p: SwitchPort) => p.name"
      :caption="t('switches.tabs.ports')"
      :loading="loading && !list"
      :empty="t('switches.noPorts')"
    >
      <template #cell-name="{ row }">
        <span class="font-mono">{{ (row as SwitchPort).name }}</span>
        <span v-if="(row as SwitchPort).uplink" class="ml-2 text-xs text-fg-muted">
          {{ t('switches.uplink') }}
        </span>
        <span v-if="(row as SwitchPort).lag" class="ml-2 text-xs text-fg-muted">
          {{ (row as SwitchPort).lag }}
        </span>
      </template>
      <template #cell-state="{ row }">
        <div class="flex flex-col gap-1">
          <StatusBadge
            :status="badge[portState(row as SwitchPort)]"
            :label="t(`switches.states.${portState(row as SwitchPort)}`)"
          />
          <span class="text-xs text-fg-muted">
            {{ formatSpeed((row as SwitchPort).speedMbps) }}
            <template v-if="(row as SwitchPort).poe?.powerDrawnW">
              · PoE {{ (row as SwitchPort).poe!.powerDrawnW }} W
            </template>
          </span>
        </div>
      </template>
      <template #cell-vlans="{ row }">
        <span class="text-xs text-fg-muted">{{
          t(`switches.modes.${(row as SwitchPort).mode}`)
        }}</span>
        <div class="font-mono">{{ portVlans(row as SwitchPort, t('switches.allVlans')) }}</div>
      </template>
      <template #cell-neighbor="{ row }">
        <div
          v-for="n in (row as SwitchPort).neighbors"
          :key="n.chassisId + n.portId"
          class="text-xs"
        >
          <span class="font-medium">{{ n.systemName || n.chassisId }}</span>
          {{ n.portId }}
          <span v-if="n.mgmtAddress" class="text-fg-muted">({{ n.mgmtAddress }})</span>
        </div>
      </template>
      <template #cell-projects="{ row }">
        <div v-if="(row as SwitchPort).netbox" class="flex flex-wrap items-center gap-2">
          <ProjectChips :names="(row as SwitchPort).netbox!.projects" />
          <BaseButton
            v-if="projects.writable.length"
            size="sm"
            variant="ghost"
            :aria-label="t('switches.editProjects', { port: (row as SwitchPort).name })"
            @click="projectDialog?.edit(row as SwitchPort)"
          >
            {{ t('projects.edit') }}
          </BaseButton>
        </div>
      </template>
      <template #cell-actions="{ row }">
        <div class="flex justify-end">
          <BaseButton
            v-if="(row as SwitchPort).canOperate"
            size="sm"
            variant="secondary"
            :aria-label="t('switches.editPort', { port: (row as SwitchPort).name })"
            @click="dialog?.edit(row as SwitchPort)"
          >
            {{ t('switches.edit') }}
          </BaseButton>
        </div>
      </template>
    </DataTable>
    <PortDialog
      ref="dialog"
      :switch-name="props.switchName"
      :vlans="list?.vlans ?? []"
      @saved="saved"
    />
    <PortProjectsDialog ref="projectDialog" @saved="load" />
  </div>
</template>
