// SPDX-License-Identifier: GPL-3.0-or-later
// Creating and deleting Proxmox guests: the wizard's form, its validation and
// the request (see docs/adr/0004-proxmox-provisioning.md).
import type { Schemas } from '@/api/client'

export type ProvisioningOptions = Schemas['ProxmoxProvisioningOptions']
export type CreateRequest = Schemas['ProxmoxCreateRequest']
export type Job = Schemas['ProxmoxJob']

/** What is created: a clone of a VM template, a VM from an ISO image, or a container. */
export type Kind = 'clone' | 'iso' | 'lxc'

export interface CreateForm {
  kind: Kind
  node: string
  /** Empty: the next free ID. */
  vmid: string
  name: string
  template: string
  full: boolean
  iso: string
  osType: 'linux' | 'windows' | 'other'
  osTemplate: string
  cores: string
  /** MiB */
  memory: string
  /** GiB; empty keeps the template's size for clones. */
  disk: string
  storage: string
  bridge: string
  vlan: string
  ipv4Mode: 'none' | 'dhcp' | 'static'
  ipv4: string
  gateway4: string
  ipv6Mode: 'none' | 'auto' | 'dhcp' | 'static'
  ipv6: string
  gateway6: string
  nameservers: string
  searchDomain: string
  user: string
  password: string
  sshKeys: string
  start: boolean
  description: string
  tags: string
}

/** A new form with the defaults of the options. */
export function newForm(o: ProvisioningOptions, kind: Kind = 'clone'): CreateForm {
  return {
    kind,
    node: o.node,
    vmid: '',
    name: '',
    template: o.templates[0] ? String(o.templates[0].vmid) : '',
    full: true,
    iso: o.isos[0]?.volid ?? '',
    osType: 'linux',
    osTemplate: o.containerTemplates[0]?.volid ?? '',
    cores: String(o.defaults.cores),
    memory: String(o.defaults.memory),
    disk: kind === 'clone' ? '' : String(o.defaults.disk),
    storage: o.defaults.storage,
    bridge: o.defaults.bridge,
    vlan: '',
    ipv4Mode: kind === 'iso' ? 'none' : 'dhcp',
    ipv4: '',
    gateway4: '',
    ipv6Mode: 'none',
    ipv6: '',
    gateway6: '',
    nameservers: '',
    searchDomain: '',
    user: '',
    password: '',
    sshKeys: '',
    start: true,
    description: '',
    tags: '',
  }
}

/** Parses ranges like ["100-199", "300"]. */
export function parseRanges(ranges: readonly string[]): [number, number][] {
  return ranges.map((r) => {
    const [lo, hi] = r.split('-')
    const a = Number(lo)
    return [a, hi === undefined ? a : Number(hi)]
  })
}

export function inRanges(n: number, ranges: readonly string[]): boolean {
  return parseRanges(ranges).some(([a, b]) => n >= a && n <= b)
}

const DNS_NAME =
  /^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$/
const UNIX_USER = /^[a-z_][a-z0-9_-]{0,31}$/
const TAG = /^[a-z0-9_][a-z0-9_+.-]{0,63}$/
const SSH_KEY = /^(ssh-|ecdsa-|sk-)[a-z0-9@.-]+ [A-Za-z0-9+/=]+( \P{Cc}*)?$/u
const IPV4 = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/

export function isIPv4(s: string): boolean {
  return IPV4.test(s)
}

export function isIPv6(s: string): boolean {
  if (!s.includes(':') || !/^[0-9A-Fa-f:.]+$/.test(s)) return false
  try {
    return new URL(`http://[${s}]/`).hostname.length > 2
  } catch {
    return false
  }
}

function isPrefix(s: string, v6: boolean): boolean {
  const [addr, len, ...rest] = s.split('/')
  if (rest.length > 0 || len === undefined || !/^\d{1,3}$/.test(len)) return false
  const n = Number(len)
  return v6 ? isIPv6(addr!) && n <= 128 : isIPv4(addr!) && n <= 32
}

function lines(s: string): string[] {
  return s
    .split('\n')
    .map((l) => l.trim())
    .filter((l) => l !== '')
}

function words(s: string): string[] {
  return s
    .split(/[\s,;]+/)
    .map((w) => w.trim())
    .filter((w) => w !== '')
}

/** Error keys (proxmox.create.errors.<key>) by field. */
export type FormErrors = Partial<Record<keyof CreateForm, string>>

