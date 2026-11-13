// SPDX-License-Identifier: GPL-3.0-or-later
import { defineStore } from 'pinia'
import { ref } from 'vue'

import { api, unwrap } from '@/api/client'
import { detectLocale, isLocale, type Locale } from '@/i18n'

export type Theme = 'system' | 'light' | 'dark'
/** An empty language follows the browser. */
export type Language = '' | Locale

export const THEMES: readonly Theme[] = ['system', 'light', 'dark']

// Keys of the per-browser cache; public/theme-init.js reads the theme key.
const THEME_KEY = 'dcdash.theme'
const LANGUAGE_KEY = 'dcdash.language'

function readCache(key: string): string {
  try {
    return localStorage.getItem(key) ?? ''
  } catch {
    return ''
  }
}

function writeCache(key: string, value: string) {
  try {
    localStorage.setItem(key, value)
  } catch {
    // storage not available (private mode): the server keeps the preferences
  }
}

function isTheme(v: string): v is Theme {
  return (THEMES as readonly string[]).includes(v)
}

/** The locale to start with: the cached language, or the browser language. */
export function initialLocale(): Locale {
  const cached = readCache(LANGUAGE_KEY)
  return isLocale(cached) ? cached : detectLocale()
}

/**
 * Theme and language. They apply immediately, are cached in the browser (also
 * before login) and stored on the server for logged-in users, so that they
 * follow the user to other browsers.
 */
export const usePreferencesStore = defineStore('preferences', () => {
  const cachedTheme = readCache(THEME_KEY)
  const cachedLanguage = readCache(LANGUAGE_KEY)
  const theme = ref<Theme>(isTheme(cachedTheme) ? cachedTheme : 'system')
  const language = ref<Language>(isLocale(cachedLanguage) ? cachedLanguage : '')
  /** True once the server preferences are loaded; changes are then saved there. */
  const synced = ref(false)

  let setLocale: (locale: Locale) => void = () => {}
  let media: MediaQueryList | undefined

  function systemDark() {
    return media?.matches ?? false
  }

  function applyTheme() {
    const dark = theme.value === 'dark' || (theme.value === 'system' && systemDark())
    document.documentElement.classList.toggle('dark', dark)
  }

  function applyLanguage() {
    const locale = language.value || detectLocale()
    setLocale(locale)
    document.documentElement.lang = locale
  }

  /** Connects the store to the page and to vue-i18n; call once at startup. */
  function init(localeSetter: (locale: Locale) => void) {
    setLocale = localeSetter
    if (typeof window.matchMedia === 'function') {
      media = window.matchMedia('(prefers-color-scheme: dark)')
      media.addEventListener('change', applyTheme)
    }
    applyTheme()
    applyLanguage()
  }

  async function save() {
    if (!synced.value) return
    await unwrap(
      api.PUT('/api/v1/me/preferences', { body: { language: language.value, theme: theme.value } }),
    )
  }

  async function setTheme(value: Theme) {
    theme.value = value
    writeCache(THEME_KEY, value)
    applyTheme()
    await save()
  }

  async function setLanguage(value: Language) {
    language.value = value
    writeCache(LANGUAGE_KEY, value)
    applyLanguage()
    await save()
  }

  /** Loads the user's preferences after login and applies them. */
  async function load() {
    const p = await unwrap(api.GET('/api/v1/me/preferences'))
    theme.value = p.theme
    language.value = p.language
    writeCache(THEME_KEY, p.theme)
    writeCache(LANGUAGE_KEY, p.language)
    applyTheme()
    applyLanguage()
    synced.value = true
  }

  /** After logout the preferences stay applied, but are no longer saved. */
  function reset() {
    synced.value = false
  }

  return { theme, language, synced, init, setTheme, setLanguage, load, reset }
})
