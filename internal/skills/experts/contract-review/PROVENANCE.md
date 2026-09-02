# Provenance audit — contract-review

## Source

`claude-office-skills/contract-review-skill`
(https://github.com/claude-office-skills/contract-review-skill, commit
`b7697d31e1fa281fcd95b2246eda83623bd6fdaf`, MIT — root `LICENSE`: "Copyright
(c) 2025 Contract Review Skill Contributors"). Same GitHub org as
`claude-office-skills/skills`, already adapted in this repo as
`internal/skills/defaults/office-xlsx`.

## What the source actually is

A Python CLI tool (`cli.py`, `src/analyze.py`) that shells out to the
Anthropic API and Claude Vision to analyze contract PDFs (including scanned
documents), plus a `SKILL.md` that is a short capability README (not
step-by-step model instructions) and a JSON knowledge base:
`src/knowledge/risk_patterns.json` — 15 risk patterns (id, bilingual
name, severity, description, Chinese/English detection keywords,
recommendation) and 5 contract-type definitions (`name_cn`/`name_en`/
`key_clauses`).

## What was reused

- The **15 risk patterns** (`unlimited_liability` through
  `audit_rights_missing`): id, severity, and substance translated into a
  single Chinese risk table in `SKILL.md`. The bilingual naming and Chinese
  keyword lists already present in the source's JSON made clear this
  content targets Chinese-market contracts, not a US-only tool as its
  README might suggest at a glance.
- The **5 contract-type key-clause lists** (service agreement, employment,
  NDA, procurement, lease): reused as the completeness-check table.

## What was NOT reused / deliberately removed

- **All code.** `cli.py`, `src/analyze.py`, `src/__init__.py`, the
  Anthropic-API/Claude-Vision pipeline, the `mcp-bridge.js`/`worker/`
  Cloudflare Worker deployment — none of it is used or referenced. This
  skill is prose-only, following octo's "no new tool code" convention for
  skills (see `../README.md` and `CLAUDE.md`).
- **Stamp/seal visual verification, scanned-document OCR, DOCX report
  generation, contract-diff comparison** — capabilities of the original CLI
  not reachable without the Python/Vision pipeline. Out of scope for a
  prompt-only skill; noted in this skill's "边界" section as future/adjacent
  work (`legal-due-diligence` covers batch/comparison scenarios instead).
- **Jurisdiction/employment JSON files** (`src/knowledge/jurisdictions/us/
  employment.json`) — US-specific, not used.

## Structure

The source `README.md`'s described report shape ("Contract Overview / Risk
Assessment / Key Terms Summary / Missing Clauses Checklist /
Recommendations") informed, but was not copied verbatim into, this skill's
four-step workflow (identify type → scan risk table → completeness check →
report). The report template in `SKILL.md` is written from scratch for this
adaptation.

## Conclusion

Genuine adaptation of MIT-licensed structured data (risk patterns,
contract-type checklists) into a prompt-only methodology; no code, no
US-specific content, and no verbatim prose carried over from the source's
README or SKILL.md.
