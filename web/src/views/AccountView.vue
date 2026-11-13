<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { errorMessage } from '@/api/errors'
import PreferenceSwitchers from '@/components/PreferenceSwitchers.vue'
import RecoveryCodes from '@/components/RecoveryCodes.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import BaseInput from '@/components/ui/BaseInput.vue'
import BaseModal from '@/components/ui/BaseModal.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import { useSessionStore } from '@/stores/session'

const session = useSessionStore()
const i18n = useI18n()
const { t, d } = i18n

const info = computed(() => session.info)
const totp = computed(() => info.value?.totp)

const regenOpen = ref(false)
const code = ref('')
const error = ref('')
const busy = ref(false)
const newCodes = ref<string[]>([])

function openRegenerate() {
  code.value = ''
  error.value = ''
  newCodes.value = []
  regenOpen.value = true
}

async function regenerate() {
  error.value = ''
  busy.value = true
  try {
    newCodes.value = await session.regenerateRecoveryCodes(code.value.trim())
  } catch (e) {
    error.value = errorMessage(i18n, e)
  } finally {
    code.value = ''
    busy.value = false
  }
}
</script>

<template>
  <div v-if="info" class="flex max-w-3xl flex-col gap-6">
    <h1 class="text-2xl font-semibold">{{ t('account.title') }}</h1>

    <section class="rounded-lg border border-border bg-surface-raised p-5">
      <h2 class="mb-3 text-lg font-semibold">{{ t('account.user') }}</h2>
      <dl class="grid grid-cols-[max-content_1fr] gap-x-6 gap-y-2 text-sm">
        <dt class="text-fg-muted">{{ t('account.username') }}</dt>
        <dd>{{ info.user.username }}</dd>
        <dt class="text-fg-muted">{{ t('account.displayName') }}</dt>
        <dd>{{ info.user.displayName || '–' }}</dd>
        <dt class="text-fg-muted">{{ t('account.source') }}</dt>
        <dd>{{ t(`account.sources.${info.user.source}`) }}</dd>
        <dt class="text-fg-muted">{{ t('account.roles') }}</dt>
        <dd>{{ info.roles.length ? info.roles.join(', ') : t('account.noRoles') }}</dd>
        <dt class="text-fg-muted">{{ t('account.groups') }}</dt>
        <dd>
          <span v-if="info.user.groups.length === 0">–</span>
          <span v-for="g in info.user.groups" :key="g.dn" :title="g.dn" class="me-2 inline-block">
            {{ g.name }}
          </span>
        </dd>
        <dt class="text-fg-muted">{{ t('account.sessionEnds') }}</dt>
        <dd>{{ d(new Date(info.expiresAt), 'short') }}</dd>
      </dl>
    </section>

    <section v-if="totp" class="rounded-lg border border-border bg-surface-raised p-5">
      <h2 class="mb-3 flex items-center gap-3 text-lg font-semibold">
        {{ t('account.totp') }}
        <StatusBadge
          :status="totp.enrolled ? 'ok' : totp.required ? 'error' : 'unknown'"
          :label="totp.enrolled ? t('account.totpEnrolled') : t('account.totpNotEnrolled')"
        />
      </h2>
      <template v-if="totp.enrolled">
        <p class="mb-3 text-sm">
          {{ t('account.recoveryRemaining', { n: totp.recoveryCodesRemaining }) }}
        </p>
        <BaseButton variant="secondary" @click="openRegenerate">
          {{ t('account.regenerate') }}
        </BaseButton>
      </template>
      <template v-else-if="info.user.source !== 'local'">
        <p class="mb-3 text-sm">{{ t('account.totpOptional') }}</p>
        <RouterLink
          :to="{ name: 'enroll' }"
          class="inline-flex rounded-md bg-primary px-4 py-2 font-medium text-primary-fg hover:bg-primary-hover"
        >
          {{ t('account.enroll') }}
        </RouterLink>
      </template>
    </section>

    <section class="rounded-lg border border-border bg-surface-raised p-5">
      <h2 class="mb-3 text-lg font-semibold">{{ t('preferences.title') }}</h2>
      <div class="max-w-xs"><PreferenceSwitchers /></div>
    </section>

    <BaseModal v-model:open="regenOpen" :title="t('account.regenerate')">
      <RecoveryCodes v-if="newCodes.length" :codes="newCodes" />
      <form v-else id="regen-form" class="flex flex-col gap-3" @submit.prevent="regenerate">
        <p class="text-sm">{{ t('account.regenerateHint') }}</p>
        <p v-if="error" role="alert" class="text-sm text-danger">{{ error }}</p>
        <BaseInput
          v-model="code"
          :label="t('login.code')"
          inputmode="numeric"
          autocomplete="one-time-code"
          required
        />
      </form>
      <template #footer>
        <BaseButton v-if="newCodes.length" @click="regenOpen = false">{{
          t('common.close')
        }}</BaseButton>
        <template v-else>
          <BaseButton variant="ghost" @click="regenOpen = false">{{
            t('common.cancel')
          }}</BaseButton>
          <BaseButton type="submit" form="regen-form" :loading="busy">{{
            t('account.regenerate')
          }}</BaseButton>
        </template>
      </template>
    </BaseModal>
  </div>
</template>
