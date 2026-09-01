# Provenance audit — legal-reasoning

## Source

`anthropics/claude-for-legal`, path `legal-clinic/skills/memo/SKILL.md`
(https://github.com/anthropics/claude-for-legal, commit
`4a6c651889c97cc9140580363c73e0eb17379c2b`), Copyright 2026 Anthropic PBC,
Apache License 2.0 (repository root `LICENSE`, referenced from `README.md`:
"Licensed under the Apache License, Version 2.0. Copyright 2026 Anthropic
PBC."). No `NOTICE` file exists in the source repository.

## What the source actually is

A `/memo` slash-command skill for `claude-for-legal`'s `legal-clinic` plugin
— aimed at **law school clinic students** writing an IRAC case-analysis memo
under a supervising attorney/professor. It scaffolds Issue/Rule/Application/
Conclusion, deliberately leaves Rule as a "research gap" and Application/
Conclusion as blank student-fill-in prompts, and adapts its behavior via a
`pedagogy_posture` setting (`guide` / `assist` / `teach`) read from a
per-practice-area supervisor guide file.

## What was reused (translated, not copied verbatim)

- The **IRAC scaffold shape**: Issue framed as a question, Rule marked
  `[RESEARCH NEEDED]`/`[VERIFY]` rather than stated as settled, Application
  scaffolding the facts that matter rather than answering for the user,
  Conclusion left to be reasoned rather than asserted first.
- The **"Rule is a research gap, not a conclusion" principle** and the
  "framework (unverified) vs. researched" distinction — this is the core
  intellectual contribution worth carrying over, generalized beyond the
  clinic-student use case to any user.
- The **source-attribution tagging system** for citations
  (`[Westlaw]`/`[CourtListener]`/`[web search — verify]`/`[model knowledge —
  verify]`/`[user provided]` in the source) — relabeled for octo's Chinese
  legal-market context as `[官方检索]`/`[网络检索-待核实]`/`[模型记忆-待核实]`/
  `[用户提供]`.
- The **"no silent supplement" policy**: when a research tool returns thin
  coverage, say so and offer explicit options rather than quietly filling
  the gap from web search or model knowledge.
- The **strengths/weaknesses/open-questions section** structure (factual /
  legal / strategic open questions).

## What was removed

- **All clinic/pedagogy machinery**: `pedagogy_posture` (guide/assist/
  teach), the supervisor-guide file lookup
  (`~/.claude/plugins/config/.../guides/<practice-area>.md`), the
  professor/student framing, the "the analysis is the student's" pedagogical
  stance, the semester/handoff/case-file directory structure specific to
  `legal-clinic`.
- **Plugin-specific paths and config loading**
  (`~/.claude/plugins/config/claude-for-legal/legal-clinic/CLAUDE.md`) — octo
  has no equivalent plugin-config layer; replaced with generic instructions
  that work standalone.
- **Blank-by-design STUDENT ANALYSIS/STUDENT CONCLUSION placeholders** — the
  source intentionally never fills these in (the pedagogical point is that
  the student must). This adaptation is for a general user, not a student
  under supervision, so Application and Conclusion are filled in by the
  model using the same "cite what's uncertain" discipline instead of left
  blank.
- **Citation platforms** (Westlaw, CourtListener) specific to US legal
  research — replaced with Chinese official sources (国家法律法规数据库,
  裁判文书网) and octo's own search/skill tools.

## Trademark note

This adaptation states factually that it is derived from Anthropic's
`claude-for-legal` repository, as Apache-2.0 §4(c) requires (retaining
attribution notices). It does not use the "Claude" or "Anthropic" names,
logos, or branding beyond that attribution, and does not imply this skill
is produced, reviewed, or endorsed by Anthropic.

## Conclusion

Genuine adaptation of a well-designed but narrowly-scoped (law-clinic
pedagogy) Apache-2.0 skill into a general-purpose legal-reasoning
methodology. The reused ideas (rule-as-research-gap, citation tagging,
no-silent-supplement) are structural/methodological, not verbatim prose;
all clinic-specific text, file paths, and pedagogy logic were rewritten or
removed.
