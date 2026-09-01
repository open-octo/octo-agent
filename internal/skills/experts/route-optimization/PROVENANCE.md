# Provenance audit — route-optimization

## Source

`ErlebnisW/travel-planner`, path
`skills/trip-architect/instructions.md`
(https://github.com/ErlebnisW/travel-planner, commit
`a7a43cc811ac723566d68c3985bc19920dc95000`), Copyright (c) 2026 Mingzhi
Wang, MIT License (root `LICENSE`, confirmed by direct fetch).

## What the source actually is

The instructions for a `trip-architect` sub-agent in a multi-agent travel
planning toolkit: researches attractions, classifies them, and designs
day-by-day routes. A thin `SKILL.md` wrapper points to this
`instructions.md` as the actual content.

## What was reused

- The four-tier attraction classification (Must-Visit / Recommended /
  Optional / Tourist Trap, with an explicit requirement to explain *why*
  something is a tourist trap rather than just labeling it).
- The per-attraction practical-details checklist (hours and closed days,
  ticket price and purchase channel with a preference for official links,
  best time to visit, visit duration, accessibility notes).
- The route-optimization principles (minimize travel time between stops,
  group nearby attractions on the same day, balance busy and relaxing
  days, account for opening hours/closing days and stated group
  composition).
- The seasonal-awareness checklist (seasonal events, weather impact,
  peak/off-peak crowds, season-exclusive experiences).
- The photography-spots guidance (best time of day, specific viewpoints,
  sunrise/sunset times).
- The day-trip research checklist (travel time/options, worth-it
  assessment, full vs. half day).
- The output requirements: every recommendation carries a stated reason,
  sources are cited, output matches the user's chosen language, and the
  plan accounts for stated group composition and interests.

## What was added

An explicit instruction not to state time-sensitive prices or opening
hours from training-data memory without live verification via
`web_search`/`web_fetch`, and to say plainly when a figure couldn't be
confirmed rather than presenting a possibly-stale number as current. The
source implies this (it says to research and cite sources) but doesn't
state the negative case (what to do when verification isn't possible)
as explicitly; this skill makes it an explicit rule consistent with this
repository's general anti-fabrication conventions.

## What was not reused

The source's other responsibilities outside `trip-architect` proper —
`budget-calculator`, `logistics-planner`, `local-intel`, `food-explorer` —
are separate sub-agents in the same repository. Only `budget-calculator`
was adapted in this batch (as this repository's `budget-allocation`
skill); the other two were not evaluated for this round.

## Conclusion

A close, direct translation of the source's attraction-research and
route-design methodology, with one addition (explicit live-verification
requirement for time-sensitive facts) consistent with this repository's
broader anti-fabrication stance.
