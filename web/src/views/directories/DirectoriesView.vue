<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, onMounted, ref, useTemplateRef } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute, useRouter } from 'vue-router'

import { api, unwrap, type Schemas } from '@/api/client'
import { errorMessage } from '@/api/errors'
import GroupsPanel from '@/components/directories/GroupsPanel.vue'
import UsersPanel from '@/components/directories/UsersPanel.vue'
import BaseSelect from '@/components/ui/BaseSelect.vue'
import IconButton from '@/components/ui/IconButton.vue'
import LinkButtons from '@/components/ui/LinkButtons.vue'
import { systemStyle } from '@/systems'

type Directory = Schemas['Directory']

/**
 * /directories: every directory with its domain and managed containers; the field filters them.
 * /directories/<name>: one directory with its users and groups (?tab=groups).
 */
const i18n = useI18n()
const { t } = i18n
const route = useRoute()
const router = useRouter()

type Tab = 'users' | 'groups'
const TABS: Tab[] = ['users', 'groups']

const directories = ref<Directory[]>([])
const loading = ref(false)
const loaded = ref(false)
const error = ref('')
const panel = useTemplateRef<{ load: () => Promise<void> }>('panel')

const name = computed(() => String(route.params.directory ?? ''))
const current = computed(() => directories.value.find((d) => d.name === name.value))
const tab = computed<Tab>(() => (route.query.tab === 'groups' ? 'groups' : 'users'))

// The filter of the overview: one directory or all ('').
const filter = computed({
  get: () => String(route.query.directory ?? ''),
  set: (v: string) => void router.replace({ query: v ? { directory: v } : {} }),
})
const filterOptions = computed(() => [
  { value: '', label: t('directories.all') },
  ...directories.value.map((d) => ({ value: d.name, label: `${d.name} (${d.domain})` })),
])
const shown = computed(() =>
  filter.value ? directories.value.filter((d) => d.name === filter.value) : directories.value,
)

async function load() {
  loading.value = true
  try {
    directories.value = (await unwrap(api.GET('/api/v1/directories'))).directories
    error.value = ''
  } catch (e) {
    error.value = errorMessage(i18n, e)
  } finally {
    loading.value = false
    loaded.value = true
  }
}

/** Reloads the directories and, on the details page, the open tab. */
async function refresh() {
  await load()
  await panel.value?.load()
}

function selectTab(next: Tab) {
  void router.replace({ query: { tab: next } })
}

onMounted(load)
</script>

<template>
  <div class="flex flex-col gap-6">
    <RouterLink
      v-if="name"
      :to="{ name: 'directories' }"
      class="-mb-4 self-start text-sm text-primary hover:underline"
      >← {{ t('directories.back') }}</RouterLink
    >
    <!-- The heading and, on the right, the filter and refresh. -->
    <div class="flex flex-wrap items-end justify-between gap-4">
      <div class="flex flex-wrap items-center gap-3">
        <h1 class="text-2xl font-semibold">
          {{ current?.domain || name || t('nav.directories') }}
        </h1>
        <LinkButtons
          v-if="current?.links"
          :name="current.name"
          :ui="current.links.ui"
          :ssh="current.links.ssh"
          :rdp="current.links.rdp"
        />
      </div>
      <div class="flex flex-wrap items-end gap-3">
        <BaseSelect
          v-if="!name && directories.length > 1"
          v-model="filter"
          :label="t('directories.directory')"
          :options="filterOptions"
        />
        <IconButton
          icon="refresh"
          :label="t('home.refresh')"
          size="md"
          variant="secondary"
          :loading="loading"
          @click="refresh"
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
    <p v-else-if="loaded && directories.length === 0" class="text-fg-muted">
      {{ t('directories.none') }}
    </p>

    <!-- Overview: every directory; the domain opens its users and groups. -->
    <ul v-else-if="!name" class="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
      <li
        v-for="d in shown"
        :key="d.name"
        class="sys-tint flex flex-col gap-1 rounded-md border p-4"
        :style="systemStyle('directory')"
      >
        <section :aria-labelledby="`directory-${d.name}`" class="flex flex-col gap-1">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <h2 :id="`directory-${d.name}`" class="text-lg font-semibold">
              <RouterLink
                :to="{ name: 'directories', params: { directory: d.name } }"
                class="text-primary underline"
                >{{ d.domain }}</RouterLink
              >
            </h2>
            <LinkButtons
              v-if="d.links"
              :name="d.name"
              :ui="d.links.ui"
              :ssh="d.links.ssh"
              :rdp="d.links.rdp"
            />
          </div>
          <p class="text-sm text-fg-muted">{{ d.name }} · {{ t(`directories.types.${d.type}`) }}</p>
          <p class="text-sm text-fg-muted">
            {{ t('directories.managed', { ous: d.managedOUs.join('; ') }) }}
          </p>
        </section>
      </li>
    </ul>

    <!-- Users and groups of one directory. -->
    <template v-else-if="loaded && !error">
      <p v-if="!current" role="alert" class="text-danger">
        {{ t('directories.notFound', { name }) }}
      </p>
      <template v-else>
        <p class="-mt-4 text-sm text-fg-muted">
          {{ current.name }} · {{ t(`directories.types.${current.type}`) }} ·
          {{ t('directories.managed', { ous: current.managedOUs.join('; ') }) }}
        </p>
        <div>
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
          <div
            :id="`dir-panel-${tab}`"
            role="tabpanel"
            :aria-labelledby="`dir-tab-${tab}`"
            class="pt-4"
          >
            <UsersPanel v-if="tab === 'users'" ref="panel" :directory="current" />
            <GroupsPanel v-else ref="panel" :directory="current" />
          </div>
        </div>
      </template>
    </template>
  </div>
</template>
