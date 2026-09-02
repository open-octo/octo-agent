# Provenance audit — weak-point-drill

## Source

`anthropics/claude-for-legal`, path
`law-student/skills/bar-prep-questions/SKILL.md`
(https://github.com/anthropics/claude-for-legal, commit
`4a6c651889c97cc9140580363c73e0eb17379c2b`), Copyright 2026 Anthropic PBC,
Apache License 2.0 (repository root `LICENSE`, confirmed by direct fetch;
no `NOTICE` file exists).

## What the source actually is

A `/bar-prep-questions` skill (~270 lines) for drilling US bar-exam
candidates on MBE-style multiple-choice and MEE-style essay questions,
weighted toward the student's weak subjects and scoped to their specific
bar exam format and jurisdiction. The large majority of the file (roughly
two-thirds by line count) is US bar-exam-specific: a "real-matter check"
referring the student to an actual lawyer, an exam-format gate (NextGen
Bar Exam vs. traditional UBE vs. state-specific exams, including a July
2026 NextGen rollout description and subject-scope differences), and an
extensive "Jurisdiction handling" section on majority-rule vs.
state-specific rule divergence (California, Louisiana, New York, Florida,
Virginia examples) with per-rule divergence tagging rules and NCBE website
citations.

## What was reused

- The weak-subtopic-weighted question generation principle: read prior
  session history, weight new questions toward subtopics that were
  previously missed.
- The `--session <n>` flow: confirm subject/count/format, generate N
  questions weighted by prior misses, present one at a time, explain each
  answer, report score + missed subtopics + weak/strong subtopics +
  pattern vs. prior sessions, write results back to a shared session-
  history file.
- The post-answer explanation structure: correct answer, why it's correct,
  why each wrong option is wrong (for multiple-choice), one-line "rule to
  remember" takeaway.
- The confidence discipline: confident (well-established rule) / uncertain
  (flag inline, tell the user to verify) / don't-invent (skip rather than
  fabricate a rule) — this three-tier framing is generic study-integrity
  discipline, not bar-exam-specific, and was kept almost verbatim in
  structure.
- The cross-session pattern-tracking idea: a subtopic missed repeatedly
  across multiple sessions gets called out explicitly as "stuck" and
  routed to a different remediation approach rather than more of the same
  drilling.

## What was deleted (not translated — no analog exists)

- **The "real-matter check"** (stop and refer to an actual lawyer if the
  question sounds like a real legal situation) — specific to a plugin
  where the AI must avoid appearing to give legal advice to an
  unsupervised law student.
- **The entire exam-type gate** (NextGen vs. traditional UBE vs.
  state-specific, NCBE website pointers, July 2026 rollout details) — this
  is entirely about which US bar exam format applies; a general
  weak-point-drill skill for any subject has no equivalent decision point.
- **The entire "Jurisdiction handling" section** (majority/UBE rule vs.
  state-specific rule divergence, per-rule divergence tagging format,
  state-by-state examples) — legal-jurisdiction-specific; no analog for a
  general study subject (there is no "California version" of organic
  chemistry).
- **MBE/essay-mode-specific formatting** (classic MBE fact-pattern-plus-
  four-choices format description, MEE essay grading rubric specific to
  bar-exam competence standards) — kept only the general shape (multiple-
  choice with wrong-answer explanations; essay/free-response graded on
  issue-spotting, rule accuracy, applied analysis, organization), since
  those four grading dimensions generalize beyond law.
- **"Schedule integration"** referencing a bar-prep-course calendar
  (supplement/replace mode) — generalized into `study-plan`'s own
  supplement/replace handling instead of duplicated here.

## Conclusion

The reusable core of this source — weighted question generation from a
weak-subtopic history, per-answer explanation structure, and confidence
discipline — is a small fraction of the original file's content by line
count, but a well-designed one. Everything specific to the US bar exam's
regulatory structure (formats, jurisdictions, rule divergence) was deleted
outright rather than awkwardly generalized, since none of it has a
meaningful analog outside that specific exam.
