// SPDX-License-Identifier: GPL-3.0-or-later
// The NetBox comparison view against the NetBox fake (test/nbfake, see server.sh): the guests
// of the Proxmox fake are missing in NetBox; bob may update NetBox (role netbox-user). Runs
// before zz-proxmox-provisioning.spec.ts, which writes a new guest to NetBox.
import { expect, test } from '@playwright/test'

import { expectAccessible } from './a11y'

test('compare NetBox with Proxmox and update it', async ({ page }) => {
  await page.goto('/')
  await page.getByLabel('User name').fill('bob')
  await page.getByLabel(/^Password$/).fill('bob-test-only')
  await page.getByRole('button', { name: 'Log in' }).click()
  await expect(page.getByRole('heading', { name: /Welcome/ })).toBeVisible()
  await expect(page.getByText('NetBox 4.7.5')).toBeVisible()

  await page.getByRole('link', { name: 'NetBox', exact: true }).click()
  await expect(page).toHaveURL(/\/netbox\/lab$/)
  const table = page.getByRole('table', { name: 'Virtual machines and containers' })
  const row = table.getByRole('row').filter({ hasText: 'web' }).first()
  await expect(row).toContainText('Missing in NetBox')
  await expectAccessible(page, 'NetBox')
  await page.screenshot({ path: '../.e2e/screenshots/netbox.png', fullPage: true })

  await row.getByRole('button', { name: 'Update NetBox' }).click()
  await expect(page.getByText('NetBox was updated for web.')).toBeVisible()
  // Matching guests are hidden by default.
  await expect(table.getByRole('row').filter({ hasText: 'web' })).toHaveCount(0)
  await page.getByLabel('Only differences').uncheck()
  await expect(table.getByRole('row').filter({ hasText: 'web' }).first()).toContainText('Matches')

  await page.getByRole('button', { name: 'Compare', exact: true }).click()
  await expect(page.getByRole('table', { name: 'Reserved addresses and DNS names' })).toBeVisible()
})
