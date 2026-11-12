# dcdash frontend

Vue 3 + TypeScript + Vite + Tailwind CSS v4 + Pinia + Vue Router + vue-i18n (English, German).

The production build (`npm run build`) is written to `dist/` and embedded into the Go binary.
See [docs/development.md](../docs/development.md) for the development workflow.

| Command | Purpose |
|---|---|
| `npm run dev` | Dev server with proxy to the Go backend (`DCDASH_BACKEND`, default `https://localhost:8443`) |
| `npm run build` | Type-check and production build |
| `npm test` | Unit tests (Vitest) |
| `npm run lint` | Lint and auto-fix |
| `npm run check` | Lint and format check without fixing (used in CI) |
| `npm run format` | Format sources with Prettier |
