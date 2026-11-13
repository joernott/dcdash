// SPDX-License-Identifier: GPL-3.0-or-later
// Smoke test of the real server with the built frontend: login with TOTP
// enrollment, overview, permissions, admin views, theme and language.
// Runs in order (playwright.config.ts): alice enrolls in the first test.
import { expect, test, type Page } from '@playwright/test'

import { totp } from './totp'

let aliceSecret = ''
let recoveryCodes: string[] = []
// Codes are accepted only once per 30 s step; use a later step for each login.
let step = 0

async function login(page: Page, user: string, password: string) {
  await page.goto('/')
  await page.getByLabel(/User name|Benutzername/).fill(user)
  await page.getByLabel(/^(Password|Passwort)$/).fill(password)
  await page.getByRole('button', { name: /^(Log in|Anmelden)$/ }).click()
}

async function expectTheme(page: Page, dark: boolean) {
  await expect(page.locator('html')).toHaveClass(dark ? /dark/ : /^(?!.*dark).*$/)
}

test('root enrolls TOTP at the first login and sees everything', async ({ page }) => {
  await login(page, 'alice', 'alice-test-only')

  await expect(page.getByRole('heading', { name: 'Set up an authenticator app' })).toBeVisible()
  await expect(page.getByRole('img', { name: 'QR code for the authenticator app' })).toBeVisible()
  await page.getByRole('button', { name: /Show the key for manual entry/ }).click()
  aliceSecret = (await page.getByTestId('secret').textContent())!.trim()
  expect(aliceSecret).toMatch(/^[A-Z2-7]{32}$/)

  await page.getByRole('textbox', { name: 'Code', exact: true }).fill(totp(aliceSecret, step))
  await page.getByRole('button', { name: 'Confirm' }).click()
  await expect(page.getByRole('list', { name: 'Recovery codes' }).getByRole('listitem')).toHaveCount(10)
  recoveryCodes = await page.getByRole('list', { name: 'Recovery codes' }).getByRole('listitem').allTextContents()
  await page.getByRole('button', { name: 'Continue' }).click()

  // Overview with health tiles.
  await expect(page.getByRole('heading', { name: /Welcome/ })).toBeVisible()
  await expect(page.getByText('All components are working.')).toBeVisible()
  await expect(page.getByRole('heading', { name: 'User directory' })).toBeVisible()

  // Root sees the admin views; the audit log shows the login.
  const nav = page.getByRole('navigation', { name: 'Main navigation' })
  await nav.getByRole('link', { name: 'Audit log' }).click()
  await expect(page.getByRole('cell', { name: 'auth.totp.enroll' }).first()).toBeVisible()
  await nav.getByRole('link', { name: 'Sessions' }).click()
  await expect(page.getByText('this session')).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Reset authenticator app' })).toBeVisible()
})

test('theme and language follow the user to the next login', async ({ page }) => {
  step += 1
  await login(page, 'alice', 'alice-test-only')
  await expect(page.getByRole('heading', { name: 'Verification code' })).toBeVisible()
  await page.getByRole('textbox', { name: 'Code', exact: true }).fill(totp(aliceSecret, step))
  await page.getByRole('button', { name: 'Verify' }).click()
  await expect(page.getByRole('heading', { name: /Welcome/ })).toBeVisible()
  await expectTheme(page, false)
  await page.screenshot({ path: '../.e2e/screenshots/overview-light-en.png', fullPage: true })

  await page.getByRole('button', { name: /Alice|alice/ }).click()
  await page.getByLabel('Theme').selectOption('dark')
  await expectTheme(page, true)
  await page.getByLabel('Language').selectOption('de')
  await expect(page.getByRole('link', { name: 'Übersicht' })).toBeVisible()
  await page.keyboard.press('Escape')
  await page.screenshot({ path: '../.e2e/screenshots/overview-dark-de.png', fullPage: true })

  // A new browser context (other browser) gets the preferences from the server.
  await page.getByRole('button', { name: /Alice|alice/ }).click()
  await page.getByRole('button', { name: 'Abmelden' }).click()
  await expect(page.getByRole('heading', { name: 'Anmelden' })).toBeVisible()
  const other = await page.context().browser()!.newContext({ ignoreHTTPSErrors: true, locale: 'en-US' })
  const page2 = await other.newPage()
  // The next TOTP step is not valid yet, so this login uses a recovery code.
  await login(page2, 'alice', 'alice-test-only')
  await page2.getByRole('button', { name: 'Use a recovery code' }).click()
  await page2.getByRole('textbox', { name: 'Recovery code' }).fill(recoveryCodes[0]!)
  await page2.getByRole('button', { name: 'Verify' }).click()
  await expect(page2.getByRole('link', { name: 'Übersicht' })).toBeVisible()
  await expectTheme(page2, true)
  await other.close()
})

test('a user without TOTP requirement and admin permissions', async ({ browser }) => {
  const context = await browser.newContext({ ignoreHTTPSErrors: true, locale: 'de-DE' })
  const page = await context.newPage()

  await login(page, 'bob', 'wrong-password')
  await expect(page.getByRole('alert')).toHaveText('Benutzername oder Passwort ist falsch.')

  await login(page, 'bob', 'bob-test-only')
  await expect(page.getByRole('heading', { name: /Willkommen/ })).toBeVisible()
  const nav = page.getByRole('navigation', { name: 'Hauptnavigation' })
  await expect(nav.getByRole('link', { name: 'Audit-Log' })).toHaveCount(0)

  await page.goto('/admin/audit')
  await expect(page.getByRole('heading', { name: 'Kein Zugriff' })).toBeVisible()
  await context.close()
})

test('pages require a login', async ({ page }) => {
  await page.goto('/admin/sessions')
  await expect(page).toHaveURL(/\/login\?redirect=%2Fadmin%2Fsessions|\/login\?redirect=\/admin\/sessions/)
  const res = await page.request.get('/api/v1/status')
  expect(res.status()).toBe(401)
})
