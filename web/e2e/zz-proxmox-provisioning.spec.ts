// SPDX-License-Identifier: GPL-3.0-or-later
// Creating and deleting a guest against the fake Proxmox API (test/pvefake, see server.sh):
// the wizard clones the template 9000 with cloud-init, the job is followed to the end, and the
// new VM is stopped and deleted. bob may create and delete guests with IDs 5000-5999. Runs
// after proxmox.spec.ts, which counts the guests.
import { expect, test } from '@playwright/test'

import { expectAccessible } from './a11y'

test('create a VM from a template and delete it', async ({ page }) => {
  await page.goto('/')
  await page.getByLabel('User name').fill('bob')
  await page.getByLabel(/^Password$/).fill('bob-test-only')
  await page.getByRole('button', { name: 'Log in' }).click()
  await expect(page.getByRole('heading', { name: /Welcome/ })).toBeVisible()

  await page.goto('/proxmox/lab')
  await page.getByRole('link', { name: 'Create' }).click()
  await expect(
    page.getByRole('heading', { name: 'Create a virtual machine or container' }),
  ).toBeVisible()
  await expect(page.getByLabel('Virtual machine from a template')).toBeChecked()
  await expectAccessible(page, 'Create guest: source')

  await page.getByLabel('Name', { exact: true }).fill('e2e-app')
  await page.getByRole('button', { name: 'Next' }).click()
  await expect(page.getByLabel('CPU cores')).toHaveValue('2')
  await page.getByLabel('Disk (GiB)').fill('20')
  await expectAccessible(page, 'Create guest: resources')
  await page.getByRole('button', { name: 'Next' }).click()

  await page.getByLabel('VLAN (optional)').fill('100')
  await page.getByLabel('IPv4').selectOption('static')
  await page.getByLabel('Address with prefix length').fill('192.0.2.50/24')
  await page.getByRole('button', { name: 'Add network device' }).click()
  await page.getByTestId('network-1').getByLabel('VLAN (optional)').fill('150')
  await page.getByLabel('Public SSH keys').fill('ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIE2e e2e@test')
  await expectAccessible(page, 'Create guest: network')
  await page.getByRole('button', { name: 'Next' }).click()

  await expect(page.getByText('192.0.2.50/24')).toBeVisible()
  await expect(page.getByText('vmbr0, VLAN 100')).toBeVisible()
  await expect(page.getByText('vmbr0, VLAN 150')).toBeVisible()
  await expectAccessible(page, 'Create guest: review')
  await page.getByRole('button', { name: 'Create' }).click()

  await expect(page.getByRole('heading', { name: 'e2e-app (5000)' })).toBeVisible()
  await expect(page.getByText('Done', { exact: true }).first()).toBeVisible()
  for (const step of ['Clone the template', 'Apply the settings', 'Grow the disk', 'Start'])
    await expect(page.getByText(step, { exact: true })).toBeVisible()
  await expectAccessible(page, 'Create guest: job')
  await page.screenshot({ path: '../.e2e/screenshots/proxmox-create.png', fullPage: true })

  await page.getByRole('link', { name: 'Open' }).click()
  await expect(page.getByRole('heading', { name: 'e2e-app', level: 1 })).toBeVisible()
  await expect(page.getByText('ip=192.0.2.50/24')).toBeVisible()
  // Hardware: a new disk is hot-plugged, a memory change stays pending while the VM runs.
  const hardware = page.getByRole('region', { name: 'Hardware' })
  await hardware.getByRole('button', { name: 'Add disk' }).click()
  const addDisk = page.getByRole('dialog', { name: 'Add disk' })
  await addDisk.getByLabel('Size (GiB)').fill('8')
  await expectAccessible(page, 'Add disk')
  await addDisk.getByRole('button', { name: 'Add', exact: true }).click()
  await expect(page.getByText('e2e-app: Add disk done.')).toBeVisible()
  const nics = page.getByRole('table', { name: 'Network devices' })
  await expect(nics.getByRole('row', { name: /net1/ })).toContainText('VLAN 150')
  const disks = page.getByRole('table', { name: 'Disks' })
  await expect(disks.getByRole('row', { name: /scsi1/ })).toContainText('8G')
  await hardware.getByRole('button', { name: 'Edit options' }).click()
  const options = page.getByRole('dialog', { name: 'Edit options' })
  await options.getByLabel('Memory (MiB)', { exact: true }).fill('4096')
  await options.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByTestId('pending-hint')).toBeVisible()
  await expectAccessible(page, 'Hardware')
  await page.screenshot({ path: '../.e2e/screenshots/proxmox-hardware.png', fullPage: true })

  // A running guest can't be deleted.
  await expect(page.getByRole('button', { name: 'Delete', exact: true }).first()).toBeDisabled()
  await page.getByRole('button', { name: 'Stop' }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Stop' }).click()
  await expect(page.getByText('Done: e2e-app (5000): Stop')).toBeVisible()

  await page.getByRole('button', { name: 'Delete', exact: true }).first().click()
  const dialog = page.getByRole('dialog', { name: 'Delete guest' })
  await expect(dialog.getByRole('button', { name: 'Delete' })).toBeDisabled()
  await dialog.getByLabel('Type e2e-app to confirm').fill('e2e-app')
  await expectAccessible(page, 'Delete guest')
  await dialog.getByRole('button', { name: 'Delete' }).click()
  await expect(page.getByText('e2e-app was deleted.')).toBeVisible()
  await expect(page).toHaveURL(/\/proxmox\/lab$/)
  const guests = page.getByRole('table', { name: 'Virtual machines and containers' })
  await expect(guests.getByRole('row')).toHaveCount(5)
})
