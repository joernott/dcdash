// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, expect, it } from 'vitest'

import {
  diskGiB,
  hasPending,
  optionChanges,
  optionsForm,
  validateOptions,
  vlanAllowed,
} from '../hardware'
import { hardware } from './fixtures'

describe('options form', () => {
  it('starts with the effective values and finds the changes', () => {
    const hw = hardware()
    const f = optionsForm(hw)
    expect(f.cores).toBe('4') // pending value
    expect(f.tags).toBe('prod, web')
    expect(optionChanges(hw, f)).toEqual({ set: {}, delete: [] })

    f.memory = '4096'
    f.tags = 'prod web, db'
    f.agent = ''
    f.name = ''
    expect(optionChanges(hw, f)).toEqual({
      set: { memory: '4096', tags: 'prod;web;db' },
      delete: ['agent'], // the name can't be removed
    })
  })

  it('validates against the limits', () => {
    const hw = hardware()
    const f = optionsForm(hw)
    expect(validateOptions(f, hw.limits)).toEqual({})
    f.cores = '4'
    f.sockets = '3'
    f.memory = '20000'
    f.balloon = '30000'
    f.boot = 'scsi0'
    f.name = 'bad name'
    const e = validateOptions(f, hw.limits)
    expect(Object.keys(e).sort()).toEqual(['balloon', 'boot', 'memory', 'name', 'sockets'])
    expect(e.sockets?.params).toEqual({ max: 8 })
  })
})

describe('helpers', () => {
  it('rounds disk sizes up and checks VLANs and pending changes', () => {
    expect(diskGiB({ ...hardware().disks[0]!, sizeBytes: 2 ** 30 + 1 })).toBe(2)
    expect(vlanAllowed(150, ['100-199'])).toBe(true)
    expect(vlanAllowed(5, ['5', '100-199'])).toBe(true)
    expect(vlanAllowed(200, ['100-199'])).toBe(false)
    expect(hasPending(hardware())).toBe(true)
    const none = hardware()
    none.options = none.options.map((o) => ({ ...o, pending: undefined }))
    expect(hasPending(none)).toBe(false)
  })
})
