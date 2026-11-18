// SPDX-License-Identifier: GPL-3.0-or-later
// Test data for the provisioning helpers and views.
import type { Hardware } from '../hardware'
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

/** A VM with an attached and an unused disk, a network device and a pending change. */
export function hardware(over: Partial<Hardware> = {}): Hardware {
  return {
    digest: 'd1',
    running: false,
    configurable: true,
    storages: ['local-lvm', 'ceph'],
    bridges: ['vmbr0'],
    vlans: ['100-199'],
    limits: { cores: 8, memory: 16384, disk: 100 },
    options: [
      { key: 'name', value: 'web', set: true },
      { key: 'description', value: '', set: false },
      { key: 'tags', value: 'prod;web', set: true },
      { key: 'onboot', value: '0', set: true },
      { key: 'protection', value: '', set: false },
      { key: 'agent', value: '1', set: true },
      { key: 'boot', value: 'order=scsi0;net0', set: true },
      { key: 'cores', value: '2', set: true, pending: '4' },
      { key: 'sockets', value: '', set: false },
      { key: 'memory', value: '2048', set: true },
      { key: 'balloon', value: '', set: false },
      { key: 'cpu', value: '', set: false },
      { key: 'numa', value: '', set: false },
      { key: 'ostype', value: 'l26', set: true },
      { key: 'hotplug', value: '', set: false },
    ],
    disks: [
      {
        key: 'scsi0',
        bus: 'scsi',
        volume: 'local-lvm:vm-101-disk-0',
        storage: 'local-lvm',
        size: '32G',
        sizeBytes: 32 * 2 ** 30,
        discard: true,
        ssd: false,
        iothread: true,
        backup: true,
        active: true,
      },
      {
        key: 'unused0',
        bus: 'unused',
        volume: 'ceph:vm-101-disk-1',
        storage: 'ceph',
        size: '',
        sizeBytes: 0,
        discard: false,
        ssd: false,
        iothread: false,
        backup: true,
        active: true,
      },
    ],
    networks: [
      {
        key: 'net0',
        model: 'virtio',
        mac: 'BC:24:11:00:00:01',
        bridge: 'vmbr0',
        vlan: 100,
        firewall: true,
        linkDown: false,
        active: true,
      },
    ],
    ...over,
  }
}
