# Provenance audit — study-plan

## Sources

Both from `anthropics/claude-for-legal`
(https://github.com/anthropics/claude-for-legal, commit
`4a6c651889c97cc9140580363c73e0eb17379c2b`), Copyright 2026 Anthropic PBC,
Apache License 2.0 (repository root `LICENSE`, confirmed by direct fetch;
no `NOTICE` file exists):

- `law-student/skills/study-plan/SKILL.md`
- `law-student/skills/session/SKILL.md`

## What the sources actually are

- **study-plan**: builds and adapts a long-term bar-exam (or law-school-
  exam) study plan — phases, subject weighting by weakness, a life-context
  sanity check on stated study hours, a prep-course supplement-vs-replace
  decision, cram-mode behavior, and adaptive re-weighting from session
  history.
- **session**: a thin dispatcher that parses a subject+count request,
  routes to `bar-prep-questions` (MBE/essay) or `flashcards` depending on
  a mode flag, runs the session, and writes results back to the plan.

## What was reused

- **Phase structure**: learning/drilling/review phases with the same
  proportional time split (~60%/30%/10%) and per-phase focus description,
  translated.
- **Weak-subject weighting**: weak subjects get ~2x the hours of strong
  subjects — kept verbatim in substance.
- **The mandatory life-context sanity check**: ask hours/week, then force
  a second question (not skippable, not bundled into the first) that asks
  about the student's actual life load and explicitly checks the stated
  hours against it, using the lower number if the check produces one, and
  recording a `confidence_flags` note if the student declines to answer.
  This is the single most valuable design element in the source and is
  reused essentially verbatim (translated) — the source's own commentary
  ("A plan you can't follow is worse than a lighter plan you can") is
  reused as the guiding principle, not the literal source's English
  wording.
- **The prep-course supplement-vs-replace gate**: if the student is
  already following a structured external course/curriculum, force a
  single explicit choice between "this plan supplements it" and "this plan
  replaces it" rather than silently running two parallel curricula.
- **Cram-mode behavior** (<4 weeks out): explicit flag-and-warn, 80/20
  high-yield prioritization, daily practice volume, taper in the final
  2-3 days with the explicit note that cramming the night before produces
  worse outcomes.
- **The YAML plan schema** (phases, subjects with priority/weekly_hours/
  methods, a day-by-day schedule, and an appended `session_history`) and
  the confirm-in-prose-before-writing step.
- **The adaptive re-weighting logic**: session results feed back into
  subject priority and weekly hours; falling behind triggers a
  compress-or-flag decision; getting ahead opens time for deeper
  weak-subject work.
- **From `session`**: the parse-subject-and-count intake, the
  weight-by-prior-misses read from session history, the per-session report
  format (score, missed list, weak subtopics, pattern vs. prior sessions),
  and the session-result write-back mechanism.

## What was removed

- **All NCBE/bar-exam-specific content**: reading the NCBE subject outline
  for the exam format, bar-jurisdiction intake as a plan input, MBE/MEE
  terminology, and the header requirement tied to the legal-advice concern
  (`STUDY NOTES — NOT LEGAL ADVICE`) — not needed here since this skill's
  domain has no equivalent liability concern.
- **Routing to law-student-specific skills**: `session`'s mode flags routed
  to `bar-prep-questions` and (implicitly, per its own integration notes)
  `irac`/`socratic-drill`; replaced with routing to this repository's own
  `weak-point-drill` and `flashcards`.
- **Plugin config paths**
  (`~/.claude/plugins/config/claude-for-legal/law-student/...`) — replaced
  with `~/.octo/learning-data/`, following this repository's
  `~/.octo/site-patterns/` precedent for skill data that must survive a
  version-bump re-materialization of the skill directory.

## Consolidation note

The two source files (`study-plan` and `session`) were merged into one
skill here because `session` is a thin, single-purpose dispatcher over
`study-plan`'s own data file and the skills this repository already has
(`flashcards`, `weak-point-drill`) — keeping it as a separate skill would
have meant a nearly-empty file whose entire content is "call one of the
other two skills and log the result." That dispatching logic is folded
into this skill's own `--session` section instead.

## Conclusion

A substantial, structurally-faithful translation of the source's planning
methodology — the life-context sanity check and the supplement/replace
gate in particular are reused because they encode hard-won, generalizable
judgment about why study plans fail, not anything specific to bar-exam
preparation.
