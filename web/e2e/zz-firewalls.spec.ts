// SPDX-License-Identifier: GPL-3.0-or-later
// The firewall pages against the OPNsense fake (test/opnfake, see server.sh): bob may read
// fw1, enable and disable WireGuard peers, change aliases e2e_* and the rules of lan (role
// firewall-operator).
import { expect, test } from '@playwright/test'

import { expectAccessible } from './a11y'

test('firewall health, VPN, aliases and rules', async ({ page }) => {
  test.setTimeout(60_000)
  await page.goto('/')
  await page.getByLabel('User name').fill('bob')
  await page.getByLabel(/^Password$/).fill('bob-test-only')
  await page.getByRole('button', { name: 'Log in' }).click()
  await expect(page.getByRole('heading', { name: /Welcome/ })).toBeVisible()
  await expect(page.getByText('OPNsense 26.1.6')).toBeVisible()

  await page.getByRole('link', { name: 'Firewalls', exact: true }).click()
  await expect(page).toHaveURL(/\/firewalls\/fw1$/)
  await expect(page.getByRole('table', { name: 'Gateways' })).toContainText('WAN_DHCP')
  // bob may not restart services.
  await expect(page.getByRole('button', { name: /^Restart / })).toHaveCount(0)
  await expectAccessible(page, 'Firewall health')
  await page.screenshot({ path: '../.e2e/screenshots/firewalls.png', fullPage: true })

  await page.getByRole('tab', { name: 'VPN' }).click()
  await page.getByRole('button', { name: 'Disable laptop-alice' }).click()
  await expect(page.getByText('laptop-alice was disabled.')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Enable laptop-alice' })).toBeVisible()
  await expectAccessible(page, 'Firewall VPN')

  await page.getByRole('tab', { name: 'Aliases' }).click()
  await page.getByRole('button', { name: 'Create alias' }).click()
  const alias = page.getByRole('dialog', { name: 'Create an alias' })
  await alias.getByLabel('Name').fill('e2e_dns')
  await alias.getByLabel('Content').fill('192.0.2.53\n192.0.2.54')
  await expectAccessible(page, 'Alias dialog')
  await alias.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('Alias e2e_dns was saved.')).toBeVisible()
  await expect(page.getByRole('table', { name: 'Aliases' })).toContainText('192.0.2.53, 192.0.2.54')

  await page.getByRole('tab', { name: 'Rules' }).click()
  const rules = page.getByRole('table', { name: 'Rules' })
  await expect(rules.getByRole('row').filter({ hasText: 'Default allow LAN' })).toContainText(
    'read-only',
  )
  await page.getByRole('button', { name: 'Create rule' }).click()
  const rule = page.getByRole('dialog', { name: 'Create a rule' })
  await rule.getByLabel('Protocol').selectOption('TCP')
  await rule.getByLabel('Destination port').fill('22')
  await rule.getByLabel('Description').fill('e2e ssh')
  await expectAccessible(page, 'Rule dialog')
  await rule.getByRole('button', { name: 'Apply' }).click()
  await expect(page.getByText('The rules were applied.')).toBeVisible({ timeout: 15_000 })
  await expect(rules.getByRole('row').filter({ hasText: 'e2e ssh' })).toContainText('any:22')
  await expectAccessible(page, 'Firewall rules')
})
