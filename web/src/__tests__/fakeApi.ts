// SPDX-License-Identifier: GPL-3.0-or-later
// A fake of the API client for view tests: routes requests to handlers by
// method and path, e.g. on('POST /api/v1/auth/login', (body) => [200, info]).
import type { SessionInfo } from '@/api/client'

type Handler = (body: unknown) => [number, unknown]

export class FakeApi {
  handlers = new Map<string, Handler>()
  calls: { route: string; body: unknown }[] = []

  on(route: string, handler: Handler) {
    this.handlers.set(route, handler)
    return this
  }

  private call(method: string, path: string, init?: { body?: unknown }) {
    const route = `${method} ${path}`
    this.calls.push({ route, body: init?.body })
    const handler = this.handlers.get(route)
    const [status, body] = handler
      ? handler(init?.body)
      : [404, { error: { code: 'not_found', message: route } }]
    const response = new Response(null, { status })
    return Promise.resolve(status < 400 ? { data: body, response } : { error: body, response })
  }

  client = {
    GET: (path: string, init?: { body?: unknown }) => this.call('GET', path, init),
    POST: (path: string, init?: { body?: unknown }) => this.call('POST', path, init),
    PUT: (path: string, init?: { body?: unknown }) => this.call('PUT', path, init),
    DELETE: (path: string, init?: { body?: unknown }) => this.call('DELETE', path, init),
  }
}

export function sessionInfo(over: Partial<SessionInfo> = {}): SessionInfo {
  const authenticated = (over.stage ?? 'authenticated') === 'authenticated'
  return {
    user: { username: 'alice', displayName: 'Alice', source: 'ldap', groups: [] },
    stage: 'authenticated',
    csrfToken: 'csrf',
    expiresAt: '2026-11-13T18:00:00Z',
    roles: authenticated ? ['root'] : [],
    permissions: authenticated ? ['*:*:*:admin'] : [],
    totp: { enrolled: true, required: true, recoveryCodesRemaining: 10 },
    ...over,
  }
}

export function apiError(code: string) {
  return { error: { code, message: code } }
}
