# Default palette

A validated starting palette for the three color systems described in
`dataviz`'s SKILL.md. Swap these hex values for a brand palette when one
exists; keep the lightness/spacing relationships between stops when you do —
that's what keeps the ramp readable, not the specific hues.

## Categorical (distinct groups, no order)

Base set is Okabe–Ito — the standard colorblind-safe categorical palette
(distinguishable under deuteranopia/protanopia/tritanopia). Light-mode value
first, dark-mode tint second (a lighter, higher-saturation shade of the same
hue for legibility on a dark background):

| Role | Light | Dark |
|---|---|---|
| Series 1 (orange) | `#E69F00` | `#fbbf24` |
| Series 2 (sky blue) | `#56B4E9` | `#38bdf8` |
| Series 3 (bluish green) | `#009E73` | `#34d399` |
| Series 4 (blue) | `#0072B2` | `#60a5fa` |
| Series 5 (vermillion) | `#D55E00` | `#f87171` |
| Series 6 (reddish purple) | `#CC79A7` | `#f472b6` |
| Series 7 (yellow — use sparingly, low contrast on light bg) | `#F0E442` | `#fde047` |
| Neutral / "everything else" | `#a8a29e` | `#78716c` |

Assign in this order — it's ordered for maximum adjacent-pair distinguishability,
so cap the series count at what you actually need and take from the top rather
than reordering.

## Sequential (low → high, one direction)

Single hue, lightness-only ramp — don't rainbow this:

| Stop | Light | Dark |
|---|---|---|
| Lowest | `#eff6ff` | `#172033` |
| | `#bfdbfe` | `#1e3a5f` |
| Mid | `#60a5fa` | `#3b82f6` |
| | `#2563eb` | `#60a5fa` |
| Highest | `#1e3a8a` | `#bfdbfe` |

Dark mode inverts the lightness direction (dark→light reads as low→high on a
dark background) — don't reuse the light-mode ramp verbatim under a dark
media query, it'll invert the meaning.

## Diverging (a meaningful midpoint — zero, a target, "no change")

Two hues radiating from a neutral center, symmetric intensity both sides:

| Stop | Light | Dark |
|---|---|---|
| Most negative | `#b91c1c` | `#f87171` |
| | `#fca5a5` | `#7f1d1d` |
| Midpoint (neutral) | `#f5f5f4` | `#292524` |
| | `#93c5fd` | `#1e3a5f` |
| Most positive | `#1d4ed8` | `#60a5fa` |

Red/blue is the default here because "negative/positive" and "below/above
target" are the most common diverging cases; swap to a domain-appropriate
pair (e.g. a single hue's light/dark tint on each side) when red/green-coded
good/bad would clash with a status color already used elsewhere on the page.

## Accent + neutral (the common case: one series matters, the rest are context)

Don't pull from the categorical table when only one series is the point of
the chart — use a single accent against a neutral background instead:

| Role | Light | Dark |
|---|---|---|
| Accent (the highlighted series) | `#2563eb` | `#60a5fa` |
| Neutral (context series) | `#d6d3d1` | `#44403c` |
| Ink (text, axis lines) | `#1c1917` | `#f5f5f4` |
| Muted text (labels, captions) | `#78716c` | `#a8a29e` |
