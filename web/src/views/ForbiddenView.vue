<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { useI18n } from 'vue-i18n'

const { t } = useI18n()
</script>

<template>
  <div class="max-w-xl">
    <h1 class="mb-2 text-2xl font-semibold">{{ t('forbidden.title') }}</h1>
    <p class="mb-4 text-fg-muted">{{ t('forbidden.text') }}</p>
    <RouterLink :to="{ name: 'home' }" class="text-primary hover:underline">{{
      t('nav.backHome')
    }}</RouterLink>
  </div>
</template>
