// SPDX-License-Identifier: GPL-3.0-or-later
// DNS module against the fake Cloudflare API (test/cffake, see server.sh):
// zone list, record list, adding and deleting a record. bob holds
// dns:cf:*:write (no TOTP), so he sees no zone actions.
import { expect, test } from '@playwright/test'

import { expectAccessible } from './a11y'

test('zones and records of a Cloudflare provider', async ({ page }) => {
  await page.goto('/')
  await page.getByLabel('User name').fill('bob')
  await page.getByLabel(/^Password$/).fill('bob-test-only')
  await page.getByRole('button', { name: 'Log in' }).click()
  await expect(page.getByRole('heading', { name: /Welcome/ })).toBeVisible()

  await page
    .getByRole('navigation', { name: 'Main navigation' })
    .getByRole('link', { name: 'DNS' })
    .click()
  await expect(page).toHaveURL(/\/dns$/)
  const zones = page.getByRole('table', { name: 'Zones of cf' })
  await expect(zones.getByRole('row')).toHaveCount(3) // header + 2 zones
  // All providers are listed; Cloudflare can't create zones for this user.
  const cf = page.getByRole('region', { name: 'cf (Cloudflare)' })
  await expect(cf.getByRole('button', { name: /Create zone/ })).toHaveCount(0)
  await expectAccessible(page, 'DNS zones')
  await expect(zones.getByRole('button')).toHaveCount(0)

  await zones.getByRole('link', { name: 'example.org' }).click()
  await expect(page).toHaveURL(/\/dns\/cf\/example\.org$/)
  const records = page.getByRole('table', { name: 'Records' })
  await expect(records.getByRole('row')).toHaveCount(3) // header + 2 records
  await expect(records.getByText('192.0.2.10')).toBeVisible()

  // Add an A record.
  await page.getByRole('button', { name: 'Add record' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByLabel('Name').fill('mail')
  await dialog.getByLabel('Type').selectOption('A')
  await dialog.getByLabel('Content').fill('192.0.2.25')
  await dialog.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('mail (A) saved.')).toBeVisible()
  await expect(records.getByRole('row')).toHaveCount(4)
  await expectAccessible(page, 'DNS records')
  await page.screenshot({ path: '../.e2e/screenshots/dns-light-en.png', fullPage: true })

  // Invalid content is rejected by the server and shown in the dialog.
  await page.getByRole('button', { name: 'Add record' }).click()
  await dialog.getByLabel('Name').fill('bad')
  await dialog.getByLabel('Type').selectOption('A')
  await dialog.getByLabel('Content').fill('not-an-address')
  await dialog.getByRole('button', { name: 'Save' }).click()
  await expect(dialog.getByRole('alert')).toBeVisible()
  await expectAccessible(page, 'DNS record dialog with error')
  await dialog.getByRole('button', { name: 'Cancel' }).click()

  // Delete it again.
  await records.getByRole('group', { name: /mail/ }).getByRole('button', { name: 'Delete' }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Delete' }).click()
  await expect(page.getByText('mail (A) deleted.')).toBeVisible()
  await expect(records.getByRole('row')).toHaveCount(3)
})
