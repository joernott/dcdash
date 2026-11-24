<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { api, unwrap, type Schemas } from '@/api/client'
import { errorMessage } from '@/api/errors'
import GroupsPanel from '@/components/directories/GroupsPanel.vue'
import UsersPanel from '@/components/directories/UsersPanel.vue'
import BaseSelect from '@/components/ui/BaseSelect.vue'
import IconButton from '@/components/ui/IconButton.vue'
import { systemStyle } from '@/systems'

type Directory = Schemas['Directory']

const i18n = useI18n()
const { t } = i18n
const route = useRoute()
const router = useRouter()

type Tab = 'users' | 'groups'
const TABS: Tab[] = ['users', 'groups']

const directories = ref<Directory[]>([])
const loading = ref(false)
const error = ref('')

const name = computed(() => String(route.params.directory ?? ''))
const current = computed(() => directories.value.find((d) => d.name === name.value))
const tab = computed<Tab>(() => (route.query.tab === 'groups' ? 'groups' : 'users'))
const options = computed(() =>
  directories.value.map((d) => ({ value: d.name, label: `${d.name} (${d.domain})` })),
)

async function load() {
  loading.value = true
  try {
    directories.value = (await unwrap(api.GET('/api/v1/directories'))).directories
    error.value = ''
    if (!name.value && directories.value.length > 0) {
      await router.replace({
        name: 'directories',
        params: { directory: directories.value[0]!.name },
        query: route.query,
      })
    }
  } catch (e) {
    error.value = errorMessage(i18n, e)
  } finally {
    loading.value = false
  }
}

function select(next: string) {
  if (next !== name.value)
    void router.push({ name: 'directories', params: { directory: next }, query: route.query })
}

function selectTab(next: Tab) {
  void router.replace({ query: { tab: next } })
}

onMounted(load)
</script>

<template>
  <div class="flex flex-col gap-6">
    <div class="flex flex-col gap-3">
      <h1 class="text-2xl font-semibold">{{ t('nav.directories') }}</h1>
      <div class="flex flex-wrap items-end gap-3">
        <BaseSelect
          v-if="directories.length > 0"
          :model-value="name"
          :label="t('directories.directory')"
          :options="options"
          @update:model-value="select"
        />
        <IconButton
          icon="refresh"
          :label="t('home.refresh')"
          size="md"
          variant="secondary"
          :loading="loading"
          @click="load"
        />
      </div>
    </div>

    <p
      v-if="error"
      role="alert"
      class="rounded-md border border-danger px-3 py-2 text-sm text-danger"
    >
      {{ error }}
    </p>
    <p v-else-if="directories.length === 0 && !loading" class="text-fg-muted">
      {{ t('directories.none') }}
    </p>

    <template v-if="current">
      <section
        aria-labelledby="directory-info"
        class="sys-tint flex flex-col gap-1 rounded-md border p-4"
        :style="systemStyle('directory')"
      >
        <h2 id="directory-info" class="sys-text text-lg font-semibold">{{ current.domain }}</h2>
        <p class="text-sm text-fg-muted">
          {{ t(`directories.types.${current.type}`) }} ·
          {{ t('directories.managed', { ous: current.managedOUs.join('; ') }) }}
        </p>
      </section>

      <div
        role="tablist"
        :aria-label="t('directories.views')"
        class="flex gap-1 border-b border-border"
      >
        <button
          v-for="x in TABS"
          :id="`dir-tab-${x}`"
          :key="x"
          type="button"
          role="tab"
          :aria-selected="tab === x"
          :aria-controls="`dir-panel-${x}`"
          class="-mb-px rounded-t-md border px-3 py-2 text-sm font-medium focus-visible:outline-2 focus-visible:outline-focus"
          :class="
            tab === x
              ? 'border-border border-b-surface bg-surface text-fg'
              : 'border-transparent text-fg-muted hover:text-fg'
          "
          @click="selectTab(x)"
        >
          {{ t(`directories.tabs.${x}`) }}
        </button>
      </div>
      <div :id="`dir-panel-${tab}`" role="tabpanel" :aria-labelledby="`dir-tab-${tab}`">
        <UsersPanel v-if="tab === 'users'" :directory="current" />
        <GroupsPanel v-else :directory="current" />
      </div>
    </template>
  </div>
</template>
