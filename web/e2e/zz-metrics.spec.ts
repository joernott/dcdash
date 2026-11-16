// SPDX-License-Identifier: GPL-3.0-or-later
// Prometheus metrics on the main listener (token from server.sh). Named "zz-" so that it
// runs last (one worker, files in name order) and sees the requests of the other tests,
// including the console websockets of proxmox.spec.ts.
import { expect, test } from '@playwright/test'

test('metrics require the token and count requests', async ({ request }) => {
  expect((await request.get('/metrics')).status()).toBe(401)
  expect(
    (await request.get('/metrics', { headers: { Authorization: 'Bearer wrong' } })).status(),
  ).toBe(401)

  const res = await request.get('/metrics', {
    headers: { Authorization: 'Bearer metrics-e2e-test-only' },
  })
  expect(res.status()).toBe(200)
  const body = await res.text()
  expect(body).toContain('dcdash_build_info{')
  expect(body).toMatch(/dcdash_certificate_expiry_timestamp_seconds \d/)
  expect(body).toContain('dcdash_backend_up{kind="dhcp",name="kea4"} 1')
  expect(body).toContain('dcdash_audit_events_total{action="auth.login",result="success"}')
  // Websockets are counted with status 101 (hijacked through the metrics middleware).
  expect(body).toMatch(
    /dcdash_http_requests_total\{code="101",method="GET",route="GET \/api\/v1\/proxmox\/console\/\{id\}"\} [1-9]/,
  )
})
