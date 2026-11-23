// SPDX-License-Identifier: GPL-3.0-or-later
// The icons of the buttons. They come from Font Awesome Free (icons CC BY 4.0, code MIT,
// https://fontawesome.com/license/free) and are bundled: only the icons named here end up
// in the build, as SVG path data. Ctrl+Alt+Del is drawn here, after the one in Proxmox.
import {
  faArrowRotateLeft,
  faArrowsRotate,
  faBoltLightning,
  faBookmark,
  faDisplay,
  faMagnifyingGlass,
  faPencil,
  faPlus,
  faPowerOff,
} from '@fortawesome/free-solid-svg-icons'
import { faMoon, faTrashCan } from '@fortawesome/free-regular-svg-icons'

/** An icon: its view box and the SVG paths, filled with the current text color. */
export interface Icon {
  width: number
  height: number
  paths: string[]
}

interface Definition {
  icon: [number, number, unknown, unknown, string | string[]]
}

function fa(def: Definition): Icon {
  const [width, height, , , d] = def.icon
  return { width, height, paths: Array.isArray(d) ? d : [d] }
}

/** A rounded square as SVG path data. */
function square(x: number, y: number, size: number, r: number): string {
  const s = size - 2 * r
  return (
    `M${x + r} ${y}h${s}a${r} ${r} 0 0 1 ${r} ${r}v${s}a${r} ${r} 0 0 1 -${r} ${r}` +
    `h-${s}a${r} ${r} 0 0 1 -${r} -${r}v-${s}a${r} ${r} 0 0 1 ${r} -${r}z`
  )
}

/**
 * Ctrl+Alt+Del: two keys side by side in the lower half (Ctrl, Alt) and one in the upper
 * half, slightly to the right (Del), as on a keyboard.
 */
const ctrlAltDel: Icon = {
  width: 512,
  height: 512,
  paths: [square(112, 280, 128, 24), square(272, 280, 128, 24), square(320, 104, 128, 24)],
}

export const icons = {
  power: fa(faPowerOff),
  reboot: fa(faArrowRotateLeft),
  suspend: fa(faMoon),
  stop: fa(faBoltLightning),
  console: fa(faDisplay),
  ctrlAltDel,
  delete: fa(faTrashCan),
  refresh: fa(faArrowsRotate),
  add: fa(faPlus),
  edit: fa(faPencil),
  leases: fa(faBookmark),
  search: fa(faMagnifyingGlass),
} satisfies Record<string, Icon>

export type IconName = keyof typeof icons
