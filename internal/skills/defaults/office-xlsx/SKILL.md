---
name: office-xlsx
description: Create, read, and edit Excel (.xlsx) spreadsheets programmatically with openpyxl — cell values, formulas, styling (fonts/fills/borders/alignment/number formats), merged cells, multiple sheets, charts, and data validation. Use whenever the user wants a spreadsheet created, inspected, or modified, or references an .xlsx file by name or path.
license: MIT (adapted from claude-office-skills/skills; complete terms in LICENSE.txt)
---

# XLSX Manipulation

Two scripts drive this skill, both built on **openpyxl**:

- `scripts/xlsx_inspect.py` — read a workbook and print its structure (sheets, dimensions, header row, sample rows, formulas, merged cells) as JSON. Use this first on any existing file so you know what you're working with before editing it.
- `scripts/xlsx_edit.py` — create a new workbook or edit an existing one by applying a JSON list of operations (set cell values/formulas, styling, merges, column widths, new sheets, charts, data validation).

Both scripts are self-contained: they declare their own dependency (`openpyxl`) via [PEP 723](https://peps.python.org/pep-0723/) inline metadata, so `uv run` installs it into an ephemeral environment automatically — no project venv, no `pip install` step, nothing left behind on the machine.

## Preflight: check for `uv`

Look at this session's toolchain note (or run `uv --version`) before the first script call:

- **`uv` present** — run scripts directly: `uv run <skill-dir>/scripts/xlsx_inspect.py <args>`. Dependency install is automatic and silent; don't run a separate `pip install openpyxl` step, it isn't needed and may not even land in the environment `uv run` actually uses.
- **`uv` absent** — do not assume `python3`/`pip` are available or silently install anything. Tell the user what's missing (`uv`, installable via `curl -LsSf https://astral.sh/uv/install.sh | sh` on macOS/Linux or the equivalent on Windows — see https://docs.astral.sh/uv/getting-started/installation/) and wait for their go-ahead before installing. If they'd rather not install `uv`, the fallback is a manual `python3 -m venv`/`pip install openpyxl` and running the scripts with `python3` instead of `uv run` — but only after checking with the user, and only if `python3`/`pip` are themselves confirmed present.

Note: `<skill-dir>/scripts/...` paths are relative to this skill's own directory (its absolute path is in the location header injected when this skill is loaded) — they are not relative to the user's working directory. There is no persistent working directory across terminal calls; pass absolute paths for `--input`/`--output`/`--ops`.

## Reading a spreadsheet

```bash
uv run <skill-dir>/scripts/xlsx_inspect.py path/to/file.xlsx
```

Options:
- `--sheet NAME` — inspect one sheet only (default: all sheets)
- `--rows N` — number of sample data rows to include per sheet (default: 5)
- `--data-only` — read cached formula *results* instead of formula text (only meaningful if the file was last saved by Excel/LibreOffice; openpyxl never computes formulas itself, so a file saved by openpyxl-only edits has no cached values to show)

Output is JSON: for each sheet, its dimensions, header row (best-effort: first non-empty row), sample rows, every formula cell found (address + formula text), and merged cell ranges. Read this before writing any edit operations so cell addresses and sheet names are correct.

## Creating or editing a spreadsheet

```bash
uv run <skill-dir>/scripts/xlsx_edit.py --output out.xlsx --ops ops.json
uv run <skill-dir>/scripts/xlsx_edit.py --input existing.xlsx --output out.xlsx --ops ops.json
```

- Omit `--input` to start from a blank workbook. Its one default sheet is named `Sheet` (not `Sheet1`) — the first `add_sheet` op on a blank workbook renames that default sheet instead of leaving an empty extra tab, so give it the name you actually want as your first sheet.
- `--input` and `--output` may be the same path to edit in place.
- `--ops` points to a JSON file (or pass inline JSON via `--ops-json '...'`) containing a list of operation objects, applied in order. Supported operations:

| `op` | Fields | Effect |
|---|---|---|
| `add_sheet` | `name`, `index` (optional) | Create a new sheet |
| `set_cell` | `sheet`, `cell`, `value` | Set one cell; a leading `=` makes it a formula |
| `set_row` | `sheet`, `row`, `values` (list, appended starting at column A of that row) | Write a whole row at once |
| `style` | `sheet`, `range` (single cell or `A1:C3`), `font` (bold/italic/size/color/name), `fill` (color, solid), `border` (true = thin box), `alignment` (horizontal/vertical/wrap_text), `number_format` | Apply formatting to a cell or range |
| `merge` | `sheet`, `range` | Merge a cell range |
| `column_width` | `sheet`, `column` (letter), `width` | Set column width |
| `row_height` | `sheet`, `row`, `height` | Set row height |
| `freeze_panes` | `sheet`, `cell` | Freeze rows/columns above/left of `cell` |
| `data_validation` | `sheet`, `range`, `type`, `formula1`, `formula2` (optional), `allow_blank` (optional) | Add a validation rule (e.g. `type: "list"` with `formula1: '"Yes,No"'`) |
| `chart` | `sheet`, `type` (`bar`\|`line`\|`pie`), `data_range`, `categories_range`, `title`, `anchor`, `titles_from_data` (optional, default true) | Add a chart built from a data range on the same sheet |

Ranges and cell references use normal Excel A1 notation. `data_range`/`categories_range` are parsed as `Sheet!A1:B5` or, if the sheet is omitted, resolved against the chart's own `sheet`.

**Formulas are not evaluated by openpyxl.** `set_cell` with a `=...` value writes the formula text; Excel, LibreOffice, or Google Sheets will compute it on open. If a user needs computed values back out of the script (rather than just handing the file to Excel), that requires a spreadsheet engine this skill doesn't bundle — say so rather than approximating a result in Python and hardcoding it into the cell, which would silently go stale the next time an input changes.

## Financial-model conventions worth following

When building anything that looks like a financial model (budgets, valuations, projections), keep the spreadsheet self-updating and readable:

- Prefer formulas over Python-computed constants for anything derived from other cells — see the note above.
- Use cell references for assumptions instead of hardcoding multipliers into formulas (`=B5*(1+$B$6)` rather than `=B5*1.05`), so a reader can find and change the assumption.
- A common (not mandatory) color convention from financial-modeling style guides (e.g. Wall Street Prep): blue text for hardcoded inputs, black for formulas, green for cross-sheet links — apply via the `style` op's `font.color` field if the user wants this or it matches an existing template's convention.
- If editing an existing template, match its existing formatting/conventions rather than imposing a different style.

## Limitations

Inherited from openpyxl itself: no VBA macro execution, no live external data connections, and pivot tables/sparklines are not supported by these scripts.
