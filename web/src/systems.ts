// SPDX-License-Identifier: GPL-3.0-or-later
// The systems dcdash manages, with their colors (--dc-sys-* in assets/main.css) and the
// links to their own interfaces on the overview page.

/** Systems with an own color. dcdash's own components ("system") have none. */
export const SYSTEMS = [
  'proxmox',
  'bind',
  'cloudflare',
  'kea',
  'netbox',
  'switch',
  'firewall',
] as const
export type System = (typeof SYSTEMS)[number]

/** The system of a status component (kind, and type for dns), or null for dcdash itself. */
export function systemOf(c: { kind: string; type?: string }): System | null {
  switch (c.kind) {
    case 'dns':
      return c.type === 'cloudflare' ? 'cloudflare' : 'bind'
    case 'dhcp':
      return 'kea'
    case 'proxmox':
    case 'netbox':
    case 'switch':
    case 'firewall':
      return c.kind
  }
  return null
}

/** Inline style that sets the system colors for the sys-tint and sys-text classes. */
export function systemStyle(s: System | null): Record<string, string> {
  return s ? { '--sys': `var(--dc-sys-${s})`, '--sys-base': `var(--dc-sys-${s}-base)` } : {}
}

/**
 * Downloads a Remote Desktop connection file for host[:port]. Browsers have no common link
 * scheme for RDP, but every RDP client (mstsc on Windows, Windows App, Remmina, ...) opens
 * .rdp files.
 */
export function downloadRdp(name: string, address: string) {
  const content = `full address:s:${address}\r\nprompt for credentials:i:1\r\n`
  const url = URL.createObjectURL(new Blob([content], { type: 'application/x-rdp' }))
  const a = document.createElement('a')
  a.href = url
  a.download = `${name.replace(/[^A-Za-z0-9._-]/g, '_')}.rdp`
  document.body.appendChild(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}
