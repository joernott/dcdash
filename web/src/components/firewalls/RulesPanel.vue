<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
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
import StatusBadge from '@/components/ui/StatusBadge.vue'
import {
  endpoint,
  newRule,
  PORT_PROTOCOLS,
  RULE_ACTIONS,
  RULE_PROTOCOLS,
  ruleInterfaces,
  type Rule,
  toRequest,
  type RuleRequest,
} from '@/firewalls/firewalls'
import { useToastStore } from '@/stores/toast'

/**
 * Filter rules. Every change is applied with a savepoint: if dcdash can't reach the firewall
 * afterwards, OPNsense restores the previous rules within a minute.
 */
const props = defineProps<{ firewall: string }>()

const i18n = useI18n()
const { t } = i18n
const toast = useToastStore()

const list = ref<Schemas['FirewallRuleList'] | null>(null)
const loading = ref(false)
const error = ref('')
const iface = ref('')

const open = ref(false)
const editing = ref<Rule | null>(null)
const form = ref<RuleRequest>(newRule('lan'))
const busy = ref(false)
const formError = ref('')
const deleting = ref<Rule | null>(null)
const deleteOpen = ref(false)

const interfaces = computed(() => ruleInterfaces(list.value?.rules ?? []))
const ifaceOptions = computed(() => [
  { value: '', label: t('firewalls.rules.allInterfaces') },
  ...interfaces.value.map((i) => ({ value: i, label: i })),
])
const rows = computed(() =>
  (list.value?.rules ?? []).filter((r) => !iface.value || r.interface === iface.value),
)
const columns = computed(() => [
  { key: 'action', label: t('firewalls.rules.action') },
  { key: 'interface', label: t('firewalls.rules.interface') },
  { key: 'protocol', label: t('firewalls.rules.protocol') },
  { key: 'source', label: t('firewalls.rules.source') },
  { key: 'destination', label: t('firewalls.rules.destination') },
  { key: 'description', label: t('firewalls.aliases.description') },
  { key: 'actions', label: t('firewalls.actions'), class: 'text-right' },
])
const opt = (xs: readonly string[]) => xs.map((x) => ({ value: x, label: x }))
const actionOptions = computed(() =>
  RULE_ACTIONS.map((a) => ({ value: a, label: t(`firewalls.rules.actions.${a}`) })),
)
const directionOptions = computed(() =>
  ['in', 'out'].map((d) => ({ value: d, label: t(`firewalls.rules.directions.${d}`) })),
)
const ipOptions = computed(() =>
  ['inet', 'inet6', 'inet46'].map((v) => ({ value: v, label: t(`firewalls.rules.ip.${v}`) })),
)
const withPorts = computed(() => PORT_PROTOCOLS.includes(form.value.protocol ?? ''))
const badge = { pass: 'ok', block: 'error', reject: 'warning' } as const

