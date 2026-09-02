# Provenance audit — structural-diagnosis

## Source

`haowjy/creative-writing-skills`
(https://github.com/haowjy/creative-writing-skills, commit
`fd7a3ad9cd7697a0645ff6ff4bd5e809cf7673a3`, 2026-08-08), Apache License
2.0. The repository's root `LICENSE` file is the unmodified Apache 2.0
template with the copyright line left as the literal placeholder
`Copyright [yyyy] [name of copyright owner]` — it was never filled in.
Attribution in this skill's `LICENSE.txt` is to the repository maintainer
(GitHub user `haowjy`) rather than a specific filled-in copyright holder,
since none exists in the source.

Files used:
- `skills/story-review/resources/editorial-review.md`
- `skills/story-review/resources/developmental-edit.md`
- `skills/story-review/SKILL.md` (for the review-level taxonomy context
  only — not itself reused as content)

## What the source actually is

Part of a large multi-agent fiction-writing toolkit (novels, short
stories, serial fiction). `story-review` is a "mode-shift" skill offering
five review levels from structural to surface (editorial review,
developmental edit, line edit, copyedit, proofreading); this skill draws
on the top two levels, which concern whole-manuscript structure rather
than sentence-level prose.

## What was reused

- The **"order of attention" reading method**: read the full draft twice
  (felt-experience pass, then diagnostic pass) before writing any note;
  read top-down from reader-promise through developmental structure,
  voice, line-level execution, to surface issues; don't lead with
  proofreading unless that's the requested level.
- The **structural-vs-execution distinction**: a scene/passage that
  dramatizes the right beat clumsily needs rewriting (execution); one that
  dramatizes the wrong beat or has no structural function needs cutting,
  and no amount of line-level polish fixes that (structural).
- The **editorial memo shape**: overall diagnosis (the dominant problem,
  not an exhaustive list) → priority queue ordered by reader cost → major
  notes anchored to passages, each naming the problem/cost/direction → 
  line/voice notes as named recurring patterns (not per-instance) →
  copy/proof notes only if recurring → revision order.
- The **editorial discipline** in full: protect the author's voice (resist
  the urge to rewrite in your own style); query before overriding
  (phrase as a question when a change would affect meaning/voice, not a
  directive); distinguish voice from error (ask, don't correct, when
  something might be a deliberate choice); stay in your lane (diagnose,
  don't produce replacement prose unless asked); name what works, briefly.
- The **developmental-edit check list** (premise/promise, causality, scene
  function, pacing, character arc, stakes, information design, genre
  contract) — reorganized as a non-fiction check list.

## What was changed (fiction → non-fiction vocabulary)

| Source (fiction) | This skill (non-fiction) |
|---|---|
| Genre promise / reader promise from opening pages | 读者承诺——引言/开头立下的期待 |
| Causality between plot events | 论点/论据之间的因果链 |
| Scene function (what changes by scene's end) | 段落功能（认知/态度/论证推进是否变化） |
| Character arc motivated by want/fear/wound | (dropped — no non-fiction analog; not force-mapped) |
| Stakes the reader can feel | (dropped — not universally applicable to non-fiction; folded into "读者承诺" instead) |
| Genre contract (mystery/romance reward types) | 文体承诺——一篇文章该给读者的那种体验契约 |
| POV/narrator voice | 视角稳定性、语气统一性 |

Two checks (character arc's want/fear/wound framing, and the
felt-stakes check) were dropped rather than mechanically translated —
they don't have a clean non-fiction equivalent and forcing one would have
been invented content, not adaptation.

## What was not reused

- `line-edit.md`, `copyedit.md`, `proofreading.md` — these cover
  sentence-level and surface-level review, reused instead (separately) in
  this repository's `line-polish` skill.
- `prose-critique.md` and its focus-area resources (character, voice,
  prose, continuity) — adversarial craft critique, a different review mode
  from editorial review; not adapted in this batch.
- Everything about the multi-agent orchestration, knowledge-base sync,
  and `meridian`-specific tooling described in the source repo's README —
  irrelevant to a single self-contained prose-only skill.

## Conclusion

A faithful translation of the source's structural-diagnosis methodology
and editorial discipline from fiction to non-fiction long-form writing.
The reused content is the *method* (how to read, how to prioritize, how
to phrase feedback, what not to do), not fiction-specific subject matter —
two checks with no non-fiction analog were dropped rather than forced.
