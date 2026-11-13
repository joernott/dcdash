// SPDX-License-Identifier: GPL-3.0-or-later
import {
  createRouter,
  createWebHistory,
  type RouteLocationNormalized,
  type RouteLocationRaw,
  type Router,
  type RouterHistory,
} from 'vue-router'

import { PERMISSIONS } from '@/auth/permissions'
import { useSessionStore } from '@/stores/session'

declare module 'vue-router' {
  interface RouteMeta {
    /** Reachable without login. */
    public?: boolean
    /** Permission required to open the page (hidden in the navigation otherwise). */
    permission?: string
    /** Module the user needs at least read on (any instance). */
    module?: string
    /** Only for the root role. */
    root?: boolean
    /** i18n key of the page title. */
    title?: string
  }
}

export const routes = [
  {
    path: '/login',
    name: 'login',
    component: () => import('@/views/LoginView.vue'),
    meta: { public: true, title: 'login.title' },
  },
  {
    path: '/enroll',
    name: 'enroll',
    component: () => import('@/views/EnrollView.vue'),
    meta: { public: true, title: 'enroll.title' },
  },
  {
    path: '/',
    component: () => import('@/layouts/AppLayout.vue'),
    children: [
      {
        path: '',
        name: 'home',
        component: () => import('@/views/HomeView.vue'),
        meta: { title: 'nav.home' },
      },
      {
        path: 'account',
        name: 'account',
        component: () => import('@/views/AccountView.vue'),
        meta: { title: 'account.title' },
      },
      {
        path: 'proxmox/:cluster?',
        name: 'proxmox',
        component: () => import('@/views/proxmox/ProxmoxView.vue'),
        meta: { title: 'nav.proxmox', module: 'proxmox' },
      },
      {
        path: 'proxmox/:cluster/:type(qemu|lxc)/:vmid(\\d+)',
        name: 'proxmox-guest',
        component: () => import('@/views/proxmox/GuestView.vue'),
        meta: { title: 'nav.proxmox', module: 'proxmox' },
      },
      {
        path: 'proxmox/:cluster/:type(qemu|lxc)/:vmid(\\d+)/console',
        name: 'proxmox-console',
        component: () => import('@/views/proxmox/ConsoleView.vue'),
        meta: { title: 'proxmox.console', module: 'proxmox' },
      },
      {
        path: 'proxmox/:cluster/nodes/:node/shell',
        name: 'proxmox-shell',
        component: () => import('@/views/proxmox/ConsoleView.vue'),
        meta: { title: 'proxmox.shell', module: 'proxmox' },
      },
      {
        path: 'admin/audit',
        name: 'audit',
        component: () => import('@/views/admin/AuditView.vue'),
        meta: { title: 'nav.audit', permission: PERMISSIONS.auditRead },
      },
      {
        path: 'admin/sessions',
        name: 'sessions',
        component: () => import('@/views/admin/SessionsView.vue'),
        meta: { title: 'nav.sessions', permission: PERMISSIONS.sessionsRead },
      },
      {
        path: 'forbidden',
        name: 'forbidden',
        component: () => import('@/views/ForbiddenView.vue'),
        meta: { title: 'forbidden.title' },
      },
      {
        path: ':pathMatch(.*)*',
        name: 'not-found',
        component: () => import('@/views/NotFoundView.vue'),
        meta: { title: 'notFound.title' },
      },
    ],
  },
]

/** Accepts only paths within the application as redirect target after login. */
export function safeRedirect(value: unknown): string {
  return typeof value === 'string' && value.startsWith('/') && !value.startsWith('//') ? value : '/'
}

/**
 * Decides where a navigation may go, based on the login stage and permissions.
 * The backend enforces all of this as well; the guard only avoids showing pages
 * that would fail.
 */
export async function guard(to: RouteLocationNormalized): Promise<true | RouteLocationRaw> {
  const session = useSessionStore()
  try {
    await session.ensureLoaded()
  } catch {
    // Server not reachable: continue; the page shows the error when it loads data.
  }
  const stage = session.stage

  if (to.name === 'login') {
    if (session.authenticated) return safeRedirect(to.query.redirect)
    if (stage === 'totp_enroll') return { name: 'enroll', query: to.query }
    return true
  }
  if (to.name === 'enroll') {
    return stage === 'totp_enroll' || session.authenticated ? true : { name: 'login' }
  }
  if (!session.authenticated) {
    if (stage === 'totp_enroll') return { name: 'enroll' }
    return { name: 'login', query: to.fullPath === '/' ? {} : { redirect: to.fullPath } }
  }
  if (
    (to.meta.permission && !session.can(to.meta.permission)) ||
    (to.meta.root && !session.isRoot) ||
    (to.meta.module && !session.canAny(to.meta.module))
  ) {
    return { name: 'forbidden' }
  }
  return true
}

export function createAppRouter(
  history: RouterHistory = createWebHistory(import.meta.env.BASE_URL),
): Router {
  const router = createRouter({ history, routes })
  router.beforeEach(guard)
  return router
}
