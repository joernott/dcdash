// SPDX-License-Identifier: GPL-3.0-or-later
import { createI18n } from 'vue-i18n'

import en from '@/locales/en.json'
import de from '@/locales/de.json'

export const SUPPORTED_LOCALES = ['en', 'de'] as const
export type Locale = (typeof SUPPORTED_LOCALES)[number]
export type MessageSchema = typeof en

export function isLocale(value: string): value is Locale {
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

// Date formats: use d(date, 'short' | 'long' | 'time'). English uses ISO-like
// 24-hour times, which operators in data centers usually expect.
const dateTimeFormat = {
  short: {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  },
  long: {
    year: 'numeric',
    month: 'long',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  },
  time: { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false },
} as const

// Number formats: use n(value, 'integer' | 'percent').
const numberFormat = {
  integer: { maximumFractionDigits: 0 },
  percent: { style: 'percent', maximumFractionDigits: 1 },
} as const

export function createAppI18n(locale: Locale = detectLocale()) {
  return createI18n<[MessageSchema], Locale, false>({
    legacy: false,
    locale,
    fallbackLocale: 'en',
    messages: { en, de },
    datetimeFormats: { en: dateTimeFormat, de: dateTimeFormat },
    numberFormats: { en: numberFormat, de: numberFormat },
  })
}
