<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'

import { api, ApiError, unwrap } from '@/api/client'
import { errorMessage } from '@/api/errors'
import BaseButton from '@/components/ui/BaseButton.vue'
import BaseInput from '@/components/ui/BaseInput.vue'
import BaseSelect from '@/components/ui/BaseSelect.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import { formatBytes } from '@/proxmox/guests'
import {
  MAX_NETWORKS,
  newForm,
  newNet,
  toRequest,
  validate,
  type CreateForm,
  type FormErrors,
  type Job,
  type Kind,
  type NetForm,
  type ProvisioningOptions,
} from '@/proxmox/provisioning'

/** Poll interval of a running job. */
const POLL_MS = 1000

const STEPS = ['source', 'resources', 'network', 'review'] as const
type Step = (typeof STEPS)[number]

/** The fields of each step, for validating one step at a time. */
const STEP_FIELDS: Record<Step, (keyof CreateForm)[]> = {
  source: ['kind', 'node', 'name', 'vmid', 'template', 'iso', 'osTemplate'],
  resources: ['cores', 'memory', 'disk', 'storage'],
  network: ['networks', 'nameservers', 'searchDomain', 'user', 'password', 'sshKeys'],
  review: ['tags', 'description'],
}

const i18n = useI18n()
const { t, locale } = i18n
const route = useRoute()
const id = useId()

const cluster = computed(() => String(route.params.cluster))
const options = ref<ProvisioningOptions | null>(null)
const form = ref<CreateForm | null>(null)
const errors = ref<FormErrors>({})
const step = ref<Step>('source')
const error = ref('')
const submitError = ref('')
const busy = ref(false)
const job = ref<Job | null>(null)
let timer: ReturnType<typeof setTimeout> | undefined

const stepIndex = computed(() => STEPS.indexOf(step.value))

const kinds = computed<Kind[]>(() => {
  const o = options.value
  if (!o) return []
  const out: Kind[] = []
  if (o.templates.length > 0) out.push('clone')
  out.push('iso', 'lxc')
  return out
})

const nodeOptions = computed(() =>
  (options.value?.nodes ?? []).map((n) => ({ value: n, label: n })),
)
const templateOptions = computed(() =>
  (options.value?.templates ?? []).map((x) => ({
    value: String(x.vmid),
    label: `${x.name || x.vmid} (${x.vmid}, ${x.node})`,
  })),
)
const isoOptions = computed(() =>
  (options.value?.isos ?? []).map((v) => ({ value: v.volid, label: v.volid })),
)
const ctOptions = computed(() =>
  (options.value?.containerTemplates ?? []).map((v) => ({ value: v.volid, label: v.volid })),
)
const osOptions = computed(() =>
  (['linux', 'windows', 'other'] as const).map((v) => ({
    value: v,
    label: t(`proxmox.create.os.${v}`),
  })),
)
const storageOptions = computed(() =>
  (options.value?.storages ?? [])
    .filter((s) => (form.value?.kind === 'lxc' ? s.container : s.vm))
    .map((s) => ({
      value: s.name,
      label: t('proxmox.create.storageOption', {
        name: s.name,
        free: formatBytes(s.avail, locale.value),
      }),
    })),
)
const bridgeOptions = computed(() =>
  (options.value?.bridges ?? []).map((b) => ({ value: b.name, label: b.name })),
)
const ipv4Options = computed(() =>
  (['none', 'dhcp', 'static'] as const).map((v) => ({
    value: v,
    label: t(`proxmox.create.ipModes.${v}`),
  })),
)
const ipv6Options = computed(() =>
  (['none', 'auto', 'dhcp', 'static'] as const).map((v) => ({
    value: v,
    label: t(`proxmox.create.ipModes.${v}`),
  })),
)

/** The node the guest is created on: the template's node for clones. */
const targetNode = computed(() => {
  const f = form.value
  if (!f) return ''
  if (f.kind === 'clone')
    return options.value?.templates.find((x) => String(x.vmid) === f.template)?.node ?? ''
  return f.node
})

const vmidHint = computed(() =>
  options.value?.vmids.length
    ? t('proxmox.create.vmidRange', { ranges: options.value.vmids.join(', ') })
    : t('proxmox.create.vmidNext'),
)
const vlanHint = computed(() =>
  options.value?.vlans.length
    ? t('proxmox.create.vlanRange', { ranges: options.value.vlans.join(', ') })
    : t('proxmox.create.vlanNone'),
)

