<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, useTemplateRef } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'

import { api, unwrap, type Schemas } from '@/api/client'
import { errorMessage } from '@/api/errors'
import BaseButton from '@/components/ui/BaseButton.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import {
  consoleSocketURL,
  inputMessage,
  PING_MESSAGE,
  resizeMessage,
  terminalTheme,
} from '@/proxmox/termproxy'

type Ticket = Schemas['ProxmoxConsoleTicket']
type State = 'connecting' | 'connected' | 'closed' | 'failed'

const PING_MS = 30_000

const i18n = useI18n()
const { t } = i18n
const route = useRoute()
const screen = useTemplateRef<HTMLDivElement>('screen')
const frame = useTemplateRef<HTMLDivElement>('frame')

const state = ref<State>('connecting')
const message = ref('')
const kind = ref<Ticket['kind'] | ''>('')

const cluster = computed(() => String(route.params.cluster))
const node = computed(() => (route.params.node ? String(route.params.node) : ''))
const title = computed(() =>
  node.value
    ? t('proxmox.shellOf', { node: node.value })
    : t('proxmox.consoleOf', { name: `${route.params.type}/${route.params.vmid}` }),
)

// Cleanup of the active console (websocket, terminal, observers, timers).
let cleanup: (() => void) | undefined
// noVNC instance, for Ctrl+Alt+Del.
let rfb: { sendCtrlAltDel(): void; disconnect(): void } | undefined

async function prepare(): Promise<Ticket> {
  if (node.value) {
    return unwrap(
      api.POST('/api/v1/proxmox/clusters/{cluster}/nodes/{node}/shell', {
        params: { path: { cluster: cluster.value, node: node.value } },
      }),
    )
  }
  return unwrap(
    api.POST('/api/v1/proxmox/clusters/{cluster}/guests/{type}/{vmid}/console', {
      params: {
        path: {
          cluster: cluster.value,
          type: String(route.params.type) as 'qemu' | 'lxc',
          vmid: Number(route.params.vmid),
        },
      },
    }),
  )
}

function closed() {
  if (state.value === 'failed') return
  state.value = state.value === 'connecting' ? 'failed' : 'closed'
}

async function startVNC(ticket: Ticket, el: HTMLElement) {
  const { default: RFB } = await import('@novnc/novnc')
  const r = new RFB(el, consoleSocketURL(ticket.id), {
    credentials: { password: ticket.password ?? '' },
    wsProtocols: ['binary'],
  })
  r.scaleViewport = true
  r.background = 'transparent'
  r.addEventListener('connect', () => {
    state.value = 'connected'
    r.focus()
  })
  r.addEventListener('disconnect', () => closed())
  r.addEventListener('securityfailure', () => {
    state.value = 'failed'
    message.value = t('proxmox.consoleAuthFailed')
  })
  rfb = r
  cleanup = () => r.disconnect()
}

async function startTerminal(ticket: Ticket, el: HTMLElement) {
  const [{ Terminal }, { FitAddon }] = await Promise.all([
    import('@xterm/xterm'),
    import('@xterm/addon-fit'),
    import('@xterm/xterm/css/xterm.css'),
  ])
  const term = new Terminal({
    cursorBlink: true,
    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
    fontSize: 14,
    theme: terminalTheme(),
  })
  const fit = new FitAddon()
  term.loadAddon(fit)
  term.open(el)
  fit.fit()

  const ws = new WebSocket(consoleSocketURL(ticket.id), ['binary'])
  ws.binaryType = 'arraybuffer'
  const decoder = new TextDecoder()
  const send = (m: string) => {
    if (ws.readyState === WebSocket.OPEN) ws.send(m)
  }
  ws.onopen = () => {
    state.value = 'connected'
    send(resizeMessage(term.cols, term.rows))
    term.focus()
  }
  ws.onmessage = (ev: MessageEvent<ArrayBuffer | string>) => {
    term.write(typeof ev.data === 'string' ? ev.data : decoder.decode(ev.data, { stream: true }))
  }
  ws.onclose = () => closed()
  const input = term.onData((d) => send(inputMessage(d)))
  const resize = term.onResize(({ cols, rows }) => send(resizeMessage(cols, rows)))
  const ping = setInterval(() => send(PING_MESSAGE), PING_MS)

  // Follow window size and the dark/light theme.
  const resizeObserver = new ResizeObserver(() => fit.fit())
  resizeObserver.observe(el)
  const themeObserver = new MutationObserver(() => {
    term.options.theme = terminalTheme()
  })
  themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })

  cleanup = () => {
    clearInterval(ping)
    resizeObserver.disconnect()
    themeObserver.disconnect()
    input.dispose()
    resize.dispose()
    ws.close()
    term.dispose()
  }
}

