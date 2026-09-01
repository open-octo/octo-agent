# Provenance audit — exam-forecast

## Source

`anthropics/claude-for-legal`, path
`law-student/skills/exam-forecast/SKILL.md`
(https://github.com/anthropics/claude-for-legal, commit
`4a6c651889c97cc9140580363c73e0eb17379c2b`), Copyright 2026 Anthropic PBC,
Apache License 2.0 (repository root `LICENSE`, confirmed by direct fetch;
no `NOTICE` file exists).

## What the source actually is

A `/exam-forecast` skill for law students: analyzes past exams from the
same professor to surface stable/variable/absent patterns (subject
weighting, question style, recurring traps, policy-vs-doctrine mix) and
combines them with the current syllabus to forecast likely emphases. The
source is already almost entirely domain-agnostic in its mechanism — the
only law-specific surface is vocabulary ("professor", "casebook", "hypo",
"issue-spotter") and the required output header
`STUDY NOTES — NOT LEGAL ADVICE`.

## What was reused

- The full workflow: intake (how many past exams, same/different course,
  format consistency) → per-exam analysis (format, subject coverage,
  question style, fact-pattern density, recurring traps, policy/doctrine
  ratio) → cross-exam pattern rollup (stable / variable / absent patterns)
  → syllabus-combined forecast → report.
- The confidence discipline verbatim in substance: pattern analysis of
  exams actually in hand is confident; inference about the upcoming exam
  defaults to `[UNCERTAIN]` and is framed as a weighting heuristic, not a
  prediction; fewer than 3 past exams is flagged as a thin sample; no past
  exams at all means the skill falls back to syllabus-coverage-only advice.
- The forecast report template (subject weighting table, question-style
  forecast, "hobby horses to watch", "covered but rarely tested" section,
  study-emphasis time allocation, closing uncertainty framing).
- The requirement that the output carry an identifying header as its
  first line, not omittable or rephraseable — the mechanism (an
  unmissable identity marker preventing the output from being mistaken
  for something it isn't) is reused; the specific text is changed (see
  below).

## What was changed

- **Header text**: source uses `STUDY NOTES — NOT LEGAL ADVICE`, whose
  purpose is specifically to prevent an AI legal-adjacent output from
  being mistaken for legal advice. This skill's context has no such
  concern (a study forecast for a chemistry final isn't at risk of being
  mistaken for professional advice), so the header was changed to
  `学习笔记——基于历年真题规律的权重分析，不是考试预测`, which serves the
  structurally equivalent purpose for this context: preventing a weighting
  analysis from being mistaken for a guaranteed prediction of exam content.
- **Vocabulary**: "professor" → 老师, "casebook" → 教材, "hypo"/"issue-spotter"
  → 例题/案例分析题 — direct terminology substitution, no structural change.
- **Integration section**: source references `outline-builder`,
  `flashcards`, `bar-prep-questions` (bar-exam specific, explicitly marked
  irrelevant in the source itself), and `irac-practice` (not adapted here).
  This skill keeps the `outline-builder` and `flashcards` cross-references
  (both exist as sibling skills in this repository) and replaces the
  bar-prep/IRAC references with `weak-point-drill`, the equivalent
  targeted-practice skill in this repository.
- **Storage path**: `~/.claude/plugins/config/.../exam-forecasts/` →
  `~/.octo/learning-data/exam-forecasts/`, following this repository's
  `~/.octo/site-patterns/` convention for skill data that must survive a
  version-bump re-materialization.

## Conclusion

This is the lightest adaptation among the five `law-student`-derived
skills in this batch — the source's mechanism was already domain-agnostic;
the only substantive change was the required header's wording (to match
this context's actual concern) and terminology.
