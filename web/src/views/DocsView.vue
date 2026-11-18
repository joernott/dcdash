<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, nextTick, onMounted, ref, useTemplateRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { api, unwrap, type Schemas } from '@/api/client'
import { errorMessage } from '@/api/errors'

type Page = Schemas['DocsPage']

const i18n = useI18n()
const { t, locale } = i18n
const route = useRoute()
const router = useRouter()
const content = useTemplateRef<HTMLElement>('content')

const pages = ref<Page[]>([])
const page = ref<Page | null>(null)
const error = ref('')

const name = computed(() => String(route.params.page ?? ''))

async function loadPages() {
  pages.value = (await unwrap(api.GET('/api/v1/docs'))).pages
  if (!name.value && pages.value.length > 0) {
    await router.replace({ name: 'docs', params: { page: pages.value[0]!.name } })
  }
}

async function loadPage() {
  if (!name.value) return
  try {
    page.value = await unwrap(
      api.GET('/api/v1/docs/{page}', { params: { path: { page: name.value } } }),
    )
    error.value = ''
  } catch (e) {
    page.value = null
    error.value = errorMessage(i18n, e)
  }
  await nextTick()
  scrollToHash()
}

/** Scrolls to the heading of the URL fragment, or to the top. */
function scrollToHash() {
  const id = decodeURIComponent(route.hash.slice(1))
  const el = id ? document.getElementById(id) : null
  if (el) el.scrollIntoView()
  else window.scrollTo(0, 0)
}

/**
 * Links within the documentation are handled by the router, so that the
 * application is not reloaded. External links open in a new tab (target is
 * set by the server).
 */
function onClick(e: MouseEvent) {
  if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) {
    return
  }
  const a = (e.target as HTMLElement).closest('a')
  const href = a?.getAttribute('href')
  if (!a || !href || a.target) return
  if (href.startsWith('#')) {
    e.preventDefault()
    void router.push({ hash: href })
  } else if (href.startsWith('/docs/')) {
    e.preventDefault()
    void router.push(href)
  }
}

watch(name, () => void loadPage())
watch(
  () => route.hash,
  () => void nextTick(scrollToHash),
)

onMounted(async () => {
  try {
    await loadPages()
  } catch (e) {
    error.value = errorMessage(i18n, e)
  }
  await loadPage()
})
</script>

<template>
  <div class="flex flex-col gap-6 lg:flex-row lg:items-start">
    <nav
      :aria-label="t('docs.pages')"
      class="shrink-0 lg:sticky lg:top-4 lg:w-56"
      data-testid="docs-nav"
    >
      <h1 class="mb-2 text-lg font-semibold">{{ t('nav.docs') }}</h1>
      <ul class="flex flex-wrap gap-1 lg:flex-col">
        <li v-for="p in pages" :key="p.name">
          <RouterLink
            :to="{ name: 'docs', params: { page: p.name } }"
            class="block rounded-md px-3 py-1.5 text-sm hover:bg-surface-sunken"
            :class="{ 'bg-surface-sunken font-medium': p.name === name }"
            :aria-current="p.name === name ? 'page' : undefined"
          >
            {{ p.title }}
          </RouterLink>
        </li>
      </ul>
    </nav>

    <div class="min-w-0 flex-1">
      <p
        v-if="error"
        role="alert"
        class="rounded-md border border-danger px-3 py-2 text-sm text-danger"
      >
        {{ error }}
      </p>
      <p v-if="locale !== 'en'" class="mb-4 text-sm text-fg-muted">{{ t('docs.englishOnly') }}</p>
      <!-- The HTML is rendered by the server from the embedded Markdown files,
           without raw HTML (see internal/help). -->
      <!-- eslint-disable vue/no-v-html -->
      <article
        v-if="page"
        ref="content"
        class="doc-content max-w-4xl"
        lang="en"
        @click="onClick"
        v-html="page.html"
      />
      <!-- eslint-enable vue/no-v-html -->
    </div>
  </div>
</template>

<style scoped>
@reference '../assets/main.css';

.doc-content :deep(h1) {
  @apply mb-4 text-2xl font-semibold;
}
.doc-content :deep(h2) {
  @apply mt-8 mb-3 border-b border-border pb-1 text-xl font-semibold;
}
.doc-content :deep(h3) {
  @apply mt-6 mb-2 text-lg font-semibold;
}
.doc-content :deep(h4) {
  @apply mt-4 mb-2 font-semibold;
}
.doc-content :deep(p),
.doc-content :deep(ul),
.doc-content :deep(ol),
.doc-content :deep(pre),
.doc-content :deep(table),
.doc-content :deep(blockquote) {
  @apply mb-4;
}
.doc-content :deep(ul) {
  @apply list-disc ps-6;
}
.doc-content :deep(ol) {
  @apply list-decimal ps-6;
}
.doc-content :deep(li) {
  @apply mb-1;
}
.doc-content :deep(li > ul),
.doc-content :deep(li > ol) {
  @apply mt-1 mb-0;
}
.doc-content :deep(a) {
  @apply text-primary underline;
}
.doc-content :deep(code) {
  @apply rounded bg-surface-sunken px-1 py-0.5 font-mono text-[0.875em];
}
.doc-content :deep(pre) {
  @apply overflow-x-auto rounded-md border border-border bg-surface-sunken p-3 text-sm;
}
.doc-content :deep(pre code) {
  @apply bg-transparent p-0;
}
.doc-content :deep(table) {
  @apply block w-full overflow-x-auto text-sm;
}
.doc-content :deep(th),
.doc-content :deep(td) {
  @apply border border-border px-2 py-1 text-left align-top;
}
.doc-content :deep(th) {
  @apply bg-surface-sunken font-medium;
}
.doc-content :deep(blockquote) {
  @apply border-s-4 border-border ps-4 text-fg-muted;
}
.doc-content :deep(h2),
.doc-content :deep(h3),
.doc-content :deep(h4) {
  @apply scroll-mt-4;
}
</style>