function netErr(i: number, field: keyof NetForm): string {
  const key = errors.value.networks?.[i]?.[field]
  return key ? t(`proxmox.create.errors.${key}`) : ''
}

function addNetwork() {
  const f = form.value
  if (f && options.value && f.networks.length < MAX_NETWORKS) {
    f.networks.push({ ...newNet(options.value, f.kind), ipv4Mode: 'none' })
  }
}

function removeNetwork(i: number) {
  form.value?.networks.splice(i, 1)
  errors.value = {}
}

function err(field: Exclude<keyof CreateForm, 'networks'>): string {
  const key = errors.value[field]
  if (!key) return ''
  const o = options.value
  return t(`proxmox.create.errors.${key}`, {
    cores: o?.limits.cores,
    memory: o?.limits.memory,
    disk: o?.limits.disk,
  })
}

async function loadOptions(node?: string) {
  try {
    const o = await unwrap(
      api.GET('/api/v1/proxmox/clusters/{cluster}/provisioning', {
        params: { path: { cluster: cluster.value }, query: node ? { node } : {} },
      }),
    )
    options.value = o
    if (!form.value) {
      form.value = newForm(o, o.templates.length > 0 ? 'clone' : 'lxc')
    } else {
      // Keep the choices that the new node offers.
      const f = form.value
      if (!o.storages.some((s) => s.name === f.storage)) f.storage = o.defaults.storage
      for (const n of f.networks) {
        if (!o.bridges.some((b) => b.name === n.bridge)) n.bridge = o.bridges[0]?.name ?? ''
      }
      if (!o.isos.some((v) => v.volid === f.iso)) f.iso = o.isos[0]?.volid ?? ''
      if (!o.containerTemplates.some((v) => v.volid === f.osTemplate))
        f.osTemplate = o.containerTemplates[0]?.volid ?? ''
    }
    error.value = ''
  } catch (e) {
    error.value = errorMessage(i18n, e)
  }
}

// The storages, images and bridges depend on the node.
watch(targetNode, (n, old) => {
  if (n && old !== undefined && n !== options.value?.node) void loadOptions(n)
})

function setKind(k: Kind) {
  const f = form.value
  const o = options.value
  if (!f || !o || f.kind === k) return
  f.kind = k
  if (k === 'clone') f.disk = ''
  else if (f.disk === '') f.disk = String(o.defaults.disk)
  for (const n of f.networks) {
    if (k === 'iso') n.ipv4Mode = 'none'
    else if (n.ipv4Mode === 'none') n.ipv4Mode = 'dhcp'
  }
  errors.value = {}
}

function check(fields: (keyof CreateForm)[]): boolean {
  if (!form.value || !options.value) return false
  const all = validate(form.value, options.value)
  const found: FormErrors = {}
  for (const f of fields) {
    if (f === 'networks') {
      if (all.networks) found.networks = all.networks
    } else if (all[f]) {
      found[f] = all[f]
    }
  }
  errors.value = found
  return Object.keys(found).length === 0
}

function next() {
  if (!check(STEP_FIELDS[step.value])) return
  step.value = STEPS[stepIndex.value + 1] ?? step.value
}

function back() {
  errors.value = {}
  step.value = STEPS[stepIndex.value - 1] ?? step.value
}

async function poll() {
  const j = job.value
  if (!j || j.state !== 'running') return
  try {
    job.value = await unwrap(
      api.GET('/api/v1/proxmox/clusters/{cluster}/jobs/{id}', {
        params: { path: { cluster: cluster.value, id: j.id } },
      }),
    )
  } catch {
    // Keep polling; the job runs on the server.
  }
  if (job.value?.state === 'running') timer = setTimeout(poll, POLL_MS)
}

async function submit() {
  const f = form.value
  const o = options.value
  if (!f || !o) return
  const all = validate(f, o)
  if (Object.keys(all).length > 0) {
    // Go to the first step with an error.
    const s = STEPS.find((x) => STEP_FIELDS[x].some((k) => all[k])) ?? 'review'
    step.value = s
    check(STEP_FIELDS[s])
    return
  }
  busy.value = true
  submitError.value = ''
  try {
    job.value = await unwrap(
      api.POST('/api/v1/proxmox/clusters/{cluster}/guests', {
        params: { path: { cluster: cluster.value } },
        body: toRequest(f),
      }),
    )
    f.password = ''
    timer = setTimeout(poll, POLL_MS)
  } catch (e) {
    submitError.value =
      e instanceof ApiError && e.code === 'invalid_request'
        ? t('proxmox.create.invalid', { message: e.message })
        : errorMessage(i18n, e)
  } finally {
    busy.value = false
  }
}

