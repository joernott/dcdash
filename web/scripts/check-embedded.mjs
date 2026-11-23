// SPDX-License-Identifier: GPL-3.0-or-later
// Fails if the built UI (../internal/ui/dist) refers to resources outside dcdash: scripts,
// styles, fonts or images from other hosts, protocol-relative URLs, or CSS imports. dcdash
// embeds everything in its binary and works without internet access; its Content Security
// Policy would block such resources anyway. Plain mentions of URLs (license comments, XML
// namespaces, links in error messages) are allowed.
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'

const dist = new URL('../../internal/ui/dist/', import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, '$1')

// A resource is loaded from elsewhere if an external URL appears where the browser fetches it.
const checks = [
  ['HTML src/href', /\b(?:src|href|srcset)\s*=\s*["']?\s*(?:https?:)?\/\/[^"'\s>]+/gi],
  ['CSS url()', /url\(\s*["']?(?:https?:)?\/\/[^)"']+/gi],
  ['CSS @import', /@import\s+(?:url\()?\s*["']?(?:https?:)?\/\//gi],
  ['JS import', /\bimport\s*\(?\s*["'](?:https?:)?\/\/[^"']+/gi],
  ['fetch/XHR/WebSocket', /\b(?:fetch|open|WebSocket|EventSource)\s*\(\s*["'`](?:https?|wss?):\/\/[^"'`]+/gi],
  ['font/script element', /createElement\(\s*["'](?:script|link)["']\s*\)[^;]{0,200}?(?:https?:)?\/\/[a-z0-9.-]+\.[a-z]{2,}/gi],
]

function files(dir) {
  return readdirSync(dir).flatMap((name) => {
    const p = join(dir, name)
    return statSync(p).isDirectory() ? files(p) : [p]
  })
}

let all
try {
  all = files(dist).filter((f) => /\.(html|js|mjs|css|svg|json)$/.test(f))
} catch (e) {
  console.error(`check-embedded: ${dist} not found; build the UI first (${e.message})`)
  process.exit(1)
}
if (!all.some((f) => f.endsWith('index.html'))) {
  console.error(`check-embedded: no index.html in ${dist}`)
  process.exit(1)
}

let failed = 0
for (const f of all) {
  const text = readFileSync(f, 'utf8')
  for (const [what, re] of checks) {
    for (const m of text.matchAll(re)) {
      console.error(`check-embedded: ${relative(dist, f)}: ${what}: ${m[0].slice(0, 120)}`)
      failed++
    }
  }
}
if (failed > 0) {
  console.error(`check-embedded: ${failed} external resource(s); embed them instead`)
  process.exit(1)
}
console.log(`check-embedded: ${all.length} files, no external resources`)
