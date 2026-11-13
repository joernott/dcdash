<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, nextTick, ref, useTemplateRef } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { errorMessage } from '@/api/errors'
import BaseButton from '@/components/ui/BaseButton.vue'
import BaseInput from '@/components/ui/BaseInput.vue'
import AuthLayout from '@/layouts/AuthLayout.vue'
import { safeRedirect } from '@/router'
import { useSessionStore } from '@/stores/session'

const session = useSessionStore()
const router = useRouter()
const route = useRoute()
const i18n = useI18n()
const { t } = i18n

const username = ref('')
const password = ref('')
const code = ref('')
const useRecovery = ref(false)
const error = ref('')
const busy = ref(false)
const codeInput = useTemplateRef<InstanceType<typeof BaseInput>>('codeInput')

// The TOTP step is shown when the password was verified in this or an earlier page load.
const totpStep = computed(() => session.stage === 'totp')

async function next() {
  if (session.stage === 'totp_enroll') {
    await router.push({ name: 'enroll', query: route.query })
  } else if (session.authenticated) {
    await router.push(safeRedirect(route.query.redirect))
  } else {
    await nextTick()
    codeInput.value?.focus()
  }
}

async function run(action: () => Promise<void>) {
  error.value = ''
  busy.value = true
  try {
    await action()
    await next()
  } catch (e) {
    error.value = errorMessage(i18n, e)
  } finally {
    busy.value = false
  }
}

function submitPassword() {
  return run(async () => {
    try {
      await session.login(username.value.trim(), password.value)
    } finally {
      password.value = ''
    }
  })
}

function submitCode() {
  return run(async () => {
    const value = code.value.trim()
    code.value = ''
    await session.verify(useRecovery.value ? { recoveryCode: value } : { code: value })
  })
}

function restart() {
  return run(() => session.logout())
}
</script>

<template>
  <AuthLayout :title="totpStep ? t('login.totpTitle') : t('login.title')">
    <p
      v-if="error"
      role="alert"
      class="mb-4 rounded-md border border-danger px-3 py-2 text-sm text-danger"
    >
      {{ error }}
    </p>

    <form v-if="!totpStep" class="flex flex-col gap-4" @submit.prevent="submitPassword">
      <BaseInput v-model="username" :label="t('login.username')" autocomplete="username" required />
      <BaseInput
        v-model="password"
        :label="t('login.password')"
        type="password"
        autocomplete="current-password"
        required
      />
      <BaseButton type="submit" :loading="busy">{{ t('login.submit') }}</BaseButton>
    </form>

    <form v-else class="flex flex-col gap-4" @submit.prevent="submitCode">
      <p class="text-sm text-fg-muted">
        {{ useRecovery ? t('login.recoveryHint') : t('login.totpHint') }}
      </p>
      <BaseInput
        ref="codeInput"
        v-model="code"
        :label="useRecovery ? t('login.recoveryCode') : t('login.code')"
        :inputmode="useRecovery ? 'text' : 'numeric'"
        autocomplete="one-time-code"
        required
      />
      <BaseButton type="submit" :loading="busy">{{ t('login.verify') }}</BaseButton>
      <div class="flex flex-wrap justify-between gap-2 text-sm">
        <button
          type="button"
          class="text-primary hover:underline"
          @click="useRecovery = !useRecovery"
        >
          {{ useRecovery ? t('login.useCode') : t('login.useRecovery') }}
        </button>
        <button type="button" class="text-fg-muted hover:underline" @click="restart">
          {{ t('login.restart') }}
        </button>
      </div>
    </form>
  </AuthLayout>
</template>
