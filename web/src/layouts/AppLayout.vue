<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { errorMessage } from '@/api/errors'
import PreferenceSwitchers from '@/components/PreferenceSwitchers.vue'
import { navigation, visibleNavigation } from '@/navigation'
import { useSessionStore } from '@/stores/session'
import { useToastStore } from '@/stores/toast'

const session = useSessionStore()
const toast = useToastStore()
const route = useRoute()
const router = useRouter()
const i18n = useI18n()
const { t } = i18n

const items = computed(() => visibleNavigation(navigation, session.can, session.canAny))
const menuOpen = ref(false)
const navOpen = ref(false)
const user = computed(() => session.info?.user)

// Close the menus when navigating.
watch(
  () => route.fullPath,
  () => {
    menuOpen.value = false
    navOpen.value = false
  },
)

// The document title follows the page.
watch(
  () => [route.meta.title, i18n.locale.value],
  () => {
    document.title = route.meta.title
      ? `${t(route.meta.title)} – ${t('app.title')}`
      : t('app.title')
  },
  { immediate: true },
)

async function logout() {
  try {
    await session.logout()
  } catch (e) {
    toast.error(errorMessage(i18n, e))
  }
  await router.push({ name: 'login' })
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') menuOpen.value = false
}
</script>

<template>
  <div class="flex min-h-screen flex-col" @keydown="onKeydown">
    <a
      href="#main"
      class="sr-only focus:not-sr-only focus:absolute focus:top-2 focus:left-2 focus:z-50 focus:rounded focus:bg-surface focus:px-3 focus:py-2"
    >
      {{ t('nav.skip') }}
    </a>
    <header class="border-b border-border bg-surface-raised">
      <div class="mx-auto flex max-w-7xl items-center gap-4 px-4 py-2">
        <RouterLink :to="{ name: 'home' }" class="text-lg font-semibold text-primary">
          {{ t('app.title') }}
        </RouterLink>

        <button
          type="button"
          class="rounded p-2 text-fg-muted hover:bg-surface-sunken md:hidden"
          :aria-expanded="navOpen"
          aria-controls="main-nav"
          :aria-label="t('nav.toggle')"
          @click="navOpen = !navOpen"
        >
          <span aria-hidden="true">☰</span>
        </button>

        <nav
          id="main-nav"
          :aria-label="t('nav.label')"
          class="absolute top-12 right-0 left-0 z-30 border-b border-border bg-surface-raised px-4 py-2 md:static md:block md:border-0 md:bg-transparent md:p-0"
          :class="navOpen ? 'block' : 'hidden'"
        >
          <ul class="flex flex-col gap-1 md:flex-row">
            <li v-for="item in items" :key="item.name">
              <RouterLink
                :to="{ name: item.name }"
                class="block rounded-md px-3 py-1.5 text-sm hover:bg-surface-sunken"
                active-class="bg-surface-sunken font-medium"
                exact-active-class="bg-surface-sunken font-medium"
              >
                {{ t(item.label) }}
              </RouterLink>
            </li>
          </ul>
        </nav>

        <div class="relative ms-auto">
          <button
            type="button"
            class="flex items-center gap-2 rounded-md px-3 py-1.5 text-sm hover:bg-surface-sunken"
            :aria-expanded="menuOpen"
            aria-haspopup="true"
            aria-controls="user-menu"
            @click="menuOpen = !menuOpen"
          >
            <span>{{ user?.displayName || user?.username }}</span>
            <span aria-hidden="true">▾</span>
          </button>
          <div
            v-if="menuOpen"
            id="user-menu"
            class="absolute right-0 z-40 mt-2 w-72 rounded-lg border border-border bg-surface-raised p-4 shadow-lg"
          >
            <p class="font-medium">{{ user?.displayName || user?.username }}</p>
            <p class="mb-3 text-sm text-fg-muted">
              {{ user?.username }}
              <template v-if="session.roles.length"> · {{ session.roles.join(', ') }}</template>
            </p>
            <RouterLink
              :to="{ name: 'account' }"
              class="mb-3 block text-sm text-primary hover:underline"
            >
              {{ t('account.title') }}
            </RouterLink>
            <PreferenceSwitchers />
            <button
              type="button"
              class="mt-4 w-full rounded-md border border-border px-3 py-1.5 text-sm hover:bg-surface-sunken"
              @click="logout"
            >
              {{ t('nav.logout') }}
            </button>
          </div>
        </div>
      </div>
    </header>

    <main id="main" class="mx-auto w-full max-w-7xl flex-1 px-4 py-6" tabindex="-1">
      <RouterView />
    </main>
  </div>
</template>
