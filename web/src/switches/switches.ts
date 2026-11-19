// SPDX-License-Identifier: GPL-3.0-or-later
// Aruba CX switches: ports, VLANs and MAC addresses.
import type { Schemas } from '@/api/client'

export type SwitchInfo = Schemas['Switch']
export type SwitchPort = Schemas['SwitchPort']
export type SwitchVlan = Schemas['SwitchVLAN']
export type MacResult = Schemas['SwitchMACResult']

/** Formats VLAN IDs compactly: [10, 20, 21, 22] -> "10, 20-22". */
export function formatVlans(ids: number[]): string {
  const sorted = [...new Set(ids)].sort((a, b) => a - b)
  const parts: string[] = []
  let i = 0
  while (i < sorted.length) {
    let j = i
    while (j + 1 < sorted.length && sorted[j + 1] === sorted[j]! + 1) j++
    parts.push(j > i + 1 ? `${sorted[i]}-${sorted[j]}` : sorted.slice(i, j + 1).join(', '))
    i = j + 1
  }
  return parts.join(', ')
}

/** Parses "10, 20-22" into sorted VLAN IDs; null if invalid (IDs 1-4094, at most 4094). */
export function parseVlans(text: string): number[] | null {
  const out = new Set<number>()
  for (const raw of text.split(/[,\s]+/)) {
    if (!raw) continue
    const m = /^(\d{1,4})(?:-(\d{1,4}))?$/.exec(raw)
    if (!m) return null
    const from = Number(m[1])
    const to = m[2] ? Number(m[2]) : from
    if (from < 1 || to > 4094 || to < from) return null
    for (let v = from; v <= to; v++) out.add(v)
  }
  return [...out].sort((a, b) => a - b)
}

/** The status of a port for the status badge. */
export function portState(p: SwitchPort): 'up' | 'down' | 'disabled' {
  if (!p.enabled) return 'disabled'
  return p.linkUp ? 'up' : 'down'
}

/** Formats a speed: 1000 -> "1 Gbit/s", 100 -> "100 Mbit/s". */
export function formatSpeed(mbps: number): string {
  if (!mbps) return ''
  return mbps >= 1000 ? `${mbps / 1000} Gbit/s` : `${mbps} Mbit/s`
}

/** The VLAN text of a port: "10" for access, "1 + 10, 20" or "1 + all" for trunks. */
export function portVlans(p: SwitchPort, all: string): string {
  if (p.mode === 'access') return String(p.vlan)
  const trunk = p.allVlans ? all : formatVlans(p.trunkVlans.filter((v) => v !== p.vlan))
  return trunk ? `${p.vlan} + ${trunk}` : String(p.vlan)
}

/** Whether a port matches a filter text (name, description, VLAN, neighbor). */
export function portMatches(p: SwitchPort, q: string): boolean {
  const s = q.trim().toLowerCase()
  if (!s) return true
  const fields = [
    p.name,
    p.description,
    String(p.vlan),
    ...p.neighbors.map((n) => n.systemName ?? ''),
  ]
  if (p.mode === 'trunk') fields.push(...p.trunkVlans.map(String))
  return fields.some((f) => f.toLowerCase().includes(s))
}

/** Whole days since a time (the uptime); null if unknown. */
export function uptimeDays(booted: string | undefined, now = Date.now()): number | null {
  if (!booted) return null
  const t = Date.parse(booted)
  return Number.isNaN(t) ? null : Math.max(0, Math.floor((now - t) / 86_400_000))
}
