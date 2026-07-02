# Provenance audit — office-xlsx

## Sources compared

- **Adapted from:** `claude-office-skills/skills`, path `xlsx-manipulation/SKILL.md`
  (https://github.com/claude-office-skills/skills, commit `9c4c7d5`, MIT
  license — root `LICENSE` file: "Copyright (c) 2026 Claude Office Skills
  Contributors").
- **Compared against (read-only, not copied from):** `anthropics/skills`,
  path `skills/xlsx/` (https://github.com/anthropics/skills), the
  no-redistribution Anthropic original this repo is legally barred from
  vendoring (see `skills/xlsx/LICENSE.txt` in that repo: "© 2025 Anthropic,
  PBC. All rights reserved" with an explicit no-derivative-works,
  no-redistribution clause).

## What each source actually is

**claude-office-skills' `xlsx-manipulation/SKILL.md`** (495 lines, single
file, no bundled scripts) is a prose knowledge-reference document aimed at
having the model *write its own openpyxl code inline per request* ("Describe
the spreadsheet you want... I'll generate openpyxl code and execute it"). It
covers, as a cheatsheet: cell read/write, formulas, `Font`/`PatternFill`/
`Border`/`Alignment` styling, number formats, conditional formatting,
charts (`BarChart`/`LineChart`/`PieChart`), data validation, sheet
management, row/column operations, and two worked examples (a budget
tracker, a sales dashboard). It ships **no runnable scripts** — every code
sample is an inline markdown fence meant to be retyped/adapted by the model
each time, not executed as-is.

**Anthropic's `skills/xlsx/`** (SKILL.md + `scripts/recalc.py` +
`scripts/office/{pack,unpack,validate,soffice}.py` + a `helpers/` package +
a bundled directory of ECMA-376/ISO-29500 OOXML XSD schemas, ~3,000 lines of
Python across the scripts alone) takes a structurally different approach:
direct OOXML XML manipulation (unpack/pack the .xlsx zip, validate against
the bundled schemas), a LibreOffice headless-recalc pipeline
(`scripts/recalc.py`, with `soffice.py` handling sandboxed Unix-socket
restrictions) that actually evaluates formulas and reports `#REF!`/`#DIV/0!`
etc. as structured JSON, and detailed financial-model formatting/color-coding
policy in the prose (blue=input/black=formula/green=cross-sheet-link,
number-format rules, a formula-error-prevention checklist).

## Also found and explicitly excluded from this adaptation

The source repo additionally contains `official-skills/xlsx-guide.md`, a
short "quick reference" file whose own frontmatter reads
`license: Proprietary (Anthropic)` and `source: https://github.com/anthropics/skills/tree/main/skills/xlsx`
— i.e. the repo's own maintainers labeled that specific file as a pointer
to Anthropic's proprietary skill, not as their MIT-licensed content. That
file **was not used** as a source for this adaptation; only
`xlsx-manipulation/SKILL.md` (plain `license: MIT` in its own frontmatter,
no such proprietary flag) was adapted.

## Comparison and conclusion

| Aspect | claude-office-skills (source) | anthropics/skills (comparison only) | This adaptation |
|---|---|---|---|
| Delivery mechanism | Prose cheatsheet, model retypes code each time | Bundled Python scripts the model shells out to | Bundled Python scripts the model shells out to |
| Formula evaluation | None (openpyxl never evaluates formulas; source doesn't mention this limitation) | `scripts/recalc.py` via headless LibreOffice, JSON error report | None (openpyxl-only; SKILL.md explicitly documents this limitation and tells the model not to fake computed values in Python) |
| File manipulation approach | openpyxl object API only | Direct OOXML XML unpack/pack/validate against bundled XSD schemas | openpyxl object API only |
| Runnable artifacts | None | 8 scripts + schema directory | 2 scripts (`xlsx_inspect.py`, `xlsx_edit.py`), written from scratch |
| Structure of code | Ad hoc snippets per topic (formulas, charts, validation, ...) | Modular package (`office/helpers`, `office/validators`, `office/schemas`) | Two flat CLI scripts with a JSON operation-list dispatcher |
| Financial-model formatting guidance | Not present | Detailed (color coding, number formats, sourcing comments, error-prevention checklist) | Summarized in SKILL.md, credited as "a common (not mandatory) color convention" — see below |

**Finding: this is a genuine, independent adaptation of the MIT source, not
a disguised republication of the Anthropic original.** The two differ in
every dimension that matters for a copyright/derivative-work assessment:
delivery mechanism (prose-for-the-model vs. bundled-scripts-for-the-model vs.
this adaptation's bundled-scripts), the technical approach to formula
handling (openpyxl-only vs. LibreOffice-recalc-with-XML-validation), the
file-manipulation strategy (object API vs. raw OOXML XML), and the actual
code (no source file in this skill contains code copied or lightly
reworded from Anthropic's scripts — Anthropic's `recalc.py`/`pack.py`/
`unpack.py`/`validate.py`/schema files have no analog here at all, since
this skill deliberately does not implement recalculation).

The overlap that does exist between all three (this skill, the MIT source,
and the Anthropic original) is limited to facts about the openpyxl library
itself — e.g. `Font(bold=True)`, `ws['A1'] = value`, `ws.merge_cells(...)`,
`BarChart()`/`Reference(...)` — which is inevitable for any tool built on
openpyxl's public API and appears near-identically in openpyxl's own
official documentation and in hundreds of independent tutorials predating
both repos. This is not the kind of similarity that indicates copying;
it is the shape any code using this library takes.

One point of genuine, acknowledged influence: the "financial-model
conventions" section in this skill's SKILL.md (blue=input/black=formula/
green=cross-sheet-link color coding) restates a color-coding practice that
is a real, decades-old financial-modeling industry convention (documented
in modeling texts and courses well before either skills repo existed, e.g.
Wall Street Prep / Corporate Finance Institute style guides) and which
Anthropic's own skill also documents. It is presented here as attributed,
non-mandatory guidance ("a common (not mandatory) color convention"), not
as a verbatim lift of Anthropic's specific checklist wording — this
skill's version is three sentences, not Anthropic's multi-page ruleset
(number-format tables, sourcing-comment format, the full error-prevention
checklist), and the MIT source repo also independently mentions the same
convention set at a similar level of generality via its
`official-skills/xlsx-guide.md` (the file explicitly excluded above), which
itself is presented as a proprietary-labeled pointer, not as this skill's
content. Flagging this for visibility rather than treating it as a
concern: color-by-role conventions for financial models are standard
industry practice, not Anthropic IP.

## What was NOT reused

- No file from `anthropics/skills` was copied, read into an editor and
  retyped, or used as a template for file/directory layout.
- No prose from `anthropics/skills/skills/xlsx/SKILL.md` was paraphrased
  into this skill's SKILL.md.
- The formula-recalculation, OOXML-unpack/pack, and schema-validation
  machinery from the Anthropic skill was deliberately **not** built here;
  this skill is openpyxl-only and says so.

## Scripts: written from scratch, not "adapted" line-by-line

Because the MIT source ships no runnable scripts, `scripts/xlsx_inspect.py`
and `scripts/xlsx_edit.py` are new code, written to cover the same
*capabilities* the source's prose describes (read/write cells, formulas,
styling, merges, sheets, charts, data validation) using a JSON
operation-list design that has no counterpart in either source repo. This
is noted as a deviation from a literal "adapt the actual Python script(s)"
reading of the assignment, made necessary by the source's actual structure.
