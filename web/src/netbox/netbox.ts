// SPDX-License-Identifier: GPL-3.0-or-later
// NetBox as a data source of the creation wizard: VLAN names and free addresses.
import type { Schemas } from '@/api/client'

export type NetBoxInfo = Schemas['NetBoxInfo']
export type NetBoxVlan = Schemas['NetBoxVLAN']

/** The permission to read VLANs and free addresses: netbox:<name>:ipam:read. */
export function ipamPermission(name: string): string {
  return `netbox:${name}:ipam:read`
}

/** Parses a VLAN ID (1-4094); null if the text is no VLAN ID. */
export function parseVid(s: string): number | null {
  if (!/^\d{1,4}$/.test(s.trim())) return null
  const v = Number(s)
  return v >= 1 && v <= 4094 ? v : null
}

/** The address without prefix length, for comparisons. */
function host(a: string): string {
  return a.split('/')[0]!.trim().toLowerCase()
}

/**
 * The first free address of the VLAN's prefixes of a family that isn't in used (addresses
 * of other network devices of the form); null if there is none.
 */
export function freeAddress(vlan: NetBoxVlan, family: 4 | 6, used: string[]): string | null {
  const taken = new Set(used.filter((u) => u).map(host))
  for (const p of vlan.prefixes) {
    if (p.family !== family) continue
    const a = p.free.find((f) => !taken.has(host(f)))
    if (a) return a
  }
  return null
}

export type NetBoxResult = Schemas['NetBoxResult']

/**
 * The message for a change that was made but could not be written to NetBox; null if the
 * write-back succeeded or didn't happen.
 */
export function netboxFailure(
  result: NetBoxResult | null | undefined,
  t: (key: string, params: Record<string, unknown>) => string,
): string | null {
  return result?.status === 'failed'
    ? t('netbox.writeBackFailed', { error: result.error ?? '' })
    : null
}
