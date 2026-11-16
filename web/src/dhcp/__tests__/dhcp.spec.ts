// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, expect, it } from 'vitest'

import {
  filterReservations,
  formatCount,
  identifierTypes,
  leaseAddress,
  leaseClient,
  pdUtilization,
  splitList,
  subnetPermission,
  utilization,
  type DhcpLease,
  type DhcpReservation,
  type DhcpSubnet,
} from '../dhcp'

const subnet = (stats?: DhcpSubnet['stats']): DhcpSubnet => ({
  id: 1,
  prefix: '192.0.2.0/24',
  pools: [],
  stats,
})

describe('dhcp helpers', () => {
  it('builds permissions and utilization', () => {
    expect(subnetPermission('kea', '2001:db8::/64', 'write')).toBe('dhcp:kea:2001:db8::/64:write')
    expect(utilization(subnet())).toBeUndefined()
    expect(utilization(subnet({ subnetId: 1, total: 0, assigned: 0, declined: 0 }))).toBeUndefined()
    expect(utilization(subnet({ subnetId: 1, total: 200, assigned: 50, declined: 0 }))).toBe(0.25)
    expect(
      pdUtilization(subnet({ subnetId: 1, total: 1, assigned: 0, declined: 0 })),
    ).toBeUndefined()
    expect(
      pdUtilization(
        subnet({ subnetId: 1, total: 1, assigned: 0, declined: 0, totalPd: 4, assignedPd: 1 }),
      ),
    ).toBe(0.25)
  })

  it('formats leases and counts', () => {
    const l = {
      ipAddress: '2001:db8:8000:100::',
      type: 'IA_PD',
      prefixLen: 56,
      duid: '00:01',
    } as DhcpLease
    expect(leaseAddress(l)).toBe('2001:db8:8000:100::/56')
    expect(leaseClient(l)).toBe('00:01')
    expect(leaseAddress({ ...l, type: 'IA_NA' })).toBe('2001:db8:8000:100::')
    expect(formatCount(1234, 'de')).toBe('1.234')
    expect(formatCount(1.8446744073709552e19, 'en')).toBe('1.8e19')
    expect(identifierTypes('v6')).toEqual(['duid', 'hw-address'])
    expect(splitList(' 2001:db8::1, 2001:db8::2;\n2001:db8::3 ')).toEqual([
      '2001:db8::1',
      '2001:db8::2',
      '2001:db8::3',
    ])
  })

  it('filters reservations', () => {
    const list = [
      { subnetId: 1, identifier: 'aa:bb', ipAddresses: ['192.0.2.10'], hostname: 'printer' },
      { subnetId: 2, identifier: 'cc:dd', ipAddresses: [], prefixes: ['2001:db8:1::/56'] },
    ] as DhcpReservation[]
    expect(filterReservations(list, '', '')).toHaveLength(2)
    expect(filterReservations(list, '2', '')).toHaveLength(1)
    expect(filterReservations(list, '', 'PRINT')).toHaveLength(1)
    expect(filterReservations(list, '', '2001:db8:1')).toHaveLength(1)
    expect(filterReservations(list, '1', 'cc')).toHaveLength(0)
  })
})
