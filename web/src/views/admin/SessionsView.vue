<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { api, unwrap, type Schemas } from '@/api/client'
import { errorMessage } from '@/api/errors'
import { PERMISSIONS } from '@/auth/permissions'
import BaseButton from '@/components/ui/BaseButton.vue'
import BaseInput from '@/components/ui/BaseInput.vue'
import BaseModal from '@/components/ui/BaseModal.vue'
import DataTable from '@/components/ui/DataTable.vue'
import { useSessionStore } from '@/stores/session'
import { useToastStore } from '@/stores/toast'

type Summary = Schemas['SessionSummary']

/** An action that needs confirmation in the modal. */
interface Pending {
  title: string
  text: string
  run: () => Promise<void>
}

const session = useSessionStore()
const toast = useToastStore()
const i18n = useI18n()
const { t, d } = i18n

const sessions = ref<Summary[]>([])
const userFilter = ref('')
const loading = ref(false)
const error = ref('')
const pending = ref<Pending | null>(null)
const confirmOpen = ref(false)
const busy = ref(false)
const resetUser = ref('')

const canWrite = computed(() => session.can(PERMISSIONS.sessionsWrite))

const columns = computed(() => [
  { key: 'username', label: t('sessions.user') },
  { key: 'stage', label: t('sessions.stage') },
  { key: 'createdAt', label: t('sessions.created'), class: 'whitespace-nowrap' },
  { key: 'lastSeenAt', label: t('sessions.lastSeen'), class: 'whitespace-nowrap' },
  { key: 'remoteAddr', label: t('sessions.client') },
  ...(canWrite.value ? [{ key: 'actions', label: t('sessions.actions') }] : []),
])

async function load() {
  loading.value = true
  error.value = ''
  try {
    const res = await unwrap(
      api.GET('/api/v1/admin/sessions', {
        params: { query: { username: userFilter.value.trim() || undefined } },
      }),
    )
    sessions.value = res.sessions
  } catch (e) {
    error.value = errorMessage(i18n, e)
  } finally {
    loading.value = false
  }
}

function ask(p: Pending) {
  pending.value = p
  confirmOpen.value = true
}

async function confirm() {
  if (!pending.value) return
  busy.value = true
  try {
    await pending.value.run()
    confirmOpen.value = false
  } catch (e) {
    toast.error(errorMessage(i18n, e))
  } finally {
    busy.value = false
  }
}

function terminate(s: Summary) {
  ask({
    title: t('sessions.terminate'),
    text: s.current
      ? t('sessions.terminateOwn')
      : t('sessions.terminateText', { user: s.username }),
    run: async () => {
      await unwrap(api.DELETE('/api/v1/admin/sessions/{id}', { params: { path: { id: s.id } } }))
      toast.success(t('sessions.terminated'))
      await load()
    },
  })
}

function terminateUser(username: string) {
  ask({
    title: t('sessions.terminateAll'),
    text: t('sessions.terminateAllText', { user: username }),
    run: async () => {
      const res = await unwrap(
        api.DELETE('/api/v1/admin/users/{username}/sessions', { params: { path: { username } } }),
      )
      toast.success(t('sessions.terminatedCount', { n: res.terminated }, res.terminated))
      await load()
    },
  })
}

function resetTOTP() {
  const username = resetUser.value.trim()
  if (!username) return
  ask({
    title: t('sessions.resetTotp'),
    text: t('sessions.resetTotpText', { user: username }),
    run: async () => {
      await unwrap(
        api.DELETE('/api/v1/admin/users/{username}/totp', { params: { path: { username } } }),
      )
      toast.success(t('sessions.resetDone', { user: username }))
      resetUser.value = ''
    },
  })
}

onMounted(load)
</script>

<template>
  <div class="flex flex-col gap-6">
    <h1 class="text-2xl font-semibold">{{ t('nav.sessions') }}</h1>

    <form class="flex flex-wrap items-end gap-3" @submit.prevent="load">
      <BaseInput v-model="userFilter" :label="t('sessions.filter')" />
      <BaseButton type="submit" variant="secondary" :loading="loading">{{
        t('audit.apply')
      }}</BaseButton>
      <BaseButton
        v-if="canWrite && userFilter.trim()"
        variant="danger"
        @click="terminateUser(userFilter.trim())"
      >
        {{ t('sessions.terminateAll') }}
      </BaseButton>
    </form>

    <p
      v-if="error"
      role="alert"
      class="rounded-md border border-danger px-3 py-2 text-sm text-danger"
    >
      {{ error }}
    </p>

    <DataTable
      :columns="columns"
      :rows="sessions"
      :row-key="(s: Summary) => s.id"
      :loading="loading && sessions.length === 0"
      :caption="t('nav.sessions')"
    >
      <template #cell-username="{ row }">
        {{ (row as Summary).displayName || (row as Summary).username }}
        <span class="block text-xs text-fg-muted">
          {{ (row as Summary).username
          }}<template v-if="(row as Summary).current"> · {{ t('sessions.current') }}</template>
        </span>
      </template>
      <template #cell-stage="{ row }">{{
        t(`sessions.stages.${(row as Summary).stage}`)
      }}</template>
      <template #cell-createdAt="{ row }">{{
        d(new Date((row as Summary).createdAt), 'short')
      }}</template>
      <template #cell-lastSeenAt="{ row }">{{
        d(new Date((row as Summary).lastSeenAt), 'short')
      }}</template>
      <template #cell-remoteAddr="{ row }">
        {{ (row as Summary).remoteAddr }}
        <span
          class="block max-w-xs truncate text-xs text-fg-muted"
          :title="(row as Summary).userAgent"
        >
          {{ (row as Summary).userAgent }}
        </span>
      </template>
      <template #cell-actions="{ row }">
        <BaseButton size="sm" variant="secondary" @click="terminate(row as Summary)">
          {{ t('sessions.terminate') }}
        </BaseButton>
      </template>
    </DataTable>

    <section
      v-if="session.isRoot"
      class="max-w-xl rounded-lg border border-border bg-surface-raised p-5"
    >
      <h2 class="mb-2 text-lg font-semibold">{{ t('sessions.resetTotp') }}</h2>
      <p class="mb-3 text-sm text-fg-muted">{{ t('sessions.resetTotpHint') }}</p>
      <form class="flex flex-wrap items-end gap-3" @submit.prevent="resetTOTP">
        <BaseInput v-model="resetUser" :label="t('sessions.user')" required />
        <BaseButton type="submit" variant="danger">{{ t('sessions.resetTotp') }}</BaseButton>
      </form>
    </section>

    <BaseModal v-model:open="confirmOpen" :title="pending?.title ?? ''">
      <p>{{ pending?.text }}</p>
      <template #footer>
        <BaseButton variant="ghost" @click="confirmOpen = false">{{
          t('common.cancel')
        }}</BaseButton>
        <BaseButton variant="danger" :loading="busy" @click="confirm">{{
          t('common.confirm')
        }}</BaseButton>
      </template>
    </BaseModal>
  </div>
</template>
