// SPDX-License-Identifier: GPL-3.0-or-later
// Tracks the Proxmox tasks started by power actions until they finish.
import { defineStore } from 'pinia'
import { ref } from 'vue'

import { api, unwrap, type Schemas } from '@/api/client'

export type TaskState = 'running' | 'ok' | 'failed' | 'unknown'

export interface TrackedTask {
  cluster: string
  upid: string
  /** What the user sees, e.g. "web (101): start". */
  label: string
  state: TaskState
  /** Error message of a failed task. */
  message: string
  log: string[]
  startedAt: Date
}

/** Poll interval and how long a task is followed. */
export const POLL_MS = 1000
export const MAX_POLL_MS = 5 * 60 * 1000
const MAX_TASKS = 20

export const useProxmoxTasksStore = defineStore('proxmoxTasks', () => {
  const tasks = ref<TrackedTask[]>([])

  async function fetchTask(cluster: string, upid: string, log: boolean) {
    return unwrap<Schemas['ProxmoxTask']>(
      api.GET('/api/v1/proxmox/clusters/{cluster}/tasks/{upid}', {
        params: { path: { cluster, upid }, query: log ? { log: '1' } : {} },
      }),
    )
  }

  /**
   * Follows a task until it finishes and resolves with its final state. The
   * callback runs once with the finished task (e.g. to show a toast and reload).
   */
  function track(
    cluster: string,
    upid: string,
    label: string,
    onDone?: (t: TrackedTask) => void,
  ): Promise<TrackedTask> {
    const t: TrackedTask = {
      cluster,
      upid,
      label,
      state: 'running',
      message: '',
      log: [],
      startedAt: new Date(),
    }
    tasks.value = [t, ...tasks.value].slice(0, MAX_TASKS)
    // Work on the reactive proxy so that views update.
    const task = tasks.value[0]!
    return new Promise((resolve) => {
      const deadline = Date.now() + MAX_POLL_MS
      const finish = (state: TaskState, message: string, log: string[]) => {
        task.state = state
        task.message = message
        task.log = log
        onDone?.(task)
        resolve(task)
      }
      const poll = async () => {
        try {
          const res = await fetchTask(cluster, upid, false)
          if (!res.running) {
            const full = await fetchTask(cluster, upid, true).catch(() => res)
            finish(res.ok ? 'ok' : 'failed', res.ok ? '' : res.exitStatus, full.log)
            return
          }
        } catch (e) {
          if (Date.now() > deadline) {
            finish('unknown', e instanceof Error ? e.message : String(e), [])
            return
          }
        }
        if (Date.now() > deadline) {
          finish('unknown', '', [])
          return
        }
        setTimeout(poll, POLL_MS)
      }
      setTimeout(poll, POLL_MS)
    })
  }

  function clear() {
    tasks.value = tasks.value.filter((t) => t.state === 'running')
  }

  return { tasks, track, clear }
})
