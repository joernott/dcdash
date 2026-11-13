// SPDX-License-Identifier: GPL-3.0-or-later
// End-to-end smoke tests against the real server (make test-e2e).
import { defineConfig, devices } from '@playwright/test'

const baseURL = process.env.DCDASH_E2E_URL ?? 'https://127.0.0.1:18443'

export default defineConfig({
  testDir: './e2e',
  // The tests share one server and log the same users in: run them in order.
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  retries: 0,
  reporter: process.env.CI ? [['github'], ['list']] : 'list',
  outputDir: '../.e2e/test-results',
  use: {
    baseURL,
    ignoreHTTPSErrors: true, // self-signed test certificate
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: {
    command: 'bash e2e/server.sh',
    url: `${baseURL}/healthz`,
    ignoreHTTPSErrors: true,
    reuseExistingServer: false,
    timeout: 30_000,
    stdout: 'pipe',
  },
})
