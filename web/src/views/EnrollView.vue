<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { errorMessage } from '@/api/errors'
import QrCode from '@/components/QrCode.vue'
import RecoveryCodes from '@/components/RecoveryCodes.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import BaseInput from '@/components/ui/BaseInput.vue'
import AuthLayout from '@/layouts/AuthLayout.vue'
import { safeRedirect } from '@/router'
import { useSessionStore, type EnrollmentInfo } from '@/stores/session'

const session = useSessionStore()
const router = useRouter()
const route = useRoute()
const i18n = useI18n()
const { t } = i18n

const enrollment = ref<EnrollmentInfo | null>(null)
const recoveryCodes = ref<string[]>([])
const code = ref('')
const error = ref('')
const busy = ref(false)
const showSecret = ref(false)
// Enrollment during login (required by a role) or voluntarily from the account page.
const voluntary = session.authenticated

async function start() {
  error.value = ''
  busy.value = true
  try {
    enrollment.value = await session.startEnrollment()
  } catch (e) {
    error.value = errorMessage(i18n, e)
  } finally {
    busy.value = false
  }
}

async function confirm() {
  error.value = ''
  busy.value = true
  try {
    recoveryCodes.value = await session.confirmEnrollment(code.value.trim())
  } catch (e) {
    error.value = errorMessage(i18n, e)
  } finally {
    code.value = ''
    busy.value = false
  }
}

function done() {
  return router.push(voluntary ? { name: 'account' } : safeRedirect(route.query.redirect))
}

async function cancel() {
  if (voluntary) return router.push({ name: 'account' })
  await session.logout()
  return router.push({ name: 'login' })
}

onMounted(start)
</script>

<template>
  <AuthLayout :title="t('enroll.title')">
    <p
      v-if="error"
      role="alert"
      class="mb-4 rounded-md border border-danger px-3 py-2 text-sm text-danger"
    >
      {{ error }}
    </p>

    <div v-if="recoveryCodes.length > 0" class="flex flex-col gap-4">
      <p class="font-medium text-success">{{ t('enroll.done') }}</p>
      <RecoveryCodes :codes="recoveryCodes" />
      <BaseButton @click="done">{{ t('enroll.continue') }}</BaseButton>
    </div>

    <form v-else-if="enrollment" class="flex flex-col gap-4" @submit.prevent="confirm">
      <p class="text-sm">
        {{ voluntary ? t('enroll.introVoluntary') : t('enroll.introRequired') }}
      </p>
      <ol class="list-decimal space-y-1 ps-5 text-sm">
        <li>{{ t('enroll.step1') }}</li>
        <li>{{ t('enroll.step2') }}</li>
      </ol>
      <div class="flex justify-center">
        <QrCode :value="enrollment.uri" :label="t('enroll.qrLabel')" />
      </div>
      <div class="text-sm">
        <button
          type="button"
          class="text-primary hover:underline"
          :aria-expanded="showSecret"
          @click="showSecret = !showSecret"
        >
          {{ t('enroll.manual') }}
        </button>
        <p v-if="showSecret" class="mt-2 font-mono break-all select-all" data-testid="secret">
          {{ enrollment.secret }}
        </p>
      </div>
      <BaseInput
        v-model="code"
        :label="t('login.code')"
        inputmode="numeric"
        autocomplete="one-time-code"
        required
      />
      <div class="flex gap-2">
        <BaseButton type="submit" :loading="busy">{{ t('enroll.confirm') }}</BaseButton>
        <BaseButton variant="ghost" @click="cancel">{{ t('common.cancel') }}</BaseButton>
      </div>
    </form>

    <div v-else class="flex flex-col gap-4">
      <p class="text-sm text-fg-muted">{{ busy ? t('common.loading') : '' }}</p>
      <BaseButton v-if="error" @click="start">{{ t('common.retry') }}</BaseButton>
      <BaseButton v-if="error" variant="ghost" @click="cancel">{{ t('common.cancel') }}</BaseButton>
    </div>
  </AuthLayout>
</template>
