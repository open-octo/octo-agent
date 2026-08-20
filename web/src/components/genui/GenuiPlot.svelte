<script lang="ts">
  // Bar / line / area / pie as hand-written SVG. No charting library: the
  // values are plain numbers and the geometry is arithmetic (plot-layout.ts),
  // so the whole node stays inside the same closed, guard-validated shape as
  // every other GenUI node. Anything past this — heatmaps, sankeys, maps,
  // brush-and-zoom — is a deliverable that outlives the reply and belongs in
  // an artifact.
  import type { GenuiPlotNode } from '../../lib/genui/types'
  import {
    unionLabels,
    alignSeries,
    valueRange,
    stackedValues,
    stackOffsets,
    pieSlices,
    arcPath,
    PLOT_COLORS,
  } from '../../lib/genui/plot-layout'

  let { node }: { node: GenuiPlotNode } = $props()

  const H = $derived(node.height ?? 160)
  const W = 320
  const PAD_L = 28
  const PAD_B = 18
  const PAD_T = 6
  const PAD_R = 6

  const labels = $derived(unionLabels(node.series))
  const aligned = $derived(alignSeries(node.series, labels))
  const isStacked = $derived(!!node.stacked && (node.plot === 'bar' || node.plot === 'area'))
  const range = $derived(valueRange(aligned, isStacked))
  const stack = $derived(isStacked ? stackedValues(aligned) : [])
  const offsets = $derived(isStacked ? stackOffsets(stack) : [])
  const showLegend = $derived(node.legend ?? node.series.length > 1)

  const plotW = $derived(W - PAD_L - PAD_R)
  const plotH = $derived(H - PAD_T - PAD_B)

  function yOf(v: number): number {
    const span = range.max - range.min
    return PAD_T + plotH - ((v - range.min) / span) * plotH
  }
  function xOf(i: number): number {
    if (labels.length === 1) return PAD_L + plotW / 2
    return PAD_L + (i / (labels.length - 1)) * plotW
  }
  function bandX(i: number): number {
    return PAD_L + (i / Math.max(labels.length, 1)) * plotW
  }
  const bandW = $derived(plotW / Math.max(labels.length, 1))

  /** Polyline segments for a line series, split at gaps — a missing point
   * breaks the line rather than diving to the axis. */
  function lineSegments(row: (number | null)[]): string[] {
    const out: string[] = []
    let cur: string[] = []
    // A lone point would be invisible as a polyline, so it is emitted as a
    // degenerate two-point segment. This has to run at every gap, not just at
    // the end: a single point sandwiched between two gaps is exactly the case
    // that would otherwise render nothing at all.
    const flush = () => {
      if (cur.length > 1) out.push(cur.join(' '))
      else if (cur.length === 1) out.push(`${cur[0]} ${cur[0]}`)
      cur = []
    }
    row.forEach((v, i) => {
      if (v === null) {
        flush()
        return
      }
      cur.push(`${xOf(i)},${yOf(v)}`)
    })
    flush()
    return out
  }

  const slices = $derived(node.plot === 'pie' ? pieSlices(node.series[0]) : [])
</script>

