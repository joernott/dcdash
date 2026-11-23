// SPDX-License-Identifier: GPL-3.0-or-later
// Projects (task 12.8) against the fakes: the project webshop is a file of server.sh, its tag
// is created in the NetBox fake. bob may assign objects to all projects and change the
// container proxy (lxc 200).
import { expect, test } from '@playwright/test'

import { expectAccessible } from './a11y'

test('projects: list, NetBox objects and a guest', async ({ page }) => {
  await page.goto('/')
  await page.getByLabel('User name').fill('bob')
  await page.getByLabel(/^Password$/).fill('bob-test-only')
  await page.getByRole('button', { name: 'Log in' }).click()
  await expect(page.getByRole('heading', { name: /Welcome/ })).toBeVisible()

  await page.getByRole('link', { name: 'Projects', exact: true }).click()
  await expect(page.getByRole('link', { name: 'Webshop' })).toBeVisible()
  await expectAccessible(page, 'Projects')
  await page.getByRole('link', { name: 'Webshop' }).click()
  await expect(page.getByRole('heading', { name: 'Webshop', level: 1 })).toBeVisible()
  await expect(page.getByText('Nothing belongs to this project yet.')).toBeVisible()

  // A prefix from NetBox.
  await page.getByLabel('Object type').selectOption('prefix')
  await page.getByRole('textbox', { name: 'Search', exact: true }).fill('192.0.2')
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  await page.getByTestId('project-results').getByRole('button', { name: 'Add' }).click()
  await expect(page.getByText('192.0.2.0/24 was added to the project.')).toBeVisible()
  await expect(page.getByRole('table', { name: 'Prefixes' })).toContainText('192.0.2.0/24')
  await expectAccessible(page, 'Project')

  // The container proxy joins the project on its page.
  await page.goto('/proxmox/lab/lxc/200')
  await page.getByRole('button', { name: 'Change projects' }).click()
  const dialog = page.getByRole('dialog', { name: 'Projects' })
  await dialog.getByLabel('Webshop').check()
  await dialog.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('The projects were saved.')).toBeVisible()
  await page.goto('/projects/webshop')
  await expect(page.getByRole('table', { name: 'Virtual machines and containers' })).toContainText(
    'proxy',
  )
  await page.screenshot({ path: '../.e2e/screenshots/project.png', fullPage: true })
})
