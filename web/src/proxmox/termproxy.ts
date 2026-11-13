// SPDX-License-Identifier: GPL-3.0-or-later
// The Proxmox termproxy protocol (used by the Proxmox web UI's xterm.js):
// input "0:<bytes>:<data>", resize "1:<cols>:<rows>:", ping "2". dcdash
// performs the login on the server side, so the browser only sends these.

const encoder = new TextEncoder()

/** Encodes keyboard input; the length is the UTF-8 byte length. */
export function inputMessage(data: string): string {
  return `0:${encoder.encode(data).length}:${data}`
}

/** Encodes a terminal size change. */
export function resizeMessage(cols: number, rows: number): string {
  return `1:${cols}:${rows}:`
}

/** Keeps the connection alive; Proxmox closes idle terminals. */
export const PING_MESSAGE = '2'

/** URL of the dcdash console websocket for a ticket. */
export function consoleSocketURL(
  id: string,
  loc: Pick<Location, 'protocol' | 'host'> = location,
): string {
  const scheme = loc.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${scheme}//${loc.host}/api/v1/proxmox/console/${encodeURIComponent(id)}`
}

/** Reads the terminal colors from the theme tokens (see assets/main.css). */
export function terminalTheme(root: HTMLElement = document.documentElement) {
  const css = getComputedStyle(root)
  const v = (name: string, fallback: string) => css.getPropertyValue(name).trim() || fallback
  return {
    background: v('--dc-surface-sunken', '#1e293b'),
    foreground: v('--dc-fg', '#f1f5f9'),
    cursor: v('--dc-primary', '#60a5fa'),
    selectionBackground: v('--dc-border', '#334155'),
  }
}
