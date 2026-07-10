---
name: dataviz
description: Use whenever you are about to create a chart, graph, plot, heatmap, sparkline, stat tile, or dashboard — whether as an HTML artifact, inline SVG, or plotting code in any library (matplotlib, plotly, Chart.js, D3, …). Covers chart-type selection, the color system for categorical/sequential/diverging data, legend/axis/tooltip conventions, and legibility at the Artifacts panel's narrow default width. Triggers on "chart", "graph", "plot", "visualize this data", "dashboard", "heatmap", "颜色", "配色", "画个图表". Read artifact-design first if the chart is going inside a full HTML artifact page — this skill is only about the chart itself, not the page it lives in.
---

# Data visualization

This skill is scoped to the chart/graph, not the page around it. If you're
building a full HTML artifact (a dashboard, a report page) that happens to
contain a chart, read `artifact-design` for the page mechanics (self-contained
HTML, theme handling, the panel's narrow default width) and come back here for
the chart itself.

## Pick the chart type from the data shape, not habit

| Data shape | Chart |
|---|---|
| One value per category, few categories (≤8) | Horizontal or vertical bar |
| A value over time | Line (single series) or small-multiple lines (few series) |
| Part-of-whole, ≤5 slices | Stacked bar, not pie — bars compare more accurately than angles |
| Distribution of one variable | Histogram |
| Two continuous variables, looking for correlation | Scatter |
| A matrix / two categorical axes | Heatmap |
| One number that matters right now | A stat tile with a sparkline, not a full chart |

Don't default to a bar chart for everything, and don't reach for a pie chart
for more than a handful of categories — angle comparison degrades fast past
3–4 slices.

## Color: three systems, pick the one that matches the data

- **Categorical** (distinct groups, no order) — one hue per series, evenly
  spaced in perceptual lightness, capped around 6–8 before colors become
  indistinguishable. Never let two adjacent categories share a hue family
  (two blues next to each other reads as one blurred category).
- **Sequential** (low→high, one direction) — a single hue ramped in
  lightness only. Don't rainbow a sequential scale — a viewer should be able
  to order values by lightness alone, and a rainbow ramp breaks that
  ordering intuition (is red higher or lower than green?).
- **Diverging** (a meaningful midpoint — zero, a target, "no change") — two
  hues radiating from a neutral midpoint, symmetric in intensity on both
  sides. Pick hues that read as opposites in the domain (red/green for
  bad/good, cool/warm for below/above target) — not arbitrary complements.

Use one accent color to mean "the thing being highlighted" and everything
else in a muted neutral — don't give every bar its own loud color when only
one of them is the point. `references/palette.md` has a validated default
palette (light/dark aware, colorblind-checked) for all three systems — use it
unless the artifact needs to match an existing brand palette, in which case
swap the hex values but keep the lightness/spacing relationships.

## Legends, axes, tooltips

- If there are only 2–3 series, label them directly on the chart (a line's
  end, a bar's top) instead of a separate legend — one less lookup for the
  reader.
- Axis labels need units in the label itself (`"latency (ms)"`), not just in
  a caption below the chart — a screenshot or a scrolled view loses the
  caption.
- Truncate a y-axis that doesn't start at zero only when the domain
  genuinely doesn't include zero (temperature, index values) — otherwise a
  truncated bar-chart axis exaggerates differences and misleads.
- A tooltip (if the chart is interactive) should repeat the exact value and
  its unit, not just re-show what the axis already says visually.

## Legibility at the panel's default width

octo's Artifacts panel docks at **420px wide by default** — see
`artifact-design` for the full constraint. For a chart specifically:

- Avoid grouped/clustered bar charts with more than ~4 categories × 3 series
  at that width — bars become too thin to read; switch to a small-multiple
  layout (one mini-chart per series, stacked vertically) instead.
- Rotate long category labels 0° if at all possible — a 420px column has no
  room for a 45°-rotated label run without eating half the chart height.
  Prefer horizontal bars (category on the y-axis) over vertical bars when
  category names are long.
- Don't rely on hover-only tooltips as the sole way to read a value if the
  chart is likely viewed at the docked width — a static value label on the
  mark itself survives both widths.

## Accessibility

- Every categorical palette in `references/palette.md` is checked against
  common color-vision deficiencies — don't hand-pick replacement hues
  without re-checking, since two colors that look distinct to you may not
  be distinguishable to a colorblind reader.
- Never encode meaning in color alone when the chart might be printed,
  screenshotted in grayscale, or read by someone colorblind — pair color
  with a position, a label, or a pattern (dashed vs solid line, filled vs
  outlined marker).
