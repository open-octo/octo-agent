# Provenance — ppt-master

## Source

- **Adapted from:** [`hugohe3/ppt-master`](https://github.com/hugohe3/ppt-master),
  path `skills/ppt-master/`, commit `d3ca09555dcfa1226ea13d530ca0846bff461010`.
- **License:** MIT — "Copyright (c) 2025-2026 Hugo He" (repo root `LICENSE`,
  reproduced here as `LICENSE.txt`). MIT permits redistribution and modification
  with the copyright notice retained, so this skill is vendored (not merely
  referenced), unlike the no-redistribution `office-*` document skills.

## What this skill is

`ppt-master` converts a source document (PDF / DOCX / URL / Markdown) into a
deck by authoring SVG pages through a multi-role pipeline (Strategist →
Executor) and exporting to editable PPTX. It shells out to a bundled Python
engine (`scripts/`) for source conversion, SVG post-processing, quality checks,
PPTX export, animations, and narration. It expects `python3` (see
`requirements.txt`) and, for some paths, LibreOffice (`soffice`).

## What was changed vs. upstream

Upstream `skills/ppt-master/` is ~700 MB / 13.5k files. Because octo embeds the
default-skill set into the binary (`//go:embed defaults`), two large asset trees
were **dropped** and one capability was **split out**:

### Dropped (size)

- **`references/ai-image-comparison/`** (≈45 MB of PNG style-comparison
  galleries) — removed. The textual style taxonomies it illustrated
  (palettes / renderings / types) survive as Markdown in the `image-gen` skill.
- **`templates/icons/`** (≈10 MB, 11,600+ Tabler / chunk-filled / phosphor /
  simple-icons SVGs) — the icon **files** are removed; `templates/icons/README.md`
  is replaced with a stub explaining how to populate the library on demand and
  noting that project-local custom icons still work (`icon_sync.py` resolves
  project-first). Nothing else about the icon workflow changed.

### Split out to the `image-gen` skill

All image **acquisition** (AI generation, web search, sheet slicing) was moved
into a separate general-purpose `image-gen` default skill so it is reusable and
not duplicated. Moved out of ppt-master:

- `references/image-generator.md`, `image-base.md`, `image-searcher.md`,
  `image-palettes/`, `image-renderings/`, `image-type-templates/`
- `scripts/image_gen.py`, `image_search.py`, `slice_images.py`,
  `gemini_watermark_remover.py`, `image_backends/`, `image_sources/`,
  `scripts/docs/image.md`, and the image-generation `.env.example`

ppt-master retains the deck-side pieces and delegates: it owns the resource list
(`design_spec.md §VIII`), on-page image placement (`references/svg-image-embedding.md`,
`image-layout-patterns.md`, `image-layout-spec.md`), LaTeX formula rendering,
and image-fact tooling (`scripts/analyze_images.py`, `rotate_images.py`), and
hands generation off through the `images/image_prompts.json` manifest. The
delegation is documented in `SKILL.md` (top-of-file note + Step 5 banner).
`console_encoding.py` and `config.py` are small shared utility modules present
in both skills so each is self-contained — the image-generation *capability* is
not duplicated.

## Kept verbatim

Everything else — `SKILL.md` (with the image-delegation edits noted above),
the remaining `references/`, `workflows/`, `scripts/`, and `templates/`
(brands / charts / decks / layouts) — is upstream content, unmodified except for
the pointer rewrites required by the drops and the split.

## Runtime: `uv run` / PEP 723

Upstream expects a manual `pip install -r requirements.txt`. Each entry script
here was given a [PEP 723](https://peps.python.org/pep-0723/) inline dependency
block (a uniform copy of `requirements.txt` minus `google-genai`, which the
image-gen split removed) so `uv run` auto-installs into a cached ephemeral env —
`uv` ships with the octo installer, so there is no manual setup step. All doc /
reference invocations were switched from `python3 …` to `uv run …`. Guarded,
system-library or optional deps (`cairosvg`, `reportlab`, `svglib`, `playwright`,
`PyYAML`) are deliberately excluded from the inline blocks; those code paths are
`try/except`-guarded and degrade gracefully, and `requirements.txt` remains as
the pip fallback.
