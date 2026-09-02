# Provenance audit — reader-simulation

## Source

`haowjy/creative-writing-skills`, path `skills/reader-sim/SKILL.md`
(https://github.com/haowjy/creative-writing-skills, commit
`fd7a3ad9cd7697a0645ff6ff4bd5e809cf7673a3`), Apache License 2.0. As with
the other two skills adapted from this repository in this batch, the root
`LICENSE` file leaves the Apache 2.0 copyright placeholder unfilled;
attribution is to the repository maintainer.

## What the source actually is

A short, tightly-scoped "mode-shift" skill: read a draft as a specified
first-time reader persona and report the felt experience through five
named "reader reward channels" (transportation, aesthetic, social
simulation, curiosity/prediction, flow) — explicitly distinct from
`story-review`'s analytical critique.

## What was reused

- **The persona mechanism**: the caller supplies genre familiarity, taste,
  audience segment, ambiguity tolerance, and what the reader does/doesn't
  know; if no persona is given, the skill states its assumed persona
  rather than defaulting to a "universal reader" reaction.
- **The "report the experience, don't turn into a critic" discipline**:
  stay in the reading rather than shifting into technical/craft critique;
  the value of the output is the felt experience, not a diagnosis.
- **Anchoring claims to the text**: passage/paragraph references and short
  quotes for specific moments that produced the reported experience.
- **The open/close structure**: start with the overall felt experience and
  which channels had the most signal; close with anything outside the
  five channels worth telling the author.
- **The "not every channel needs equal coverage; watch middle passages
  where attention thins" guidance.**

## What was changed — the five reward channels

| Source (fiction) | This skill (non-fiction) |
|---|---|
| Transportation (immersion in story world) | (dropped as a distinct channel — no direct analog for non-fiction) |
| Aesthetic (pleasure from language/craft) | Folded into 阅读心流 rather than kept separate — prose aesthetics matter less as an independent signal in expository writing than whether the argument lands |
| Social simulation (modeling characters as minds) | 情感共鸣 — narrowed to apply when the piece has a narrative/personal-experience component, since most non-fiction has no characters to model |
| Curiosity/prediction (plot questions) | 好奇心与追随欲 — kept as a direct structural equivalent (does the reader want to know what comes next, is a raised question answered at the right time) |
| Flow (absorbing, easy to stay with) | 阅读心流 — kept as a direct equivalent |
| (no equivalent in source) | 可信度与说服力 — added, since non-fiction reading has a persuasion/credibility dimension fiction generally doesn't |
| (no equivalent in source) | 理解摩擦点 — added, since non-fiction's central failure mode (a reader getting lost or needing a term explained) has no fiction analog in the source's channel list |

This is the heaviest adaptation of the three `haowjy`-sourced skills in
this batch: two source channels were dropped or folded rather than
translated, and two new channels were added, because fiction's reward
structure (immersion, character empathy) and non-fiction's (credibility,
comprehension) genuinely diverge — a literal channel-for-channel mapping
would have produced a checklist that doesn't match what actually happens
when someone reads an argument or an explainer.

## Conclusion

The mechanism (persona-bound simulated reading, reported as felt
experience rather than critique, anchored to text) is reused faithfully;
the specific taxonomy of what to notice while reading was substantially
reworked because it is the one part of the source that is genuinely
fiction-specific rather than a generalizable method.
