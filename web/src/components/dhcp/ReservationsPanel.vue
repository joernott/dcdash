<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, onMounted, ref, useTemplateRef } from 'vue'
import { useI18n } from 'vue-i18n'

import { api, unwrap } from '@/api/client'
import { ApiError } from '@/api/client'
import { errorMessage } from '@/api/errors'
import ReservationDialog from '@/components/dhcp/ReservationDialog.vue'
import AppIcon from '@/components/ui/AppIcon.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import IconButton from '@/components/ui/IconButton.vue'
import BaseInput from '@/components/ui/BaseInput.vue'
import BaseModal from '@/components/ui/BaseModal.vue'
import BaseSelect from '@/components/ui/BaseSelect.vue'
import DataTable from '@/components/ui/DataTable.vue'
import {
  filterReservations,
  GLOBAL_RESOURCE,
  subnetPermission,
  type DhcpReservation,
  type DhcpServer,
  type DhcpSubnet,
} from '@/dhcp/dhcp'
import { useSessionStore } from '@/stores/session'
import { netboxFailure } from '@/netbox/netbox'
import { useToastStore } from '@/stores/toast'

/** The reservations of a server: list, filter, add, edit and delete. */
const props = defineProps<{ server: DhcpServer; subnets: DhcpSubnet[] }>()

const i18n = useI18n()
const { t } = i18n
const session = useSessionStore()
const toast = useToastStore()
const dialog = useTemplateRef<InstanceType<typeof ReservationDialog>>('dialog')

const reservations = ref<DhcpReservation[] | null>(null)
const loading = ref(false)
const error = ref('')
const subnet = ref('')
const text = ref('')

/** Subnets (and global) where the user may add reservations. */
const writableSubnets = computed(() => {
  const all = [{ id: 0, prefix: GLOBAL_RESOURCE }, ...props.subnets]
  return all.filter((s) => session.can(subnetPermission(props.server.name, s.prefix, 'write')))
})
/** Changes need a host database here or on an HA partner (shared database). */
const canAdd = computed(
  () =>
    (props.server.capabilities.reservationWrite || !!props.server.partners?.length) &&
    writableSubnets.value.length > 0,
)
const shown = computed(() => filterReservations(reservations.value ?? [], subnet.value, text.value))
const editable = computed(() => (reservations.value ?? []).some((r) => !r.readOnly))
const subnetOptions = computed(() => [
  { value: '', label: t('dhcp.allSubnets') },
  ...props.subnets.map((s) => ({ value: String(s.id), label: s.prefix })),
])
const columns = computed(() => [
  { key: 'identifier', label: t('dhcp.identifier') },
  { key: 'addresses', label: t('dhcp.addresses') },
  { key: 'hostname', label: t('dhcp.hostname') },
  { key: 'subnet', label: t('dhcp.subnet') },
  { key: 'source', label: t('dhcp.source') },
  ...(editable.value || canAdd.value
    ? [{ key: 'actions', label: t('dhcp.actionsColumn'), class: 'w-px whitespace-nowrap' }]
    : []),
])

async function load() {
  loading.value = true
  try {
    const res = await unwrap(
      api.GET('/api/v1/dhcp/servers/{server}/reservations', {
        params: { path: { server: props.server.name } },
      }),
    )
    reservations.value = res.reservations
    error.value = ''
  } catch (e) {
    reservations.value = null
    error.value = errorMessage(i18n, e)
  } finally {
    loading.value = false
  }
}

function label(r: DhcpReservation): string {
  return r.hostname || r.ipAddresses[0] || r.identifier
}

async function saved(r: DhcpReservation, created: boolean) {
  toast.success(t(created ? 'dhcp.reservationAdded' : 'dhcp.reservationSaved', { name: label(r) }))
  const nb = netboxFailure(r.netbox, t)
  if (nb) toast.error(nb)
  await load()
}

// Delete a reservation.
const deleting = ref<DhcpReservation | null>(null)
const deleteError = ref('')
const busy = ref(false)
const deleteOpen = computed({
  get: () => deleting.value !== null,
  set: (v: boolean) => {
    if (!v) deleting.value = null
  },
})

function askDelete(r: DhcpReservation) {
  deleteError.value = ''
  deleting.value = r
}

async function remove() {
  const r = deleting.value
  if (!r) return
  busy.value = true
  try {
    const res = await unwrap(
      api.DELETE('/api/v1/dhcp/servers/{server}/reservations/{id}', {
        params: { path: { server: props.server.name, id: r.id }, query: { version: r.version } },
      }),
    )
    deleting.value = null
    toast.success(t('dhcp.reservationDeleted', { name: label(r) }))
    const nb = netboxFailure(res?.netbox, t)
    if (nb) toast.error(nb)
    await load()
  } catch (e) {
    deleteError.value = errorMessage(i18n, e)
    if (e instanceof ApiError && e.code === 'conflict') await load()
  } finally {
    busy.value = false
  }
}