function again() {
  clearTimeout(timer)
  job.value = null
  step.value = 'source'
  if (options.value) form.value = newForm(options.value, form.value?.kind)
}

const jobBadge = { running: 'warning', done: 'ok', failed: 'error' } as const
const stepBadge = {
  pending: 'unknown',
  running: 'warning',
  done: 'ok',
  failed: 'error',
  skipped: 'unknown',
} as const

function keyCount(keys: string): number {
  return keys.split('\n').filter((l) => l.trim() !== '').length
}

/** The summary of the review step. */
const summary = computed(() => {
  const f = form.value
  if (!f) return []
  const rows: [string, string][] = [
    [t('proxmox.create.kind'), t(`proxmox.create.kinds.${f.kind}`)],
    [t('proxmox.node'), targetNode.value],
    [t('proxmox.name'), f.name],
    [t('proxmox.vmid'), f.vmid || t('proxmox.create.vmidAuto')],
  ]
  if (f.kind === 'clone') {
    const tpl = templateOptions.value.find((x) => x.value === f.template)
    rows.push([t('proxmox.create.template'), tpl?.label ?? f.template])
    rows.push([
      t('proxmox.create.cloneMode'),
      f.full ? t('proxmox.create.full') : t('proxmox.create.linked'),
    ])
  }
  if (f.kind === 'iso') rows.push([t('proxmox.create.iso'), f.iso])
  if (f.kind === 'lxc') rows.push([t('proxmox.create.osTemplate'), f.osTemplate])
  rows.push([t('proxmox.create.cores'), f.cores])
  rows.push([t('proxmox.create.memory'), `${f.memory} MiB`])
  rows.push([t('proxmox.create.disk'), f.disk ? `${f.disk} GiB` : t('proxmox.create.diskTemplate')])
  if (f.kind !== 'clone' || f.full) rows.push([t('proxmox.create.storage'), f.storage])
  f.networks.forEach((n, i) => {
    const parts = [n.vlan ? `${n.bridge}, VLAN ${n.vlan}` : n.bridge, n.mac]
    if (f.kind !== 'iso') {
      const ip4 = n.ipv4Mode === 'static' ? n.ipv4 : t(`proxmox.create.ipModes.${n.ipv4Mode}`)
      const ip6 = n.ipv6Mode === 'static' ? n.ipv6 : t(`proxmox.create.ipModes.${n.ipv6Mode}`)
      parts.push(`IPv4 ${ip4}`, `IPv6 ${ip6}`)
    }
    rows.push([t('proxmox.create.networkN', { n: i }), parts.join(' · ')])
  })
  if (f.kind !== 'iso') {
    if (f.user) rows.push([t('proxmox.create.user'), f.user])
    rows.push([
      t('proxmox.create.login'),
      [
        f.password ? t('proxmox.create.passwordSet') : '',
        keyCount(f.sshKeys) > 0
          ? t('proxmox.create.keys', { n: keyCount(f.sshKeys) }, keyCount(f.sshKeys))
          : '',
      ]
        .filter(Boolean)
        .join(', ') || '–',
    ])
  }
  return rows
})

onMounted(() => loadOptions())
onBeforeUnmount(() => clearTimeout(timer))
</script>

