# Provenance audit — flashcards

## Source

`anthropics/claude-for-legal`, path `law-student/skills/flashcards/SKILL.md`
(https://github.com/anthropics/claude-for-legal, commit
`4a6c651889c97cc9140580363c73e0eb17379c2b`), Copyright 2026 Anthropic PBC,
Apache License 2.0 (repository root `LICENSE`, confirmed by direct fetch;
no `NOTICE` file exists).

## What the source actually is

A `/flashcards` skill for `claude-for-legal`'s `law-student` plugin: Leitner-
bucket flashcard generation and drilling for law students memorizing
black-letter rules, with a "real-matter check" that stops and refers the
student to an actual lawyer if their question sounds like a real legal
situation rather than a study hypothetical, and integration with the same
plugin's `socratic-drill` and `bar-prep-questions` skills.

## What was reused

- The card structure (Q/A/Source/Bucket/Last reviewed/Next review/Notes)
  and the four card-writing rules (one concept per card, question-form
  front, rule-not-paragraph back, cite the source).
- The Leitner bucket/interval table (right/partial/wrong/don't know →
  bucket change + next-review offset) — translated verbatim in substance.
- The five modes (generate/drill/review/stats/session) and their
  respective workflows.
- The confidence discipline: cards generated from a user-provided source
  are confident; cards generated from model knowledge without a source are
  flagged, and the skill prefers a smaller confident deck over padding
  with guesses.
- The `--session <n>` mechanic that appends structured results to a shared
  study-plan history file.

## What was removed

- **The "real-matter check"** (stop if the question sounds like a real
  legal situation, refer to an actual lawyer) — specific to a law-student
  plugin where the AI must not appear to give legal advice; not applicable
  to a general study-flashcards skill.
- **Law-school-specific integration**: routing a stuck card to
  `socratic-drill` (a law-student-plugin skill not adapted here) and
  weighting `bar-prep-questions` MBE drilling by flashcard stats — replaced
  with references to this repository's own `weak-point-drill` and
  `study-plan` skills, which play the equivalent roles.
- **Citation-check wording specific to legal research** ("confirm against
  Westlaw, CourtListener, or your casebook") — generalized to "对照教材、笔记
  或权威来源核实".
- **Plugin config paths**
  (`~/.claude/plugins/config/claude-for-legal/law-student/...`) — replaced
  with a persistent path under `~/.octo/learning-data/`, following this
  repository's existing convention (`internal/skills/defaults/web-access`'s
  `~/.octo/site-patterns/`) for skill data that must survive a
  version-bump re-materialization of the skill directory itself.

## Conclusion

A domain-generalization of a well-designed Leitner-flashcard skill: the
spaced-repetition mechanics and confidence discipline are reused near
verbatim (translated), while every law-school-specific safety check and
integration point was either removed or replaced with the equivalent
sibling skill in this repository.
