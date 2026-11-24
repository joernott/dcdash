<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute } from 'vue-router'

import { api, unwrap, type Schemas } from '@/api/client'
import { errorMessage } from '@/api/errors'
import BaseButton from '@/components/ui/BaseButton.vue'
import IconButton from '@/components/ui/IconButton.vue'
import BaseInput from '@/components/ui/BaseInput.vue'
import BaseSelect from '@/components/ui/BaseSelect.vue'
import DataTable from '@/components/ui/DataTable.vue'
import { useToastStore } from '@/stores/toast'

type Page = Schemas['ProjectPage']
type Guest = Schemas['ProjectGuest']
type NbObject = Schemas['ProjectObject']
type ObjType = Schemas['ProjectObjectType']
type Group = Schemas['ProjectGroup']

const TYPES: ObjType[] = ['vm', 'ip', 'iprange', 'prefix', 'vlan', 'interface']

const i18n = useI18n()
const { t } = i18n
const route = useRoute()
const toast = useToastStore()

const name = computed(() => String(route.params.name))
const page = ref<Page | null>(null)
const loading = ref(false)
const error = ref('')

async function load() {
  loading.value = true
  try {
    page.value = await unwrap(
      api.GET('/api/v1/projects/{name}', { params: { path: { name: name.value } } }),
    )
    error.value = ''
  } catch (e) {
    page.value = null
    error.value = errorMessage(i18n, e)
  } finally {
    loading.value = false
  }
}

const groupColumns = computed(() => [
  { key: 'name', label: t('projects.columns.group') },
  { key: 'directory', label: t('projects.columns.directory') },
  { key: 'members', label: t('projects.columns.members') },
])

/** The members of a group as text: names, with display names. */
function members(g: Group): string {
  if (!g.visible) return t('projects.groupHidden')
  if (g.missing) return t('projects.groupMissing')
  if (!g.members?.length) return t('projects.noMembers')
  return g.members.map((m) => (m.displayName ? `${m.displayName} (${m.name})` : m.name)).join(', ')
}

const guestColumns = computed(() => [
  { key: 'name', label: t('projects.columns.guest') },
  { key: 'cluster', label: t('projects.columns.cluster') },
  { key: 'status', label: t('projects.columns.status') },
])
const objectColumns = computed(() => [
  { key: 'display', label: t('projects.columns.object') },
  { key: 'detail', label: t('projects.columns.detail') },
  { key: 'actions', label: t('projects.columns.actions'), class: 'w-px whitespace-nowrap' },
])
const sections = computed(() =>
  TYPES.map((typ) => ({ typ, objects: page.value?.netbox?.[typ] ?? [] })).filter(
    (s) => s.objects.length > 0,
  ),
)
const empty = computed(
  () =>
    !!page.value &&
    page.value.guests.length === 0 &&
    page.value.groups.length === 0 &&
    sections.value.length === 0,
)

// Adding NetBox objects.
const addType = ref<ObjType>('prefix')
const query = ref('')
const results = ref<NbObject[] | null>(null)
const searching = ref(false)
const busy = ref('')
const typeOptions = computed(() =>
  TYPES.map((v) => ({ value: v, label: t(`projects.types.${v}`) })),
)

async function search() {
  if (!query.value.trim()) return
  searching.value = true
  try {
    const res = await unwrap(
      api.GET('/api/v1/projects/{name}/search', {
        params: {
          path: { name: name.value },
          query: { type: addType.value, q: query.value.trim() },
        },
      }),
    )
    results.value = res.objects
  } catch (e) {
    toast.error(errorMessage(i18n, e))
  } finally {
    searching.value = false
  }
}

function key(o: NbObject): string {
  return `${o.type}/${o.id}`
}

const assigned = computed(
  () => new Set(Object.values(page.value?.netbox ?? {}).flatMap((l) => l.map(key))),
)

async function add(o: NbObject) {
  busy.value = key(o)
  try {
    await unwrap(
      api.POST('/api/v1/projects/{name}/objects', {
        params: { path: { name: name.value } },
        body: { type: o.type, id: o.id },
      }),
    )
    toast.success(t('projects.added', { object: o.display }))
    await load()
  } catch (e) {
    toast.error(errorMessage(i18n, e))
  } finally {
    busy.value = ''
  }
}

async function remove(o: NbObject) {
  busy.value = key(o)
  try {
    await unwrap(
      api.DELETE('/api/v1/projects/{name}/objects/{type}/{id}', {
        params: { path: { name: name.value, type: o.type, id: o.id } },
      }),
    )
    toast.success(t('projects.removed', { object: o.display }))
    await load()
  } catch (e) {
    toast.error(errorMessage(i18n, e))
  } finally {
    busy.value = ''
  }
}

watch(name, () => void load(), { immediate: true })
</script>