async function load() {
  loading.value = true
  try {
    list.value = await unwrap(
      api.GET('/api/v1/firewalls/{firewall}/rules', {
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
  form.value = newRule(iface.value || interfaces.value[0] || 'lan')
  formError.value = ''
  open.value = true
}

function edit(r: Rule) {
  editing.value = r
  form.value = toRequest(r)
  formError.value = ''
  open.value = true
}

/** Shows the outcome of a change; true if it was kept. */
function outcome(res: Schemas['FirewallRuleResult']): boolean {
  if (res.rolledBack) {
    toast.error(t('firewalls.rules.rolledBack', { firewall: props.firewall }))
    return false
  }
  if (res.lockedOut) {
    toast.error(t('firewalls.rules.lockedOut', { firewall: props.firewall }))
    return false
  }
  if (res.warning) toast.error(res.warning)
  else toast.success(t('firewalls.rules.kept'))
  return true
}

async function save() {
  busy.value = true
  formError.value = ''
  const body = { ...form.value }
  if (!withPorts.value) {
    body.sourcePort = ''
    body.destinationPort = ''
  }
  const path = { firewall: props.firewall }
  try {
    const res = editing.value
      ? await unwrap(
          api.PUT('/api/v1/firewalls/{firewall}/rules/{uuid}', {
            params: { path: { ...path, uuid: editing.value.uuid } },
            body,
          }),
        )
      : await unwrap(api.POST('/api/v1/firewalls/{firewall}/rules', { params: { path }, body }))
    open.value = false
    outcome(res)
    await load()
  } catch (e) {
    formError.value = errorMessage(i18n, e)
  } finally {
    busy.value = false
  }
}

function askDelete(r: Rule) {
  deleting.value = r
  deleteOpen.value = true
}

async function remove() {
  busy.value = true
  try {
    const res = await unwrap(
      api.DELETE('/api/v1/firewalls/{firewall}/rules/{uuid}', {
        params: { path: { firewall: props.firewall, uuid: deleting.value!.uuid } },
      }),
    )
    deleteOpen.value = false
    outcome(res)
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
    <p
      v-if="list?.noSavepoint"
      role="note"
      class="rounded-md border border-warning px-3 py-2 text-sm"
    >
      {{
        list.guardError
          ? t('firewalls.rules.guardUnknown', { error: list.guardError })
          : t('firewalls.rules.noSavepoint', {
              interfaces: (list.protectedInterfaces ?? []).join(', '),
            })
      }}
    </p>
    <p v-else class="text-sm text-fg-muted">{{ t('firewalls.rules.savepointHint') }}</p>
    <div class="flex flex-wrap items-end gap-3">
      <BaseSelect v-model="iface" :label="t('firewalls.rules.interface')" :options="ifaceOptions" />
      <IconButton
        icon="add"
        :label="t('firewalls.rules.create')"
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
      :row-key="(r: Rule) => r.uuid || `legacy-${r.sequence}-${r.description}`"
      :caption="t('firewalls.tabs.rules')"
      :loading="loading && !list"
    >
      <template #cell-action="{ row }">
        <div class="flex flex-col gap-1">
          <StatusBadge
            :status="(row as Rule).enabled ? badge[(row as Rule).action] : 'unknown'"
            :label="t(`firewalls.rules.actions.${(row as Rule).action}`)"
          />
          <span v-if="!(row as Rule).enabled" class="text-xs text-fg-muted">
            {{ t('firewalls.rules.disabled') }}
          </span>
          <span v-if="(row as Rule).legacy" class="text-xs text-fg-muted">
            {{ t('firewalls.rules.legacy') }}
          </span>
        </div>
      </template>
      <template #cell-interface="{ row }">
        {{ (row as Rule).interface }}
        <span class="text-xs text-fg-muted">
          {{ t(`firewalls.rules.directions.${(row as Rule).direction}`) }}
        </span>
      </template>
      <template #cell-protocol="{ row }">
        {{ (row as Rule).protocol }}
        <span class="text-xs text-fg-muted">{{
          t(`firewalls.rules.ip.${(row as Rule).ipProtocol}`)
        }}</span>
      </template>
      <template #cell-source="{ row }">
        <span class="font-mono text-xs">{{
          endpoint((row as Rule).source, (row as Rule).sourceNot, (row as Rule).sourcePort)
        }}</span>
      </template>
      <template #cell-destination="{ row }">
        <span class="font-mono text-xs">{{
          endpoint(
            (row as Rule).destination,
            (row as Rule).destinationNot,
            (row as Rule).destinationPort,
          )
        }}</span>
      </template>
      <template #cell-actions="{ row }">
        <div v-if="(row as Rule).canWrite" class="flex justify-end gap-2">
          <IconButton
            icon="edit"
            size="sm"
            variant="secondary"
            :label="
              t('firewalls.rules.editLabel', {
                rule: (row as Rule).description || (row as Rule).uuid,
              })
            "
            @click="edit(row as Rule)"
          />
          <IconButton
            icon="delete"
            size="sm"
            variant="danger"
            :label="
              t('firewalls.rules.deleteLabel', {
                rule: (row as Rule).description || (row as Rule).uuid,
              })
            "
            @click="askDelete(row as Rule)"
          />
        </div>
      </template>
    </DataTable>

    <BaseModal
      v-model:open="open"
      :title="editing ? t('firewalls.rules.editTitle') : t('firewalls.rules.createTitle')"
    >
      <form id="fw-rule" class="flex flex-col gap-3" @submit.prevent="save">
        <div class="grid gap-3 sm:grid-cols-2">
          <BaseSelect
            v-model="form.action"
            :label="t('firewalls.rules.action')"
            :options="actionOptions"
          />
          <BaseInput v-model="form.interface" :label="t('firewalls.rules.interface')" required />
          <BaseSelect
            v-model="form.direction"
            :label="t('firewalls.rules.direction')"
            :options="directionOptions"
          />
          <BaseSelect
            v-model="form.ipProtocol"
            :label="t('firewalls.rules.ipVersion')"
            :options="ipOptions"
          />
          <BaseSelect
            :model-value="form.protocol ?? 'any'"
            :label="t('firewalls.rules.protocol')"
            :options="opt(RULE_PROTOCOLS)"
            @update:model-value="form.protocol = $event"
          />
        </div>
        <div class="grid gap-3 sm:grid-cols-2">
          <BaseInput
            :model-value="form.source ?? ''"
            :label="t('firewalls.rules.source')"
            :hint="t('firewalls.rules.netHint')"
            @update:model-value="form.source = $event"
          />
          <BaseInput
            v-if="withPorts"
            :model-value="form.sourcePort ?? ''"
            :label="t('firewalls.rules.sourcePort')"
            @update:model-value="form.sourcePort = $event"
          />
          <BaseInput
            :model-value="form.destination ?? ''"
            :label="t('firewalls.rules.destination')"
            :hint="t('firewalls.rules.netHint')"
            @update:model-value="form.destination = $event"
          />
          <BaseInput
            v-if="withPorts"
            :model-value="form.destinationPort ?? ''"
            :label="t('firewalls.rules.destinationPort')"
            :hint="t('firewalls.rules.portHint')"
            @update:model-value="form.destinationPort = $event"
          />
        </div>
        <BaseInput
          :model-value="form.description ?? ''"
          :label="t('firewalls.aliases.description')"
          @update:model-value="form.description = $event"
        />
        <div class="flex flex-wrap gap-4 text-sm">
          <label class="flex items-center gap-2">
            <input v-model="form.enabled" type="checkbox" class="size-4" />
            {{ t('firewalls.rules.enabled') }}
          </label>
          <label class="flex items-center gap-2">
            <input v-model="form.log" type="checkbox" class="size-4" />
            {{ t('firewalls.rules.log') }}
          </label>
        </div>
        <p class="text-sm text-fg-muted">{{ t('firewalls.rules.savepointHint') }}</p>
        <p v-if="formError" role="alert" class="text-sm text-danger">{{ formError }}</p>
      </form>
      <template #footer>
        <BaseButton variant="secondary" @click="open = false">{{ t('common.cancel') }}</BaseButton>
        <BaseButton variant="primary" type="submit" form="fw-rule" :loading="busy">
          {{ t('firewalls.rules.apply') }}
        </BaseButton>
      </template>
    </BaseModal>

    <BaseModal v-model:open="deleteOpen" :title="t('firewalls.rules.deleteTitle')">
      <p>
        {{
          t('firewalls.rules.deleteText', { rule: deleting?.description || deleting?.uuid || '' })
        }}
      </p>
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
