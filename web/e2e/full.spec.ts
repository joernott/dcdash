// SPDX-License-Identifier: GPL-3.0-or-later
// End-to-end tests against the real services of the docker compose environment
// (scripts/e2e-full.sh): bind9 with rndc, SFTP and catalog zone, Kea with a PostgreSQL
// host database, and a Kea HA pair. bob holds the role full-e2e (see server.sh).
import { expect, test, type APIRequestContext, type Page } from '@playwright/test'

import { expectAccessible } from './a11y'

test.skip(process.env.DCDASH_E2E_FULL !== '1', 'needs the full environment (scripts/e2e-full.sh)')

const keaAuth =
  'Basic ' + Buffer.from(`dcdash:${process.env.DCDASH_IT_KEA_PASSWORD ?? ''}`).toString('base64')

/** Sends a command to a Kea control socket and returns the first answer. */
async function kea(request: APIRequestContext, url: string, command: string, args: object) {
  const res = await request.post(url, {
    data: { command, arguments: args },
    headers: { Authorization: keaAuth },
  })
  expect(res.ok()).toBeTruthy()
  return ((await res.json()) as { result: number; text?: string }[])[0]!
}

async function loginBob(page: Page) {
  await page.goto('/')
  await page.getByLabel('User name').fill('bob')
  await page.getByLabel(/^Password$/).fill('bob-test-only')
  await page.getByRole('button', { name: 'Log in' }).click()
  await expect(page.getByRole('heading', { name: /Welcome/ })).toBeVisible()
}

test('bind9: create a zone, change its records and delete it', async ({ page }) => {
  const zone = 'app.e2e.dcdash.test'
  await loginBob(page)
  await page.goto('/dns/bind')
  const zones = page.getByRole('table', { name: 'Zones' })
  await expect(zones.getByRole('link', { name: 'static.dcdash.test' })).toBeVisible()

  // bob may create zones only below e2e.dcdash.test.
  await page.getByRole('button', { name: 'Create zone' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByLabel('Zone name').fill('other.dcdash.test')
  await dialog.getByRole('button', { name: 'Create', exact: true }).click()
  await expect(dialog).toContainText("You don't have permission to do this.")
  await dialog.getByLabel('Zone name').fill(zone)
  await dialog.getByRole('button', { name: 'Create', exact: true }).click()
  await expect(page.getByText(`Zone ${zone} created.`)).toBeVisible()

  // The new zone has the SOA and NS records from the template; add and delete a record.
  await zones.getByRole('link', { name: zone }).click()
  const records = page.getByRole('table', { name: 'Records' })
  await expect(records.getByRole('row', { name: /SOA/ })).toBeVisible()
  await page.getByRole('button', { name: 'Add record' }).click()
  await dialog.getByLabel('Name').fill('www')
  await dialog.getByLabel('Type').selectOption('A')
  await dialog.getByLabel('Content').fill('192.0.2.80')
  await dialog.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText(/www.*\(A\) saved\./)).toBeVisible()
  await expect(records.getByText('192.0.2.80')).toBeVisible()
  await expectAccessible(page, 'bind9 records')
  await records.getByRole('group', { name: /www/ }).getByRole('button', { name: 'Delete' }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Delete' }).click()
  await expect(page.getByText(/www.*\(A\) deleted\./)).toBeVisible()

  // Delete the zone; the name must be typed to confirm.
  await page.goto('/dns/bind')
  await zones
    .getByRole('row', { name: new RegExp(zone.replaceAll('.', '\\.')) })
    .getByRole('button', { name: 'Delete zone' })
    .click()
  await dialog.getByLabel(`Type ${zone} to confirm`).fill(zone)
  await dialog.getByRole('button', { name: 'Delete zone' }).click()
  await expect(page.getByText(`Zone ${zone} deleted.`)).toBeVisible()
  await expect(zones.getByRole('link', { name: zone })).toHaveCount(0)
})

test('Kea: reservations in the host database, leases of an HA pair', async ({ page, request }) => {
  await loginBob(page)
  await page.goto('/dhcp/kea4-db')
  await expect(page.getByRole('table', { name: 'Subnets' })).toContainText('192.0.2.0/24')

  // Reservations: the one from the configuration file is read-only, new ones go to PostgreSQL.
  await page.getByRole('tab', { name: 'Reservations' }).click()
  const reservations = page.getByRole('table', { name: 'Reservations' })
  await expect(reservations.getByRole('row', { name: /printer/ })).toContainText('read-only')
  await page.getByRole('button', { name: 'Add reservation' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByLabel('Identifier', { exact: true }).fill('aa:bb:cc:00:00:22')
  await dialog.getByLabel('IPv4 address').fill('192.0.2.22')
  await dialog.getByLabel('Host name').fill('e2e-host')
  await dialog.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('Reservation e2e-host added.')).toBeVisible()
  await expect(reservations.getByRole('row', { name: /e2e-host/ })).toContainText('Host database')
  await reservations
    .getByRole('group', { name: 'Actions for e2e-host' })
    .getByRole('button', { name: 'Delete' })
    .click()
  await page.getByRole('dialog').getByRole('button', { name: 'Delete' }).click()
  await expect(page.getByText('Reservation e2e-host deleted.')).toBeVisible()

  // Kea doesn't pass leases changed through the API to the HA partner, so dcdash deletes
  // the lease on both servers of the group.
  const ip = '192.0.2.180'
  const ha = [process.env.DCDASH_IT_KEA_HA1_URL!, process.env.DCDASH_IT_KEA_HA2_URL!]
  for (const url of ha) {
    const r = await kea(request, url, 'lease4-add', {
      'ip-address': ip,
      'hw-address': '11:22:33:44:58:01',
      hostname: 'ha-e2e',
    })
    expect(r.result, r.text).toBe(0)
  }
  await page.goto('/dhcp/kea-ha1?tab=leases')
  await page.getByLabel('Search by').selectOption('ip')
  await page.getByLabel('Value').fill(ip)
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  await page.getByRole('button', { name: `Delete lease ${ip}` }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Delete' }).click()
  await expect(page.getByText(`Lease ${ip} deleted.`)).toBeVisible()
  for (const url of ha) {
    const r = await kea(request, url, 'lease4-get', { 'ip-address': ip })
    expect(r.result, `lease on ${url}`).toBe(3) // empty: not found
  }
})
