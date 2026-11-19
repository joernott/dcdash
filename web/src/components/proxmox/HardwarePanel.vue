<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, onMounted, ref, useId, useTemplateRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { api, unwrap, type Schemas } from '@/api/client'
import { errorMessage } from '@/api/errors'
import DiskDialog, { type DiskChange } from '@/components/proxmox/DiskDialog.vue'
import NetworkDialog from '@/components/proxmox/NetworkDialog.vue'
import OptionsDialog from '@/components/proxmox/OptionsDialog.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import BaseInput from '@/components/ui/BaseInput.vue'
import BaseModal from '@/components/ui/BaseModal.vue'
import DataTable from '@/components/ui/DataTable.vue'
import {
  hasPending,
  type Hardware,
  type HardwareDisk,
  type NetworkDevice,
  type OptionSetting,
} from '@/proxmox/hardware'
import { useProxmoxTasksStore } from '@/stores/proxmoxTasks'
import { useToastStore } from '@/stores/toast'

/**
 * Options, disks and network devices of a VM, with the dialogs to change
 * them. Every change sends the digest of the configuration it was made on.
 */
const props = defineProps<{
  cluster: string
  vmid: number
  name: string
  /** write on the VM. */
  canWrite: boolean
  /** admin on the VM (deleting disks). */
  canAdmin: boolean
}>()
const emit = defineEmits<{ changed: [] }>()

const i18n = useI18n()
const { t } = i18n
const id = useId()
const toast = useToastStore()
const tasks = useProxmoxTasksStore()
const optionsDialog = useTemplateRef<InstanceType<typeof OptionsDialog>>('optionsDialog')
const diskDialog = useTemplateRef<InstanceType<typeof DiskDialog>>('diskDialog')
const netDialog = useTemplateRef<InstanceType<typeof NetworkDialog>>('netDialog')

const hw = ref<Hardware | null>(null)
const error = ref('')
const editable = computed(() => props.canWrite && !!hw.value?.configurable)

/** A confirmation for detaching or deleting a disk or removing a device. */
const confirm = ref<{
  title: string
  text: string
  /** What the user has to type; empty: no typing. */
  expect: string
  action: string
  danger: boolean
  run: () => Promise<void>
} | null>(null)
const confirmOpen = ref(false)
const confirmInput = ref('')
const confirmBusy = ref(false)
const confirmError = ref('')

const path = computed(() => ({
  cluster: props.cluster,
  type: 'qemu' as const,
  vmid: props.vmid,
}))

async function load() {
  try {
    hw.value = await unwrap(
      api.GET('/api/v1/proxmox/clusters/{cluster}/guests/{type}/{vmid}/hardware', {
        params: { path: path.value },
      }),
    )
    error.value = ''
  } catch (e) {
    error.value = errorMessage(i18n, e)
  }
}

/**
 * Reports the result of a change: done, or still running in a task that is
 * followed in the task list. Errors are thrown to the dialog.
 */
async function finish(
  label: string,
  request: Promise<{ data?: Schemas['ProxmoxConfigResult']; error?: unknown; response: Response }>,
) {
  const res = await unwrap(request)
  if (res.done) {
    toast.success(t('proxmox.hardware.done', { change: label, name: props.name }))
  } else if (res.upid) {
    const task = t('proxmox.taskLabel', { name: `${props.name} (${props.vmid})`, action: label })
    toast.info(t('proxmox.taskStarted', { task }))
    void tasks.track(props.cluster, res.upid, task, () => void load())
  }
  await load()
  emit('changed')
}

const digest = () => hw.value?.digest ?? ''

async function saveOptions(c: { set: Record<string, string>; delete: string[] }) {
  await finish(
    t('proxmox.hardware.changeOptions'),
    api.POST('/api/v1/proxmox/clusters/{cluster}/guests/{type}/{vmid}/hardware/options', {
      params: { path: path.value },
      body: { digest: digest(), set: c.set, delete: c.delete },
    }),
  )
}

