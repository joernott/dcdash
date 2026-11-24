<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { api, unwrap, type Schemas } from '@/api/client'
import { errorMessage } from '@/api/errors'
import AppIcon from '@/components/ui/AppIcon.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import BaseInput from '@/components/ui/BaseInput.vue'
import BaseModal from '@/components/ui/BaseModal.vue'
import BaseSelect from '@/components/ui/BaseSelect.vue'
import DataTable from '@/components/ui/DataTable.vue'
import IconButton from '@/components/ui/IconButton.vue'
import { useToastStore } from '@/stores/toast'

type Group = Schemas['DirectoryGroup']
type Member = NonNullable<Group['members']>[number]
type Scope = Schemas['DirectoryGroupScope']
type Directory = Schemas['Directory']

/** The groups of a directory: search, create, change, delete, members. */
const props = defineProps<{ directory: Directory }>()

const i18n = useI18n()
const { t } = i18n
const toast = useToastStore()

const groups = ref<Group[]>([])
const truncated = ref(false)
const loading = ref(false)
const error = ref('')
const query = ref('')
const saving = ref(false)
const formError = ref('')

const columns = computed(() => [
  { key: 'name', label: t('directories.groups.name') },
  { key: 'description', label: t('directories.description') },
  { key: 'scope', label: t('directories.groups.scope') },
  { key: 'actions', label: t('directories.actions'), class: 'text-right' },
])
const scopeOptions = computed(() =>
  (['global', 'domainLocal', 'universal'] as Scope[]).map((s) => ({
    value: s,
    label: t(`directories.groups.scopes.${s}`),
  })),
)
const path = computed(() => ({ directory: props.directory.name }))

async function load() {
  loading.value = true
  try {
    const out = await unwrap(
      api.GET('/api/v1/directories/{directory}/groups', {
        params: { path: path.value, query: { q: query.value.trim() || undefined } },
      }),
    )
    groups.value = out.groups
    truncated.value = out.truncated
    error.value = ''
  } catch (e) {
    groups.value = []
    error.value = errorMessage(i18n, e)
  } finally {
    loading.value = false
  }
}

// Creating and changing groups.
const formOpen = ref(false)
const editing = ref<Group | null>(null)
const containers = ref<{ value: string; label: string }[]>([])
const emptyForm = () => ({
  ou: '',
  name: '',
  description: '',
  mail: '',
  scope: 'global' as Scope,
  security: true,
})
const form = ref(emptyForm())

async function create() {
  editing.value = null
  form.value = emptyForm()
  formError.value = ''
  try {
    if (!containers.value.length) {
      const out = await unwrap(
        api.GET('/api/v1/directories/{directory}/containers', { params: { path: path.value } }),
      )
      containers.value = out.containers.map((c) => ({ value: c.dn, label: c.dn }))
    }
    form.value.ou = containers.value[0]?.value ?? ''
    formOpen.value = true
  } catch (e) {
    toast.error(errorMessage(i18n, e))
  }
}

function edit(g: Group) {
  editing.value = g
  form.value = {
    ou: '',
    name: g.name,
    description: g.description ?? '',
    mail: g.mail ?? '',
    scope: g.scope,
    security: g.security,
  }
  formError.value = ''
  formOpen.value = true
}

async function save() {
  saving.value = true
  formError.value = ''
  const f = form.value
  const name = f.name.trim()
  try {
    if (editing.value) {
      const g = editing.value
      await unwrap(
        api.PATCH('/api/v1/directories/{directory}/groups/{name}', {
          params: { path: { ...path.value, name: g.name } },
          body: {
            ...(name !== g.name ? { name } : {}),
            description: f.description.trim(),
            mail: f.mail.trim(),
            ...(f.scope !== g.scope ? { scope: f.scope } : {}),
            ...(f.security !== g.security ? { security: f.security } : {}),
          },
        }),
      )
    } else {
      await unwrap(
        api.POST('/api/v1/directories/{directory}/groups', {
          params: { path: path.value },
          body: {
            ou: f.ou,
            name,
            description: f.description.trim() || undefined,
            mail: f.mail.trim() || undefined,
            scope: f.scope,
            security: f.security,
          },
        }),
      )
    }
    formOpen.value = false
    toast.success(t('directories.groups.saved', { name }))
    await load()
  } catch (e) {
    formError.value = errorMessage(i18n, e)
  } finally {
    saving.value = false
  }
}