<div class="genui-plot">
  {#if node.plot === 'pie'}
    <svg viewBox="0 0 {W} {H}" width="100%" height={H} role="img" aria-label={node.yLabel ?? 'chart'}>
      {#each slices as s, i (i)}
        {@const from = slices.slice(0, i).reduce((a, x) => a + x.fraction, 0)}
        <path d={arcPath(W / 2, H / 2, Math.min(W, H) / 2 - 6, from, s.fraction)} fill={PLOT_COLORS[i % PLOT_COLORS.length]} />
      {/each}
    </svg>
  {:else}
    <svg viewBox="0 0 {W} {H}" width="100%" height={H} role="img" aria-label={node.yLabel ?? 'chart'}>
      <!-- baseline -->
      <line x1={PAD_L} y1={yOf(Math.max(range.min, 0))} x2={W - PAD_R} y2={yOf(Math.max(range.min, 0))} class="axis" />
      <text x={PAD_L - 4} y={PAD_T + 8} class="tick" text-anchor="end">{Math.round(range.max)}</text>
      <text x={PAD_L - 4} y={PAD_T + plotH} class="tick" text-anchor="end">{Math.round(range.min)}</text>

      {#if node.plot === 'bar'}
        {#each aligned as row, si (si)}
          {#each row as v, i (i)}
            {@const val = isStacked ? stack[si][i] : (v ?? 0)}
            {@const base = isStacked ? offsets[si][i] : Math.max(range.min, 0)}
            {#if val !== 0}
              <rect
                x={isStacked ? bandX(i) + bandW * 0.15 : bandX(i) + bandW * (0.15 + (0.7 / aligned.length) * si)}
                width={isStacked ? bandW * 0.7 : (bandW * 0.7) / aligned.length}
                y={Math.min(yOf(base), yOf(base + val))}
                height={Math.abs(yOf(base) - yOf(base + val))}
                fill={PLOT_COLORS[si % PLOT_COLORS.length]}
              />
            {/if}
          {/each}
        {/each}
      {:else}
        {#each aligned as row, si (si)}
          {#if node.plot === 'area'}
            {@const vals = isStacked ? stack[si] : row.map(v => v ?? 0)}
            {@const bases = isStacked ? offsets[si] : row.map(() => Math.max(range.min, 0))}
            <polygon
              points={[
                ...vals.map((v, i) => `${xOf(i)},${yOf(bases[i] + v)}`),
                ...bases.map((b, i) => `${xOf(bases.length - 1 - i)},${yOf(bases[bases.length - 1 - i])}`),
              ].join(' ')}
              fill={PLOT_COLORS[si % PLOT_COLORS.length]}
              fill-opacity="0.35"
            />
          {/if}
          {#each lineSegments(row) as seg, k (k)}
            <polyline points={seg} fill="none" stroke={PLOT_COLORS[si % PLOT_COLORS.length]} stroke-width="2" />
          {/each}
        {/each}
      {/if}

      {#each labels as l, i (i)}
        <text x={node.plot === 'bar' ? bandX(i) + bandW / 2 : xOf(i)} y={H - 5} class="tick" text-anchor="middle">{l}</text>
      {/each}
    </svg>
  {/if}

  {#if node.xLabel || node.yLabel}
    <div class="genui-plot-axes">
      {#if node.yLabel}<span>{node.yLabel}</span>{/if}
      {#if node.xLabel}<span>{node.xLabel}</span>{/if}
    </div>
  {/if}

  {#if showLegend}
    <div class="genui-plot-legend">
      {#each node.plot === 'pie' ? slices.map(s => ({ name: s.label })) : node.series as s, i (i)}
        <span class="genui-plot-key">
          <span class="genui-plot-swatch" style="background: {PLOT_COLORS[i % PLOT_COLORS.length]}"></span>
          {s.name ?? `series ${i + 1}`}
        </span>
      {/each}
    </div>
  {/if}
</div>

<style>
  .genui-plot {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }
  .axis {
    stroke: var(--border);
    stroke-width: 1;
  }
  .tick {
    font-size: 9px;
    fill: var(--text-secondary);
  }
  .genui-plot-axes {
    display: flex;
    justify-content: space-between;
    font-size: 11px;
    color: var(--text-secondary);
  }
  .genui-plot-legend {
    display: flex;
    flex-wrap: wrap;
    gap: 10px;
    font-size: 11px;
    color: var(--text-secondary);
  }
  .genui-plot-key {
    display: inline-flex;
    align-items: center;
    gap: 4px;
  }
  .genui-plot-swatch {
    width: 8px;
    height: 8px;
    border-radius: 2px;
    display: inline-block;
  }
</style>
