<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, onMounted, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { api, unwrap, type Schemas } from '@/api/client'
import { errorMessage } from '@/api/errors'
import AppIcon from '@/components/ui/AppIcon.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import IconButton from '@/components/ui/IconButton.vue'
import BaseInput from '@/components/ui/BaseInput.vue'
import BaseModal from '@/components/ui/BaseModal.vue'
import BaseSelect from '@/components/ui/BaseSelect.vue'
import DataTable from '@/components/ui/DataTable.vue'
import { ALIAS_TYPES, parseContent, type Alias } from '@/firewalls/firewalls'
import { useToastStore } from '@/stores/toast'

/** Firewall aliases: list, create, change and delete. */
const props = defineProps<{ firewall: string }>()

const i18n = useI18n()
const { t } = i18n
const toast = useToastStore()
const contentId = useId()

const list = ref<Schemas['FirewallAliasList'] | null>(null)
const loading = ref(false)
const error = ref('')
const filter = ref('')

const open = ref(false)
const editing = ref<Alias | null>(null)
const form = ref({ name: '', type: 'host', enabled: true, content: '', description: '' })
const busy = ref(false)
const formError = ref('')
const deleting = ref<Alias | null>(null)
const deleteOpen = ref(false)

const columns = computed(() => [
  { key: 'name', label: t('firewalls.aliases.name') },
  { key: 'type', label: t('firewalls.aliases.type') },
  { key: 'content', label: t('firewalls.aliases.content') },
  { key: 'description', label: t('firewalls.aliases.description') },
  { key: 'actions', label: t('firewalls.actions'), class: 'text-right' },
])
const rows = computed(() => {
  const q = filter.value.trim().toLowerCase()
  return (list.value?.aliases ?? []).filter(
    (a) =>
      !q ||
      a.name.toLowerCase().includes(q) ||
      a.description.toLowerCase().includes(q) ||
      a.content.some((c) => c.toLowerCase().includes(q)),
  )
})
const typeOptions = ALIAS_TYPES.map((x) => ({ value: x, label: x }))