// Deleting.
const deleteOpen = ref(false)
const deleting = ref<Group | null>(null)

function askDelete(g: Group) {
  deleting.value = g
  deleteOpen.value = true
}

async function remove() {
  const g = deleting.value!
  saving.value = true
  try {
    await unwrap(
      api.DELETE('/api/v1/directories/{directory}/groups/{name}', {
        params: { path: { ...path.value, name: g.name } },
      }),
    )
    deleteOpen.value = false
    toast.success(t('directories.groups.deleted', { name: g.name }))
    await load()
  } catch (e) {
    toast.error(errorMessage(i18n, e))
  } finally {
    saving.value = false
  }
}

// Members.
const membersOpen = ref(false)
const current = ref<Group | null>(null)
const newMember = ref('')
const memberError = ref('')
const memberColumns = computed(() => [
  { key: 'name', label: t('directories.groups.member') },
  { key: 'kind', label: t('directories.groups.kind') },
  { key: 'actions', label: t('directories.actions'), class: 'text-right' },
])

async function showMembers(g: Group) {
  memberError.value = ''
  newMember.value = ''
  try {
    current.value = await unwrap(
      api.GET('/api/v1/directories/{directory}/groups/{name}', {
        params: { path: { ...path.value, name: g.name } },
      }),
    )
    membersOpen.value = true
  } catch (e) {
    toast.error(errorMessage(i18n, e))
  }
}

async function changeMembers(add: string[], remove: string[]) {
  saving.value = true
  memberError.value = ''
  try {
    current.value = await unwrap(
      api.POST('/api/v1/directories/{directory}/groups/{name}/members', {
        params: { path: { ...path.value, name: current.value!.name } },
        body: { add, remove },
      }),
    )
    newMember.value = ''
  } catch (e) {
    memberError.value = errorMessage(i18n, e)
  } finally {
    saving.value = false
  }
}

watch(
  () => props.directory.name,
  () => {
    containers.value = []
    void load()
  },
)
onMounted(load)
</script>

