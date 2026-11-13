// SPDX-License-Identifier: GPL-3.0-or-later
// The Proxmox clusters the user can see, loaded once per session.
import { defineStore } from 'pinia'
import { ref } from 'vue'

import { api, unwrap, type Schemas } from '@/api/client'

export type ClusterInfo = Schemas['ProxmoxClusterList']['clusters'][number]

export const useProxmoxClustersStore = defineStore('proxmoxClusters', () => {
  const clusters = ref<ClusterInfo[]>([])
  const loaded = ref(false)

  /** Loads the clusters; with force, also if they were loaded already. */
  async function load(force = false): Promise<ClusterInfo[]> {
    if (!loaded.value || force) {
      const res = await unwrap(api.GET('/api/v1/proxmox/clusters'))
      clusters.value = res.clusters
      loaded.value = true
    }
    return clusters.value
  }

  /**
   * Whether container terminals and node shells are available on a cluster.
   * They need a console user in the cluster configuration (see ADR 0001).
   */
  function terminals(name: string): boolean {
    return clusters.value.find((c) => c.name === name)?.terminals ?? false
  }

  return { clusters, loaded, load, terminals }
})
