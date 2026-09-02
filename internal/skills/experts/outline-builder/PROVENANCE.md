# Provenance audit — outline-builder

## Source

`anthropics/claude-for-legal`, path
`law-student/skills/outline-builder/SKILL.md`
(https://github.com/anthropics/claude-for-legal, commit
`4a6c651889c97cc9140580363c73e0eb17379c2b`), Copyright 2026 Anthropic PBC,
Apache License 2.0 (repository root `LICENSE`, confirmed by direct fetch;
no `NOTICE` file exists).

## What the source actually is

A `/outline-builder` skill for law students building course outlines from
class notes and casebooks, built around a "don't write it for me" hard
rule: the skill scaffolds structure and asks Socratic questions, and
refuses to populate rule content from its own knowledge even when asked,
except when extracting from source text the student pastes.

## What was reused

Essentially the entire design, translated:

- The "don't write it for me" hard rule and its exact boundary (scaffold
  yes, populate-from-own-knowledge no, extract-from-pasted-source yes).
- The confrontation script for when a student asks the skill to cross the
  line (offer scaffold-mode vs. source-extract-mode, don't just comply).
- The confidence-discipline provenance-cue system (unmarked for
  student-sourced or confidently-known content, `[VERIFY]`/`[UNCERTAIN]`
  for unconfident model-knowledge content).
- The narrow carve-out for surfacing (not resolving) a contradiction
  between a stated rule and the student's own uploaded materials, with its
  two preconditions (citable prior material exists; the disagreement is
  substantive, not phrasing) and the exact "quote the student's own
  material back to them, don't volunteer the correction" mechanism.
- The four-step workflow (inputs → structure → scaffold-first build with
  three example format styles → gap-marking) and the three gap-marker
  templates.
- The citation-check paragraph and the "what this skill does not do" list.

This is the least-modified of the five `law-student`-derived skills in
this batch — its mechanism was already fully domain-agnostic, and the
underlying pedagogical principle (a scaffold you build yourself teaches
you something a populated document doesn't) applies to studying any
subject, not just law.

## What was changed

- **Vocabulary only**: "casebook" → 教材, "case-brief" → (dropped, no
  general-study equivalent needed), "class notes" → 课堂笔记, "professor" →
  老师, "Westlaw/CourtListener" → generic "权威来源".
- **Integration section**: source points a stuck-outline-section at
  `/law-student:socratic-drill` (a law-student-plugin skill not adapted in
  this batch); replaced with a pointer to this repository's own
  `flashcards` skill for the "generate cards from new material" handoff,
  and a note to fall back to Feynman-technique explanation (already part
  of `learning-coach`'s base system prompt) for a topic that keeps getting
  gap-marked.
- **No storage-path change was needed** — unlike `flashcards`,
  `exam-forecast`, and `study-plan`, this skill does not mandate a fixed
  file location; the source doesn't either (the student's outline can live
  wherever they already keep it), so this skill preserves that
  flexibility rather than imposing a `~/.octo/learning-data/` path.

## Conclusion

A near-verbatim translation of the source's design. The reused content is
not just a technique or a data structure but the skill's entire
pedagogical architecture — appropriate here because that architecture
(scaffold, don't populate; ask, don't tell; extract from the student's own
material, never invent) has no domain-specific content baked into it in
the first place.
