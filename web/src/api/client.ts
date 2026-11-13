// SPDX-License-Identifier: GPL-3.0-or-later
import createClient, { type Middleware } from 'openapi-fetch'

import type { components, paths } from './schema'

export type Schemas = components['schemas']
export type VersionInfo = Schemas['VersionInfo']
export type ErrorBody = Schemas['ErrorBody']
export type SessionInfo = Schemas['SessionInfo']

/** An API error with the stable error code from the response (see api/openapi.yaml). */
export class ApiError extends Error {
  constructor(
    readonly code: string,
    readonly status: number,
    message: string,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

export interface ClientOptions {
  baseUrl?: string
  fetch?: (input: Request) => Promise<Response>
  /** Returns the CSRF token of the session; sent with every state-changing request. */
  csrfToken?: () => string
  /** Called when a request is answered with 401 (e.g. the session expired). */
  onUnauthorized?: (code: string) => void
}

const safeMethods = new Set(['GET', 'HEAD', 'OPTIONS'])

/** Creates a typed client for the dcdash API (see api/openapi.yaml). */
export function createApiClient(options: ClientOptions = {}) {
  const client = createClient<paths>({ baseUrl: options.baseUrl ?? '', fetch: options.fetch })
  const middleware: Middleware = {
    onRequest({ request }) {
      const token = options.csrfToken?.()
      if (token && !safeMethods.has(request.method)) {
        request.headers.set('X-CSRF-Token', token)
      }
      return request
    },
    async onResponse({ response }) {
      if (response.status === 401 && options.onUnauthorized) {
        const body = (await response
          .clone()
          .json()
          .catch(() => null)) as ErrorBody | null
        options.onUnauthorized(body?.error.code ?? 'not_authenticated')
      }
      return response
    },
  }
  client.use(middleware)
  return client
}

/**
 * Returns the data of an openapi-fetch result or throws an ApiError. Network
 * failures (the request itself failed) become the code "network_error".
 */
export async function unwrap<T>(
  request: Promise<{ data?: T; error?: unknown; response: Response }>,
): Promise<T> {
  let result: { data?: T; error?: unknown; response: Response }
  try {
    result = await request
  } catch (e) {
    throw new ApiError('network_error', 0, e instanceof Error ? e.message : String(e))
  }
  if (result.error !== undefined || !result.response.ok) {
    const body = result.error as ErrorBody | undefined
    throw new ApiError(
      body?.error?.code ?? 'http_' + result.response.status,
      result.response.status,
      body?.error?.message ?? result.response.statusText,
    )
  }
  return result.data as T
}

// The application-wide client. The session store provides the CSRF token and
// handles expired sessions (see src/stores/session.ts).
let csrfToken = ''
let unauthorizedHandler: ((code: string) => void) | undefined

export function setCsrfToken(token: string) {
  csrfToken = token
}

export function setUnauthorizedHandler(handler: ((code: string) => void) | undefined) {
  unauthorizedHandler = handler
}

export const api = createApiClient({
  csrfToken: () => csrfToken,
  onUnauthorized: (code) => unauthorizedHandler?.(code),
})
