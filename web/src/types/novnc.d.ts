// SPDX-License-Identifier: GPL-3.0-or-later
// Minimal types for the parts of noVNC (MPL-2.0) that dcdash uses.
declare module '@novnc/novnc' {
  export interface RFBOptions {
    credentials?: { password?: string; username?: string; target?: string }
    wsProtocols?: string[]
    shared?: boolean
  }
  export default class RFB extends EventTarget {
    constructor(target: HTMLElement, urlOrChannel: string | WebSocket, options?: RFBOptions)
    scaleViewport: boolean
    resizeSession: boolean
    viewOnly: boolean
    background: string
    disconnect(): void
    sendCtrlAltDel(): void
    sendCredentials(credentials: { password?: string }): void
    focus(): void
  }
}
