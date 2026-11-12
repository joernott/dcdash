// SPDX-License-Identifier: GPL-3.0-or-later
//
// Verifies that every locale in src/locales has exactly the same message keys as
// the reference locale (en) and that no message is empty. Exits non-zero otherwise.
import { readdirSync, readFileSync } from 'node:fs'
import { join, basename } from 'node:path'
import { fileURLToPath } from 'node:url'

const dir = fileURLToPath(new URL('../src/locales/', import.meta.url))
const reference = 'en'

function flatten(obj, prefix = '') {
  return Object.entries(obj).flatMap(([key, value]) => {
    const path = prefix ? `${prefix}.${key}` : key
    return value !== null && typeof value === 'object' ? flatten(value, path) : [[path, value]]
  })
}

const locales = Object.fromEntries(
  readdirSync(dir)
    .filter((f) => f.endsWith('.json'))
    .map((f) => [
      basename(f, '.json'),
      new Map(flatten(JSON.parse(readFileSync(join(dir, f), 'utf8')))),
    ]),
)

if (!locales[reference]) {
  console.error(`reference locale ${reference}.json not found in ${dir}`)
  process.exit(1)
}

const problems = []
const refKeys = locales[reference]
for (const [name, messages] of Object.entries(locales)) {
  for (const key of refKeys.keys()) {
    if (!messages.has(key)) problems.push(`${name}: missing key ${key}`)
  }
  for (const [key, value] of messages) {
    if (!refKeys.has(key)) problems.push(`${name}: key ${key} does not exist in ${reference}`)
    if (typeof value !== 'string' || value.trim() === '')
      problems.push(`${name}: empty message ${key}`)
  }
}

if (problems.length > 0) {
  console.error(problems.join('\n'))
  process.exit(1)
}
console.log(`i18n: ${Object.keys(locales).sort().join(', ')} complete (${refKeys.size} keys)`)