<template>
  <div class="flex max-w-3xl flex-col gap-6">
    <nav :aria-label="t('proxmox.breadcrumb')" class="text-sm">
      <RouterLink :to="{ name: 'proxmox', params: { cluster } }" class="text-primary underline">
        {{ t('nav.proxmox') }} · {{ cluster }}
      </RouterLink>
    </nav>
    <h1 class="text-2xl font-semibold">{{ t('proxmox.create.title') }}</h1>

    <p
      v-if="error"
      role="alert"
      class="rounded-md border border-danger px-3 py-2 text-sm text-danger"
    >
      {{ error }}
    </p>

    <!-- Progress of the job. -->
    <section v-if="job" aria-labelledby="job-title" class="flex flex-col gap-4">
      <div class="flex flex-wrap items-center gap-3">
        <h2 id="job-title" class="text-lg font-semibold">
          {{ t('proxmox.create.jobTitle', { name: job.name, vmid: job.vmid }) }}
        </h2>
        <StatusBadge
          :status="jobBadge[job.state]"
          :label="t(`proxmox.create.jobStates.${job.state}`)"
        />
      </div>
      <ol class="flex flex-col gap-2" aria-live="polite">
        <li
          v-for="s in job.steps"
          :key="s.name"
          class="flex flex-wrap items-center gap-2 rounded-md border border-border bg-surface-raised p-3 text-sm"
        >
          <StatusBadge
            :status="stepBadge[s.state]"
            :label="t(`proxmox.create.stepStates.${s.state}`)"
          />
          <span class="font-medium">{{ t(`proxmox.create.jobSteps.${s.name}`) }}</span>
          <p v-if="s.error" class="w-full text-danger">{{ s.error }}</p>
        </li>
      </ol>
      <p v-if="job.state === 'failed'" role="alert" class="text-sm text-danger">
        {{ t('proxmox.create.failed') }}
      </p>
      <div class="flex flex-wrap gap-2">
        <RouterLink
          v-if="job.state === 'done'"
          :to="{ name: 'proxmox-guest', params: { cluster, type: job.type, vmid: job.vmid } }"
          class="inline-flex items-center rounded-md bg-primary px-4 py-2 font-medium text-primary-fg hover:bg-primary-hover"
        >
          {{ t('proxmox.create.open') }}
        </RouterLink>
        <BaseButton v-if="job.state !== 'running'" variant="secondary" @click="again">
          {{ t('proxmox.create.again') }}
        </BaseButton>
      </div>
    </section>

    <template v-else-if="form && options">
      <ol class="flex flex-wrap gap-2 text-sm" :aria-label="t('proxmox.create.steps')">
        <li
          v-for="(s, i) in STEPS"
          :key="s"
          class="rounded-full border px-3 py-1"
          :class="
            s === step
              ? 'border-primary bg-primary text-primary-fg'
              : i < stepIndex
                ? 'border-border text-fg'
                : 'border-border text-fg-muted'
          "
          :aria-current="s === step ? 'step' : undefined"
        >
          {{ i + 1 }}. {{ t(`proxmox.create.stepNames.${s}`) }}
        </li>
      </ol>

      <form
        :id="`${id}-form`"
        class="flex flex-col gap-4"
        novalidate
        @submit.prevent="step === 'review' ? submit() : next()"
      >
        <!-- 1: what and where -->
        <template v-if="step === 'source'">
          <fieldset class="flex flex-col gap-2">
            <legend class="mb-1 text-sm font-medium">{{ t('proxmox.create.kind') }}</legend>
            <label v-for="k in kinds" :key="k" class="flex items-start gap-2 text-sm">
              <input
                type="radio"
                :name="`${id}-kind`"
                :value="k"
                :checked="form.kind === k"
                class="mt-0.5 size-4"
                @change="setKind(k)"
              />
              <span>
                <span class="font-medium">{{ t(`proxmox.create.kinds.${k}`) }}</span>
                <span class="block text-fg-muted">{{ t(`proxmox.create.kindHints.${k}`) }}</span>
              </span>
            </label>
          </fieldset>
          <template v-if="form.kind === 'clone'">
            <BaseSelect
              v-model="form.template"
              :label="t('proxmox.create.template')"
              :options="templateOptions"
            />
            <label class="flex items-center gap-2 text-sm">
              <input v-model="form.full" type="checkbox" class="size-4" />
              {{ t('proxmox.create.fullClone') }}
            </label>
            <p class="-mt-2 text-sm text-fg-muted">{{ t('proxmox.create.fullCloneHint') }}</p>
          </template>
          <template v-else>
            <BaseSelect v-model="form.node" :label="t('proxmox.node')" :options="nodeOptions" />
            <template v-if="form.kind === 'iso'">
              <BaseSelect
                v-model="form.iso"
                :label="t('proxmox.create.iso')"
                :options="isoOptions"
              />
              <p v-if="err('iso')" class="-mt-3 text-sm text-danger">{{ err('iso') }}</p>
              <BaseSelect
                v-model="form.osType"
                :label="t('proxmox.create.osType')"
                :options="osOptions"
              />
            </template>
            <template v-else>
              <BaseSelect
                v-model="form.osTemplate"
                :label="t('proxmox.create.osTemplate')"
                :options="ctOptions"
              />
              <p v-if="err('osTemplate')" class="-mt-3 text-sm text-danger">
                {{ err('osTemplate') }}
              </p>
            </template>
          </template>
          <BaseInput
            v-model="form.name"
            :label="t('proxmox.create.hostname')"
            :hint="t('proxmox.create.hostnameHint')"
            :error="err('name')"
            required
          />
          <BaseInput
            v-model="form.vmid"
            :label="t('proxmox.create.vmid')"
            :hint="vmidHint"
            :error="err('vmid')"
            inputmode="numeric"
          />
        </template>

        <!-- 2: resources -->
        <template v-else-if="step === 'resources'">
          <BaseInput
            v-model="form.cores"
            :label="t('proxmox.create.cores')"
            :hint="t('proxmox.create.max', { max: options.limits.cores })"
            :error="err('cores')"
            inputmode="numeric"
            required
          />
          <BaseInput
            v-model="form.memory"
            :label="t('proxmox.create.memoryMiB')"
            :hint="t('proxmox.create.memoryHint', { max: options.limits.memory })"
            :error="err('memory')"
            inputmode="numeric"
            required
          />
          <BaseInput
            v-model="form.disk"
            :label="t('proxmox.create.diskGiB')"
            :hint="
              form.kind === 'clone'
                ? t('proxmox.create.diskCloneHint', { max: options.limits.disk })
                : t('proxmox.create.max', { max: options.limits.disk })
            "
            :error="err('disk')"
            inputmode="numeric"
          />
          <template v-if="form.kind !== 'clone' || form.full">
            <BaseSelect
              v-model="form.storage"
              :label="t('proxmox.create.storage')"
              :options="storageOptions"
            />
            <p v-if="err('storage')" class="-mt-3 text-sm text-danger">{{ err('storage') }}</p>
          </template>
          <p v-else class="text-sm text-fg-muted">{{ t('proxmox.create.linkedStorage') }}</p>
        </template>

        <!-- 3: network and login -->
        <template v-else-if="step === 'network'">
          <p v-if="form.kind === 'iso'" class="text-sm text-fg-muted">
            {{ t('proxmox.create.isoNetworkHint') }}
          </p>
          <p v-else-if="form.kind === 'clone'" class="text-sm text-fg-muted">
            {{ t('proxmox.create.cloudInitHint') }}
          </p>
          <fieldset
            v-for="(n, i) in form.networks"
            :key="i"
            class="flex flex-col gap-4 rounded-md border border-border p-4"
            :data-testid="`network-${i}`"
          >
            <legend class="px-1 font-semibold">{{ t('proxmox.create.networkN', { n: i }) }}</legend>
            <div class="grid gap-4 sm:grid-cols-2">
              <div class="flex flex-col gap-1">
                <BaseSelect
                  v-model="n.bridge"
                  :label="t('proxmox.create.bridge')"
                  :options="bridgeOptions"
                />
                <p v-if="netErr(i, 'bridge')" class="text-sm text-danger">
                  {{ netErr(i, 'bridge') }}
                </p>
              </div>
              <BaseInput
                v-model="n.vlan"
                :label="t('proxmox.create.vlan')"
                :hint="vlanHint"
                :error="netErr(i, 'vlan')"
                inputmode="numeric"
                :disabled="options.vlans.length === 0"
              />
              <BaseInput
                v-model="n.mac"
                :label="t('proxmox.create.mac')"
                :hint="t('proxmox.create.macHint')"
                :error="netErr(i, 'mac')"
              />
              <div />
              <template v-if="form.kind !== 'iso'">
                <BaseSelect
                  v-model="n.ipv4Mode"
                  :label="t('proxmox.create.ipv4')"
                  :options="ipv4Options"
                />
                <div />
                <template v-if="n.ipv4Mode === 'static'">
                  <BaseInput
                    v-model="n.ipv4"
                    :label="t('proxmox.create.address')"
                    placeholder="192.0.2.10/24"
                    :error="netErr(i, 'ipv4')"
                  />
                  <BaseInput
                    v-model="n.gateway4"
                    :label="t('proxmox.create.gateway')"
                    placeholder="192.0.2.1"
                    :error="netErr(i, 'gateway4')"
                  />
                </template>
                <BaseSelect
                  v-model="n.ipv6Mode"
                  :label="t('proxmox.create.ipv6')"
                  :options="ipv6Options"
                />
                <div />
                <template v-if="n.ipv6Mode === 'static'">
                  <BaseInput
                    v-model="n.ipv6"
                    :label="t('proxmox.create.address')"
                    placeholder="2001:db8::10/64"
                    :error="netErr(i, 'ipv6')"
                  />
                  <BaseInput
                    v-model="n.gateway6"
                    :label="t('proxmox.create.gateway')"
                    placeholder="2001:db8::1"
                    :error="netErr(i, 'gateway6')"
                  />
                </template>
              </template>
            </div>
            <div v-if="form.networks.length > 1">
              <BaseButton variant="secondary" size="sm" @click="removeNetwork(i)">
                {{ t('proxmox.create.removeNetwork', { n: i }) }}
              </BaseButton>
            </div>
          </fieldset>
          <div v-if="form.networks.length < MAX_NETWORKS">
            <BaseButton variant="secondary" @click="addNetwork">
              {{ t('proxmox.create.addNetwork') }}
            </BaseButton>
          </div>
          <template v-if="form.kind !== 'iso'">
            <div class="grid gap-4 sm:grid-cols-2">
              <BaseInput
                v-model="form.nameservers"
                :label="t('proxmox.create.nameservers')"
                :hint="t('proxmox.create.nameserversHint')"
                :error="err('nameservers')"
              />
              <BaseInput
                v-model="form.searchDomain"
                :label="t('proxmox.create.searchDomain')"
                :error="err('searchDomain')"
              />
            </div>
            <h2 class="text-lg font-semibold">{{ t('proxmox.create.login') }}</h2>
            <BaseInput
              v-if="form.kind === 'clone'"
              v-model="form.user"
              :label="t('proxmox.create.user')"
              :hint="t('proxmox.create.userHint')"
              :error="err('user')"
            />
            <BaseInput
              v-model="form.password"
              type="password"
              autocomplete="new-password"
              :label="
                form.kind === 'lxc'
                  ? t('proxmox.create.rootPassword')
                  : t('proxmox.create.password')
              "
              :error="err('password')"
            />
            <div class="flex flex-col gap-1">
              <label :for="`${id}-keys`" class="text-sm font-medium">
                {{ t('proxmox.create.sshKeys') }}
              </label>
              <textarea
                :id="`${id}-keys`"
                v-model="form.sshKeys"
                rows="3"
                spellcheck="false"
                :aria-invalid="err('sshKeys') ? true : undefined"
                :aria-describedby="`${id}-keys-desc`"
                class="rounded-md border bg-surface px-3 py-2 font-mono text-sm text-fg"
                :class="err('sshKeys') ? 'border-danger' : 'border-border'"
              />
              <p
                :id="`${id}-keys-desc`"
                class="text-sm"
                :class="err('sshKeys') ? 'text-danger' : 'text-fg-muted'"
              >
                {{ err('sshKeys') || t('proxmox.create.sshKeysHint') }}
              </p>
            </div>
          </template>
        </template>

        <!-- 4: review -->
        <template v-else>
          <dl class="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-[max-content_1fr]">
            <template v-for="[k, v] in summary" :key="k">
              <dt class="text-fg-muted">{{ k }}</dt>
              <dd class="break-all">{{ v }}</dd>
            </template>
          </dl>
          <BaseInput
            v-model="form.tags"
            :label="t('proxmox.tags')"
            :hint="t('proxmox.create.tagsHint')"
            :error="err('tags')"
          />
          <BaseInput v-model="form.description" :label="t('proxmox.create.description')" />
          <label class="flex items-center gap-2 text-sm">
            <input v-model="form.start" type="checkbox" class="size-4" />
            {{ t('proxmox.create.start') }}
          </label>
          <p v-if="options.pool" class="text-sm text-fg-muted">
            {{ t('proxmox.create.poolHint', { pool: options.pool }) }}
          </p>
          <p v-if="submitError" role="alert" class="text-sm text-danger">{{ submitError }}</p>
        </template>

        <div class="flex flex-wrap justify-between gap-2 border-t border-border pt-4">
          <BaseButton v-if="stepIndex > 0" variant="secondary" @click="back">
            {{ t('proxmox.create.back') }}
          </BaseButton>
          <span v-else />
          <BaseButton type="submit" :loading="busy">
            {{ step === 'review' ? t('proxmox.create.submit') : t('proxmox.create.next') }}
          </BaseButton>
        </div>
      </form>
    </template>
    <p v-else-if="!error" class="text-fg-muted">{{ t('common.loading') }}</p>
  </div>
</template>
