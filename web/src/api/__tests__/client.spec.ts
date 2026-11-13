// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, it, expect, vi } from 'vitest'
import type { Composer } from 'vue-i18n'

import { ApiError, createApiClient, unwrap, type VersionInfo } from '../client'
import { errorMessage } from '../errors'
import { createAppI18n } from '@/i18n'

function jsonResponse(status: number, body: unknown) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

const info: VersionInfo = { version: 'v0.1.0', commit: 'abc', date: 'unknown', goVersion: 'go1' }

describe('api client', () => {
  it('fetches the version', async () => {
    const fetchMock = vi.fn<(req: Request) => Promise<Response>>(async () =>
      jsonResponse(200, info),
    )
    const client = createApiClient({ baseUrl: 'http://dcdash.test', fetch: fetchMock })

    expect(await unwrap(client.GET('/api/v1/version'))).toEqual(info)
    expect(fetchMock.mock.calls[0]?.[0].url).toBe('http://dcdash.test/api/v1/version')
  })

  it('sends the CSRF token only with state-changing requests', async () => {
    const requests: Request[] = []
    const client = createApiClient({
      baseUrl: 'http://dcdash.test',
      csrfToken: () => 'tok',
      fetch: async (r) => {
        requests.push(r)
        return new Response(null, { status: 204 })
      },
    })
    await client.GET('/api/v1/version')
    await client.POST('/api/v1/auth/logout')
    expect(requests[0]?.headers.get('X-CSRF-Token')).toBeNull()
    expect(requests[1]?.headers.get('X-CSRF-Token')).toBe('tok')
  })

  it('throws ApiError with the error code and reports 401', async () => {
    const onUnauthorized = vi.fn<(code: string) => void>()
    const client = createApiClient({
      baseUrl: 'http://dcdash.test',
      onUnauthorized,
      fetch: async () =>
        jsonResponse(401, { error: { code: 'not_authenticated', message: 'not logged in' } }),
    })
    const err = await unwrap(client.GET('/api/v1/auth/me')).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).code).toBe('not_authenticated')
    expect((err as ApiError).status).toBe(401)
    expect(onUnauthorized).toHaveBeenCalledWith('not_authenticated')
  })

  it('maps network failures and bodies without error code', async () => {
    const down = createApiClient({
      baseUrl: 'http://dcdash.test',
      fetch: async () => {
        throw new TypeError('Failed to fetch')
      },
    })
    expect(((await unwrap(down.GET('/api/v1/version')).catch((e) => e)) as ApiError).code).toBe(
      'network_error',
    )
    const proxy = createApiClient({
      baseUrl: 'http://dcdash.test',
      fetch: async () => new Response('Bad Gateway', { status: 502, statusText: 'Bad Gateway' }),
    })
    expect(((await unwrap(proxy.GET('/api/v1/version')).catch((e) => e)) as ApiError).code).toBe(
      'http_502',
    )
  })
})

describe('errorMessage', () => {
  it('translates known codes and falls back for unknown ones', () => {
    const de = createAppI18n('de').global as unknown as Composer
    expect(errorMessage(de, new ApiError('invalid_credentials', 401, 'x'))).toBe(
      'Benutzername oder Passwort ist falsch.',
    )
    const en = createAppI18n('en').global as unknown as Composer
    expect(errorMessage(en, new ApiError('something_new', 500, 'boom'))).toContain('boom')
    expect(errorMessage(en, new Error('oops'))).toContain('oops')
  })
})
