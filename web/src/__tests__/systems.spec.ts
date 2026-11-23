// SPDX-License-Identifier: GPL-3.0-or-later
// The system colors (--dc-sys-* in assets/main.css) must stay readable: as text on their own
// tint (4.5:1, WCAG AA), as lines on the page (3:1), and the normal, muted and error text on
// the tint (4.5:1), in light and dark mode.
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

import { SYSTEMS, systemOf, systemStyle } from '../systems'

const css = readFileSync(resolve(__dirname, '../assets/main.css'), 'utf8')

function tokens(selector: RegExp): Record<string, string> {
  const block = css.match(selector)?.[1] ?? ''
  return Object.fromEntries(
    [...block.matchAll(/--([\w-]+):\s*([^;]+);/g)].map((m) => [m[1], m[2]!.trim()]),
  )
}
const light = tokens(/\n:root\s*\{([^}]*)\}/)
const dark = { ...light, ...tokens(/\n:root\.dark\s*\{([^}]*)\}/) }

type RGB = [number, number, number]
const rgb = (hex: string): RGB => [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16)) as RGB
const lin = (c: number) => ((c /= 255) <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4)
const lum = ([r, g, b]: RGB) => 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b)
const ratio = (a: RGB, b: RGB) => {
  const [x, y] = [lum(a), lum(b)].sort((p, q) => q - p) as [number, number]
  return (x + 0.05) / (y + 0.05)
}
// color-mix(in srgb, sys p%, surface)
const mix = (a: RGB, b: RGB, p: number): RGB => a.map((c, i) => c * p + b[i]! * (1 - p)) as RGB

describe('system colors', () => {
  for (const [mode, t] of [
    ['light', light],
    ['dark', dark],
  ] as const) {
    it(`are readable in ${mode} mode`, () => {
      const surface = rgb(t['dc-surface-raised']!)
      const page = rgb(t['dc-surface']!)
      const tint = parseFloat(t['dc-sys-tint']!) / 100
      expect(tint).toBeGreaterThan(0)
      for (const s of SYSTEMS) {
        const hex = t[`dc-sys-${s}`]
        expect(hex, `--dc-sys-${s} in ${mode} mode`).toMatch(/^#[0-9a-f]{6}$/i)
        const color = rgb(hex!)
        const bg = mix(rgb(t[`dc-sys-${s}-base`]!), surface, tint)
        expect(ratio(color, bg), `${s} text on its tint`).toBeGreaterThanOrEqual(4.5)
        expect(ratio(color, page), `${s} border on the page`).toBeGreaterThanOrEqual(3)
        for (const fg of ['dc-fg', 'dc-fg-muted', 'dc-danger']) {
          expect(ratio(rgb(t[fg]!), bg), `${fg} on the ${s} tint`).toBeGreaterThanOrEqual(4.5)
        }
      }
    })
  }

  it('maps status components to systems', () => {
    expect(systemOf({ kind: 'system' })).toBeNull()
    expect(systemOf({ kind: 'dns', type: 'cloudflare' })).toBe('cloudflare')
    expect(systemOf({ kind: 'dns', type: 'bind' })).toBe('bind')
    expect(systemOf({ kind: 'dhcp' })).toBe('kea')
    expect(systemOf({ kind: 'firewall' })).toBe('firewall')
    expect(systemStyle('kea')).toEqual({
      '--sys': 'var(--dc-sys-kea)',
      '--sys-base': 'var(--dc-sys-kea-base)',
    })
    expect(systemStyle(null)).toEqual({})
  })
})