async function revert(keys: string[]) {
  try {
    await finish(
      t('proxmox.hardware.revert'),
      api.POST('/api/v1/proxmox/clusters/{cluster}/guests/{type}/{vmid}/hardware/options', {
        params: { path: path.value },
        body: { digest: digest(), revert: keys },
      }),
    )
  } catch (e) {
    toast.error(errorMessage(i18n, e))
  }
}

async function saveDisk(c: DiskChange) {
  const base = '/api/v1/proxmox/clusters/{cluster}/guests/{type}/{vmid}/hardware/disks' as const
  switch (c.mode) {
    case 'create':
      return finish(
        t('proxmox.hardware.addDisk'),
        api.POST(base, { params: { path: path.value }, body: { ...c.body, digest: digest() } }),
      )
    case 'edit':
      return finish(
        t('proxmox.hardware.changeDisk', { disk: c.disk }),
        api.PUT(`${base}/{disk}`, {
          params: { path: { ...path.value, disk: c.disk } },
          body: { ...c.body, digest: digest() },
        }),
      )
    case 'attach':
      return finish(
        t('proxmox.hardware.attach'),
        api.POST(`${base}/{disk}/attach`, {
          params: { path: { ...path.value, disk: c.disk } },
          body: { digest: digest(), bus: c.bus },
        }),
      )
  }
}

async function saveNetwork(key: string, body: Schemas['ProxmoxNetworkRequest']) {
  const base = '/api/v1/proxmox/clusters/{cluster}/guests/{type}/{vmid}/hardware/networks' as const
  if (!key) {
    return finish(
      t('proxmox.hardware.addNetwork'),
      api.POST(base, { params: { path: path.value }, body: { ...body, digest: digest() } }),
    )
  }
  return finish(
    t('proxmox.hardware.changeNetwork', { net: key }),
    api.PUT(`${base}/{net}`, {
      params: { path: { ...path.value, net: key } },
      body: { ...body, digest: digest() },
    }),
  )
}

function ask(c: NonNullable<typeof confirm.value>) {
  confirm.value = c
  confirmInput.value = ''
  confirmError.value = ''
  confirmOpen.value = true
}

async function confirmed() {
  const c = confirm.value
  if (!c || (c.expect && confirmInput.value.trim() !== c.expect)) return
  confirmBusy.value = true
  confirmError.value = ''
  try {
    await c.run()
    confirmOpen.value = false
  } catch (e) {
    confirmError.value = errorMessage(i18n, e)
  } finally {
    confirmBusy.value = false
  }
}

function askDetach(d: HardwareDisk) {
  ask({
    title: t('proxmox.hardware.detachTitle'),
    text: t('proxmox.hardware.detachText', { disk: d.key, volume: d.volume }),
    expect: '',
    action: t('proxmox.hardware.detach'),
    danger: false,
    run: () =>
      finish(
        t('proxmox.hardware.detach'),
        api.POST(
          '/api/v1/proxmox/clusters/{cluster}/guests/{type}/{vmid}/hardware/disks/{disk}/detach',
          {
            params: { path: { ...path.value, disk: d.key } },
            body: { digest: digest() },
          },
        ),
      ),
  })
}

function askDeleteDisk(d: HardwareDisk) {
  ask({
    title: t('proxmox.hardware.deleteDiskTitle'),
    text: t('proxmox.hardware.deleteDiskText', { disk: d.key, volume: d.volume }),
    expect: d.key,
    action: t('proxmox.hardware.delete'),
    danger: true,
    run: () =>
      finish(
        t('proxmox.hardware.deleteDisk', { disk: d.key }),
        api.DELETE(
          '/api/v1/proxmox/clusters/{cluster}/guests/{type}/{vmid}/hardware/disks/{disk}',
          {
            params: { path: { ...path.value, disk: d.key } },
            body: { digest: digest(), confirm: d.key },
          },
        ),
      ),
  })
}

function askRemoveNetwork(n: NetworkDevice) {
  ask({
    title: t('proxmox.hardware.removeNetworkTitle'),
    text: t('proxmox.hardware.removeNetworkText', { net: n.key, mac: n.mac }),
    expect: '',
    action: t('proxmox.hardware.remove'),
    danger: true,
    run: () =>
      finish(
        t('proxmox.hardware.removeNetwork', { net: n.key }),
        api.DELETE(
          '/api/v1/proxmox/clusters/{cluster}/guests/{type}/{vmid}/hardware/networks/{net}',
          {
            params: { path: { ...path.value, net: n.key } },
            body: { digest: digest() },
          },
        ),
      ),
  })
}

