// SPDX-License-Identifier: GPL-3.0-or-later
// Creating and deleting Proxmox guests: the wizard's form, its validation and
// the request (see docs/adr/0004-proxmox-provisioning.md).
import type { Schemas } from '@/api/client'

export type ProvisioningOptions = Schemas['ProxmoxProvisioningOptions']
export type CreateRequest = Schemas['ProxmoxCreateRequest']
export type Job = Schemas['ProxmoxJob']

/** What is created: a clone of a VM template, a VM from an ISO image, or a container. */
export type Kind = 'clone' | 'iso' | 'lxc'

/** A network device of the form. */
export interface NetForm {
  bridge: string
  vlan: string
  ipv4Mode: 'none' | 'dhcp' | 'static'
  ipv4: string
  gateway4: string
  ipv6Mode: 'none' | 'auto' | 'dhcp' | 'static'
  ipv6: string
  gateway6: string
  /** Generated when the device is added, so that it is known before the guest exists. */
  mac: string
}

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
  /** Network devices net0, net1, ... */
  networks: NetForm[]
  /** Name servers and search domain apply to the first network device. */
  nameservers: string
  searchDomain: string
  user: string
  password: string
  sshKeys: string
  start: boolean
  description: string
  tags: string
  /** Projects of the new guest (task 12.8), sent as projects. */
  projects: string[]
  /** DHCP reservations and DNS records added after the creation (optional steps). */
  reservations: ResForm[]
  records: RecForm[]
}

/** A DHCP reservation for a network device (its MAC address). */
export interface ResForm {
  /** Index of the network device. */
  network: number
  server: string
  subnetId: string
  ipAddress: string
  hostname: string
}

export type RecordType = 'A' | 'AAAA' | 'PTR'

/** A DNS record of the new guest. */
export interface RecForm {
  provider: string
  zone: string
  name: string
  type: RecordType
  content: string
  /** Seconds; empty: 3600. */
  ttl: string
}

/** Reservations and records together (as the server allows). */
export const MAX_ENTRIES = 16

/** The most network devices of a new guest (as the server allows). */
export const MAX_NETWORKS = 8

/** A random unicast MAC address with the prefix Proxmox uses. */
export function randomMAC(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(3))
  const hex = Array.from(bytes, (x) => x.toString(16).padStart(2, '0').toUpperCase())
  return ['BC', '24', '11', ...hex].join(':')
}

const MAC = /^[0-9A-F]{2}(:[0-9A-F]{2}){5}$/i

export function isUnicastMAC(s: string): boolean {
  return MAC.test(s) && (parseInt(s.slice(0, 2), 16) & 1) === 0
}

/** A new network device on the default bridge. */
export function newNet(o: ProvisioningOptions, kind: Kind): NetForm {
  return {
    bridge: o.defaults.bridge,
    vlan: '',
    ipv4Mode: kind === 'iso' ? 'none' : 'dhcp',
    ipv4: '',
    gateway4: '',
    ipv6Mode: 'none',
    ipv6: '',
    gateway6: '',
    mac: randomMAC(),
  }
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
    networks: [newNet(o, kind)],
    nameservers: '',
    searchDomain: '',
    user: '',
    password: '',
    sshKeys: '',
    start: true,
    description: '',
    tags: '',
    projects: [],
    reservations: [],
    records: [],
  }
}

/** The static address of a network device, without prefix length. */
export function staticAddress(n: NetForm, v6: boolean): string {
  const mode = v6 ? n.ipv6Mode : n.ipv4Mode
  return mode === 'static' ? ((v6 ? n.ipv6 : n.ipv4).trim().split('/')[0] ?? '') : ''
}

/**
 * The reverse zone name and the relative owner name of a PTR record for an
 * address in a zone, or undefined if the address is not in the zone.
 */
export function ptrName(address: string, zone: string): string | undefined {
  let labels: string[]
  if (isIPv4(address)) {
    labels = address.split('.').reverse()
    labels.push('in-addr', 'arpa')
  } else if (isIPv6(address)) {
    labels = expandIPv6(address).split('').reverse()
    labels.push('ip6', 'arpa')
  } else {
    return undefined
  }
  const full = labels.join('.')
  const z = zone.toLowerCase().replace(/\.$/, '')
  if (!full.endsWith(`.${z}`)) return undefined
  return full.slice(0, -(z.length + 1))
}

