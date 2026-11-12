<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { useI18n } from 'vue-i18n'

const { t } = useI18n()
</script>

<template>
  <div class="min-h-screen bg-white text-slate-900 dark:bg-slate-950 dark:text-slate-100">
    <main class="mx-auto max-w-3xl p-8">
      <h1 class="text-2xl font-semibold">{{ t('app.title') }}</h1>
      <p class="mt-2 text-slate-600 dark:text-slate-400">{{ t('app.tagline') }}</p>
      <RouterView />
    </main>
  </div>
</template>
