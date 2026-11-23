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
import { cnOf, userStates } from '@/directories/directories'
import { useToastStore } from '@/stores/toast'

type User = Schemas['DirectoryUser']
type Directory = Schemas['Directory']

/** The users of a directory: search, create, change, disable, unlock, reset, delete. */
const props = defineProps<{ directory: Directory }>()

const i18n = useI18n()
const { t } = i18n
const toast = useToastStore()

const users = ref<User[]>([])
const truncated = ref(false)
const loading = ref(false)
const error = ref('')
const query = ref('')
const busy = ref('')

const columns = computed(() => [
  { key: 'name', label: t('directories.users.name') },
  { key: 'displayName', label: t('directories.users.displayName') },
  { key: 'mail', label: t('directories.users.mail') },
  { key: 'state', label: t('directories.users.state') },
  { key: 'actions', label: t('directories.actions'), class: 'text-right' },
])
const path = computed(() => ({ directory: props.directory.name }))

async function load() {
  loading.value = true
  try {
    const out = await unwrap(
      api.GET('/api/v1/directories/{directory}/users', {
        params: { path: path.value, query: { q: query.value.trim() || undefined } },
      }),
    )
    users.value = out.users
    truncated.value = out.truncated
    error.value = ''
  } catch (e) {
    users.value = []
    error.value = errorMessage(i18n, e)
  } finally {
    loading.value = false
  }
}

/** Runs an operation on a user and reloads the list. */
async function run(u: User, op: 'enable' | 'disable' | 'unlock', done: string) {
  busy.value = `${op}:${u.name}`
  try {
    const p = { params: { path: { ...path.value, name: u.name } } }
    if (op === 'enable')
      await unwrap(api.POST('/api/v1/directories/{directory}/users/{name}/enable', p))
    else if (op === 'disable')
      await unwrap(api.POST('/api/v1/directories/{directory}/users/{name}/disable', p))
    else await unwrap(api.POST('/api/v1/directories/{directory}/users/{name}/unlock', p))
    toast.success(t(done, { name: u.name }))
    await load()
  } catch (e) {
    toast.error(errorMessage(i18n, e))
  } finally {
    busy.value = ''
  }
}

// Creating and changing users.
const formOpen = ref(false)
const editing = ref<User | null>(null)
const containers = ref<{ value: string; label: string }[]>([])
const emptyForm = () => ({
  ou: '',
  name: '',
  upn: '',
  givenName: '',
  surname: '',
  displayName: '',
  mail: '',
  description: '',
  password: '',
  mustChangePassword: true,
  disabled: false,
})
const form = ref(emptyForm())
const formError = ref('')
const saving = ref(false)

async function loadContainers() {
  if (containers.value.length) return
  const out = await unwrap(
    api.GET('/api/v1/directories/{directory}/containers', { params: { path: path.value } }),
  )
  containers.value = out.containers.map((c) => ({ value: c.dn, label: c.dn }))
}

async function create() {
  editing.value = null
  form.value = emptyForm()
  formError.value = ''
  try {
    await loadContainers()
    form.value.ou = containers.value[0]?.value ?? ''
    formOpen.value = true
  } catch (e) {
    toast.error(errorMessage(i18n, e))
  }
}

function edit(u: User) {
  editing.value = u
  form.value = {
    ...emptyForm(),
    name: u.name,
    upn: u.upn ?? '',
    givenName: u.givenName ?? '',
    surname: u.surname ?? '',
    displayName: u.displayName ?? '',
    mail: u.mail ?? '',
    description: u.description ?? '',
  }
  formError.value = ''
  formOpen.value = true
}

async function save() {
  saving.value = true
  formError.value = ''
  const f = form.value
  const attrs = {
    givenName: f.givenName.trim(),
    surname: f.surname.trim(),
    displayName: f.displayName.trim(),
    mail: f.mail.trim(),
    description: f.description.trim(),
  }
  try {
    if (editing.value) {
      await unwrap(
        api.PATCH('/api/v1/directories/{directory}/users/{name}', {
          params: { path: { ...path.value, name: editing.value.name } },
          body: { ...attrs, ...(f.upn.trim() ? { upn: f.upn.trim() } : {}) },
        }),
      )
    } else {
      await unwrap(
        api.POST('/api/v1/directories/{directory}/users', {
          params: { path: path.value },
          body: {
            ...attrs,
            ou: f.ou,
            name: f.name.trim(),
            upn: f.upn.trim() || undefined,
            password: f.password,
            mustChangePassword: f.mustChangePassword,
            disabled: f.disabled,
          },
        }),
      )
    }
    formOpen.value = false
    toast.success(t('directories.users.saved', { name: f.name.trim() }))
    await load()
  } catch (e) {
    formError.value = errorMessage(i18n, e)
  } finally {
    form.value.password = ''
    saving.value = false
  }
}

