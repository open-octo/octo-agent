# Provenance — image-gen

## Source

- **Adapted from:** [`hugohe3/ppt-master`](https://github.com/hugohe3/ppt-master),
  commit `d3ca09555dcfa1226ea13d530ca0846bff461010`. This skill is the image
  **acquisition** capability extracted out of that repo's `skills/ppt-master/`
  and repackaged as a standalone, general-purpose skill.
- **License:** MIT — "Copyright (c) 2025-2026 Hugo He" (reproduced as
  `LICENSE.txt`).
- **`references/prompt-craft/` adapted from:** [`wuyoscar/GPT-Image2-Skill`](https://github.com/wuyoscar/GPT-Image2-Skill),
  commit `e48b023f38769b6377fe38f1de97197d5e12e6d4`, MIT — "Copyright (c) 2026
  Wuyoscar" (reproduced as `references/prompt-craft/LICENSE.txt`). See the
  "Prompt-craft library" section below.

## What this skill is

`image-gen` produces image files on disk through three paths: AI generation
(`image_gen.py`, 14 provider backends), openly-licensed web search
(`image_search.py`), and slicing one generated grid sheet into elements
(`slice_images.py`). It can be driven one-off with a single prompt or in batch
from an `image_prompts.json` manifest whose per-item status it writes back. Any
skill that needs images to disk (e.g. `ppt-master`) delegates here.

## What came from upstream

Moved verbatim out of `skills/ppt-master/`:

- **Scripts:** `image_gen.py`, `image_search.py`, `slice_images.py`,
  `gemini_watermark_remover.py`, `image_backends/` (14 provider backends),
  `image_sources/` (Openverse / Pexels / Pixabay / Wikimedia), and
  `scripts/docs/image.md`.
- **References:** `image-generator.md`, `image-base.md`, `image-searcher.md`,
  `image-palettes/`, `image-renderings/`, `image-type-templates/`.
- **Config:** the image-generation `.env.example` (backend selection + per-
  provider keys).
- **Shared utility modules** `console_encoding.py` and `config.py` are copied
  from ppt-master so this skill is self-contained (both skills carry their own
  copy; the modules are stdio/config helpers, not image logic).

## What was added / changed

- **`SKILL.md`** is new: a general-purpose front door written for this skill
  (input modes, the three paths, preflight/config, references map, and a
  "being invoked by another skill" section). Upstream had no standalone image
  SKILL.md — image behavior was inlined into ppt-master's pipeline.
- **`requirements.txt`** is new: the image-only dependency subset
  (`requests`, `Pillow`, `numpy`; per-provider SDKs optional).
- The moved reference docs retain some ppt-master-flavored wording (deck
  palettes, `page_role`, spec_lock) and a few see-also links back to
  ppt-master-only files; these are harmless in standalone use and were left
  intact rather than rewritten, to preserve the upstream prompt engineering.
  The backend/provider `Use via: …` hints were corrected to this skill's own
  `uv run scripts/image_gen.py …` form, and `scripts/docs/image.md` was trimmed
  to the three image scripts it actually ships (the `latex_render.py` /
  `analyze_images.py` sections belonged to ppt-master and were removed). The
  remaining `skills/ppt-master` / `~/.ppt-master` strings are in the shared
  `config.py` constants and the `.env` lookup-path examples — that shared
  config heritage (both skills carry the same `config.py`) is left as-is.

## Prompt-craft library (`references/prompt-craft/`)

Vendored from [`wuyoscar/GPT-Image2-Skill`](https://github.com/wuyoscar/GPT-Image2-Skill)
(MIT) to lift generated-image quality. It carries `craft.md` (an 18-point
prompt-design checklist), `gallery.md` + ~30 `gallery-*.md` category files (a
~160-prompt exemplar atlas), and `openai-cookbook.md` (gpt-image API/parameter
semantics). Tuned for `gpt-image-2` but the principles apply across backends.

- **Dropped:** the upstream `docs/*.png` preview gallery (hundreds of MB) — the
  category `.md` files keep the prompt text + metadata, which is what matters
  for authoring; a note in `gallery.md` points to the upstream repo for the
  visual previews and flags the image links as unbundled.
- **Not vendored:** the upstream `gpt-image` CLI / `scripts/generate.py` and its
  standalone `SKILL.md` — this skill already generates through its own
  multi-backend `image_gen.py`, so only the prompt-craft knowledge was taken.
- **Wiring:** `SKILL.md` gains a "Prompt quality" section and reference-table
  rows directing the model to read `craft.md` + the closest `gallery-*.md`
  before authoring any AI prompt; `references/image-generator.md` §4 points at
  the same library from its assembly template.

## Default model change

The `volcengine` (Seedream) backend default was moved from
`doubao-seedream-4-5-251128` to `doubao-seedream-5-0-pro-260628` (Seedream 5.0
Pro — the flagship, positioned against gpt-image-2; released 2026-07). The exact
dated Ark model id was not resolvable from public sources (third-party proxies
expose `doubao/doubao-seedream-5.0-pro`; official docs are JS-rendered), so it
was supplied and confirmed by the maintainer. `.env.example` lists the cheaper
5.0 Lite (`doubao-seedream-5-0-260128`) and 4.5 as `VOLCENGINE_MODEL` overrides.

## Runtime: `uv run` / PEP 723

Each script carries a [PEP 723](https://peps.python.org/pep-0723/) inline
dependency block (scoped per script: `image_gen.py` → requests + google-genai;
`image_search.py` → requests; `slice_images.py` → Pillow;
`gemini_watermark_remover.py` → Pillow + numpy) so `uv run` auto-installs into a
cached ephemeral environment — no manual `pip install`. Doc invocations use
`uv run …`; `requirements.txt` is the pip fallback for environments without uv.
