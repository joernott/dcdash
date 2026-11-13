// SPDX-License-Identifier: GPL-3.0-or-later
// TOTP codes (RFC 6238, SHA-1, 6 digits, 30 s) for the end-to-end tests.
import { createHmac } from 'node:crypto'

function base32Decode(s: string): Buffer {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'
  let bits = ''
  for (const c of s.replace(/=+$/, '').toUpperCase()) {
    const v = alphabet.indexOf(c)
    if (v < 0) throw new Error(`invalid base32 character ${c}`)
    bits += v.toString(2).padStart(5, '0')
  }
  const bytes = []
  for (let i = 0; i + 8 <= bits.length; i += 8) bytes.push(parseInt(bits.slice(i, i + 8), 2))
  return Buffer.from(bytes)
}

/** Returns the code for the time step `offset` steps after now. */
export function totp(secret: string, offset = 0, now = Date.now()): string {
  const step = Math.floor(now / 30_000) + offset
  const msg = Buffer.alloc(8)
  msg.writeBigUInt64BE(BigInt(step))
  const sum = createHmac('sha1', base32Decode(secret)).update(msg).digest()
  const off = sum[sum.length - 1]! & 0x0f
  const v = sum.readUInt32BE(off) & 0x7fffffff
  return String(v % 1_000_000).padStart(6, '0')
}
