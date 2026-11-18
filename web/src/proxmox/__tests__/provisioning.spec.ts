// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, expect, it } from 'vitest'

import { inRanges, isIPv4, isIPv6, newForm, newNet, toRequest, validate } from '../provisioning'
import { options } from './fixtures'

const KEY = 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample+/= user@host'

describe('provisioning helpers', () => {
  it('checks ranges and addresses', () => {
    expect(inRanges(150, ['100-199'])).toBe(true)
    expect(inRanges(300, ['100-199', '300'])).toBe(true)
    expect(inRanges(200, ['100-199'])).toBe(false)
    expect(isIPv4('192.0.2.1')).toBe(true)
    expect(isIPv4('256.0.0.1')).toBe(false)
    expect(isIPv6('2001:db8::1')).toBe(true)
    expect(isIPv6('2001:db8::g')).toBe(false)
    expect(isIPv6('192.0.2.1')).toBe(false)
  })

  it('starts with the defaults and builds a clone request', () => {
    const f = newForm(options)
    expect(f).toMatchObject({
      kind: 'clone',
      template: '9000',
      cores: '2',
      memory: '2048',
      disk: '',
    })
    expect(validate(f, options)).toEqual({ name: 'name' })
    Object.assign(f.networks[0]!, {
      vlan: '100',
      ipv4Mode: 'static',
      ipv4: '10.0.0.5/24',
      gateway4: '10.0.0.1',
      ipv6Mode: 'auto',
      mac: 'bc:24:11:00:00:01',
    })
    Object.assign(f, {
      name: 'app1',
      nameservers: '10.0.0.53, 2001:db8::53',
      user: 'ops',
      password: 'secret1',
      sshKeys: `${KEY}\n\n`,
      tags: 'dev web',
      disk: '40',
    })
    expect(validate(f, options)).toEqual({})
    expect(toRequest(f)).toEqual({
      type: 'qemu',
      source: 'template',
      template: 9000,
      full: true,
      storage: 'local-lvm',
      name: 'app1',
      vmid: undefined,
      cores: 2,
      memory: 2048,
      disk: 40,
      user: 'ops',
      password: 'secret1',
      sshKeys: KEY,
      start: true,
      description: undefined,
      tags: ['dev', 'web'],
      networks: [
        {
          bridge: 'vmbr0',
          vlan: 100,
          mac: 'BC:24:11:00:00:01',
          ipv4: '10.0.0.5/24',
          gateway4: '10.0.0.1',
          ipv6: 'auto',
          nameservers: ['10.0.0.53', '2001:db8::53'],
          searchDomain: undefined,
        },
      ],
    })
    f.full = false
    expect(toRequest(f).storage).toBeUndefined()
  })

  it('builds ISO and container requests', () => {
    const iso = {
      ...newForm(options, 'iso'),
      name: 'win',
      osType: 'windows' as const,
      vmid: '5100',
    }
    expect(validate(iso, options)).toEqual({})
    expect(toRequest(iso)).toMatchObject({
      type: 'qemu',
      source: 'iso',
      node: 'pve1',
      iso: 'local:iso/debian.iso',
      osType: 'windows',
      vmid: 5100,
      disk: 32,
      storage: 'local-lvm',
      networks: [{ bridge: 'vmbr0' }],
    })
    expect(toRequest(iso).networks![0]!.ipv4).toBeUndefined()

    const ct = { ...newForm(options, 'lxc'), name: 'ct1' }
    expect(validate(ct, options)).toEqual({ password: 'login' })
    ct.password = 'rootpw'
    expect(validate(ct, options)).toEqual({})
    expect(toRequest(ct)).toMatchObject({
      type: 'lxc',
      node: 'pve1',
      osTemplate: 'local:vztmpl/debian-13.tar.zst',
      password: 'rootpw',
      networks: [{ bridge: 'vmbr0', ipv4: 'dhcp' }],
    })
  })

  it('reports invalid fields', () => {
    const f = {
      ...newForm(options, 'lxc'),
      name: 'bad name',
      vmid: '4000',
      cores: '9',
      memory: '10',
      disk: '0',
      storage: 'ceph',
      networks: [
        {
          ...newNet(options, 'lxc'),
          vlan: '300',
          ipv4Mode: 'static' as const,
          ipv4: '10.0.0.5',
          gateway4: 'x',
          ipv6Mode: 'static' as const,
          ipv6: '2001:db8::5',
        },
      ],
      nameservers: '1.1.1.1 2.2.2.2 3.3.3.3 4.4.4.4',
      searchDomain: 'bad domain',
      password: 'abc',
      sshKeys: 'not a key',
      tags: 'Bad',
    }
    expect(validate(f, options)).toEqual({
      name: 'name',
      vmid: 'vmid',
      cores: 'cores',
      memory: 'memory',
      disk: 'disk',
      storage: 'required',
      networks: [{ vlan: 'vlan', ipv4: 'ipv4', gateway4: 'ipv4Address', ipv6: 'ipv6' }],
      nameservers: 'nameservers',
      searchDomain: 'domain',
      password: 'password',
      sshKeys: 'sshKeys',
      tags: 'tags',
    })
  })

  it('handles several network devices with their own MAC addresses', () => {
    const f = { ...newForm(options, 'lxc'), name: 'ct1', password: 'rootpw' }
    expect(f.networks[0]!.mac).toMatch(/^BC:24:11(:[0-9A-F]{2}){3}$/)
    f.networks.push({ ...newNet(options, 'lxc'), vlan: '150', ipv4Mode: 'none' })
    expect(f.networks[1]!.mac).not.toBe(f.networks[0]!.mac)
    expect(validate(f, options)).toEqual({})
    const req = toRequest(f)
    expect(req.networks).toHaveLength(2)
    expect(req.networks![1]).toEqual({ bridge: 'vmbr0', vlan: 150, mac: f.networks[1]!.mac })

    f.networks[1]!.mac = f.networks[0]!.mac.toLowerCase()
    f.networks.push({ ...newNet(options, 'lxc'), mac: '01:00:5E:00:00:01' })
    expect(validate(f, options).networks).toEqual([{}, { mac: 'macTwice' }, { mac: 'mac' }])
  })
})