<template>
  <div class="flex flex-col gap-6">
    <nav :aria-label="t('projects.breadcrumb')" class="text-sm">
      <RouterLink :to="{ name: 'projects' }" class="text-primary underline">
        {{ t('nav.projects') }}
      </RouterLink>
    </nav>
    <p v-if="error" role="alert" class="text-danger">{{ error }}</p>
    <template v-if="page">
      <div class="flex flex-col gap-1">
        <h1 class="flex items-center gap-3 text-2xl font-semibold">
          <span
            class="size-4 rounded-full"
            aria-hidden="true"
            :style="{ backgroundColor: `#${page.color}` }"
          />
          {{ page.title }}
        </h1>
        <p class="font-mono text-sm text-fg-muted">{{ page.tag }}</p>
        <p v-if="page.description">{{ page.description }}</p>
      </div>
      <ul v-if="page.errors.length" role="alert" class="text-sm text-warning">
        <li v-for="e in page.errors" :key="e">{{ t('projects.sourceError', { error: e }) }}</li>
      </ul>
      <p v-if="empty" class="text-fg-muted">{{ t('projects.empty') }}</p>

      <section
        v-if="page.guests.length"
        aria-labelledby="project-guests"
        class="flex flex-col gap-3"
      >
        <h2 id="project-guests" class="text-lg font-semibold">{{ t('projects.guests') }}</h2>
        <DataTable
          :columns="guestColumns"
          :rows="page.guests"
          :row-key="(g: Guest) => `${g.cluster}/${g.type}/${g.vmid}`"
          :caption="t('projects.guests')"
        >
          <template #cell-name="{ row }">
            <RouterLink
              v-if="(row as Guest).visible"
              class="text-primary underline"
              :to="{
                name: 'proxmox-guest',
                params: {
                  cluster: (row as Guest).cluster,
                  type: (row as Guest).type,
                  vmid: (row as Guest).vmid,
                },
              }"
            >
              {{ (row as Guest).name }}
            </RouterLink>
            <span v-else>{{ (row as Guest).name }}</span>
            <span class="block text-xs text-fg-muted">
              {{ (row as Guest).type }} {{ (row as Guest).vmid }}
            </span>
          </template>
          <template #cell-status="{ row }">
            {{ (row as Guest).visible ? (row as Guest).status : '–' }}
          </template>
        </DataTable>
      </section>

      <section
        v-if="page.groups.length"
        aria-labelledby="project-groups"
        class="flex flex-col gap-3"
      >
        <h2 id="project-groups" class="text-lg font-semibold">{{ t('projects.groups') }}</h2>
        <DataTable
          :columns="groupColumns"
          :rows="page.groups"
          :row-key="(g: Group) => `${g.directory}/${g.name}`"
          :caption="t('projects.groups')"
        >
          <template #cell-name="{ row }">
            {{ (row as Group).name }}
            <span v-if="(row as Group).description" class="block text-xs text-fg-muted">
              {{ (row as Group).description }}
            </span>
          </template>
          <template #cell-members="{ row }">
            <span :class="{ 'text-fg-muted': !(row as Group).visible || (row as Group).missing }">
              {{ members(row as Group) }}
            </span>
          </template>
        </DataTable>
      </section>

      <section
        v-for="s in sections"
        :key="s.typ"
        :aria-labelledby="`project-${s.typ}`"
        class="flex flex-col gap-3"
      >
        <h2 :id="`project-${s.typ}`" class="text-lg font-semibold">
          {{ t(`projects.sections.${s.typ}`) }}
        </h2>
        <DataTable
          :columns="objectColumns"
          :rows="s.objects"
          :row-key="key"
          :caption="t(`projects.sections.${s.typ}`)"
        >
          <template #cell-display="{ row }">
            <RouterLink
              v-if="
                (row as NbObject).type === 'vm' &&
                (row as NbObject).proxmoxCluster &&
                (row as NbObject).vmid
              "
              class="text-primary underline"
              :to="{
                name: 'proxmox-guest',
                params: {
                  cluster: (row as NbObject).proxmoxCluster,
                  type: 'qemu',
                  vmid: (row as NbObject).vmid,
                },
              }"
            >
              {{ (row as NbObject).display }}
            </RouterLink>
            <a
              v-else-if="(row as NbObject).url"
              :href="(row as NbObject).url"
              class="text-primary underline"
              target="_blank"
              rel="noopener"
            >
              {{ (row as NbObject).display }}
            </a>
            <span v-else class="font-mono">{{ (row as NbObject).display }}</span>
          </template>
          <template #cell-actions="{ row }">
            <BaseButton
              v-if="
                page.canWrite &&
                ((row as NbObject).type !== 'vm' || !(row as NbObject).proxmoxCluster)
              "
              size="sm"
              variant="secondary"
              :loading="busy === key(row as NbObject)"
              @click="remove(row as NbObject)"
            >
              {{ t('projects.remove') }}
            </BaseButton>
          </template>
        </DataTable>
      </section>

      <section
        v-if="page.canWrite && page.netbox"
        aria-labelledby="project-add"
        class="flex flex-col gap-3"
      >
        <h2 id="project-add" class="text-lg font-semibold">{{ t('projects.add') }}</h2>
        <p class="text-sm text-fg-muted">{{ t('projects.addHint') }}</p>
        <form class="flex flex-wrap items-end gap-3" @submit.prevent="search">
          <div class="w-48">
            <BaseSelect v-model="addType" :label="t('projects.type')" :options="typeOptions" />
          </div>
          <div class="w-72">
            <BaseInput v-model="query" :label="t('projects.search')" inputmode="search" />
          </div>
          <IconButton
            icon="search"
            :label="t('projects.searchButton')"
            size="md"
            type="submit"
            variant="secondary"
            :loading="searching"
          />
        </form>
        <ul v-if="results" class="flex flex-col gap-2" data-testid="project-results">
          <li v-if="results.length === 0" class="text-fg-muted">{{ t('projects.noResults') }}</li>
          <li v-for="o in results" :key="key(o)" class="flex items-center gap-3">
            <span class="font-mono">{{ o.display }}</span>
            <span v-if="o.detail" class="text-sm text-fg-muted">{{ o.detail }}</span>
            <BaseButton
              v-if="!assigned.has(key(o))"
              size="sm"
              variant="secondary"
              :loading="busy === key(o)"
              @click="add(o)"
            >
              {{ t('projects.addButton') }}
            </BaseButton>
            <span v-else class="text-sm text-fg-muted">{{ t('projects.alreadyAdded') }}</span>
          </li>
        </ul>
      </section>
    </template>
    <p v-else-if="loading" class="text-fg-muted">{{ t('common.loading') }}</p>
  </div>
</template>
