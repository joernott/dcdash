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
  CONTENT_EXAMPLES,
  EDITABLE_TYPES,
  PROXIABLE_TYPES,
  relativeName,
  TTL_CHOICES,
  type Capabilities,
  type DnsRecord,
  type RecordType,
} from '@/dns/records'
import { useProjectsStore } from '@/stores/projects'

/** Creates or edits a record. Open it with create() or edit(). */
const props = defineProps<{ provider: string; zone: string; capabilities: Capabilities }>()
const emit = defineEmits<{ saved: [record: DnsRecord] }>()

const i18n = useI18n()
const { t } = i18n

const open = ref(false)
const busy = ref(false)
const error = ref('')
const editing = ref<DnsRecord | null>(null)
const version = ref('')
const form = ref({
  name: '',
  type: 'A' as RecordType,
  ttl: '0',
  content: '',
  proxied: false,
  comment: '',
})

const typeOptions = EDITABLE_TYPES.map((x) => ({ value: x, label: x }))
const ttlOptions = computed(() => [
  {
    value: '0',
    label: props.capabilities.autoTtl ? t('dns.ttlAuto') : t('dns.ttlDefault'),
  },
  ...TTL_CHOICES.map((s) => ({ value: String(s), label: t('dns.ttlSeconds', { n: s }) })),
])
const canProxy = computed(
  () => props.capabilities.proxied && PROXIABLE_TYPES.includes(form.value.type),
)
const title = computed(() => (editing.value ? t('dns.editRecord') : t('dns.addRecord')))

// Projects of the address of an A or AAAA record in NetBox (task 12.8).
const projectsStore = useProjectsStore()
const projects = ref<string[]>([])
const withProjects = computed(
  () =>
    projectsStore.addressProjects &&
    projectsStore.projects.length > 0 &&
    (form.value.type === 'A' || form.value.type === 'AAAA'),
)

async function loadProjects(address: string) {
  projects.value = []
  if (!withProjects.value) return
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

function reset() {
  error.value = ''
  busy.value = false
}

/** Opens the dialog for a new record; version is the record set version. */
function create(recordSetVersion: string) {
  reset()
  editing.value = null
  version.value = recordSetVersion
  form.value = { name: '', type: 'A', ttl: '0', content: '', proxied: false, comment: '' }
  projects.value = []
  open.value = true
}

/** Opens the dialog to edit a record. */
function edit(r: DnsRecord) {
  reset()
  editing.value = r
  version.value = r.version
  const ttl = r.ttl === 1 && props.capabilities.autoTtl ? 0 : r.ttl
  // Keep a TTL that is not in the list selectable.
  form.value = {
    name: relativeName(r.name, props.zone),
    type: r.type as RecordType,
    ttl: String(ttl),
    content: r.type === 'TXT' ? unquoteTXT(r.content) : r.content,
    proxied: r.proxied ?? false,
    comment: r.comment ?? '',
  }
  void loadProjects(r.content)
  open.value = true
}

/** Shows a single quoted TXT string as plain text; several strings stay as they are. */
function unquoteTXT(s: string): string {
  const m = /^"((?:[^"\\]|\\.)*)"$/.exec(s)
  return m ? m[1]!.replace(/\\(["\\])/g, '$1') : s
}

const ttlSelectOptions = computed(() => {
  const opts = ttlOptions.value
  return opts.some((o) => o.value === form.value.ttl)
    ? opts
    : [
        ...opts,
        { value: form.value.ttl, label: t('dns.ttlSeconds', { n: Number(form.value.ttl) }) },
      ]
})

async function save() {
  busy.value = true
  error.value = ''
  const body = {
    name: form.value.name.trim() || '@',
    type: form.value.type,
    ttl: Number(form.value.ttl),
    content: form.value.content.trim(),
    proxied: canProxy.value ? form.value.proxied : undefined,
    comment: props.capabilities.comments ? form.value.comment.trim() : undefined,
    version: version.value,
    projects: withProjects.value ? projects.value : undefined,
  }
  const path = { provider: props.provider, zone: props.zone }
  try {
    const saved = editing.value
      ? await unwrap(
          api.PUT('/api/v1/dns/providers/{provider}/zones/{zone}/records/{id}', {
            params: { path: { ...path, id: editing.value.id } },
            body,
          }),
        )
      : await unwrap(
          api.POST('/api/v1/dns/providers/{provider}/zones/{zone}/records', {
            params: { path },
            body,
          }),
        )
    open.value = false
    emit('saved', saved)
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
    <form id="dns-record" class="flex flex-col gap-3" @submit.prevent="save">
      <div class="grid gap-3 sm:grid-cols-[1fr_8rem]">
        <BaseInput
          v-model="form.name"
          :label="t('dns.name')"
          :hint="t('dns.nameHint', { zone: props.zone })"
          placeholder="@"
        />
        <BaseSelect v-model="form.type" :label="t('dns.type')" :options="typeOptions" />
      </div>
      <BaseInput
        v-model="form.content"
        :label="t('dns.content')"
        :hint="t(`dns.contentHints.${form.type}`)"
        :placeholder="CONTENT_EXAMPLES[form.type]"
        required
      />
      <BaseSelect v-model="form.ttl" :label="t('dns.ttl')" :options="ttlSelectOptions" />
      <label v-if="canProxy" class="flex items-center gap-2 text-sm">
        <input v-model="form.proxied" type="checkbox" class="size-4" />
        {{ t('dns.proxied') }}
      </label>
      <BaseInput
        v-if="props.capabilities.comments"
        v-model="form.comment"
        :label="t('dns.comment')"
      />
      <ProjectPicker v-if="withProjects" v-model="projects" />
      <p v-if="error" role="alert" class="text-sm text-danger">{{ error }}</p>
    </form>
    <template #footer>
      <BaseButton variant="secondary" @click="open = false">{{ t('common.cancel') }}</BaseButton>
      <BaseButton
        variant="primary"
        type="submit"
        form="dns-record"
        :loading="busy"
        :disabled="!form.content.trim()"
      >
        {{ t('common.save') }}
      </BaseButton>
    </template>
  </BaseModal>
</template>