async function connect() {
  cleanup?.()
  cleanup = undefined
  rfb = undefined
  state.value = 'connecting'
  message.value = ''
  const el = screen.value
  if (!el) return
  el.replaceChildren()
  try {
    const ticket = await prepare()
    kind.value = ticket.kind
    if (ticket.kind === 'vnc') await startVNC(ticket, el)
    else await startTerminal(ticket, el)
  } catch (e) {
    state.value = 'failed'
    message.value = errorMessage(i18n, e)
  }
}

function fullscreen() {
  void frame.value?.requestFullscreen?.()
}

const badge = computed(
  () => ({ connecting: 'warning', connected: 'ok', closed: 'unknown', failed: 'error' }) as const,
)

onMounted(connect)
onBeforeUnmount(() => cleanup?.())
</script>

<template>
  <div class="flex h-full flex-col gap-4">
    <nav :aria-label="t('proxmox.breadcrumb')" class="text-sm">
      <RouterLink :to="{ name: 'proxmox', params: { cluster } }" class="text-primary underline">
        {{ t('nav.proxmox') }} · {{ cluster }}
      </RouterLink>
      <template v-if="!node">
        ›
        <RouterLink
          :to="{
            name: 'proxmox-guest',
            params: { cluster, type: route.params.type, vmid: route.params.vmid },
          }"
          class="text-primary underline"
        >
          {{ route.params.type }}/{{ route.params.vmid }}
        </RouterLink>
      </template>
    </nav>

    <div class="flex flex-wrap items-center justify-between gap-3">
      <div class="flex items-center gap-3">
        <h1 class="text-2xl font-semibold">{{ title }}</h1>
        <StatusBadge :status="badge[state]" :label="t(`proxmox.consoleStates.${state}`)" />
      </div>
      <div class="flex flex-wrap gap-2">
        <BaseButton
          v-if="kind === 'vnc' && state === 'connected'"
          variant="secondary"
          size="sm"
          @click="rfb?.sendCtrlAltDel()"
        >
          {{ t('proxmox.ctrlAltDel') }}
        </BaseButton>
        <BaseButton variant="secondary" size="sm" @click="fullscreen">
          {{ t('proxmox.fullscreen') }}
        </BaseButton>
        <BaseButton
          v-if="state === 'closed' || state === 'failed'"
          variant="primary"
          size="sm"
          @click="connect"
        >
          {{ t('proxmox.reconnect') }}
        </BaseButton>
      </div>
    </div>

    <p
      v-if="message"
      role="alert"
      class="rounded-md border border-danger px-3 py-2 text-sm text-danger"
    >
      {{ message }}
    </p>

    <div
      ref="frame"
      class="relative min-h-[60vh] flex-1 overflow-hidden rounded-lg border border-border bg-surface-sunken"
    >
      <!-- Absolute, so the screen has a definite size: noVNC sizes its canvas
           container with height: 100%, which is 0 under a min-height alone. -->
      <div
        ref="screen"
        class="absolute inset-2"
        data-testid="console-screen"
        :aria-label="title"
        role="application"
      />
    </div>
  </div>
</template>
