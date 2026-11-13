// SPDX-License-Identifier: GPL-3.0-or-later
import { PERMISSIONS, type Action } from '@/auth/permissions'

/** An entry of the main navigation. */
export interface NavItem {
  /** Route name. */
  name: string
  /** i18n key of the label. */
  label: string
  /** Shown only with this permission. */
  permission?: string
  /** Shown only with at least `module.action` on any instance of the module. */
  module?: { name: string; action: Action }
}

/**
 * The main navigation. Modules (Proxmox, DNS, DHCP) are added in their sprints
 * with `module`, so that users only see modules they may use.
 */
export const navigation: NavItem[] = [
  { name: 'home', label: 'nav.home' },
  { name: 'audit', label: 'nav.audit', permission: PERMISSIONS.auditRead },
  { name: 'sessions', label: 'nav.sessions', permission: PERMISSIONS.sessionsRead },
]

/** Returns the entries the user may see. */
export function visibleNavigation(
  items: NavItem[],
  can: (permission: string) => boolean,
  canAny: (module: string, action: Action) => boolean,
): NavItem[] {
  return items.filter(
    (i) =>
      (!i.permission || can(i.permission)) && (!i.module || canAny(i.module.name, i.module.action)),
  )
}
