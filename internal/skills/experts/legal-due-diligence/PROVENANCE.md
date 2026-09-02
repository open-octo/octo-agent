# Provenance audit — legal-due-diligence

## Sources

Both from `anthropics/claude-for-legal`
(https://github.com/anthropics/claude-for-legal, commit
`4a6c651889c97cc9140580363c73e0eb17379c2b`), Copyright 2026 Anthropic PBC,
Apache License 2.0:

- `corporate-legal/skills/tabular-review/SKILL.md`
- `corporate-legal/skills/diligence-issue-extraction/SKILL.md`

No `NOTICE` file exists in the source repository.

## What the sources actually are

Two `/`-command skills for `claude-for-legal`'s `corporate-legal` plugin,
built for M&A due diligence at a law firm or in-house legal team:

- **tabular-review**: build a typed column schema, fan out one sub-agent per
  document (parallel), produce a document × field spreadsheet with every
  cell backed by a verbatim quote and location, spot-check quotes in a
  normalization pass.
- **diligence-issue-extraction**: read a VDR (virtual data room) against
  house materiality thresholds and per-category standard issue checklists,
  produce severity-graded findings in house memo format.

Both are deeply integrated with plugin-specific infrastructure: VDR MCP
connectors (Box/Intralinks/Datasite), a "matter workspace" multi-deal
management system with per-deal config files, per-firm "house format"
(materiality thresholds, severity scheme, finding template) loaded from
`~/.claude/plugins/config/claude-for-legal/corporate-legal/CLAUDE.md`, and
handoffs to sibling skills (`ai-tool-handoff` to Luminance/Kira,
`material-contract-schedule`, `closing-checklist`, `deal-team-summary`) that
are not adapted here.

## What was reused (translated, not copied verbatim)

- **The column type system** (`verbatim`/`classify`/`date`/`duration`/
  `currency`/`number`/`free` → 逐字/分类/日期/期限/金额/数字/自由文本) and the
  "every non-verbatim column also carries a verbatim companion quote" rule.
- **The three-state "not found" rule** (`not_present`/`unclear`/
  `needs_review` → 未涉及/不确定/待人工判断) and the rationale for why a blank
  cell is worse than an explicit state.
- **The verbatim-quote discipline**: quotes must be character-for-character,
  location must be re-navigable, composed/paraphrased/reconstructed quotes
  are never acceptable, and the normalization-pass spot-check (sample
  3-5 rows or 10% per column, widen the check on any mismatch, downgrade a
  mismatched "answered" cell more aggressively than an "unclear" one).
- **The sample-run-before-full-run workflow step** (run 3-5 documents,
  check for ambiguous prompts/wrong classify options/paraphrased quotes,
  fix the schema before scaling).
- **The per-category standard issue-extraction checklists** (material
  contracts / corporate / IP / employment / litigation) — translated
  substantively, not just linguistically.
- **The R/Y/G severity grading definitions** for issue extraction.
- **The "no silent supplement" citation-gap policy** and the
  "don't invent a statute's meaning — quote it or decline to characterize
  it" principle from diligence-issue-extraction, generalized and reused
  consistently with the same idea in `legal-reasoning`.

## What was removed

- **Parallel sub-agent fan-out.** None of octo's built-in expert profiles
  (checked: all 8 in `internal/agentprofile/defaults/`) grant the
  `sub_agent` tool. Adding it to `legal-helper` specifically was out of
  scope for this change (a profile-capability decision, not a skill-content
  one) — see the accompanying report to the user. This skill processes
  documents sequentially instead, with an explicit note about the time cost
  at scale (50+ documents) and a recommendation to schema-test on a small
  sample first, which does the same risk-mitigation job as the source's
  sample-run step without requiring parallelism.
- **VDR MCP connectors** (Box/Datasite/iManage/Intralinks) — octo has no
  such connector; replaced with local files / user-provided text via the
  profile's existing `read_file` tool.
- **Matter-workspace multi-deal management** (`matter-workspace switch`,
  per-deal `deal-context.md`, cross-matter isolation) — single-session
  scope only; no equivalent machinery in octo.
- **Per-firm "house format" config layer** (materiality thresholds, finding
  template, severity scheme all pluggable per firm) — replaced with the
  fixed R/Y/G scheme and a fixed finding template stated directly in this
  skill; a firm-specific override system was judged out of scope for a
  first version.
- **Office/Sheets cloud integration** (Claude-in-Excel / Sheets MCP /
  `openpyxl` output pipeline, `_schema` sheet, cell comments/notes, colored
  cells, a `Verified` column) — output is markdown + CSV via the profile's
  existing `write_file` tool; users wanting a formatted spreadsheet are
  pointed at the already-adapted `office-xlsx` default skill rather than
  building a dependency on it into this skill.
- **`ai-tool-handoff` to Luminance/Kira**, **`material-contract-schedule`**,
  **`closing-checklist`**, **`deal-team-summary`** handoff logic — these are
  sibling skills in the source plugin family that were not adapted; this
  skill's "边界" section points users at `contract-review` and
  `legal-reasoning` instead, which are the equivalents that do exist in
  octo.
- **Work-product/privilege distribution header boilerplate specific to a
  law firm's document management practice** — replaced with a shorter,
  generic confidentiality reminder in the output-safeguards section.

## Trademark note

This adaptation states factually that it is derived from Anthropic's
`claude-for-legal` repository, as Apache-2.0 §4(c) requires. It does not use
the "Claude" or "Anthropic" names, logos, or branding beyond that
attribution, and does not imply this skill is produced, reviewed, or
endorsed by Anthropic.

## Conclusion

Genuine adaptation of two Apache-2.0 skills' verification discipline and
domain checklists into a single self-contained methodology that runs on
octo's actual tool set (no VDR connectors, no sub_agent, no Office-agent
integration) and Chinese-market legal categories. All plugin-infrastructure
prose (config paths, matter workspace, house-format loading, sibling-skill
handoffs) was removed rather than translated, since none of it has an
analog in octo.
