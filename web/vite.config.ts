// SPDX-License-Identifier: GPL-3.0-or-later
import { fileURLToPath, URL } from 'node:url'

import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'
import vueDevTools from 'vite-plugin-vue-devtools'

// Backend used by the dev server proxy (`npm run dev`). The Go backend usually
// runs with a self-signed certificate during development, hence secure: false.
const backend = process.env.DCDASH_BACKEND ?? 'https://localhost:8443'
const proxy = { target: backend, changeOrigin: true, secure: false }

// https://vite.dev/config/
export default defineConfig({
  plugins: [vue(), tailwindcss(), vueDevTools()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    proxy: {
      '/api': { ...proxy, ws: true },
      '/healthz': proxy,
      '/readyz': proxy,
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
})