/** Validates the form like the server does; returns the errors by field. */
export function validate(f: CreateForm, o: ProvisioningOptions): FormErrors {
  const e: FormErrors = {}
  const int = (s: string) => (/^\d+$/.test(s.trim()) ? Number(s) : NaN)
  if (f.vmid.trim() !== '') {
    const id = int(f.vmid)
    if (!(id >= 100 && id <= 999999999) || (o.vmids.length > 0 && !inRanges(id, o.vmids)))
      e.vmid = 'vmid'
  }
  if (f.name.trim().length > 253 || !DNS_NAME.test(f.name.trim())) e.name = 'name'
  if (f.kind === 'clone' && !o.templates.some((t) => String(t.vmid) === f.template))
    e.template = 'required'
  if (f.kind === 'iso' && !f.iso) e.iso = 'required'
  if (f.kind === 'lxc' && !f.osTemplate) e.osTemplate = 'required'
  if (f.kind !== 'clone' && !f.node) e.node = 'required'
  const cores = int(f.cores)
  if (!(cores >= 1 && cores <= o.limits.cores)) e.cores = 'cores'
  const mem = int(f.memory)
  if (!(mem >= 16 && mem <= o.limits.memory)) e.memory = 'memory'
  if (f.kind !== 'clone' || f.disk.trim() !== '') {
    const disk = int(f.disk)
    if (!(disk >= 1 && disk <= o.limits.disk)) e.disk = 'disk'
  }
  if (
    (f.kind !== 'clone' || f.full) &&
    !o.storages.some((s) => s.name === f.storage && (f.kind === 'lxc' ? s.container : s.vm))
  )
    e.storage = 'required'
  if (!o.bridges.some((b) => b.name === f.bridge)) e.bridge = 'required'
  if (f.vlan.trim() !== '' && !inRanges(int(f.vlan), o.vlans)) e.vlan = 'vlan'
  if (f.kind !== 'iso') {
    if (f.ipv4Mode === 'static') {
      if (!isPrefix(f.ipv4.trim(), false)) e.ipv4 = 'ipv4'
      if (f.gateway4.trim() !== '' && !isIPv4(f.gateway4.trim())) e.gateway4 = 'ipv4Address'
    }
    if (f.ipv6Mode === 'static') {
      if (!isPrefix(f.ipv6.trim(), true)) e.ipv6 = 'ipv6'
      if (f.gateway6.trim() !== '' && !isIPv6(f.gateway6.trim())) e.gateway6 = 'ipv6Address'
    }
    const ns = words(f.nameservers)
    if (ns.length > 3 || ns.some((a) => !isIPv4(a) && !isIPv6(a))) e.nameservers = 'nameservers'
    if (f.searchDomain.trim() !== '' && !DNS_NAME.test(f.searchDomain.trim()))
      e.searchDomain = 'domain'
    if (f.kind === 'clone' && f.user.trim() !== '' && !UNIX_USER.test(f.user.trim()))
      e.user = 'user'
    if (f.password !== '' && (f.password.length < 5 || f.password.length > 128))
      e.password = 'password'
    if (lines(f.sshKeys).some((k) => !SSH_KEY.test(k))) e.sshKeys = 'sshKeys'
    if (f.kind === 'lxc' && f.password === '' && lines(f.sshKeys).length === 0) e.password = 'login'
  }
  if (words(f.tags).some((t) => !TAG.test(t))) e.tags = 'tags'
  return e
}

/** The request for a valid form. */
export function toRequest(f: CreateForm): CreateRequest {
  const opt = (s: string) => (s.trim() === '' ? undefined : s.trim())
  const num = (s: string) => (s.trim() === '' ? undefined : Number(s))
  const req: CreateRequest = {
    type: f.kind === 'lxc' ? 'lxc' : 'qemu',
    name: f.name.trim(),
    vmid: num(f.vmid),
    cores: Number(f.cores),
    memory: Number(f.memory),
    disk: num(f.disk) ?? 0,
    network: { bridge: f.bridge, vlan: num(f.vlan) },
    start: f.start,
    description: opt(f.description),
  }
  const tags = words(f.tags)
  if (tags.length > 0) req.tags = tags
  if (f.kind === 'clone') {
    req.source = 'template'
    req.template = Number(f.template)
    req.full = f.full
    if (f.full) req.storage = f.storage
    req.user = opt(f.user)
  } else {
    req.node = f.node
    req.storage = f.storage
  }
  if (f.kind === 'iso') {
    req.source = 'iso'
    req.iso = f.iso
    req.osType = f.osType
    return req
  }
  if (f.kind === 'lxc') req.osTemplate = f.osTemplate
  const n = req.network
  if (f.ipv4Mode === 'dhcp') n.ipv4 = 'dhcp'
  if (f.ipv4Mode === 'static') {
    n.ipv4 = f.ipv4.trim()
    n.gateway4 = opt(f.gateway4)
  }
  if (f.ipv6Mode === 'auto' || f.ipv6Mode === 'dhcp') n.ipv6 = f.ipv6Mode
  if (f.ipv6Mode === 'static') {
    n.ipv6 = f.ipv6.trim()
    n.gateway6 = opt(f.gateway6)
  }
  const ns = words(f.nameservers)
  if (ns.length > 0) n.nameservers = ns
  n.searchDomain = opt(f.searchDomain)
  if (f.password !== '') req.password = f.password
  const keys = lines(f.sshKeys)
  if (keys.length > 0) req.sshKeys = keys.join('\n')
  return req
}

/** Permission to create or delete a guest. */
export function guestAdminPermission(cluster: string, type: string, vmid: number): string {
  return `proxmox:${cluster}:${type}/${vmid}:admin`
}
