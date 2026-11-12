// SPDX-License-Identifier: GPL-3.0-or-later
import createClient from 'openapi-fetch'

import type { components, paths } from './schema'

export type VersionInfo = components['schemas']['VersionInfo']
export type ErrorBody = components['schemas']['ErrorBody']

/** Creates a typed client for the dcdash API (see api/openapi.yaml). */
export function createApiClient(
  options: { baseUrl?: string; fetch?: (input: Request) => Promise<Response> } = {},
) {
  return createClient<paths>({ baseUrl: options.baseUrl ?? '', fetch: options.fetch })
}

export const api = createApiClient()
