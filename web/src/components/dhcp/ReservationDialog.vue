<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { api, unwrap } from '@/api/client'
import { errorMessage } from '@/api/errors'
import ProjectPicker from '@/components/projects/ProjectPicker.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import BaseInput from '@/components/ui/BaseInput.vue'
import BaseModal from '@/components/ui/BaseModal.vue'
import BaseSelect from '@/components/ui/BaseSelect.vue'
import {
  identifierTypes,
  splitList,
  type DhcpReservation,
  type DhcpServer,
  type IdentifierType,
} from '@/dhcp/dhcp'
import { useProjectsStore } from '@/stores/projects'

/** Adds or edits a reservation. Open it with create() or edit(). */
const props = defineProps<{
  server: DhcpServer
  /** Subnets the user may write, with id 0 for global reservations. */
  subnets: { id: number; prefix: string }[]
}>()
const emit = defineEmits<{ saved: [reservation: DhcpReservation, created: boolean] }>()

const i18n = useI18n()
const { t } = i18n

const open = ref(false)
const busy = ref(false)
const error = ref('')
const editing = ref<DhcpReservation | null>(null)
const v6 = computed(() => props.server.family === 'v6')
const form = ref({
  subnetId: '',
  identifierType: 'hw-address' as IdentifierType,
  identifier: '',
  addresses: '',
  prefixes: '',
  hostname: '',
})

const subnetOptions = computed(() => {
  const opts = props.subnets.map((s) => ({
    value: String(s.id),
    label: s.id === 0 ? t('dhcp.global') : s.prefix,
  }))
  const cur = editing.value
  // An edited reservation keeps its subnet even if it is not in the list.
  if (cur && !opts.some((o) => o.value === String(cur.subnetId))) {
    opts.push({ value: String(cur.subnetId), label: cur.subnet })
  }
  return opts
})
const typeOptions = computed(() =>
  identifierTypes(props.server.family).map((x) => ({
    value: x,
    label: t(`dhcp.identifierTypes.${x}`),
  })),
)
const title = computed(() => (editing.value ? t('dhcp.editReservation') : t('dhcp.addReservation')))

// Projects of the reserved addresses in NetBox (task 12.8).
const projectsStore = useProjectsStore()
const projects = ref<string[]>([])
const withProjects = computed(
  () => projectsStore.addressProjects && projectsStore.projects.length > 0,
)

async function loadProjects(address: string | undefined) {
  projects.value = []
  if (!withProjects.value || !address) return
  try {
    const res = await unwrap(
      api.GET('/api/v1/netbox/addresses/{address}/projects', { params: { path: { address } } }),
    )
    projects.value = res.projects
  } catch {
    // Without NetBox data the address has no projects.
  }
}

onMounted(() => void projectsStore.load())

function create() {
  error.value = ''
  editing.value = null
  const first = props.subnets.find((s) => s.id !== 0) ?? props.subnets[0]
  form.value = {
    subnetId: first ? String(first.id) : '',
    identifierType: identifierTypes(props.server.family)[0]!,
    identifier: '',
    addresses: '',
    prefixes: '',
    hostname: '',
  }
  projects.value = []
  open.value = true
}

function edit(r: DhcpReservation) {
  error.value = ''
  editing.value = r
  form.value = {
    subnetId: String(r.subnetId),
    identifierType: r.identifierType as IdentifierType,
    identifier: r.identifier,
    addresses: r.ipAddresses.join(', '),
    prefixes: (r.prefixes ?? []).join(', '),
    hostname: r.hostname ?? '',
  }
  void loadProjects(r.ipAddresses[0])
  open.value = true
}

async function save() {
  busy.value = true
  error.value = ''
  const body = {
    subnetId: Number(form.value.subnetId),
    identifierType: form.value.identifierType,
    identifier: form.value.identifier.trim(),
    ipAddresses: splitList(form.value.addresses),
    prefixes: v6.value ? splitList(form.value.prefixes) : undefined,
    hostname: form.value.hostname.trim() || undefined,
    version: editing.value?.version,
    projects: withProjects.value ? projects.value : undefined,
  }
  const server = props.server.name
  try {
    const cur = editing.value
    const saved = cur
      ? await unwrap(
          api.PUT('/api/v1/dhcp/servers/{server}/reservations/{id}', {
            params: { path: { server, id: cur.id } },
            body,
          }),
        )
      : await unwrap(
          api.POST('/api/v1/dhcp/servers/{server}/reservations', {
            params: { path: { server } },
            body,
          }),
        )
    open.value = false
    emit('saved', saved, cur === null)
  } catch (e) {
    error.value = errorMessage(i18n, e)
  } finally {
    busy.value = false
  }
}

defineExpose({ create, edit })
</script>

<template>
  <BaseModal v-model:open="open" :title="title">
    <form id="dhcp-reservation" class="flex flex-col gap-3" @submit.prevent="save">
      <BaseSelect
        v-model="form.subnetId"
        :label="t('dhcp.subnet')"
        :options="subnetOptions"
        :disabled="editing !== null"
      />
      <div class="grid gap-3 sm:grid-cols-[10rem_1fr]">
        <BaseSelect
          v-model="form.identifierType"
          :label="t('dhcp.identifierType')"
          :options="typeOptions"
          :disabled="editing !== null"
        />
        <BaseInput
          v-model="form.identifier"
          :label="t('dhcp.identifier')"
          :hint="t(`dhcp.identifierHints.${form.identifierType}`)"
          :disabled="editing !== null"
          autocomplete="off"
          required
        />
      </div>
      <BaseInput
        v-model="form.addresses"
        :label="v6 ? t('dhcp.ipv6Addresses') : t('dhcp.ipv4Address')"
        :hint="v6 ? t('dhcp.listHint') : t('dhcp.ipv4Hint')"
        autocomplete="off"
      />
      <BaseInput
        v-if="v6"
        v-model="form.prefixes"
        :label="t('dhcp.delegatedPrefixes')"
        :hint="t('dhcp.listHint')"
        autocomplete="off"
      />
      <BaseInput v-model="form.hostname" :label="t('dhcp.hostname')" autocomplete="off" />
      <ProjectPicker v-if="withProjects" v-model="projects" />
      <p
        v-if="editing && (editing.clientClasses?.length || editing.options)"
        class="text-sm text-fg-muted"
      >
        {{ t('dhcp.keptFields') }}
      </p>
      <p v-if="error" role="alert" class="text-sm text-danger">{{ error }}</p>
    </form>
    <template #footer>
      <BaseButton variant="secondary" @click="open = false">{{ t('common.cancel') }}</BaseButton>
      <BaseButton
        variant="primary"
        type="submit"
        form="dhcp-reservation"
        :loading="busy"
        :disabled="!form.identifier.trim() || form.subnetId === ''"
      >
        {{ t('common.save') }}
      </BaseButton>
    </template>
  </BaseModal>
</template>
