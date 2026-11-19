// SPDX-License-Identifier: GPL-3.0-or-later
// Changing the hardware of VMs: options, disks and network devices
// (docs/adr/0005-proxmox-vm-configuration.md).
import type { Schemas } from '@/api/client'

export type Hardware = Schemas['ProxmoxHardware']
export type OptionSetting = Schemas['ProxmoxOptionSetting']
export type OptionKey = OptionSetting['key']
export type HardwareDisk = Schemas['ProxmoxHardwareDisk']
export type NetworkDevice = Schemas['ProxmoxNetworkDevice']
export type DiskBus = Schemas['ProxmoxDiskRequest']['bus']
export type NetModel = Schemas['ProxmoxNetworkRequest']['model']

export const DISK_BUSES: DiskBus[] = ['scsi', 'virtio', 'sata', 'ide']
export const NET_MODELS: NetModel[] = ['virtio', 'e1000', 'e1000e', 'vmxnet3', 'rtl8139']
export const OS_TYPES = [
  'l26',
  'l24',
  'win11',
  'win10',
  'win8',
  'win7',
  'w2k8',
  'w2k3',
  'w2k',
  'wvista',
  'wxp',
  'solaris',
  'other',
]

/** How an option is edited. */
export type OptionKind = 'text' | 'textarea' | 'flag' | 'number' | 'ostype'

export const OPTION_KINDS: Record<OptionKey, OptionKind> = {
  name: 'text',
  description: 'textarea',
  tags: 'text',
  onboot: 'flag',
  protection: 'flag',
  agent: 'text',
  boot: 'text',
  cores: 'number',
  sockets: 'number',
  memory: 'number',
  balloon: 'number',
  cpu: 'text',
  numa: 'flag',
  ostype: 'ostype',
  hotplug: 'text',
}

export function guestWritePermission(cluster: string, vmid: number): string {
  return `proxmox:${cluster}:qemu/${vmid}:write`
}

/** The value an option has after its pending change. */
export function effectiveValue(o: OptionSetting): string {
  if (o.delete) return ''
  if (o.pending !== undefined) return o.pending
  return o.set ? o.value : ''
}

/** Form values of the options: the effective values; tags with ", ". */
export function optionsForm(hw: Hardware): Record<OptionKey, string> {
  const out = {} as Record<OptionKey, string>
  for (const o of hw.options) {
    const v = effectiveValue(o)
    out[o.key] =
      o.key === 'tags'
        ? v
            .split(/[;,\s]+/)
            .filter(Boolean)
            .join(', ')
        : v
  }
  return out
}

/** Normalizes a form value as the API expects it. */
function normalize(key: OptionKey, v: string): string {
  v = key === 'description' ? v : v.trim()
  if (key === 'tags') {
    return v
      .split(/[;,\s]+/)
      .filter(Boolean)
      .join(';')
  }
  return v
}

/** The changes between the hardware and the form: set and removed options. */
export function optionChanges(
  hw: Hardware,
  form: Record<OptionKey, string>,
): { set: Record<string, string>; delete: string[] } {
  const set: Record<string, string> = {}
  const del: string[] = []
  for (const o of hw.options) {
    const before = normalize(o.key, optionsForm(hw)[o.key])
    const after = normalize(o.key, form[o.key] ?? '')
    if (after === before) continue
    if (after === '') {
      if (o.key !== 'name') del.push(o.key)
    } else {
      set[o.key] = after
    }
  }
  return { set, delete: del }
}

/** Validation errors of the option form, as i18n keys with parameters. */
export function validateOptions(
  form: Record<OptionKey, string>,
  limits: Hardware['limits'],
): Partial<Record<OptionKey, { key: string; params?: Record<string, unknown> }>> {
  const errors: Partial<Record<OptionKey, { key: string; params?: Record<string, unknown> }>> = {}
  const int = (k: OptionKey, min: number, max: number) => {
    const v = form[k]?.trim()
    if (!v) return
    const n = Number(v)
    if (!Number.isInteger(n) || n < min || n > max) {
      errors[k] = { key: 'proxmox.hardware.errors.range', params: { min, max } }
    }
  }
  if (
    !/^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$/.test(
      form.name?.trim() ?? '',
    )
  ) {
    errors.name = { key: 'proxmox.create.errors.name' }
  }
  int('cores', 1, limits.cores)
  int('sockets', 1, 4)
  int('memory', 16, limits.memory)
  int('balloon', 0, limits.memory)
  const cores = Number(form.cores || 1) * Number(form.sockets || 1)
  if (!errors.cores && !errors.sockets && cores > limits.cores) {
    errors.sockets = { key: 'proxmox.hardware.errors.coresTotal', params: { max: limits.cores } }
  }
  if (!errors.balloon && form.balloon && Number(form.balloon) > Number(form.memory || 0)) {
    errors.balloon = { key: 'proxmox.hardware.errors.balloon' }
  }
  if (form.boot && !/^order=[a-z]+[0-9]{1,2}(;[a-z]+[0-9]{1,2})*$/.test(form.boot.trim())) {
    errors.boot = { key: 'proxmox.hardware.errors.boot' }
  }
  return errors
}

/** The size of a disk in whole GiB, rounded up. */
export function diskGiB(d: HardwareDisk): number {
  return Math.ceil(d.sizeBytes / 2 ** 30)
}

/** Whether the bus supports the option. */
export function supportsSSD(bus: string): boolean {
  return bus !== 'virtio'
}
export function supportsIOThread(bus: string): boolean {
  return bus === 'scsi' || bus === 'virtio'
}

/** Whether a VLAN tag is in one of the ranges ("100-199" or "5"). */
export function vlanAllowed(vlan: number, ranges: string[]): boolean {
  return ranges.some((r) => {
    const [a, b] = r.split('-').map(Number)
    return vlan >= a! && vlan <= (b ?? a)!
  })
}

/** Whether the hardware has pending changes. */
export function hasPending(hw: Hardware): boolean {
  return (
    hw.options.some((o) => o.pending !== undefined || o.delete) ||
    hw.disks.some((d) => d.pending !== undefined || d.delete) ||
    hw.networks.some((n) => n.pending !== undefined || n.delete)
  )
}
