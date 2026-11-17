// SPDX-License-Identifier: GPL-3.0-or-later
// Test data for the provisioning helpers and views.
import type { ProvisioningOptions } from '../provisioning'

export const options: ProvisioningOptions = {
  node: 'pve1',
  nodes: ['pve1', 'pve2'],
  storages: [
    {
      name: 'local-lvm',
      type: 'lvmthin',
      shared: false,
      avail: 100 * 2 ** 30,
      total: 200 * 2 ** 30,
      vm: true,
      container: true,
    },
    {
      name: 'ceph',
      type: 'rbd',
      shared: true,
      avail: 2 ** 40,
      total: 2 ** 41,
      vm: true,
      container: false,
    },
  ],
  isos: [{ volid: 'local:iso/debian.iso', size: 1, format: 'iso' }],
  containerTemplates: [{ volid: 'local:vztmpl/debian-13.tar.zst', size: 1, format: 'tzst' }],
  bridges: [{ name: 'vmbr0', vlanAware: true }],
  vlans: ['100-199'],
  vmids: ['5000-5999'],
  templates: [{ vmid: 9000, name: 'debian-ci', node: 'pve2' }],
  pool: 'dev',
  limits: { cores: 8, memory: 16384, disk: 100 },
  defaults: { cores: 2, memory: 2048, disk: 32, storage: 'local-lvm', bridge: 'vmbr0' },
}
