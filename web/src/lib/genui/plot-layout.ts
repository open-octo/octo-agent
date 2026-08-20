// Geometry for the `plot` node. Pure arithmetic over the guarded spec — no
// charting library, and nothing to evaluate, because a plot's values are
// plain numbers. See dev-docs/genui-interactive-panels-design.md.

import type { GenuiPlotSeries } from './types'

/** The x axis: the union of every series' labels, in first-appearance order.
 * Series need not agree on their labels or their length. */
export function unionLabels(series: GenuiPlotSeries[]): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  for (const s of series) {
    for (const p of s.points) {
      if (!seen.has(p.label)) {
        seen.add(p.label)
        out.push(p.label)
      }
    }
  }
  return out
}

/**
 * Each series' values aligned to `labels`, with null where that series has no
 * point for a label.
 *
 * Null is preserved rather than zeroed here because the two renderings differ:
 * a `line` breaks at a gap (a missing measurement and a measurement of zero
 * are different claims) while `bar`/`area` draw it as zero height, which is
 * what stacking requires to stay additive. The caller decides; this function
 * keeps the distinction available.
 */
export function alignSeries(series: GenuiPlotSeries[], labels: string[]): (number | null)[][] {
  return series.map(s => {
    const byLabel = new Map<string, number>()
    for (const p of s.points) {
      // First point wins for a duplicated label — the axis took its position
      // from the first occurrence too, so they stay consistent.
      if (!byLabel.has(p.label)) byLabel.set(p.label, p.value)
    }
    return labels.map(l => (byLabel.has(l) ? (byLabel.get(l) as number) : null))
  })
}

export interface ValueRange {
  min: number
  max: number
}

/**
 * The value axis range. Stacked plots sum each column (negatives clamped to
 * zero, since a stacked chart mixing signs is unreadable); unstacked plots
 * take the extremes across all series. The range always includes zero, so
 * bars have a baseline to grow from, and a flat series still gets a non-zero
 * span to avoid dividing by zero.
 */
export function valueRange(values: (number | null)[][], stacked: boolean): ValueRange {
  let min = 0
  let max = 0

  if (stacked) {
    const columns = values[0]?.length ?? 0
    for (let c = 0; c < columns; c++) {
      let sum = 0
      for (const row of values) {
        const v = row[c]
        if (v !== null && v > 0) sum += v
      }
      if (sum > max) max = sum
    }
  } else {
    for (const row of values) {
      for (const v of row) {
        if (v === null) continue
        if (v < min) min = v
        if (v > max) max = v
      }
    }
  }

  if (max === min) max = min + 1
  return { min, max }
}

/** Values clamped for stacked rendering: negatives become zero and gaps
 * become zero, so each column's segments are additive. */
export function stackedValues(values: (number | null)[][]): number[][] {
  return values.map(row => row.map(v => (v === null || v < 0 ? 0 : v)))
}

/** Running offsets for stacked segments, one per series per column. */
export function stackOffsets(stacked: number[][]): number[][] {
  const columns = stacked[0]?.length ?? 0
  const running = new Array(columns).fill(0)
  return stacked.map(row =>
    row.map((v, c) => {
      const base = running[c]
      running[c] = base + v
      return base
    })
  )
}

/** Slices of a pie, as {value, fraction} over the first series' non-negative
 * points. Returns an empty array when nothing positive is present. */
export function pieSlices(series: GenuiPlotSeries): { label: string; value: number; fraction: number }[] {
  const points = series.points.filter(p => p.value > 0)
  const total = points.reduce((a, p) => a + p.value, 0)
  if (total <= 0) return []
  return points.map(p => ({ label: p.label, value: p.value, fraction: p.value / total }))
}

/** Arc path for one pie slice over the unit circle scaled to `r`, starting at
 * `from` turns (0 = 12 o'clock) and covering `sweep` turns. */
export function arcPath(cx: number, cy: number, r: number, from: number, sweep: number): string {
  // A full circle can't be expressed as a single arc (start and end points
  // coincide), so draw it as two half circles.
  if (sweep >= 1) {
    return `M ${cx} ${cy - r} A ${r} ${r} 0 1 1 ${cx} ${cy + r} A ${r} ${r} 0 1 1 ${cx} ${cy - r} Z`
  }
  const a0 = from * 2 * Math.PI - Math.PI / 2
  const a1 = (from + sweep) * 2 * Math.PI - Math.PI / 2
  const x0 = cx + r * Math.cos(a0)
  const y0 = cy + r * Math.sin(a0)
  const x1 = cx + r * Math.cos(a1)
  const y1 = cy + r * Math.sin(a1)
  const large = sweep > 0.5 ? 1 : 0
  return `M ${cx} ${cy} L ${x0} ${y0} A ${r} ${r} 0 ${large} 1 ${x1} ${y1} Z`
}

/** The fixed colour sequence, as CSS custom properties with literal
 * fallbacks. Length matches MAX_PLOT_SERIES — a ninth series would have no
 * distinct colour to take, which is why the guard stops at eight. */
export const PLOT_COLORS = [
  'var(--blue-6, #1677ff)',
  'var(--green-6, #52c41a)',
  'var(--orange-6, #fa8c16)',
  'var(--purple-6, #722ed1)',
  'var(--cyan-6, #13c2c2)',
  'var(--magenta-6, #eb2f96)',
  'var(--gold-6, #faad14)',
  'var(--red-6, #f5222d)',
]