<template>
  <div class="flex flex-col gap-3">
    <form class="flex flex-wrap items-end gap-3" @submit.prevent="load">
      <BaseInput
        v-model="query"
        :label="t('directories.search')"
        :placeholder="t('directories.groups.searchHint')"
        inputmode="search"
      />
      <IconButton icon="search" type="submit" :label="t('directories.searchButton')" size="md" />
      <IconButton
        v-if="props.directory.canCreateGroups"
        icon="add"
        :label="t('directories.groups.create')"
        size="md"
        @click="create"
      />
    </form>
    <p
      v-if="error"
      role="alert"
      class="rounded-md border border-danger px-3 py-2 text-sm text-danger"
    >
      {{ error }}
    </p>
    <p v-if="truncated" class="text-sm text-fg-muted">{{ t('directories.truncated') }}</p>
    <DataTable
      system="directory"
      :columns="columns"
      :rows="groups"
      :row-key="(g: Group) => g.dn"
      :caption="t('directories.tabs.groups')"
      :loading="loading && groups.length === 0"
    >
      <template #cell-name="{ row }">
        <span class="font-mono">{{ (row as Group).name }}</span>
        <span
          v-if="(row as Group).protected"
          class="ml-2 rounded border border-border px-1.5 text-xs text-fg-muted"
        >
          {{ t('directories.users.states.protected') }}
        </span>
      </template>
      <template #cell-scope="{ row }">
        {{ t(`directories.groups.scopes.${(row as Group).scope}`) }}
        <span class="block text-xs text-fg-muted">
          {{
            (row as Group).security
              ? t('directories.groups.security')
              : t('directories.groups.distribution')
          }}
        </span>
      </template>
      <template #cell-actions="{ row }">
        <div class="flex justify-end gap-2">
          <IconButton
            icon="members"
            size="sm"
            :label="t('directories.groups.membersLabel', { name: (row as Group).name })"
            @click="showMembers(row as Group)"
          />
          <template v-if="(row as Group).canWrite">
            <IconButton
              icon="edit"
              size="sm"
              :label="t('directories.groups.editLabel', { name: (row as Group).name })"
              @click="edit(row as Group)"
            />
            <IconButton
              icon="delete"
              size="sm"
              variant="danger"
              :label="t('directories.groups.deleteLabel', { name: (row as Group).name })"
              @click="askDelete(row as Group)"
            />
          </template>
        </div>
      </template>
    </DataTable>

    <BaseModal
      v-model:open="formOpen"
      :title="
        editing
          ? t('directories.groups.editTitle', { name: editing.name })
          : t('directories.groups.createTitle')
      "
    >
      <form id="dir-group" class="flex flex-col gap-3" @submit.prevent="save">
        <BaseSelect
          v-if="!editing"
          v-model="form.ou"
          :label="t('directories.container')"
          :options="containers"
        />
        <BaseInput v-model="form.name" :label="t('directories.groups.name')" required />
        <BaseInput v-model="form.description" :label="t('directories.description')" />
        <BaseInput v-model="form.mail" :label="t('directories.users.mail')" type="email" />
        <BaseSelect
          v-model="form.scope"
          :label="t('directories.groups.scope')"
          :options="scopeOptions"
        />
        <p v-if="editing" class="-mt-2 text-xs text-fg-muted">
          {{ t('directories.groups.scopeHint') }}
        </p>
        <label class="flex items-center gap-2 text-sm">
          <input v-model="form.security" type="checkbox" class="size-4" />
          {{ t('directories.groups.security') }}
        </label>
        <p v-if="formError" role="alert" class="text-sm text-danger">{{ formError }}</p>
      </form>
      <template #footer>
        <BaseButton variant="secondary" @click="formOpen = false">{{
          t('common.cancel')
        }}</BaseButton>
        <BaseButton
          variant="primary"
          type="submit"
          form="dir-group"
          :loading="saving"
          :disabled="!form.name.trim() || (!editing && !form.ou)"
        >
          {{ t('common.save') }}
        </BaseButton>
      </template>
    </BaseModal>

    <BaseModal
      v-model:open="deleteOpen"
      :title="t('directories.groups.deleteTitle', { name: deleting?.name ?? '' })"
    >
      <p>{{ t('directories.groups.deleteText', { dn: deleting?.dn ?? '' }) }}</p>
      <template #footer>
        <BaseButton variant="secondary" @click="deleteOpen = false">{{
          t('common.cancel')
        }}</BaseButton>
        <BaseButton variant="danger" :loading="saving" @click="remove">
          <AppIcon name="delete" />{{ t('directories.delete') }}
        </BaseButton>
      </template>
    </BaseModal>

    <BaseModal
      v-model:open="membersOpen"
      :title="t('directories.groups.membersTitle', { name: current?.name ?? '' })"
    >
      <div v-if="current" class="flex flex-col gap-3">
        <p v-if="!current.members?.length" class="text-sm text-fg-muted">
          {{ t('directories.groups.noMembers') }}
        </p>
        <DataTable
          system="directory"
          v-else
          :columns="memberColumns"
          :rows="current.members ?? []"
          :row-key="(m: Member) => m.dn"
          :caption="t('directories.groups.membersTitle', { name: current.name })"
        >
          <template #cell-name="{ row }">
            <span class="font-mono" :class="{ 'line-through': (row as Member).disabled }">{{
              (row as Member).name
            }}</span>
            <span v-if="(row as Member).displayName" class="block text-xs text-fg-muted">
              {{ (row as Member).displayName }}
            </span>
          </template>
          <template #cell-kind="{ row }">
            {{ t(`directories.groups.kinds.${(row as Member).kind}`) }}
          </template>
          <template #cell-actions="{ row }">
            <div class="flex justify-end">
              <IconButton
                v-if="current.canWrite"
                icon="delete"
                size="sm"
                variant="danger"
                :label="t('directories.groups.removeLabel', { name: (row as Member).name })"
                :loading="saving"
                @click="changeMembers([], [(row as Member).name])"
              />
            </div>
          </template>
        </DataTable>
        <form
          v-if="current.canWrite"
          class="flex flex-wrap items-end gap-3"
          @submit.prevent="changeMembers([newMember.trim()], [])"
        >
          <BaseInput
            v-model="newMember"
            :label="t('directories.groups.addMember')"
            :hint="t('directories.groups.addMemberHint')"
          />
          <IconButton
            icon="add"
            type="submit"
            :label="t('directories.groups.addMemberButton')"
            size="md"
            :loading="saving"
            :disabled="!newMember.trim()"
          />
        </form>
        <p v-if="memberError" role="alert" class="text-sm text-danger">{{ memberError }}</p>
      </div>
      <template #footer>
        <BaseButton variant="secondary" @click="membersOpen = false">{{
          t('common.close')
        }}</BaseButton>
      </template>
    </BaseModal>
  </div>
</template>
