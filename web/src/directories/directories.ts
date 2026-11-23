// SPDX-License-Identifier: GPL-3.0-or-later
// Helpers for the Active Directory pages.
import type { Schemas } from '@/api/client'

type User = Schemas['DirectoryUser']

/** The value of the first RDN of a DN, unescaped: "CN=Doe\, John,OU=x" -> "Doe, John". */
export function cnOf(dn: string): string {
  let out = ''
  let i = dn.indexOf('=') + 1
  for (; i > 0 && i < dn.length; i++) {
    const c = dn[i]!
    if (c === '\\' && i + 1 < dn.length) {
      out += dn[++i]
    } else if (c === ',') {
      break
    } else {
      out += c
    }
  }
  return out
}

export interface UserState {
  /** i18n key below directories.users.states. */
  key: 'disabled' | 'lockedOut' | 'mustChange' | 'expired' | 'protected' | 'unmanaged'
  tone: 'danger' | 'muted'
}

/** The notable states of a user, most important first. */
export function userStates(u: User): UserState[] {
  const out: UserState[] = []
  if (u.disabled) out.push({ key: 'disabled', tone: 'danger' })
  if (u.lockedOut) out.push({ key: 'lockedOut', tone: 'danger' })
  if (u.passwordExpired) out.push({ key: 'expired', tone: 'danger' })
  if (u.mustChangePassword) out.push({ key: 'mustChange', tone: 'muted' })
  if (u.protected) out.push({ key: 'protected', tone: 'muted' })
  if (!u.managed) out.push({ key: 'unmanaged', tone: 'muted' })
  return out
}
