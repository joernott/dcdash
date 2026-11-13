// SPDX-License-Identifier: GPL-3.0-or-later
// Checks the design tokens in main.css for both themes: every token is defined
// in light and dark mode, and text colors reach WCAG AA contrast.
import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'

// Vitest runs in web/; CSS imports are not processed in tests, so read the file.
const css = readFileSync(join(process.cwd(), 'src/assets/main.css'), 'utf8')

function block(selector: string): Record<string, string> {
  const start = css.indexOf(`${selector} {`)
  const body = css.slice(start, css.indexOf('}', start))
  return Object.fromEntries(
    [...body.matchAll(/--dc-([\w-]+):\s*(#[0-9a-f]{6})/g)].map((m) => [m[1], m[2]]),
  )
}

function luminance(hex: string): number {
  const [r, g, b] = [1, 3, 5].map((i) => {
    const c = parseInt(hex.slice(i, i + 2), 16) / 255
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
  }) as [number, number, number]
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}

function contrast(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x) as [number, number]
  return (hi + 0.05) / (lo + 0.05)
}

const themes = { light: block(':root'), dark: block(':root.dark') }

describe('design tokens', () => {
  it('defines the same tokens in both themes', () => {
    expect(Object.keys(themes.light).length).toBeGreaterThan(10)
    expect(Object.keys(themes.dark).sort()).toEqual(Object.keys(themes.light).sort())
  })

  for (const [name, t] of Object.entries(themes)) {
    it(`has readable text in the ${name} theme`, () => {
      const pairs: [string, string, number][] = [
        ['fg', 'surface', 7],
        ['fg', 'surface-raised', 7],
        ['fg', 'surface-sunken', 4.5],
        ['fg-muted', 'surface', 4.5],
        ['fg-muted', 'surface-raised', 4.5],
        ['fg-muted', 'surface-sunken', 4.5],
        ['primary-fg', 'primary', 4.5],
        ['danger-fg', 'danger', 4.5],
        ['primary', 'surface', 4.5],
        ['danger', 'surface', 4.5],
        ['danger', 'surface-raised', 4.5],
        ['success', 'surface', 4.5],
        ['warning', 'surface', 4.5],
      ]
      for (const [fg, bg, min] of pairs) {
        const ratio = contrast(t[fg]!, t[bg]!)
        expect(ratio, `${fg} on ${bg}: ${ratio.toFixed(2)}`).toBeGreaterThanOrEqual(min)
      }
    })
  }
})
