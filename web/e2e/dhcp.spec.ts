// SPDX-License-Identifier: GPL-3.0-or-later
// DHCP module against the fake Kea servers (test/keafake, see server.sh):
// subnet utilization, leases of a subnet with deletion, and reservations.
// bob may read everything and change subnet 192.0.2.0/24 of kea4 (no TOTP).
import { expect, test } from '@playwright/test'

import { expectAccessible } from './a11y'

test('subnets, leases and reservations of a Kea server', async ({ page }) => {
  await page.goto('/')
  await page.getByLabel('User name').fill('bob')
  await page.getByLabel(/^Password$/).fill('bob-test-only')
  await page.getByRole('button', { name: 'Log in' }).click()
  await expect(page.getByRole('heading', { name: /Welcome/ })).toBeVisible()

  await page
    .getByRole('navigation', { name: 'Main navigation' })
    .getByRole('link', { name: 'DHCP' })
    .click()
  await expect(page).toHaveURL(/\/dhcp\/kea4$/)
  await expect(page.getByText('Kea 3.0.4')).toBeVisible()
  const subnets = page.getByRole('table', { name: 'Subnets' })
  await expect(subnets.getByRole('row')).toHaveCount(3) // header + 2 subnets
  await expect(subnets.getByRole('row', { name: /192\.0\.2\.0\/24/ })).toContainText('2 of 100')
  await expectAccessible(page, 'DHCP subnets')
  await page.screenshot({ path: '../.e2e/screenshots/dhcp-light-en.png', fullPage: true })

  // Leases of a subnet; bob may delete leases in 192.0.2.0/24.
  await subnets
    .getByRole('row', { name: /192\.0\.2\.0\/24/ })
    .getByRole('button', { name: 'Show leases' })
    .click()
  const leases = page.getByRole('table', { name: 'Leases' })
  await expect(leases.getByRole('row')).toHaveCount(3)
  await expectAccessible(page, 'DHCP leases')
  await leases.getByRole('button', { name: 'Delete lease 192.0.2.101' }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Delete' }).click()
  await expect(page.getByText('Lease 192.0.2.101 deleted.')).toBeVisible()
  await expect(leases.getByRole('row')).toHaveCount(2)

  // Search: leases of other subnets are shown, but not deletable.
  await page.getByLabel('Search by').selectOption('hostname')
  await page.getByLabel('Value').fill('camera')
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  await expect(leases.getByRole('row')).toHaveCount(2)
  await expect(leases.getByRole('button')).toHaveCount(0)

  // Reservations: the configuration file one is read-only.
  await page.getByRole('tab', { name: 'Reservations' }).click()
  const reservations = page.getByRole('table', { name: 'Reservations' })
  await expect(reservations.getByRole('row', { name: /printer/ })).toContainText('read-only')
  await expectAccessible(page, 'DHCP reservations')
  await page.getByRole('button', { name: 'Add reservation' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByLabel('Identifier', { exact: true }).fill('aa:bb:cc:00:00:02')
  await dialog.getByLabel('IPv4 address').fill('192.0.2.20')
  await dialog.getByLabel('Host name').fill('scanner')
  await dialog.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('Reservation scanner added.')).toBeVisible()

  // Kea's own checks are shown in the dialog: the address must be in the subnet.
  await page.getByRole('button', { name: 'Add reservation' }).click()
  await dialog.getByLabel('Identifier', { exact: true }).fill('aa:bb:cc:00:00:03')
  await dialog.getByLabel('IPv4 address').fill('198.51.100.5')
  await dialog.getByRole('button', { name: 'Save' }).click()
  await expect(dialog.getByRole('alert')).toContainText('is not in subnet 192.0.2.0/24')
  await expectAccessible(page, 'DHCP reservation dialog with error')
  await dialog.getByRole('button', { name: 'Cancel' }).click()

  await reservations
    .getByRole('group', { name: 'Actions for scanner' })
    .getByRole('button', { name: 'Delete' })
    .click()
  await page.getByRole('dialog').getByRole('button', { name: 'Delete' }).click()
  await expect(page.getByText('Reservation scanner deleted.')).toBeVisible()
  await expect(reservations.getByRole('row')).toHaveCount(2)

  // The DHCPv6 server: bob may only read.
  await page.getByLabel('Server').selectOption('kea6')
  await expect(page).toHaveURL(/\/dhcp\/kea6/)
  await expect(page.getByRole('table', { name: 'Subnets' })).toContainText('2001:db8:1::/64')
})
