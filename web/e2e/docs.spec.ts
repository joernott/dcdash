// SPDX-License-Identifier: GPL-3.0-or-later
// Built-in documentation: the pages are embedded in the binary and rendered by the server.
import { expect, test } from '@playwright/test'

import { expectAccessible } from './a11y'

test('documentation pages and links', async ({ page }) => {
  await page.goto('/')
  await page.getByLabel('User name').fill('bob')
  await page.getByLabel(/^Password$/).fill('bob-test-only')
  await page.getByRole('button', { name: 'Log in' }).click()
  await expect(page.getByRole('heading', { name: /Welcome/ })).toBeVisible()

  await page.getByRole('link', { name: 'Documentation' }).click()
  await expect(page).toHaveURL(/\/docs\/user-guide$/)
  await expect(page.getByRole('heading', { level: 1, name: 'User guide' })).toBeVisible()
  await expectAccessible(page, 'documentation')

  // A link to another page and heading stays within dcdash.
  await page
    .getByRole('navigation', { name: 'Documentation pages' })
    .getByRole('link', { name: 'Configuration reference' })
    .click()
  await expect(page).toHaveURL(/\/docs\/configuration$/)
  const link = page.locator('article a[href^="/docs/security#"]').first()
  await link.click()
  await expect(page).toHaveURL(/\/docs\/security#doc-/)
  await expect(page.getByRole('heading', { level: 1, name: /Security/ })).toBeVisible()
  await page.screenshot({ path: '../.e2e/screenshots/docs-light-en.png', fullPage: false })
})
