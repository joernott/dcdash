// SPDX-License-Identifier: GPL-3.0-or-later
import { existsSync, readFileSync } from 'node:fs'
import { fileURLToPath, URL } from 'node:url'

import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'
import vueDevTools from 'vite-plugin-vue-devtools'

// Backend used by the dev server proxy (`npm run dev`). The Go backend usually
// runs with a self-signed certificate during development, hence secure: false.
// changeOrigin stays false: the backend's CSRF check compares the browser's
// Origin header with the Host header, which must remain the dev server's.
const backend = process.env.DCDASH_BACKEND ?? 'https://localhost:8443'
const proxy = { target: backend, changeOrigin: false, secure: false }

// The session cookie is always Secure (__Host- prefix), so the dev server must use
// HTTPS as well. It uses the certificate created by `make dev-cert` if it exists.
const devCert = fileURLToPath(new URL('../.dev/tls/cert.pem', import.meta.url))
const devKey = fileURLToPath(new URL('../.dev/tls/key.pem', import.meta.url))
const https = existsSync(devCert) && existsSync(devKey)
  ? { cert: readFileSync(devCert), key: readFileSync(devKey) }
  : undefined

// https://vite.dev/config/
export default defineConfig({
  plugins: [vue(), tailwindcss(), vueDevTools()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    https,
    proxy: {
      '/api': { ...proxy, ws: true },
      '/healthz': proxy,
      '/readyz': proxy,
    },
  },
  build: {
    outDir: '../internal/ui/dist',
    emptyOutDir: true,
  },
})
