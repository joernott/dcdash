// SPDX-License-Identifier: GPL-3.0-or-later
import { defineStore } from 'pinia'
import { ref } from 'vue'

export type ToastKind = 'success' | 'error' | 'info'

export interface Toast {
  id: number
  kind: ToastKind
  /** Already translated message. */
  message: string
}

/** Short notifications, e.g. "Saved" or an error message. */
export const useToastStore = defineStore('toast', () => {
  const toasts = ref<Toast[]>([])
  let nextId = 1

  function dismiss(id: number) {
    toasts.value = toasts.value.filter((t) => t.id !== id)
  }

  /** Shows a message; errors stay until dismissed, others disappear after `timeout` ms. */
  function show(kind: ToastKind, message: string, timeout = 5000) {
    const id = nextId++
    toasts.value.push({ id, kind, message })
    if (kind !== 'error' && timeout > 0) setTimeout(() => dismiss(id), timeout)
    return id
  }

  return {
    toasts,
    show,
    dismiss,
    success: (m: string) => show('success', m),
    error: (m: string) => show('error', m),
    info: (m: string) => show('info', m),
  }
})
