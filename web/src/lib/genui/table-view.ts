// Local filtering and sorting for a `table` node — the view the user sees is
// derived from the rows already in the spec, never fetched. See
// dev-docs/genui-interactive-panels-design.md, "Local interaction".

import type { GenuiTableNode, GenuiFieldValue } from './types'

export type SortDirection = 'asc' | 'desc'

export interface TableSort {
  /** Index into the table's `columns`. */
  column: number
  direction: SortDirection
}

/**
 * Three-state header cycle: unsorted → ascending → descending → unsorted.
 * Clicking a different column starts that column at ascending rather than
 * inheriting the previous column's direction.
 */
export function nextSort(current: TableSort | null, column: number): TableSort | null {
  if (!current || current.column !== column) return { column, direction: 'asc' }
  if (current.direction === 'asc') return { column, direction: 'desc' }
  return null
}

/**
 * The rows to render, after filtering and sorting. Returns the rows as-is
 * when neither applies, so the common case allocates nothing extra.
 */
export function tableRows(
  node: GenuiTableNode,
  fields: Record<string, GenuiFieldValue>,
  sort: TableSort | null
): (string | number)[][] {
  let rows = node.rows

  if (node.filterBy) {
    const needle = String(fields[node.filterBy.field] ?? '').trim().toLowerCase()
    if (needle !== '') {
      const col = node.columns.indexOf(node.filterBy.column)
      if (col >= 0) {
        rows = rows.filter(r => String(r[col] ?? '').toLowerCase().includes(needle))
      }
    }
  }

  if (sort && sort.column >= 0 && sort.column < node.columns.length) {
    const col = sort.column
    const factor = sort.direction === 'asc' ? 1 : -1
    // A column sorts numerically only when every cell present in it is a
    // number; one non-numeric cell makes the whole column lexicographic,
    // which is the only way to keep the ordering total and stable.
    const numeric = rows.every(r => {
      const v = r[col]
      if (v === undefined || v === null || v === '') return true
      return typeof v === 'number' || (typeof v === 'string' && v.trim() !== '' && Number.isFinite(Number(v)))
    })
    // Copy before sorting: node.rows belongs to the guarded spec, and sorting
    // in place would mutate what every other reader of that spec sees.
    rows = rows.slice().sort((a, b) => {
      const av = a[col]
      const bv = b[col]
      if (numeric) return factor * (Number(av ?? 0) - Number(bv ?? 0))
      return factor * String(av ?? '').localeCompare(String(bv ?? ''))
    })
  }

  return rows
}
