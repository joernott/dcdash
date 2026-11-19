// SPDX-License-Identifier: GPL-3.0-or-later
// The projects the user can see, loaded once per session (task 12.8).
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { api, unwrap, type Schemas } from '@/api/client'

export type Project = Schemas['Project']

/** The tag prefix of projects. */
export const PROJECT_PREFIX = 'project-'

/** The project names of Proxmox tags. */
export function projectsOfTags(tags: string[]): string[] {
  return tags
    .filter((t) => t.startsWith(PROJECT_PREFIX))
    .map((t) => t.slice(PROJECT_PREFIX.length))
    .sort()
}

export const useProjectsStore = defineStore('projects', () => {
  const projects = ref<Project[]>([])
  const loaded = ref(false)
  /** Whether projects of addresses can be stored (NetBox with write-back). */
  const addressProjects = ref(false)

  /** Loads the projects; errors leave the list empty. */
  async function load(force = false): Promise<Project[]> {
    if (!loaded.value || force) {
      try {
        projects.value = (await unwrap(api.GET('/api/v1/projects'))).projects
      } catch {
        projects.value = []
      }
      try {
        addressProjects.value = (await unwrap(api.GET('/api/v1/netbox'))).writeBack
      } catch {
        addressProjects.value = false
      }
      loaded.value = true
    }
    return projects.value
  }

  /** Projects the user may assign objects to. */
  const writable = computed(() => projects.value.filter((p) => p.canWrite))

  function byName(name: string): Project | undefined {
    return projects.value.find((p) => p.name === name)
  }

  return { projects, loaded, load, writable, byName, addressProjects }
})
