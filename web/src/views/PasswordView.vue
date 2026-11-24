<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { api, ApiError, unwrap, type Schemas } from '@/api/client'
import { errorMessage } from '@/api/errors'
import BaseButton from '@/components/ui/BaseButton.vue'
import BaseInput from '@/components/ui/BaseInput.vue'
import BaseSelect from '@/components/ui/BaseSelect.vue'
import AuthLayout from '@/layouts/AuthLayout.vue'

type Directory = Schemas['SelfServiceDirectoryList']['directories'][number]

const i18n = useI18n()
const { t } = i18n

const directories = ref<Directory[]>([])
const loaded = ref(false)
const directory = ref('')
const username = ref('')
const current = ref('')
const next = ref('')
const confirm = ref('')
const error = ref('')
const success = ref(false)
const busy = ref(false)

const selected = computed(() => directories.value.find((d) => d.name === directory.value))
const policy = computed(() => selected.value?.policy)
const options = computed(() =>
  directories.value.map((d) => ({ value: d.name, label: `${d.domain} (${d.name})` })),
)

/** The rules of the password policy, as sentences. */
const rules = computed(() => {
  const p = policy.value
  if (!p) return []
  const out: string[] = []
  if (p.minLength > 0) out.push(t('password.policy.minLength', { n: p.minLength }, p.minLength))
  if (p.complexity) out.push(t('password.policy.complexity'))
  if (p.history > 0) out.push(t('password.policy.history', { n: p.history }, p.history))
  if (p.minAge > 0) {
    const h = Math.ceil(p.minAge / 3600)
    out.push(t('password.policy.minAge', { n: h }, h))
  }
  return out
})

/** A problem the server would report as well, found before sending. */
const localProblem = computed(() => {
  if (!next.value || !confirm.value) return ''
  if (next.value !== confirm.value) return t('password.mismatch')
  if (next.value === current.value) return t('password.errors.same_password')
  const min = policy.value?.minLength ?? 0
  if (next.value.length < min) return t('password.policy.minLength', { n: min }, min)
  return ''
})

onMounted(async () => {
  try {
    directories.value = (await unwrap(api.GET('/api/v1/password/directories'))).directories
    directory.value = directories.value[0]?.name ?? ''
  } catch (e) {
    if (!(e instanceof ApiError && e.status === 404)) error.value = errorMessage(i18n, e)
  } finally {
    loaded.value = true
  }
})

function message(e: unknown): string {
  if (e instanceof ApiError) {
    const key = `password.errors.${e.code}`
    if (i18n.te(key)) return t(key, { message: e.message })
  }
  return errorMessage(i18n, e)
}

async function submit() {
  if (localProblem.value) return
  error.value = ''
  success.value = false
  busy.value = true
  try {
    await unwrap(
      api.POST('/api/v1/password/change', {
        body: {
          directory: directory.value,
          username: username.value.trim(),
          currentPassword: current.value,
          newPassword: next.value,
        },
      }),
    )
    success.value = true
  } catch (e) {
    error.value = message(e)
  } finally {
    current.value = ''
    next.value = ''
    confirm.value = ''
    busy.value = false
  }
}
</script>

<template>
  <AuthLayout :title="t('password.title')">
    <p
      v-if="error"
      role="alert"
      class="mb-4 rounded-md border border-danger px-3 py-2 text-sm text-danger"
    >
      {{ error }}
    </p>
    <p
      v-if="success"
      role="status"
      class="mb-4 rounded-md border border-success px-3 py-2 text-sm text-success"
    >
      {{ t('password.success') }}
    </p>

    <p v-if="loaded && directories.length === 0 && !error" class="text-sm text-fg-muted">
      {{ t('password.none') }}
    </p>

    <form v-else-if="directories.length > 0" class="flex flex-col gap-4" @submit.prevent="submit">
      <p class="text-sm text-fg-muted">{{ t('password.intro') }}</p>
      <BaseSelect
        v-if="directories.length > 1"
        v-model="directory"
        :label="t('password.directory')"
        :options="options"
      />
      <p v-else class="text-sm">
        {{ t('password.directory') }}: <strong>{{ selected?.domain }}</strong>
      </p>
      <BaseInput
        v-model="username"
        :label="t('password.username')"
        autocomplete="username"
        required
      />
      <p class="-mt-3 text-xs text-fg-muted">
        {{ t('password.usernameHint', { domain: selected?.domain ?? '' }) }}
      </p>
      <BaseInput
        v-model="current"
        :label="t('password.current')"
        type="password"
        autocomplete="current-password"
        required
      />
      <BaseInput
        v-model="next"
        :label="t('password.new')"
        type="password"
        autocomplete="new-password"
        required
      />
      <BaseInput
        v-model="confirm"
        :label="t('password.confirm')"
        type="password"
        autocomplete="new-password"
        required
      />
      <p v-if="localProblem" class="-mt-2 text-sm text-danger">{{ localProblem }}</p>
      <div v-if="rules.length" class="rounded-md border border-border px-3 py-2 text-sm">
        <p class="font-medium">{{ t('password.policy.title') }}</p>
        <ul class="ml-5 list-disc text-fg-muted">
          <li v-for="r in rules" :key="r">{{ r }}</li>
        </ul>
      </div>
      <BaseButton type="submit" :loading="busy" :disabled="!!localProblem">
        {{ t('password.submit') }}
      </BaseButton>
    </form>

    <p class="mt-6 text-sm">
      <RouterLink :to="{ name: 'login' }" class="text-primary hover:underline">{{
        t('password.toLogin')
      }}</RouterLink>
    </p>
  </AuthLayout>
</template>
