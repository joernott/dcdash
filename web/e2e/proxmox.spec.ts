// SPDX-License-Identifier: GPL-3.0-or-later
// Proxmox module against the fake Proxmox API (test/pvefake, see server.sh):
// inventory, a power action with its task, and a container terminal through
// the console websocket proxy. bob holds proxmox:*:*:console (no TOTP).
import { expect, test } from '@playwright/test'

test('inventory, power action and container terminal', async ({ page }) => {
  await page.goto('/')
  await page.getByLabel('User name').fill('bob')
  await page.getByLabel(/^Password$/).fill('bob-test-only')
  await page.getByRole('button', { name: 'Log in' }).click()
  await expect(page.getByRole('heading', { name: /Welcome/ })).toBeVisible()

  await page.getByRole('navigation', { name: 'Main navigation' }).getByRole('link', { name: 'Proxmox' }).click()
  await expect(page).toHaveURL(/\/proxmox\/lab$/)
  await expect(page.getByRole('heading', { name: 'pve1' })).toBeVisible()
  const guests = page.getByRole('table', { name: 'Virtual machines and containers' })
  await expect(guests.getByRole('row')).toHaveCount(4) // header + 3 guests
  // No node shell without admin on the node.
  await expect(page.getByRole('link', { name: 'Shell' })).toHaveCount(0)

  // Start the stopped VM and follow its task.
  const db = guests.getByRole('row', { name: /db/ })
  await db.getByRole('button', { name: 'Start' }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Start' }).click()
  await expect(page.getByText('Done: db (102): Start')).toBeVisible()
  await expect(db.getByText('Running')).toBeVisible()
  await page.screenshot({ path: '../.e2e/screenshots/proxmox-light-en.png', fullPage: true })

  // Guest details.
  await guests.getByRole('link', { name: 'web' }).click()
  await expect(page.getByRole('heading', { name: 'web', level: 1 })).toBeVisible()
  await expect(page.getByText('bridge=vmbr0')).toBeVisible()

  // Container terminal: the fake echoes the input.
  await page.goto('/proxmox/lab/lxc/200/console')
  await expect(page.getByText('Connected')).toBeVisible()
  await page.getByTestId('console-screen').click()
  await page.keyboard.type('hello dcdash')
  await expect(page.locator('.xterm-rows')).toContainText('hello dcdash')
  await page.screenshot({ path: '../.e2e/screenshots/proxmox-terminal.png' })
})