// Resetting passwords.
const resetOpen = ref(false)
const resetUser = ref<User | null>(null)
const reset = ref({ password: '', mustChangePassword: true })

function askReset(u: User) {
  resetUser.value = u
  reset.value = { password: '', mustChangePassword: true }
  formError.value = ''
  resetOpen.value = true
}

async function doReset() {
  saving.value = true
  formError.value = ''
  try {
    await unwrap(
      api.POST('/api/v1/directories/{directory}/users/{name}/password', {
        params: { path: { ...path.value, name: resetUser.value!.name } },
        body: reset.value,
      }),
    )
    resetOpen.value = false
    toast.success(t('directories.users.resetDone', { name: resetUser.value!.name }))
    await load()
  } catch (e) {
    formError.value = errorMessage(i18n, e)
  } finally {
    reset.value.password = ''
    saving.value = false
  }
}

// Deleting.
const deleteOpen = ref(false)
const deleting = ref<User | null>(null)

function askDelete(u: User) {
  deleting.value = u
  deleteOpen.value = true
}

async function remove() {
  const u = deleting.value!
  saving.value = true
  try {
    await unwrap(
      api.DELETE('/api/v1/directories/{directory}/users/{name}', {
        params: { path: { ...path.value, name: u.name } },
      }),
    )
    deleteOpen.value = false
    toast.success(t('directories.users.deleted', { name: u.name }))
    await load()
  } catch (e) {
    toast.error(errorMessage(i18n, e))
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
        :placeholder="t('directories.users.searchHint')"
        inputmode="search"
      />
      <IconButton icon="search" type="submit" :label="t('directories.searchButton')" size="md" />
      <IconButton
        v-if="props.directory.canCreateUsers"
        icon="add"
        :label="t('directories.users.create')"
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
      :columns="columns"
      :rows="users"
      :row-key="(u: User) => u.dn"
      :caption="t('directories.tabs.users')"
      :loading="loading && users.length === 0"
    >
      <template #cell-name="{ row }">
        <span class="font-mono">{{ (row as User).name }}</span>
        <span class="block text-xs text-fg-muted">{{ cnOf((row as User).dn) }}</span>
      </template>
      <template #cell-state="{ row }">
        <ul class="flex flex-wrap gap-1">
          <li
            v-for="s in userStates(row as User)"
            :key="s.key"
            class="rounded border px-1.5 text-xs"
            :class="
              s.tone === 'danger' ? 'border-danger text-danger' : 'border-border text-fg-muted'
            "
          >
            {{ t(`directories.users.states.${s.key}`) }}
          </li>
        </ul>
      </template>
      <template #cell-actions="{ row }">
        <div class="flex justify-end gap-2">
          <template v-if="(row as User).canOperate">
            <IconButton
              v-if="(row as User).lockedOut"
              icon="unlock"
              size="sm"
              :label="t('directories.users.unlockLabel', { name: (row as User).name })"
              :loading="busy === `unlock:${(row as User).name}`"
              @click="run(row as User, 'unlock', 'directories.users.unlocked')"
            />
            <IconButton
              v-if="(row as User).disabled"
              icon="power"
              tone="success"
              size="sm"
              :label="t('directories.users.enableLabel', { name: (row as User).name })"
              :loading="busy === `enable:${(row as User).name}`"
              @click="run(row as User, 'enable', 'directories.users.enabled')"
            />
            <IconButton
              v-else
              icon="power"
              tone="danger"
              size="sm"
              :label="t('directories.users.disableLabel', { name: (row as User).name })"
              :loading="busy === `disable:${(row as User).name}`"
              @click="run(row as User, 'disable', 'directories.users.disabled')"
            />
            <IconButton
              icon="password"
              size="sm"
              :label="t('directories.users.resetLabel', { name: (row as User).name })"
              @click="askReset(row as User)"
            />
          </template>
          <template v-if="(row as User).canWrite">
            <IconButton
              icon="edit"
              size="sm"
              :label="t('directories.users.editLabel', { name: (row as User).name })"
              @click="edit(row as User)"
            />
            <IconButton
              icon="delete"
              size="sm"
              variant="danger"
              :label="t('directories.users.deleteLabel', { name: (row as User).name })"
              @click="askDelete(row as User)"
            />
          </template>
        </div>
      </template>
    </DataTable>

    <BaseModal
      v-model:open="formOpen"
      :title="
        editing
          ? t('directories.users.editTitle', { name: editing.name })
          : t('directories.users.createTitle')
      "
    >
      <form id="dir-user" class="flex flex-col gap-3" @submit.prevent="save">
        <template v-if="!editing">
          <BaseSelect v-model="form.ou" :label="t('directories.container')" :options="containers" />
          <BaseInput
            v-model="form.name"
            :label="t('directories.users.name')"
            :hint="t('directories.users.nameHint')"
            required
          />
        </template>
        <BaseInput
          v-model="form.upn"
          :label="t('directories.users.upn')"
          :hint="t('directories.users.upnHint', { domain: props.directory.domain })"
        />
        <div class="grid gap-3 sm:grid-cols-2">
          <BaseInput v-model="form.givenName" :label="t('directories.users.givenName')" />
          <BaseInput v-model="form.surname" :label="t('directories.users.surname')" />
        </div>
        <BaseInput v-model="form.displayName" :label="t('directories.users.displayName')" />
        <BaseInput v-model="form.mail" :label="t('directories.users.mail')" type="email" />
        <BaseInput v-model="form.description" :label="t('directories.description')" />
        <template v-if="!editing">
          <BaseInput
            v-model="form.password"
            :label="t('directories.users.password')"
            type="password"
            autocomplete="new-password"
            required
          />
          <label class="flex items-center gap-2 text-sm">
            <input v-model="form.mustChangePassword" type="checkbox" class="size-4" />
            {{ t('directories.users.mustChange') }}
          </label>
          <label class="flex items-center gap-2 text-sm">
            <input v-model="form.disabled" type="checkbox" class="size-4" />
            {{ t('directories.users.createDisabled') }}
          </label>
        </template>
        <p v-if="formError" role="alert" class="text-sm text-danger">{{ formError }}</p>
      </form>
      <template #footer>
        <BaseButton variant="secondary" @click="formOpen = false">{{
          t('common.cancel')
        }}</BaseButton>
        <BaseButton
          variant="primary"
          type="submit"
          form="dir-user"
          :loading="saving"
          :disabled="!editing && (!form.name.trim() || !form.password || !form.ou)"
        >
          {{ t('common.save') }}
        </BaseButton>
      </template>
    </BaseModal>

    <BaseModal
      v-model:open="resetOpen"
      :title="t('directories.users.resetTitle', { name: resetUser?.name ?? '' })"
    >
      <form id="dir-reset" class="flex flex-col gap-3" @submit.prevent="doReset">
        <BaseInput
          v-model="reset.password"
          :label="t('directories.users.newPassword')"
          type="password"
          autocomplete="new-password"
          required
        />
        <label class="flex items-center gap-2 text-sm">
          <input v-model="reset.mustChangePassword" type="checkbox" class="size-4" />
          {{ t('directories.users.mustChange') }}
        </label>
        <p v-if="formError" role="alert" class="text-sm text-danger">{{ formError }}</p>
      </form>
      <template #footer>
        <BaseButton variant="secondary" @click="resetOpen = false">{{
          t('common.cancel')
        }}</BaseButton>
        <BaseButton
          variant="primary"
          type="submit"
          form="dir-reset"
          :loading="saving"
          :disabled="!reset.password"
        >
          {{ t('directories.users.reset') }}
        </BaseButton>
      </template>
    </BaseModal>

    <BaseModal
      v-model:open="deleteOpen"
      :title="t('directories.users.deleteTitle', { name: deleting?.name ?? '' })"
    >
      <p>{{ t('directories.users.deleteText', { dn: deleting?.dn ?? '' }) }}</p>
      <template #footer>
        <BaseButton variant="secondary" @click="deleteOpen = false">{{
          t('common.cancel')
        }}</BaseButton>
        <BaseButton variant="danger" :loading="saving" @click="remove">
          <AppIcon name="delete" />{{ t('directories.delete') }}
        </BaseButton>
      </template>
    </BaseModal>
  </div>
</template>
