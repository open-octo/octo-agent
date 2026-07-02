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

**Citation correction (post-review):** an earlier version of this document
justified the "financial-model conventions" section's blue/black/green
color-coding paragraph by pointing partly at the MIT source repo's
`official-skills/xlsx-guide.md`. That was wrong on inspection: the actual
adapted source, `xlsx-manipulation/SKILL.md`, contains **no color-coding
convention at all** (checked directly — no mention of "blue", "black",
"green", or any input/formula/link color mapping anywhere in that file).
The three-color/three-role mapping appears only in `official-skills/
xlsx-guide.md` and in Anthropic's own proprietary `skills/xlsx/SKILL.md` —
i.e. exactly the file this document already excludes for being
Anthropic-licensed. Citing it as support for this paragraph while
excluding it elsewhere was an internal contradiction, not a defensible
citation.

The correct provenance for this one paragraph: it is **not** attributed to
either skills repo. Blue-input/black-formula/green-link color coding is a
public financial-modeling industry convention taught independently of
both, verified directly (fetched, not taken on faith) during this
correction:

- Wall Street Prep's own financial modeling guide states the blue/black
  half of the convention verbatim: "This blue text with a yellow
  background is a standard practice across Wall Street... Corresponding
  with this is the practice of using black text font and a clear
  background to identify formulas in a financial model."
  (https://www.wallstreetprep.com/knowledge/financial-modeling-techniques/,
  fetched and confirmed 2026-07-02)
- WallStreetMojo's reference page states the full blue/black/green/red
  mapping verbatim: "Blue: For hardcoded (i.e. typed) inputs... Black: For
  calculation & cell references within the same sheet... Green: For
  references made to other sheets... Red: For external links outside the
  working file."
  (https://www.wallstreetmojo.com/financial-modeling-color-formatting/,
  fetched and confirmed 2026-07-02)

(A Corporate Finance Institute page was checked as a candidate third
citation and dropped — the specific page found,
`corporatefinanceinstitute.com/resources/financial-modeling/financial-modeling-code/`,
covers general model-quality principles, not this color convention;
citing it would have repeated the same unverified-citation mistake this
correction exists to fix.)

Both confirmed sources are public, unrelated to either skills repo, and
independent of Anthropic's material. SKILL.md's wording has been updated
to cite Wall Street Prep / WallStreetMojo directly instead of resting on
the excluded file. The paragraph remains three sentences of attributed,
non-mandatory guidance ("a common (not mandatory) color convention...
from financial-modeling style guides"), not a lift of Anthropic's
multi-page ruleset (number-format tables, sourcing-comment format, the
full error-prevention checklist) — color-by-role conventions for
financial models are standard industry practice belonging to no one, but
the citation must point at that public
practice directly rather than at a file this document itself excludes.

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