/** Text of an option value; flags as yes/no, unset as "default". */
function optionText(o: OptionSetting, value: string, set: boolean): string {
  if (!set || value === '') return t('proxmox.hardware.default')
  if (['onboot', 'protection', 'numa'].includes(o.key)) {
    return value === '1' ? t('proxmox.hardware.yes') : t('proxmox.hardware.no')
  }
  if (o.key === 'ostype') {
    const key = `proxmox.hardware.osTypes.${value}`
    return i18n.te(key) ? t(key) : value
  }
  return value
}

function pendingText(o: OptionSetting): string {
  return o.delete ? t('proxmox.hardware.default') : optionText(o, o.pending ?? '', true)
}

const optionColumns = computed(() => [
  { key: 'option', label: t('proxmox.hardware.option'), class: 'w-56' },
  { key: 'value', label: t('proxmox.hardware.value') },
  { key: 'pending', label: t('proxmox.hardware.pending') },
])
const diskColumns = computed(() => [
  { key: 'key', label: t('proxmox.hardware.disk'), class: 'w-24' },
  { key: 'volume', label: t('proxmox.hardware.volume') },
  { key: 'size', label: t('proxmox.hardware.size'), class: 'w-24' },
  { key: 'flags', label: t('proxmox.hardware.diskOptions') },
  ...(editable.value
    ? [{ key: 'actions', label: t('proxmox.actionsColumn'), class: 'w-px whitespace-nowrap' }]
    : []),
])
const netColumns = computed(() => [
  { key: 'key', label: t('proxmox.hardware.device'), class: 'w-24' },
  { key: 'model', label: t('proxmox.hardware.model') },
  { key: 'mac', label: t('proxmox.hardware.mac') },
  { key: 'bridge', label: t('proxmox.create.bridge') },
  { key: 'flags', label: t('proxmox.hardware.netOptions') },
  ...(editable.value
    ? [{ key: 'actions', label: t('proxmox.actionsColumn'), class: 'w-px whitespace-nowrap' }]
    : []),
])

function diskFlags(d: HardwareDisk): string {
  const f: string[] = []
  if (d.discard) f.push(t('proxmox.hardware.discardShort'))
  if (d.ssd) f.push('SSD')
  if (d.iothread) f.push(t('proxmox.hardware.iothreadShort'))
  if (!d.backup) f.push(t('proxmox.hardware.noBackupShort'))
  return f.join(', ')
}

function netFlags(n: NetworkDevice): string {
  const f: string[] = []
  if (n.vlan) f.push(`VLAN ${n.vlan}`)
  if (n.firewall) f.push(t('proxmox.hardware.firewallShort'))
  if (n.linkDown) f.push(t('proxmox.hardware.linkDownShort'))
  return f.join(', ')
}

const pendingKeys = computed(() =>
  hw.value
    ? [
        ...hw.value.options.filter((o) => o.pending !== undefined || o.delete).map((o) => o.key),
        ...hw.value.disks.filter((d) => d.pending !== undefined || d.delete).map((d) => d.key),
        ...hw.value.networks.filter((n) => n.pending !== undefined || n.delete).map((n) => n.key),
      ]
    : [],
)

watch(() => [props.cluster, props.vmid], load)
onMounted(load)
defineExpose({ load })
</script>

