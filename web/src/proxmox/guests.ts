// SPDX-License-Identifier: GPL-3.0-or-later
// Helpers for Proxmox guests: permissions, available actions, formatting.
import type { Schemas } from '@/api/client'

export type Guest = Schemas['ProxmoxGuest']
export type GuestType = Guest['type']
export type PowerAction = 'start' | 'shutdown' | 'stop' | 'reboot' | 'suspend' | 'resume'

/** Permission string for a guest, e.g. proxmox:lab:qemu/101:operate. */
export function guestPermission(
  cluster: string,
  type: string,
  vmid: number,
  action: 'read' | 'operate' | 'console',
): string {
  return `proxmox:${cluster}:${type}/${vmid}:${action}`
}

/** Permission string for a node shell (root on the hypervisor: admin). */
export function nodeShellPermission(cluster: string, node: string): string {
  return `proxmox:${cluster}:node/${node}:admin`
}

/** The power actions that make sense in the guest's current state. */
export function availableActions(g: Pick<Guest, 'status' | 'template' | 'lock'>): PowerAction[] {
  if (g.template) return []
  switch (g.status) {
    case 'running':
      return ['shutdown', 'reboot', 'suspend', 'stop']
    case 'stopped':
      return ['start']
    case 'paused':
    case 'suspended':
      return ['resume', 'stop']
    default:
      return []
  }
}

/** Actions that may lose data and are shown as dangerous. */
export const DANGEROUS_ACTIONS: readonly PowerAction[] = ['stop', 'reboot']

/** Maps a Proxmox status to the badge style. */
export function statusStyle(status: string): 'ok' | 'warning' | 'error' | 'unknown' {
  switch (status) {
    case 'running':
    case 'online':
    case 'available':
      return 'ok'
    case 'paused':
    case 'suspended':
      return 'warning'
    case 'offline':
      return 'error'
    default:
      return 'unknown'
  }
}

/** Ratio used/total in 0..1, or undefined if the total is unknown. */
export function ratio(used: number, total: number): number | undefined {
  return total > 0 ? Math.min(1, Math.max(0, used / total)) : undefined
}

const UNITS = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB']

/** Formats bytes with binary units, e.g. 1.5 GiB. */
export function formatBytes(bytes: number, locale: string): string {
  let v = bytes
  let i = 0
  while (Math.abs(v) >= 1024 && i < UNITS.length - 1) {
    v /= 1024
    i++
  }
  const digits = i === 0 || v >= 100 ? 0 : 1
  return `${new Intl.NumberFormat(locale, { maximumFractionDigits: digits }).format(v)} ${UNITS[i]}`
}

/** Splits seconds into days, hours and minutes for display. */
export function uptimeParts(seconds: number): { days: number; hours: number; minutes: number } {
  const s = Math.max(0, Math.floor(seconds))
  return {
    days: Math.floor(s / 86400),
    hours: Math.floor((s % 86400) / 3600),
    minutes: Math.floor((s % 3600) / 60),
  }
}

/** Filter of the guest list. */
export interface GuestFilter {
  text: string
  type: '' | GuestType
  status: string
  node: string
}

/** Applies the filter; text matches name, VMID and tags (case-insensitive). */
export function filterGuests(guests: Guest[], f: GuestFilter): Guest[] {
  const text = f.text.trim().toLowerCase()
  return guests.filter(
    (g) =>
      (!f.type || g.type === f.type) &&
      (!f.status || g.status === f.status) &&
      (!f.node || g.node === f.node) &&
      (!text ||
        g.name.toLowerCase().includes(text) ||
        String(g.vmid).includes(text) ||
        g.tags.some((t) => t.toLowerCase().includes(text))),
  )
}
