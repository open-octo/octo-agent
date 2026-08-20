import { describe, it, expect } from 'vitest'
import { tableRows, nextSort } from './table-view'
import type { GenuiTableNode } from './types'

const base: GenuiTableNode = {
  type: 'table',
  columns: ['name', 'score'],
  rows: [
    ['Bravo', 9],
    ['alpha', 10],
    ['Charlie', 2],
  ],
}

describe('nextSort', () => {
  it('cycles unsorted → asc → desc → unsorted', () => {
    const a = nextSort(null, 0)
    expect(a).toEqual({ column: 0, direction: 'asc' })
    const b = nextSort(a, 0)
    expect(b).toEqual({ column: 0, direction: 'desc' })
    expect(nextSort(b, 0)).toBeNull()
  })

  it('starts a different column fresh at ascending', () => {
    expect(nextSort({ column: 0, direction: 'desc' }, 1)).toEqual({ column: 1, direction: 'asc' })
  })
})

describe('tableRows', () => {
  it('returns rows untouched with no filter and no sort', () => {
    expect(tableRows(base, {}, null)).toBe(base.rows)
  })

  it('filters case-insensitively on the named column', () => {
    const node = { ...base, filterBy: { field: 'q', column: 'name' } }
    expect(tableRows(node, { q: 'a' }, null).length).toBe(3)
    expect(tableRows(node, { q: 'CHAR' }, null)).toEqual([['Charlie', 2]])
  })

  it('ignores an empty filter value', () => {
    const node = { ...base, filterBy: { field: 'q', column: 'name' } }
    expect(tableRows(node, { q: '  ' }, null).length).toBe(3)
  })

  it('sorts a numeric column numerically', () => {
    const sorted = tableRows(base, {}, { column: 1, direction: 'asc' })
    expect(sorted.map(r => r[1])).toEqual([2, 9, 10])
  })

  it('sorts a text column lexicographically, case-insensitively via localeCompare', () => {
    const sorted = tableRows(base, {}, { column: 0, direction: 'asc' })
    expect(sorted.map(r => r[0])).toEqual(['alpha', 'Bravo', 'Charlie'])
  })

  it('falls back to lexicographic when one cell is not numeric', () => {
    const mixed: GenuiTableNode = { ...base, rows: [['a', 10], ['b', 'n/a' as unknown as number], ['c', 9]] }
    const sorted = tableRows(mixed, {}, { column: 1, direction: 'asc' })
    expect(sorted.map(r => r[1])).toEqual([10, 9, 'n/a'])
  })

  it('does not mutate the spec it was given', () => {
    const before = base.rows.map(r => [...r])
    tableRows(base, {}, { column: 1, direction: 'desc' })
    expect(base.rows).toEqual(before)
  })

  it('combines filter and sort', () => {
    const node = { ...base, filterBy: { field: 'q', column: 'name' } }
    const out = tableRows(node, { q: 'a' }, { column: 1, direction: 'desc' })
    expect(out.map(r => r[1])).toEqual([10, 9, 2])
  })
})