<template>
  <section aria-labelledby="hardware-title" class="flex flex-col gap-4">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <h2 id="hardware-title" class="text-lg font-semibold">{{ t('proxmox.hardware.title') }}</h2>
      <BaseButton
        v-if="editable && hw && pendingKeys.length"
        variant="secondary"
        size="sm"
        @click="revert(pendingKeys)"
      >
        {{ t('proxmox.hardware.revertAll') }}
      </BaseButton>
    </div>
    <p
      v-if="error"
      role="alert"
      class="rounded-md border border-danger px-3 py-2 text-sm text-danger"
    >
      {{ error }}
    </p>
    <template v-if="hw">
      <p
        v-if="hasPending(hw)"
        role="status"
        class="rounded-md border border-warning px-3 py-2 text-sm"
        data-testid="pending-hint"
      >
        {{ t('proxmox.hardware.pendingHint') }}
      </p>
      <p v-if="canWrite && !hw.configurable" class="text-sm text-fg-muted">
        {{ t('proxmox.hardware.notConfigurable') }}
      </p>

      <div class="flex flex-col gap-2">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <h3 :id="`${id}-options`" class="font-semibold">
            {{ t('proxmox.hardware.optionsTitle') }}
          </h3>
          <BaseButton
            v-if="editable"
            variant="secondary"
            size="sm"
            @click="optionsDialog?.edit(hw)"
          >
            {{ t('proxmox.hardware.editOptions') }}
          </BaseButton>
        </div>
        <DataTable
          :columns="optionColumns"
          :rows="hw.options"
          :row-key="(o: OptionSetting) => o.key"
          :caption="t('proxmox.hardware.optionsTitle')"
        >
          <template #cell-option="{ row }">
            {{ t(`proxmox.hardware.options.${(row as OptionSetting).key}`) }}
          </template>
          <template #cell-value="{ row }">
            <span
              class="whitespace-pre-wrap"
              :class="{ 'text-fg-muted': !(row as OptionSetting).set }"
            >
              {{
                optionText(
                  row as OptionSetting,
                  (row as OptionSetting).value,
                  (row as OptionSetting).set,
                )
              }}
            </span>
          </template>
          <template #cell-pending="{ row }">
            <span
              v-if="(row as OptionSetting).pending !== undefined || (row as OptionSetting).delete"
              class="font-medium"
            >
              {{ pendingText(row as OptionSetting) }}
            </span>
          </template>
        </DataTable>
      </div>

      <div class="flex flex-col gap-2">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <h3 class="font-semibold">{{ t('proxmox.hardware.disks') }}</h3>
          <BaseButton v-if="editable" variant="secondary" size="sm" @click="diskDialog?.create(hw)">
            {{ t('proxmox.hardware.addDisk') }}
          </BaseButton>
        </div>
        <DataTable
          :columns="diskColumns"
          :rows="hw.disks"
          :row-key="(d: HardwareDisk) => d.key"
          :caption="t('proxmox.hardware.disks')"
          :empty="t('proxmox.hardware.noDisks')"
        >
          <template #cell-key="{ row }">
            <span class="font-mono">{{ (row as HardwareDisk).key }}</span>
            <span v-if="(row as HardwareDisk).bus === 'unused'" class="block text-xs text-fg-muted">
              {{ t('proxmox.hardware.unused') }}
            </span>
            <span
              v-if="
                !(row as HardwareDisk).active ||
                (row as HardwareDisk).pending !== undefined ||
                (row as HardwareDisk).delete
              "
              class="block text-xs font-medium"
            >
              {{
                (row as HardwareDisk).delete
                  ? t('proxmox.hardware.pendingRemoval')
                  : t('proxmox.hardware.pending')
              }}
            </span>
          </template>
          <template #cell-volume="{ row }">
            <span class="font-mono text-xs break-all">{{ (row as HardwareDisk).volume }}</span>
          </template>
          <template #cell-flags="{ row }">{{ diskFlags(row as HardwareDisk) }}</template>
          <template #cell-actions="{ row }">
            <div
              class="flex gap-1.5"
              role="group"
              :aria-label="t('proxmox.hardware.actionsFor', { item: (row as HardwareDisk).key })"
            >
              <template v-if="(row as HardwareDisk).bus === 'unused'">
                <BaseButton
                  size="sm"
                  variant="secondary"
                  @click="diskDialog?.attach(hw, row as HardwareDisk)"
                >
                  {{ t('proxmox.hardware.attach') }}
                </BaseButton>
              </template>
              <template v-else>
                <BaseButton
                  size="sm"
                  variant="secondary"
                  @click="diskDialog?.edit(hw, row as HardwareDisk)"
                >
                  {{ t('proxmox.hardware.edit') }}
                </BaseButton>
                <BaseButton size="sm" variant="secondary" @click="askDetach(row as HardwareDisk)">
                  {{ t('proxmox.hardware.detach') }}
                </BaseButton>
              </template>
              <BaseButton
                v-if="canAdmin"
                size="sm"
                variant="danger"
                :disabled="(row as HardwareDisk).bus !== 'unused' && hw.running"
                :title="
                  (row as HardwareDisk).bus !== 'unused' && hw.running
                    ? t('proxmox.hardware.deleteRunning')
                    : undefined
                "
                @click="askDeleteDisk(row as HardwareDisk)"
              >
                {{ t('proxmox.hardware.delete') }}
              </BaseButton>
            </div>
          </template>
        </DataTable>
      </div>

      <div class="flex flex-col gap-2">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <h3 class="font-semibold">{{ t('proxmox.hardware.networks') }}</h3>
          <BaseButton v-if="editable" variant="secondary" size="sm" @click="netDialog?.create(hw)">
            {{ t('proxmox.hardware.addNetwork') }}
          </BaseButton>
        </div>
        <DataTable
          :columns="netColumns"
          :rows="hw.networks"
          :row-key="(n: NetworkDevice) => n.key"
          :caption="t('proxmox.hardware.networks')"
          :empty="t('proxmox.hardware.noNetworks')"
        >
          <template #cell-key="{ row }">
            <span class="font-mono">{{ (row as NetworkDevice).key }}</span>
            <span
              v-if="
                !(row as NetworkDevice).active ||
                (row as NetworkDevice).pending !== undefined ||
                (row as NetworkDevice).delete
              "
              class="block text-xs font-medium"
            >
              {{
                (row as NetworkDevice).delete
                  ? t('proxmox.hardware.pendingRemoval')
                  : t('proxmox.hardware.pending')
              }}
            </span>
          </template>
          <template #cell-mac="{ row }">
            <span class="font-mono">{{ (row as NetworkDevice).mac }}</span>
          </template>
          <template #cell-flags="{ row }">{{ netFlags(row as NetworkDevice) }}</template>
          <template #cell-actions="{ row }">
            <div
              class="flex gap-1.5"
              role="group"
              :aria-label="t('proxmox.hardware.actionsFor', { item: (row as NetworkDevice).key })"
            >
              <BaseButton
                size="sm"
                variant="secondary"
                @click="netDialog?.edit(hw, row as NetworkDevice)"
              >
                {{ t('proxmox.hardware.edit') }}
              </BaseButton>
              <BaseButton
                size="sm"
                variant="danger"
                @click="askRemoveNetwork(row as NetworkDevice)"
              >
                {{ t('proxmox.hardware.remove') }}
              </BaseButton>
            </div>
          </template>
        </DataTable>
      </div>
    </template>

    <OptionsDialog ref="optionsDialog" :run="saveOptions" />
    <DiskDialog ref="diskDialog" :run="saveDisk" />
    <NetworkDialog ref="netDialog" :run="saveNetwork" />
    <BaseModal v-model:open="confirmOpen" :title="confirm?.title ?? ''">
      <form :id="`${id}-confirm`" class="flex flex-col gap-3" @submit.prevent="confirmed">
        <p>{{ confirm?.text }}</p>
        <BaseInput
          v-if="confirm?.expect"
          v-model="confirmInput"
          :label="t('proxmox.hardware.typeToConfirm', { value: confirm.expect })"
          autocomplete="off"
        />
        <p v-if="confirmError" role="alert" class="text-sm text-danger">{{ confirmError }}</p>
      </form>
      <template #footer>
        <BaseButton variant="secondary" @click="confirmOpen = false">{{
          t('common.cancel')
        }}</BaseButton>
        <BaseButton
          :variant="confirm?.danger ? 'danger' : 'primary'"
          type="submit"
          :form="`${id}-confirm`"
          :loading="confirmBusy"
          :disabled="!!confirm?.expect && confirmInput.trim() !== confirm.expect"
        >
          {{ confirm?.action }}
        </BaseButton>
      </template>
    </BaseModal>
  </section>
</template>
