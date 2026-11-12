// SPDX-License-Identifier: GPL-3.0-or-later
import { createI18n } from 'vue-i18n'

import en from '@/locales/en.json'
import de from '@/locales/de.json'

export const SUPPORTED_LOCALES = ['en', 'de'] as const
export type Locale = (typeof SUPPORTED_LOCALES)[number]
export type MessageSchema = typeof en

function isLocale(value: string): value is Locale {
  return (SUPPORTED_LOCALES as readonly string[]).includes(value)
}

/** Picks the first supported language from the browser's preference list, falling back to English. */
export function detectLocale(languages: readonly string[] = navigator.languages): Locale {
  for (const language of languages) {
    const base = language.toLowerCase().split('-')[0] ?? ''
    if (isLocale(base)) {
      return base
    }
  }
  return 'en'
}

export function createAppI18n(locale: Locale = detectLocale()) {
  return createI18n<[MessageSchema], Locale, false>({
    legacy: false,
    locale,
    fallbackLocale: 'en',
    messages: { en, de },
  })
}