/** The 32 hex digits of an IPv6 address. */
function expandIPv6(a: string): string {
  const [head, tail = ''] = a.toLowerCase().split('::')
  const h = head ? head.split(':') : []
  const t = tail ? tail.split(':') : []
  const groups = a.includes('::') ? [...h, ...Array(8 - h.length - t.length).fill('0'), ...t] : h
  return groups.map((g) => g.padStart(4, '0')).join('')
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

/** Error keys (proxmox.create.errors.<key>) by field; network devices by index. */
export type NetErrors = Partial<Record<keyof NetForm, string>>
export type ResErrors = Partial<Record<keyof ResForm, string>>
export type RecErrors = Partial<Record<keyof RecForm, string>>
type ListField = 'networks' | 'reservations' | 'records'
export type FormErrors = Partial<Record<Exclude<keyof CreateForm, ListField>, string>> & {
  networks?: NetErrors[]
  reservations?: ResErrors[]
  records?: RecErrors[]
}

function validateReservation(r: ResForm, f: CreateForm): ResErrors {
  const e: ResErrors = {}
  if (r.network < 0 || r.network >= f.networks.length) e.network = 'required'
  if (!r.server) e.server = 'required'
  if (!r.subnetId) e.subnetId = 'required'
  const ip = r.ipAddress.trim()
  if (ip !== '' && !isIPv4(ip) && !isIPv6(ip)) e.ipAddress = 'ipAddress'
  if (r.hostname.trim() !== '' && !DNS_NAME.test(r.hostname.trim())) e.hostname = 'name'
  return e
}

function validateRecord(r: RecForm): RecErrors {
  const e: RecErrors = {}
  if (!r.provider || !r.zone) e.zone = 'required'
  const name = r.name.trim().replace(/\.$/, '')
  if (name !== '@' && !DNS_NAME.test(name)) e.name = 'name'
  const c = r.content.trim()
  if (
    (r.type === 'A' && !isIPv4(c)) ||
    (r.type === 'AAAA' && !isIPv6(c)) ||
    (r.type === 'PTR' && !DNS_NAME.test(c.replace(/\.$/, '')))
  )
    e.content = `content${r.type}`
  if (r.ttl.trim() !== '' && !/^\d{1,7}$/.test(r.ttl.trim())) e.ttl = 'ttl'
  return e
}

/** The errors of one list, or undefined if there are none. */
function listErrors<T extends object>(errs: T[]): T[] | undefined {
  return errs.some((x) => Object.keys(x).length > 0) ? errs : undefined
}

/** Validates one network device; macs are the MAC addresses of all devices. */
function validateNet(
  n: NetForm,
  i: number,
  macs: string[],
  f: CreateForm,
  o: ProvisioningOptions,
): NetErrors {
  const e: NetErrors = {}
  if (!o.bridges.some((b) => b.name === n.bridge)) e.bridge = 'required'
  if (n.vlan.trim() !== '' && !(/^\d+$/.test(n.vlan.trim()) && inRanges(Number(n.vlan), o.vlans)))
    e.vlan = 'vlan'
  if (!isUnicastMAC(macs[i]!)) e.mac = 'mac'
  else if (macs.indexOf(macs[i]!) !== i) e.mac = 'macTwice'
  if (f.kind !== 'iso') {
    if (n.ipv4Mode === 'static') {
      if (!isPrefix(n.ipv4.trim(), false)) e.ipv4 = 'ipv4'
      if (n.gateway4.trim() !== '' && !isIPv4(n.gateway4.trim())) e.gateway4 = 'ipv4Address'
    }
    if (n.ipv6Mode === 'static') {
      if (!isPrefix(n.ipv6.trim(), true)) e.ipv6 = 'ipv6'
      if (n.gateway6.trim() !== '' && !isIPv6(n.gateway6.trim())) e.gateway6 = 'ipv6Address'
    }
  }
  return e
}

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
  const macs = f.networks.map((n) => n.mac.trim().toUpperCase())
  const nets = f.networks.map((n, i) => validateNet(n, i, macs, f, o))
  if (nets.some((ne) => Object.keys(ne).length > 0)) e.networks = nets
  if (f.kind !== 'iso') {
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
  const res = listErrors(f.reservations.map((r) => validateReservation(r, f)))
  if (res) e.reservations = res
  const recs = listErrors(f.records.map(validateRecord))
  if (recs) e.records = recs
  return e
}

/** The network devices of the request. */
function toNetworks(f: CreateForm): NonNullable<CreateRequest['networks']> {
  const opt = (s: string) => (s.trim() === '' ? undefined : s.trim())
  return f.networks.map((nf, i) => {
    const n: NonNullable<CreateRequest['networks']>[number] = {
      bridge: nf.bridge,
      vlan: nf.vlan.trim() === '' ? undefined : Number(nf.vlan),
      mac: nf.mac.trim().toUpperCase(),
    }
    if (f.kind === 'iso') return n
    if (nf.ipv4Mode === 'dhcp') n.ipv4 = 'dhcp'
    if (nf.ipv4Mode === 'static') {
      n.ipv4 = nf.ipv4.trim()
      n.gateway4 = opt(nf.gateway4)
    }
    if (nf.ipv6Mode === 'auto' || nf.ipv6Mode === 'dhcp') n.ipv6 = nf.ipv6Mode
    if (nf.ipv6Mode === 'static') {
      n.ipv6 = nf.ipv6.trim()
      n.gateway6 = opt(nf.gateway6)
    }
    if (i === 0) {
      const ns = words(f.nameservers)
      if (ns.length > 0) n.nameservers = ns
      n.searchDomain = opt(f.searchDomain)
    }
    return n
  })
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
    networks: toNetworks(f),
    start: f.start,
    description: opt(f.description),
  }
  const tags = words(f.tags)
  if (tags.length > 0) req.tags = tags
  if (f.projects.length > 0) req.projects = [...f.projects]
  if (f.reservations.length > 0) {
    req.reservations = f.reservations.map((r) => ({
      network: r.network,
      server: r.server,
      subnetId: Number(r.subnetId),
      ipAddress: opt(r.ipAddress),
      hostname: opt(r.hostname),
    }))
  }
  if (f.records.length > 0) {
    req.records = f.records.map((r) => ({
      provider: r.provider,
      zone: r.zone,
      name: r.name.trim(),
      type: r.type,
      content: r.content.trim(),
      ttl: num(r.ttl),
    }))
  }
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
  if (f.password !== '') req.password = f.password
  const keys = lines(f.sshKeys)
  if (keys.length > 0) req.sshKeys = keys.join('\n')
  return req
}

/** Permission to create or delete a guest. */
export function guestAdminPermission(cluster: string, type: string, vmid: number): string {
  return `proxmox:${cluster}:${type}/${vmid}:admin`
}
