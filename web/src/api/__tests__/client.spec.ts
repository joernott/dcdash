// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, it, expect, vi } from 'vitest'

import { createApiClient, type VersionInfo } from '../client'

function jsonResponse(status: number, body: unknown) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

describe('api client', () => {
  it('fetches the version', async () => {
    const info: VersionInfo = {
      version: 'v0.1.0',
      commit: 'abc',
      date: 'unknown',
      goVersion: 'go1',
    }
    const fetchMock = vi.fn<(req: Request) => Promise<Response>>(async () =>
      jsonResponse(200, info),
    )
    const client = createApiClient({ baseUrl: 'http://dcdash.test', fetch: fetchMock })

    const { data, error } = await client.GET('/api/v1/version')

    expect(error).toBeUndefined()
    expect(data).toEqual(info)
    expect(fetchMock.mock.calls[0]?.[0].url).toBe('http://dcdash.test/api/v1/version')
  })

  it('returns the error body', async () => {
    const body = { error: { code: 'not_found', message: 'no such API endpoint' } }
    const client = createApiClient({
      baseUrl: 'http://dcdash.test',
      fetch: async () => jsonResponse(404, body),
    })

    const { data, error } = await client.GET('/api/v1/version')

    expect(data).toBeUndefined()
    expect(error).toEqual(body)
  })
})
