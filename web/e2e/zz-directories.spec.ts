// SPDX-License-Identifier: GPL-3.0-or-later
// End-to-end tests against the Samba AD DC of the docker compose environment
// (scripts/e2e-full.sh, test/integration/samba): users and groups, the self-service
// password change, the groups of a project and the AD-integrated DNS. bob holds the role
// full-e2e (see server.sh): he may change users and groups named e2e-*.
import { expect, test, type Page } from '@playwright/test'

import { expectAccessible } from './a11y'

test.skip(process.env.DCDASH_E2E_FULL !== '1', 'needs the full environment (scripts/e2e-full.sh)')

async function loginBob(page: Page) {
  await page.goto('/')
  await page.getByLabel('User name').fill('bob')
  await page.getByLabel(/^Password$/).fill('bob-test-only')
  await page.getByRole('button', { name: 'Log in' }).click()
  await expect(page.getByRole('heading', { name: /Welcome/ })).toBeVisible()
}

test('Active Directory: users, self-service password change and groups', async ({
  page,
  browser,
}) => {
  const user = 'e2e-carol'
  await loginBob(page)
  await page.getByRole('link', { name: 'Directories' }).click()
  await expect(page.getByRole('heading', { name: 'ad.dcdash.test' })).toBeVisible()
  const users = page.getByRole('table', { name: 'Users' })
  // Provisioned users: alice can't be changed by bob, Administrator is protected.
  await expect(users.getByRole('row', { name: /alice/ })).toBeVisible()
  await expect(users.getByRole('row', { name: /Administrator/ })).toContainText('protected')
  await expect(users.getByRole('row', { name: /alice/ }).getByRole('button')).toHaveCount(0)
  await expectAccessible(page, 'directory users')

  // Leftovers of an aborted run.
  const dialog = page.getByRole('dialog')
  if ((await users.getByRole('button', { name: `Delete ${user}` }).count()) > 0) {
    await users.getByRole('button', { name: `Delete ${user}` }).click()
    await dialog.getByRole('button', { name: 'Delete' }).click()
    await expect(page.getByText(`${user} was deleted.`)).toBeVisible()
  }
  await page.getByRole('tab', { name: 'Groups' }).click()
  const leftover = page.getByRole('button', { name: 'Delete e2e-team' })
  await expect(page.getByRole('table', { name: 'Groups' })).toBeVisible()
  if ((await leftover.count()) > 0) {
    await leftover.click()
    await dialog.getByRole('button', { name: 'Delete' }).click()
    await expect(page.getByText('e2e-team was deleted.')).toBeVisible()
  }
  await page.getByRole('tab', { name: 'Users' }).click()

  // Create a user who must change the password.
  await page.getByRole('button', { name: 'Create a user' }).click()
  await dialog.getByLabel('Account name').fill(user)
  await dialog.getByLabel('Display name').fill('Carol E2E')
  await dialog.getByLabel('Initial password').fill('Carol-initial-1')
  await dialog.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText(`${user} was saved.`)).toBeVisible()
  await expect(users.getByRole('row', { name: new RegExp(user) })).toContainText(
    'must change password',
  )

  // The self-service page works without a login.
  const context = await browser.newContext()
  const anonymous = await context.newPage()
  await anonymous.goto('/login')
  await anonymous.getByRole('link', { name: 'Change your domain password' }).click()
  await expect(anonymous.getByRole('heading', { name: 'Change password' })).toBeVisible()
  await expectAccessible(anonymous, 'self-service password change')
  await anonymous.getByLabel('User name').fill(user)
  await anonymous.getByLabel('Current password').fill('wrong-password-1')
  await anonymous.getByLabel('New password', { exact: true }).fill('Carol-changed-2')
  await anonymous.getByLabel('Repeat the new password').fill('Carol-changed-2')
  await anonymous.getByRole('button', { name: 'Change password' }).click()
  await expect(anonymous.getByRole('alert')).toContainText(
    'The user name or the current password is wrong.',
  )
  await anonymous.getByLabel('Current password').fill('Carol-initial-1')
  await anonymous.getByLabel('New password', { exact: true }).fill('Carol-changed-2')
  await anonymous.getByLabel('Repeat the new password').fill('Carol-changed-2')
  await anonymous.getByRole('button', { name: 'Change password' }).click()
  await expect(anonymous.getByRole('status')).toContainText('Your password was changed.')
  await context.close()
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  await expect(users.getByRole('row', { name: new RegExp(user) })).not.toContainText(
    'must change password',
  )

  // Disable, enable, reset.
  const row = users.getByRole('row', { name: new RegExp(user) })
  await row.getByRole('button', { name: `Disable ${user}` }).click()
  await expect(page.getByText(`${user} was disabled`)).toBeVisible()
  await expect(row).toContainText('disabled')
  await row.getByRole('button', { name: `Enable ${user}` }).click()
  await expect(page.getByText(`${user} was enabled.`)).toBeVisible()
  await row.getByRole('button', { name: `Reset the password of ${user}` }).click()
  await dialog.getByLabel('New password').fill('Carol-reset-3')
  await dialog.getByRole('button', { name: 'Reset password' }).click()
  await expect(page.getByText(`The password of ${user} was reset.`)).toBeVisible()

  // A group with the user as member.
  await page.getByRole('tab', { name: 'Groups' }).click()
  const groups = page.getByRole('table', { name: 'Groups' })
  await page.getByRole('button', { name: 'Create a group' }).click()
  await dialog.getByLabel('Group name').fill('e2e-team')
  await dialog.getByLabel('Description').fill('E2E team')
  await dialog.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('e2e-team was saved.')).toBeVisible()
  await groups.getByRole('button', { name: 'Members of e2e-team' }).click()
  await dialog.getByLabel('Add a member').fill(user)
  await dialog.getByRole('button', { name: 'Add', exact: true }).click()
  await expect(dialog.getByRole('table')).toContainText('Carol E2E')
  await expectAccessible(page, 'group members')
  await dialog.getByRole('button', { name: `Remove ${user} from the group` }).click()
  await expect(dialog).toContainText('The group has no direct members.')
  await dialog.getByRole('button', { name: 'Close' }).last().click()
  await groups.getByRole('button', { name: 'Delete e2e-team' }).click()
  await dialog.getByRole('button', { name: 'Delete' }).click()
  await expect(page.getByText('e2e-team was deleted.')).toBeVisible()

  // Delete the user.
  await page.getByRole('tab', { name: 'Users' }).click()
  await users.getByRole('button', { name: `Delete ${user}` }).click()
  await dialog.getByRole('button', { name: 'Delete' }).click()
  await expect(page.getByText(`${user} was deleted.`)).toBeVisible()
  await expect(users.getByRole('row', { name: new RegExp(user) })).toHaveCount(0)
})

