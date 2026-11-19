// SPDX-License-Identifier: GPL-3.0-or-later
// The switch pages against the switch fake (test/cxfake, see server.sh), whose device is in
// the NetBox fake: bob may read sw1, change port 1/1/5 and VLANs from 4000 (role
// switch-operator) and assign projects (role netbox-user).
import { expect, test } from '@playwright/test'

import { expectAccessible } from './a11y'

test('switch ports, VLANs, projects and MAC search', async ({ page }) => {
  await page.goto('/')
  await page.getByLabel('User name').fill('bob')
  await page.getByLabel(/^Password$/).fill('bob-test-only')
  await page.getByRole('button', { name: 'Log in' }).click()
  await expect(page.getByRole('heading', { name: /Welcome/ })).toBeVisible()
  await expect(page.getByText('6000 24G 4SFP Swch sw-e2e, AOS-CX PL.10.08.1010')).toBeVisible()

  await page.getByRole('link', { name: 'Switches', exact: true }).click()
  await expect(page).toHaveURL(/\/switches\/sw1$/)
  const ports = page.getByRole('table', { name: 'Ports' })
  await expect(ports.getByRole('row').filter({ hasText: 'core-sw' })).toContainText('uplink')
  await expectAccessible(page, 'Switch ports')
  await page.screenshot({ path: '../.e2e/screenshots/switches.png', fullPage: true })

  // Only port 1/1/5 may be changed.
  await expect(page.getByRole('button', { name: /^Change port/ })).toHaveCount(1)
  await page.getByRole('button', { name: 'Change port 1/1/5' }).click()
  const dialog = page.getByRole('dialog', { name: 'Port 1/1/5' })
  await dialog.getByLabel('Description').fill('esx1')
  await dialog.getByLabel('VLAN', { exact: true }).selectOption('20')
  await expectAccessible(page, 'Port dialog')
  await dialog.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('Port 1/1/5 was changed.')).toBeVisible()
  await expect(ports.getByRole('row').filter({ hasText: 'esx1' })).toContainText('20')

  // Projects of a port are the tags of its NetBox interface.
  await page.getByRole('button', { name: 'Change projects (port 1/1/2)' }).click()
  const projects = page.getByRole('dialog', { name: 'Projects of port 1/1/2' })
  await projects.getByLabel('Webshop').check()
  await projects.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('The projects of port 1/1/2 were saved.')).toBeVisible()
  await expect(ports.getByRole('row').filter({ hasText: 'app1' })).toContainText('Webshop')

  await page.getByRole('tab', { name: 'VLANs' }).click()
  await page.getByRole('button', { name: 'Create VLAN' }).click()
  const vlan = page.getByRole('dialog', { name: 'Create a VLAN' })
  await vlan.getByLabel('VLAN ID').fill('4001')
  await vlan.getByLabel('Name').fill('lab')
  await vlan.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('VLAN 4001 was saved.')).toBeVisible()
  const vlans = page.getByRole('table', { name: 'VLANs' })
  await expect(vlans.getByRole('row').filter({ hasText: 'lab' })).toBeVisible()
  await expectAccessible(page, 'Switch VLANs')
  await page.getByRole('button', { name: 'Delete VLAN 4001' }).click()
  await page
    .getByRole('dialog', { name: /Delete VLAN 4001/ })
    .getByRole('button', { name: 'Delete' })
    .click()
  await expect(page.getByText('VLAN 4001 was deleted.')).toBeVisible()

  await page.getByRole('tab', { name: 'Find a device' }).click()
  await page.getByLabel('MAC address').fill('bc:24:11:00:00:01')
  await page.getByRole('button', { name: 'Search' }).click()
  const hits = page.getByRole('table', { name: 'Find a device' })
  await expect(hits.getByRole('row').filter({ hasText: '1/1/2' })).toContainText('Connected here')
  await expectAccessible(page, 'MAC search')
})
