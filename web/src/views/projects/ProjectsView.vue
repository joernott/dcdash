<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'

import { api, unwrap, type Schemas } from '@/api/client'
import { errorMessage } from '@/api/errors'
import DataTable from '@/components/ui/DataTable.vue'

type Project = Schemas['Project']

const i18n = useI18n()
const { t } = i18n
const list = ref<Schemas['ProjectList'] | null>(null)
const error = ref('')
const loading = ref(true)

const columns = computed(() => [
  { key: 'title', label: t('projects.columns.project') },
  { key: 'tag', label: t('projects.columns.tag') },
  { key: 'description', label: t('projects.columns.description') },
])

onMounted(async () => {
  try {
    list.value = await unwrap(api.GET('/api/v1/projects'))
  } catch (e) {
    error.value = errorMessage(i18n, e)
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <div class="flex flex-col gap-6">
    <h1 class="text-2xl font-semibold">{{ t('nav.projects') }}</h1>
    <p v-if="error" role="alert" class="text-danger">{{ error }}</p>
    <DataTable
      :columns="columns"
      :rows="list?.projects ?? []"
      :row-key="(p: Project) => p.name"
      :caption="t('nav.projects')"
      :loading="loading"
      :empty="t('projects.none')"
    >
      <template #cell-title="{ row }">
        <RouterLink
          :to="{ name: 'project', params: { name: (row as Project).name } }"
          class="inline-flex items-center gap-2 text-primary underline"
        >
          <span
            class="size-3 rounded-full"
            aria-hidden="true"
            :style="{ backgroundColor: `#${(row as Project).color}` }"
          />
          {{ (row as Project).title }}
        </RouterLink>
      </template>
      <template #cell-tag="{ row }">
        <span class="font-mono text-xs">{{ (row as Project).tag }}</span>
      </template>
    </DataTable>

    <section v-if="list?.state" aria-labelledby="projects-state" class="flex flex-col gap-2">
      <h2 id="projects-state" class="text-lg font-semibold">{{ t('projects.state') }}</h2>
      <p class="text-sm text-fg-muted">{{ t('projects.stateHint') }}</p>
      <ul class="flex flex-col gap-1 text-sm">
        <li v-for="e in list.state.errors" :key="e" class="text-danger">
          {{ t('projects.invalidFile', { error: e }) }}
        </li>
        <li v-if="list.state.tagError" class="text-danger">
          {{ t('projects.tagError', { error: list.state.tagError }) }}
        </li>
        <li v-for="o in list.state.orphaned" :key="o" class="text-warning">
          {{ t('projects.orphaned', { tag: o }) }}
        </li>
        <li
          v-if="!list.state.errors.length && !list.state.tagError && !list.state.orphaned.length"
          class="text-success"
        >
          {{ t('projects.stateOk') }}
        </li>
      </ul>
    </section>
  </div>
</template>