onMounted(load)
</script>

<template>
  <section aria-labelledby="dhcp-reservations" class="flex flex-col gap-3">
    <h2 id="dhcp-reservations" class="sr-only">{{ t('dhcp.tabs.reservations') }}</h2>
    <div class="flex flex-wrap items-end justify-between gap-3">
      <form class="flex flex-wrap items-end gap-3" role="search" @submit.prevent>
        <BaseSelect v-model="subnet" :label="t('dhcp.subnet')" :options="subnetOptions" />
        <BaseInput v-model="text" :label="t('dhcp.filterReservations')" type="search" />
      </form>
      <div class="flex gap-3">
        <IconButton
          icon="refresh"
          :label="t('home.refresh')"
          size="md"
          variant="secondary"
          :loading="loading"
          @click="load"
        />
        <BaseButton v-if="canAdd" variant="primary" @click="dialog?.create()">
          {{ t('dhcp.addReservation') }}
        </BaseButton>
      </div>
    </div>
    <p
      v-if="!props.server.capabilities.reservationWrite && !props.server.partners?.length"
      class="text-sm text-fg-muted"
    >
      {{ t('dhcp.missing.hostDatabase') }}
    </p>

    <p
      v-if="error"
      role="alert"
      class="rounded-md border border-danger px-3 py-2 text-sm text-danger"
    >
      {{ error }}
    </p>
    <template v-else-if="reservations">
      <p class="text-sm text-fg-muted" aria-live="polite">
        {{ t('dhcp.shownOf', { shown: shown.length, total: reservations.length }) }}
      </p>
      <DataTable
        system="kea"
        :columns="columns"
        :rows="shown"
        :row-key="(r: DhcpReservation) => r.id"
        :caption="t('dhcp.tabs.reservations')"
        :empty="t('dhcp.noReservations')"
      >
        <template #cell-identifier="{ row }">
          <span class="block text-xs text-fg-muted">
            {{ t(`dhcp.identifierTypes.${(row as DhcpReservation).identifierType}`) }}
          </span>
          <span class="font-mono text-xs break-all">{{ (row as DhcpReservation).identifier }}</span>
        </template>
        <template #cell-addresses="{ row }">
          <ul class="font-mono text-xs">
            <li v-for="a in (row as DhcpReservation).ipAddresses" :key="a">{{ a }}</li>
            <li v-for="p in (row as DhcpReservation).prefixes ?? []" :key="p">{{ p }}</li>
          </ul>
        </template>
        <template #cell-hostname="{ row }">{{ (row as DhcpReservation).hostname || '–' }}</template>
        <template #cell-subnet="{ row }">
          <span class="font-mono text-xs">{{
            (row as DhcpReservation).subnetId === 0
              ? t('dhcp.global')
              : (row as DhcpReservation).subnet
          }}</span>
        </template>
        <template #cell-source="{ row }">
          {{ t(`dhcp.sources.${(row as DhcpReservation).source}`) }}
        </template>
        <template #cell-actions="{ row }">
          <div
            v-if="!(row as DhcpReservation).readOnly"
            class="flex gap-1.5"
            role="group"
            :aria-label="t('dhcp.actionsFor', { name: label(row as DhcpReservation) })"
          >
            <IconButton
              icon="edit"
              :label="t('dhcp.edit')"
              size="sm"
              variant="secondary"
              @click="dialog?.edit(row as DhcpReservation)"
            />
            <IconButton
              icon="delete"
              :label="t('dhcp.delete')"
              size="sm"
              variant="danger"
              @click="askDelete(row as DhcpReservation)"
            />
          </div>
          <span v-else class="text-xs text-fg-muted">{{ t('dhcp.readOnly') }}</span>
        </template>
      </DataTable>
    </template>

    <ReservationDialog
      v-if="canAdd || editable"
      ref="dialog"
      :server="props.server"
      :subnets="writableSubnets"
      @saved="saved"
    />

    <BaseModal v-if="editable" v-model:open="deleteOpen" :title="t('dhcp.deleteReservation')">
      <p>{{ t('dhcp.deleteReservationQuestion', { name: deleting ? label(deleting) : '' }) }}</p>
      <p v-if="deleting" class="mt-2 font-mono text-sm break-all text-fg-muted">
        {{ deleting.identifier }}
      </p>
      <p v-if="deleteError" role="alert" class="mt-2 text-sm text-danger">{{ deleteError }}</p>
      <template #footer>
        <BaseButton variant="secondary" @click="deleteOpen = false">{{
          t('common.cancel')
        }}</BaseButton>
        <BaseButton variant="danger" :loading="busy" @click="remove">
          <AppIcon name="delete" />{{ t('dhcp.delete') }}</BaseButton
        >
      </template>
    </BaseModal>
  </section>
</template>