test('Active Directory: the groups of a project and the AD DNS', async ({ page }) => {
  await loginBob(page)
  await page.goto('/projects/ad-e2e')
  const groups = page.getByRole('table', { name: 'Active Directory groups' })
  await expect(groups.getByRole('row', { name: /lab-admins/ })).toContainText('alice')

  await page.goto('/dns/it-dns')
  const zones = page.getByRole('table', { name: 'Zones' })
  await zones.getByRole('link', { name: 'lab.dcdash.test' }).click()
  const records = page.getByRole('table', { name: 'Records' })
  await expect(records.getByRole('row', { name: /SOA/ })).toBeVisible()
  await page.getByRole('button', { name: 'Add record' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByLabel('Name').fill('e2e')
  await dialog.getByLabel('Type').selectOption('A')
  await dialog.getByLabel('Content').fill('192.0.2.81')
  await dialog.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText(/e2e.*\(A\) saved\./)).toBeVisible()
  await expect(records.getByText('192.0.2.81')).toBeVisible()
  await expectAccessible(page, 'AD DNS records')
  await records.getByRole('group', { name: /e2e/ }).getByRole('button', { name: 'Delete' }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Delete' }).click()
  await expect(page.getByText(/e2e.*\(A\) deleted\./)).toBeVisible()
})
