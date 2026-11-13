// SPDX-License-Identifier: GPL-3.0-or-later
import type { Composer } from 'vue-i18n'

import { ApiError } from './client'

/**
 * Returns a localized message for an error. API errors are translated by their
 * stable code (errors.<code> in the message files); unknown codes fall back to
 * a generic message with the English text from the server.
 */
export function errorMessage(i18n: Pick<Composer, 't' | 'te'>, err: unknown): string {
  if (err instanceof ApiError) {
    const key = `errors.${err.code}`
    if (i18n.te(key)) return i18n.t(key)
    return i18n.t('errors.unknown', { message: err.message })
  }
  return i18n.t('errors.unknown', { message: err instanceof Error ? err.message : String(err) })
}
