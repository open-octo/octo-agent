import { describe, it, expect, vi } from 'vitest'

const getEndpoints = vi.fn()
vi.mock('../lib/api', () => ({ getEndpoints: () => getEndpoints() }))

import { loadModelOptions, normalizePermMode } from './sessionOptions'

describe('loadModelOptions', () => {
  it('lists composite ids and qualifies a model offered by two endpoints', async () => {
    getEndpoints.mockResolvedValue({ endpoints: [
      { id: 'ep1', name: 'Work', models: [{ model: 'gpt-x' }, { model: 'shared' }] },
      { id: 'ep2', name: '', models: [{ model: 'shared' }] },
    ] })
    expect(await loadModelOptions()).toEqual([
      { id: 'ep1::gpt-x', label: 'gpt-x' },
      { id: 'ep1::shared', label: 'shared · Work' },
      { id: 'ep2::shared', label: 'shared · ep2' },
    ])
  })
})

describe('normalizePermMode', () => {
  it('keeps an engine mode and falls back for anything else', () => {
    expect(normalizePermMode('auto')).toBe('auto')
    expect(normalizePermMode('ask')).toBe('interactive')
    expect(normalizePermMode(undefined, 'strict')).toBe('strict')
    expect(normalizePermMode('')).toBe('interactive')
  })
})
