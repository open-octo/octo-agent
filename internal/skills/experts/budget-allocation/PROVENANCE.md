# Provenance audit — budget-allocation

## Source

`ErlebnisW/travel-planner`, path
`skills/budget-calculator/instructions.md`
(https://github.com/ErlebnisW/travel-planner, commit
`a7a43cc811ac723566d68c3985bc19920dc95000`), Copyright (c) 2026 Mingzhi
Wang, MIT License (root `LICENSE`, confirmed by direct fetch). Same
repository as `route-optimization`.

## What the source actually is

The instructions for a `budget-calculator` sub-agent in the same
multi-agent travel-planning toolkit used for `route-optimization`:
aggregates cost estimates across categories into an itemized budget. A
thin `SKILL.md` wrapper points to this `instructions.md` as the actual
content.

## What was reused

- The six cost categories in full: accommodation (budget tiers, seasonal
  variation), food (per-meal daily estimate across three tiers plus
  drinks), transportation (airport transfers, daily local transport
  options, self-driving costs, inter-city transport), attractions/
  activities (entrance fees, city passes/combo tickets, guided tours),
  miscellaneous (SIM/data, insurance, souvenirs, tips, laundry, emergency
  fund), and currency & payment guide (exchange rate, where to exchange,
  card acceptance, ATM availability, mobile payment, cash-carry
  recommendation).
- The budget-tier system (budget/mid-range/luxury) applied consistently
  across categories.
- The budget-summary table shape (category × daily estimate × trip total,
  per-person and per-group totals).
- The money-saving-tips checklist and the "where to splurge vs. save"
  framework (including the "common tourist overcharges to avoid" item).
- The output requirements: local currency with an approximate USD/CNY
  equivalent, a stated budget-tier assumption, cited sources, the user's
  chosen output language, practical rounding, a dated-confidence note,
  and optimistic/conservative ranges where variance is significant.

## What was added

An explicit prohibition on false-precision totals (e.g., stating a trip
costs exactly "8742.56 元") — the source already says to round to
practical amounts, but this skill makes explicit that a budget is an
estimate and should never be presented with spurious decimal precision,
which is a common failure mode when an LLM aggregates several rough
estimates into a single summed figure.

## What was not reused

The source's other sub-agents (`trip-architect`, `logistics-planner`,
`local-intel`, `food-explorer`) are separate from `budget-calculator`;
only `trip-architect` was also adapted in this batch (as
`route-optimization`). The other two were not evaluated for this round.

## Conclusion

A close, direct translation of the source's budget-aggregation
methodology, with one added guardrail (no false-precision totals)
addressing a failure mode specific to LLM-generated cost summaries that
the source's human-authored instructions didn't need to call out.
