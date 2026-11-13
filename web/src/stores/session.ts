// SPDX-License-Identifier: GPL-3.0-or-later
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { api, ApiError, setCsrfToken, unwrap, type Schemas, type SessionInfo } from '@/api/client'
import { can as canPerm, canAny as canAnyPerm, type Action } from '@/auth/permissions'
import { usePreferencesStore } from './preferences'

export type EnrollmentInfo = Schemas['EnrollmentInfo']
export type TOTPRequest = Schemas['TOTPRequest']

/** The login session: password step, TOTP step, enrollment, logout. */
export const useSessionStore = defineStore('session', () => {
  const info = ref<SessionInfo | null>(null)
  const loaded = ref(false)

  const stage = computed(() => info.value?.stage ?? null)
  const authenticated = computed(() => stage.value === 'authenticated')
  const permissions = computed(() => (authenticated.value ? (info.value?.permissions ?? []) : []))
  const roles = computed(() => (authenticated.value ? (info.value?.roles ?? []) : []))
  const isRoot = computed(() => roles.value.includes('root'))

  function set(next: SessionInfo | null) {
    const wasAuthenticated = authenticated.value
    info.value = next
    setCsrfToken(next?.csrfToken ?? '')
    const prefs = usePreferencesStore()
    if (authenticated.value && !wasAuthenticated) {
      // The interface works with the cached preferences if this fails.
      prefs.load().catch(() => {})
    } else if (!authenticated.value) {
      prefs.reset()
    }
  }

  /** Loads the current session once (e.g. after a page reload). */
  async function ensureLoaded() {
    if (loaded.value) return
    try {
      set(await unwrap(api.GET('/api/v1/auth/me')))
    } catch (e) {
      if (!(e instanceof ApiError && e.status === 401)) throw e
      set(null)
    } finally {
      loaded.value = true
    }
  }

  async function login(username: string, password: string) {
    set(await unwrap(api.POST('/api/v1/auth/login', { body: { username, password } })))
  }

  /** Completes the login with a TOTP code or a recovery code. */
  async function verify(req: TOTPRequest) {
    set(await unwrap(api.POST('/api/v1/auth/totp', { body: req })))
  }

  async function startEnrollment(): Promise<EnrollmentInfo> {
    return unwrap(api.POST('/api/v1/auth/totp/enroll'))
  }

  /** Confirms the enrollment and returns the recovery codes (shown only once). */
  async function confirmEnrollment(code: string): Promise<string[]> {
    const res = await unwrap(api.POST('/api/v1/auth/totp/enroll/confirm', { body: { code } }))
    set(res.session)
    return res.recoveryCodes
  }

  async function regenerateRecoveryCodes(code: string): Promise<string[]> {
    const res = await unwrap(api.POST('/api/v1/auth/totp/recovery-codes', { body: { code } }))
    await refresh()
    return res.recoveryCodes
  }

  /** Reloads the session info (e.g. the number of remaining recovery codes). */
  async function refresh() {
    set(await unwrap(api.GET('/api/v1/auth/me')))
  }

  async function logout() {
    try {
      await unwrap(api.POST('/api/v1/auth/logout'))
    } finally {
      set(null)
    }
  }

  /** Called when the server reports that the session is gone. */
  function expire() {
    set(null)
  }

  function can(permission: string) {
    return canPerm(permissions.value, permission)
  }

  function canAny(module: string, action: Action = 'read') {
    return canAnyPerm(permissions.value, module, action)
  }

  return {
    info,
    loaded,
    stage,
    authenticated,
    permissions,
    roles,
    isRoot,
    ensureLoaded,
    login,
    verify,
    startEnrollment,
    confirmEnrollment,
    regenerateRecoveryCodes,
    refresh,
    logout,
    expire,
    can,
    canAny,
  }
})
