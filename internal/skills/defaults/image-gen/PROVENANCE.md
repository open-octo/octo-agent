# Provenance — image-gen

## Source

- **Adapted from:** [`hugohe3/ppt-master`](https://github.com/hugohe3/ppt-master),
  commit `d3ca09555dcfa1226ea13d530ca0846bff461010`. This skill is the image
  **acquisition** capability extracted out of that repo's `skills/ppt-master/`
  and repackaged as a standalone, general-purpose skill.
- **License:** MIT — "Copyright (c) 2025-2026 Hugo He" (reproduced as
  `LICENSE.txt`).

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
  Cosmetic `skills/ppt-master/...` path strings in script comments / user-agent
  were likewise left as upstream attribution.
