// SPDX-License-Identifier: GPL-3.0-or-later
// OPNsense firewalls: health, VPN, aliases and filter rules.
import type { Schemas } from '@/api/client'

export type FirewallInfo = Schemas['Firewall']
export type Gateway = Schemas['FirewallGateway']
export type FirewallService = Schemas['FirewallService']
export type Peer = Schemas['FirewallPeer']
export type Tunnel = Schemas['FirewallTunnel']
export type Alias = Schemas['FirewallAlias']
export type AliasRequest = Schemas['FirewallAliasRequest']
export type Rule = Schemas['FirewallRule']
export type RuleRequest = Schemas['FirewallRuleRequest']

export const ALIAS_TYPES = [
  'host',
  'network',
  'port',
  'url',
  'urltable',
  'mac',
  'networkgroup',
] as const
export const RULE_ACTIONS = ['pass', 'block', 'reject'] as const
export const RULE_PROTOCOLS = ['any', 'TCP', 'UDP', 'TCP/UDP', 'ICMP', 'ESP', 'GRE'] as const
export const PORT_PROTOCOLS = ['TCP', 'UDP', 'TCP/UDP']

/** Formats bytes with binary units: 1536 -> "1.5 KiB". */
export function formatBytes(n: number): string {
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let v = n
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return i === 0 ? `${v} B` : `${v.toFixed(1)} ${units[i]}`
}

/** Seconds since a time; null for the zero time (Go's 0001-01-01) or an invalid one. */
export function ageSeconds(time: string, now = Date.now()): number | null {
  const t = Date.parse(time)
  if (Number.isNaN(t) || t <= 0) return null
  return Math.max(0, Math.round((now - t) / 1000))
}

/** A compact age: 45 -> "45 s", 300 -> "5 min", 7200 -> "2 h", 172800 -> "2 d". */
export function formatAge(s: number): string {
  if (s < 60) return `${s} s`
  if (s < 3600) return `${Math.floor(s / 60)} min`
  if (s < 86400) return `${Math.floor(s / 3600)} h`
  return `${Math.floor(s / 86400)} d`
}

/** The interfaces of a rule list, sorted, for the interface filter. */
export function ruleInterfaces(rules: Rule[]): string[] {
  return [...new Set(rules.map((r) => r.interface))].sort()
}

/** Source or destination with "not" and port: "! 10.0.0.0/8:443". */
export function endpoint(net?: string, not?: boolean, port?: string): string {
  return `${not ? '! ' : ''}${net || 'any'}${port ? `:${port}` : ''}`
}

/** Parses alias content: one entry per line, commas also separate; empty lines are dropped. */
export function parseContent(text: string): string[] {
  return text
    .split(/[\n,]/)
    .map((s) => s.trim())
    .filter((s) => s)
}

/** An empty rule for the create dialog. */
export function newRule(iface: string): RuleRequest {
  return {
    enabled: true,
    sequence: 100,
    action: 'pass',
    quick: true,
    interface: iface,
    direction: 'in',
    ipProtocol: 'inet',
    protocol: 'any',
    source: 'any',
    sourceNot: false,
    sourcePort: '',
    destination: 'any',
    destinationNot: false,
    destinationPort: '',
    log: false,
    description: '',
  }
}

/** The request fields of a rule, for the change dialog. */
export function toRequest(r: Rule): RuleRequest {
  return {
    enabled: r.enabled,
    sequence: r.sequence,
    action: r.action,
    quick: r.quick,
    interface: r.interface,
    direction: r.direction,
    ipProtocol: r.ipProtocol,
    protocol: r.protocol,
    source: r.source,
    sourceNot: r.sourceNot,
    sourcePort: r.sourcePort,
    destination: r.destination,
    destinationNot: r.destinationNot,
    destinationPort: r.destinationPort,
    log: r.log,
    description: r.description,
  }
}
