<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import { errorMessage } from '@/api/errors'
import BaseSelect from '@/components/ui/BaseSelect.vue'
import { THEMES, usePreferencesStore, type Language, type Theme } from '@/stores/preferences'
import { useToastStore } from '@/stores/toast'

/** Theme and language selection, used in the user menu and on the login page. */
const prefs = usePreferencesStore()
const toast = useToastStore()
const i18n = useI18n()
const { t } = i18n

const themeOptions = computed(() =>
  THEMES.map((v) => ({ value: v, label: t(`preferences.theme.${v}`) })),
)
// Language names are shown in their own language.
const languageOptions = computed(() => [
  { value: '', label: t('preferences.language.browser') },
  { value: 'en', label: 'English' },
  { value: 'de', label: 'Deutsch' },
])

async function run(action: Promise<void>) {
  try {
    await action
  } catch (e) {
    toast.error(errorMessage(i18n, e))
  }
}

const theme = computed({
  get: () => prefs.theme,
  set: (v: string) => void run(prefs.setTheme(v as Theme)),
})
const language = computed({
  get: () => prefs.language,
  set: (v: string) => void run(prefs.setLanguage(v as Language)),
})
</script>

<template>
  <div class="flex flex-col gap-3">
    <BaseSelect v-model="theme" :label="t('preferences.theme.label')" :options="themeOptions" />
    <BaseSelect
      v-model="language"
      :label="t('preferences.language.label')"
      :options="languageOptions"
    />
  </div>
</template>
