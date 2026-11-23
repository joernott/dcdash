// SPDX-License-Identifier: GPL-3.0-or-later
// Accessibility check for the end-to-end tests: axe-core with the WCAG 2.1 A and AA rules
// (including color contrast), in light and in dark mode.
import AxeBuilder from '@axe-core/playwright'
import { expect, type Page } from '@playwright/test'

const tags = ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']

async function violations(page: Page) {
  const res = await new AxeBuilder({ page }).withTags(tags).analyze()
  return res.violations.map((v) => ({
    rule: v.id,
    impact: v.impact,
    help: v.help,
    nodes: v.nodes.map(
      (n) => `${n.target.join(' ')}: ${n.failureSummary?.split('\n').slice(1).join(' ')}`,
    ),
  }))
}

/**
 * Checks the current page in both themes. The theme is switched with the class on <html>
 * (Tailwind's class-based dark variant) and restored afterwards.
 */
export async function expectAccessible(page: Page, name: string) {
  const html = page.locator('html')
  const wasDark = ((await html.getAttribute('class')) ?? '').split(' ').includes('dark')
  const found: Record<string, unknown> = {}
  for (const dark of [wasDark, !wasDark]) {
    await html.evaluate((el, d) => el.classList.toggle('dark', d), dark)
    // Let transitions finish, so that colors are final.
    await page.waitForTimeout(250)
    const v = await violations(page)
    if (v.length > 0) found[dark ? 'dark' : 'light'] = v
  }
  await html.evaluate((el, d) => el.classList.toggle('dark', d), wasDark)
  await page.waitForTimeout(250)
  expect.soft(found, `accessibility violations on ${name}`).toEqual({})
}