async function load() {
  loading.value = true
  try {
    list.value = await unwrap(
      api.GET('/api/v1/firewalls/{firewall}/aliases', {
        params: { path: { firewall: props.firewall } },
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

function create() {
  editing.value = null
  form.value = { name: '', type: 'host', enabled: true, content: '', description: '' }
  formError.value = ''
  open.value = true
}

function edit(a: Alias) {
  editing.value = a
  form.value = {
    name: a.name,
    type: a.type,
    enabled: a.enabled,
    content: a.content.join('\n'),
    description: a.description,
  }
  formError.value = ''
  open.value = true
}

async function save() {
  busy.value = true
  formError.value = ''
  const body = {
    name: form.value.name.trim(),
    type: form.value.type as Schemas['FirewallAliasRequest']['type'],
    enabled: form.value.enabled,
    content: parseContent(form.value.content),
    description: form.value.description.trim(),
  }
  const path = { firewall: props.firewall }
  try {
    if (editing.value) {
      await unwrap(
        api.PUT('/api/v1/firewalls/{firewall}/aliases/{uuid}', {
          params: { path: { ...path, uuid: editing.value.uuid } },
          body,
        }),
      )
    } else {
      await unwrap(api.POST('/api/v1/firewalls/{firewall}/aliases', { params: { path }, body }))
    }
    open.value = false
    toast.success(t('firewalls.aliases.saved', { name: body.name }))
    await load()
  } catch (e) {
    formError.value = errorMessage(i18n, e)
  } finally {
    busy.value = false
  }
}

function askDelete(a: Alias) {
  deleting.value = a
  deleteOpen.value = true
}

async function remove() {
  const a = deleting.value!
  busy.value = true
  try {
    await unwrap(
      api.DELETE('/api/v1/firewalls/{firewall}/aliases/{uuid}', {
        params: { path: { firewall: props.firewall, uuid: a.uuid } },
      }),
    )
    deleteOpen.value = false
    toast.success(t('firewalls.aliases.deleted', { name: a.name }))
    await load()
  } catch (e) {
    toast.error(errorMessage(i18n, e))
  } finally {
    busy.value = false
  }
}

watch(() => props.firewall, load)
onMounted(load)
</script>

<template>
  <div class="flex flex-col gap-3">
    <div class="flex flex-wrap items-end gap-3">
      <BaseInput
        v-model="filter"
        :label="t('switches.filter')"
        :placeholder="t('firewalls.aliases.filterHint')"
        inputmode="search"
      />
      <IconButton
        icon="add"
        :label="t('firewalls.aliases.create')"
        size="md"
        v-if="list?.canCreate"
        @click="create"
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
    <p
      v-if="error"
      role="alert"
      class="rounded-md border border-danger px-3 py-2 text-sm text-danger"
    >
      {{ error }}
    </p>
    <DataTable
      system="firewall"
      :columns="columns"
      :rows="rows"
      :row-key="(a: Alias) => a.uuid"
      :caption="t('firewalls.tabs.aliases')"
      :loading="loading && !list"
    >
      <template #cell-name="{ row }">
        <span class="font-mono" :class="(row as Alias).enabled ? '' : 'text-fg-muted line-through'">
          {{ (row as Alias).name }}
        </span>
      </template>
      <template #cell-content="{ row }">
        <span class="font-mono text-xs">
          {{ (row as Alias).content.slice(0, 5).join(', ')
          }}{{ (row as Alias).content.length > 5 ? ` … (${(row as Alias).content.length})` : '' }}
        </span>
      </template>
      <template #cell-actions="{ row }">
        <div
          v-if="(row as Alias).canWrite && (row as Alias).editable"
          class="flex justify-end gap-2"
        >
          <IconButton
            icon="edit"
            size="sm"
            variant="secondary"
            :label="t('firewalls.aliases.editLabel', { name: (row as Alias).name })"
            @click="edit(row as Alias)"
          />
          <IconButton
            icon="delete"
            size="sm"
            variant="danger"
            :label="t('firewalls.aliases.deleteLabel', { name: (row as Alias).name })"
            @click="askDelete(row as Alias)"
          />
        </div>
      </template>
    </DataTable>

    <BaseModal
      v-model:open="open"
      :title="
        editing
          ? t('firewalls.aliases.editTitle', { name: editing.name })
          : t('firewalls.aliases.createTitle')
      "
    >
      <form id="fw-alias" class="flex flex-col gap-3" @submit.prevent="save">
        <BaseInput
          v-model="form.name"
          :label="t('firewalls.aliases.name')"
          :hint="t('firewalls.aliases.nameHint')"
          required
        />
        <BaseSelect
          v-model="form.type"
          :label="t('firewalls.aliases.type')"
          :options="typeOptions"
        />
        <div class="flex flex-col gap-1">
          <label :for="contentId" class="text-sm font-medium">{{
            t('firewalls.aliases.content')
          }}</label>
          <textarea
            :id="contentId"
            v-model="form.content"
            rows="6"
            class="rounded-md border border-border bg-surface px-3 py-2 font-mono text-sm text-fg"
            :aria-describedby="`${contentId}-hint`"
          />
          <p :id="`${contentId}-hint`" class="text-xs text-fg-muted">
            {{ t('firewalls.aliases.contentHint') }}
          </p>
        </div>
        <BaseInput v-model="form.description" :label="t('firewalls.aliases.description')" />
        <label class="flex items-center gap-2 text-sm">
          <input v-model="form.enabled" type="checkbox" class="size-4" />
          {{ t('firewalls.aliases.enabled') }}
        </label>
        <p class="text-sm text-fg-muted">{{ t('firewalls.aliases.appliedHint') }}</p>
        <p v-if="formError" role="alert" class="text-sm text-danger">{{ formError }}</p>
      </form>
      <template #footer>
        <BaseButton variant="secondary" @click="open = false">{{ t('common.cancel') }}</BaseButton>
        <BaseButton
          variant="primary"
          type="submit"
          form="fw-alias"
          :loading="busy"
          :disabled="!form.name.trim()"
        >
          {{ t('common.save') }}
        </BaseButton>
      </template>
    </BaseModal>

    <BaseModal
      v-model:open="deleteOpen"
      :title="t('firewalls.aliases.deleteTitle', { name: deleting?.name ?? '' })"
    >
      <p>{{ t('firewalls.aliases.deleteText') }}</p>
      <template #footer>
        <BaseButton variant="secondary" @click="deleteOpen = false">
          {{ t('common.cancel') }}
        </BaseButton>
        <BaseButton variant="danger" :loading="busy" @click="remove">
          <AppIcon name="delete" />{{ t('firewalls.delete') }}</BaseButton
        >
      </template>
    </BaseModal>
  </div>
</template>
