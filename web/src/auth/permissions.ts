// SPDX-License-Identifier: GPL-3.0-or-later
// Permission matching as in internal/rbac (module:instance:resource:action, "*"
// wildcards, action hierarchy). The frontend only uses it to hide what the user
// cannot use; the backend checks every request.

export const ACTIONS = ['read', 'operate', 'console', 'write', 'admin'] as const
export type Action = (typeof ACTIONS)[number]

interface Permission {
  module: string
  instance: string
  resource: string
  level: number
}

function level(action: string): number {
  return action === '*' ? ACTIONS.length : ACTIONS.indexOf(action as Action) + 1
}

/** Parses a permission string; returns undefined if it is malformed. */
export function parsePermission(s: string): Permission | undefined {
  const parts = s.trim().split(':')
  if (parts.length < 4) return undefined
  const action = parts[parts.length - 1]!
  const p = {
    module: parts[0]!,
    instance: parts[1]!,
    resource: parts.slice(2, -1).join(':'),
    level: level(action),
  }
  if (!p.module || !p.instance || !p.resource || p.level === 0) return undefined
  return p
}

/** Matches a value against a pattern in which "*" stands for any text. */
export function matchPattern(pattern: string, value: string): boolean {
  if (pattern === '*') return true
  if (!pattern.includes('*')) return pattern === value
  const parts = pattern.split('*')
  const first = parts[0]!
  const last = parts[parts.length - 1]!
  if (!value.startsWith(first)) return false
  let rest = value.slice(first.length)
  for (const part of parts.slice(1, -1)) {
    const i = rest.indexOf(part)
    if (i < 0) return false
    rest = rest.slice(i + part.length)
  }
  return rest.endsWith(last)
}

/** Reports whether one of the granted permissions covers the requested one. */
export function can(granted: readonly string[], requested: string): boolean {
  const req = parsePermission(requested)
  if (!req) return false
  return granted.some((g) => {
    const p = parsePermission(g)
    return (
      !!p &&
      p.level >= req.level &&
      matchPattern(p.module, req.module) &&
      matchPattern(p.instance, req.instance) &&
      matchPattern(p.resource, req.resource)
    )
  })
}

/**
 * Reports whether the user holds at least `action` on any resource of a module
 * (optionally of one instance); used to decide whether to show a module at all.
 */
export function canAny(
  granted: readonly string[],
  module: string,
  action: Action = 'read',
  instance?: string,
): boolean {
  const min = level(action)
  return granted.some((g) => {
    const p = parsePermission(g)
    return (
      !!p &&
      p.level >= min &&
      matchPattern(p.module, module) &&
      (instance === undefined || matchPattern(p.instance, instance))
    )
  })
}

/** System permissions (see docs/security.md). */
export const PERMISSIONS = {
  auditRead: 'system:core:audit:read',
  sessionsRead: 'system:core:sessions:read',
  sessionsWrite: 'system:core:sessions:write',
} as const
