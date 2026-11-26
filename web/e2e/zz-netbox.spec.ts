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
  // All clusters, no redirect.
  await expect(page).toHaveURL(/\/netbox$/)
  const table = page.getByRole('table', { name: 'Virtual machines and containers of lab' })
  const row = table.getByRole('row').filter({ hasText: 'web' }).first()
  await expect(row).toContainText('Missing in NetBox')
  await expectAccessible(page, 'NetBox')
  await page.screenshot({ path: '../.e2e/screenshots/netbox.png', fullPage: true })

  // Physical machines: pve1 gets its cluster and primary IP, pve2 has no device.
  const nodes = page.getByRole('table', { name: 'Physical machines of lab' })
  await expect(nodes.getByRole('row').filter({ hasText: 'pve2' })).toContainText(
    'Missing in NetBox',
  )
  const pve1 = nodes.getByRole('row').filter({ hasText: 'pve1' })
  await expect(pve1).toContainText('Primary IP')
  await pve1.getByRole('button', { name: 'Update NetBox: pve1' }).click()
  await expect(page.getByText('NetBox was updated for pve1.')).toBeVisible()
  await expect(nodes.getByRole('row').filter({ hasText: 'pve1' })).toHaveCount(0)
  // Switches: the serial number is updated, the model has to be changed in NetBox.
  const sw1 = page
    .getByRole('table', { name: 'Switches', exact: true })
    .getByRole('row')
    .filter({ hasText: 'sw1' })
  await expect(sw1).toContainText('NetBox: sw-e2e')
  await sw1.getByRole('button', { name: 'Update NetBox: sw1' }).click()
  await expect(page.getByText('NetBox was updated for sw1.')).toBeVisible()
  await expect(sw1).toContainText('Change this in NetBox')
  await expectAccessible(page, 'NetBox devices')

  await row.getByRole('button', { name: 'Update NetBox', exact: true }).click()
  await expect(page.getByText('NetBox was updated for web.')).toBeVisible()
  // Matching guests are hidden by default.
  await expect(table.getByRole('row').filter({ hasText: 'web' })).toHaveCount(0)
  await page.getByLabel('Only differences').uncheck()
  await expect(table.getByRole('row').filter({ hasText: 'web' }).first()).toContainText('Matches')

  await page.getByRole('button', { name: 'Compare DNS and DHCP' }).click()
  await expect(page.getByRole('table', { name: 'Reserved addresses and DNS names' })).toBeVisible()
  await expectAccessible(page, 'NetBox address comparison')
  await page.getByRole('button', { name: 'Close the comparison' }).click()
  await expect(page.getByRole('table', { name: 'Reserved addresses and DNS names' })).toHaveCount(0)
})
