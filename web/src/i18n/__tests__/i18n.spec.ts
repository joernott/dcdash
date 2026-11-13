// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, it, expect } from 'vitest'

import { createAppI18n } from '..'

describe('formats', () => {
  const date = new Date(Date.UTC(2026, 10, 13, 14, 5, 9))

  it('formats dates and numbers per language', () => {
    const en = createAppI18n('en').global
    const de = createAppI18n('de').global
    const opts = { key: 'long', timeZone: 'UTC' } as const
    expect(en.d(date, opts)).toContain('November')
    expect(de.d(date, opts)).toContain('13. November')
    expect(en.d(date, { key: 'time', timeZone: 'UTC' })).toBe('14:05:09')
    expect(en.n(1234567, 'integer')).toBe('1,234,567')
    expect(de.n(1234567, 'integer')).toBe('1.234.567')
    expect(de.n(0.256, 'percent')).toMatch(/25,6\s?%/)
  })
})
