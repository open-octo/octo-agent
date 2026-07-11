---
name: image-gen
description: >
  Acquire images as files — generate them with an AI image model (14 providers:
  OpenAI/gpt-image, Gemini, Qwen, Zhipu, Volcengine, Stability, FLUX, Ideogram,
  MiniMax, and more), search openly-licensed stock (Openverse/Pexels/Pixabay/
  Wikimedia), or slice one generated sheet into elements. Drive it one-off with a
  single prompt, or in batch from an image_prompts.json manifest with a written-
  back status/audit trail. Use when the user wants to generate/create/make an
  image or illustration, source a photo, or when another skill (e.g. ppt-master)
  needs images produced to disk.
license: MIT (adapted from hugohe3/ppt-master; complete terms in LICENSE.txt)
---

# Image Generation & Sourcing

Produce image files on disk through one of three acquisition paths. This skill
is the single home for image acquisition — other skills delegate to it rather
than embedding their own copy.

| Path | Script | Output status | What it does |
|---|---|---|---|
| **AI generation** | `scripts/image_gen.py` | `Generated` | Text prompt → rendered image via a configured provider backend |
| **Web search** | `scripts/image_search.py` | `Sourced` | Query → best openly-licensed match downloaded, with attribution recorded |
| **Slice** | `scripts/slice_images.py` | `Generated` | One generated grid "sheet" → N individual element files |

All `<skill-dir>/...` paths below are relative to **this skill's own directory**
(its absolute path is in the location header injected when the skill loads), not
the user's working directory. There is no persistent CWD across terminal calls —
pass absolute paths for `--output` and inputs.

## Preflight

1. **Install deps once** (no PEP 723 inline metadata here, so `uv run` will not
   auto-install): `uv pip install -r <skill-dir>/requirements.txt` — or `pip
   install -r <skill-dir>/requirements.txt`. Web search + most AI backends need
   only `requests`; slicing/rotation need `Pillow`/`numpy`.
2. **Configure a backend** for AI generation. Set `IMAGE_BACKEND` and the
   provider's own key (e.g. `OPENAI_API_KEY`) in the process environment, or in a
   `.env` file — see `<skill-dir>/.env.example` for every provider's variables
   and the `.env` lookup order. `scripts/image_gen.py --list-backends` prints
   what's available. Web search (`image_search.py`) needs no key for CC0/public-
   domain providers; Pexels/Pixabay use their own keys if you want those sources.

### First AI-generation run — backend setup gate (do NOT skip)

Before the **first** AI generation of a session (a positional `image_gen.py` call
or `image_gen.py --manifest`), check whether an AI backend is configured, and if
not, **guide the user proactively instead of running the script and dumping its
error**:

1. **Detect config.** Is `IMAGE_BACKEND` set in the environment, or present in a
   `.env` on the lookup path (`./.env`, `<skill-dir>/.env`, `<repo>/.env`,
   `~/.ppt-master/.env`)? If yes — and its provider key is present — **proceed
   silently**, ask nothing.
2. **If no backend is configured, stop and ask the user** (in the TUI/web use a
   question prompt; in an IM channel ask in plain language) to pick one of:
   - **Configure an AI backend now** — recommend a **CORE** backend
     (`openai` / `gemini` / `qwen` / `volcengine` / `zhipu`). Tell them exactly
     which env vars that backend needs (from `--list-backends` / `.env.example`),
     take the key, and **write it for them** to `./.env` (project-local) or
     `~/.ppt-master/.env` (user-level) — e.g. `IMAGE_BACKEND=openai` +
     `OPENAI_API_KEY=…`. Never echo the key back in chat, and never commit `.env`.
   - **Skip AI, use keyless web search instead** — run `image_search.py`
     (Openverse / Wikimedia are CC0 / public-domain, no key needed). Good when the
     user just wants real photos or has no API key.
   - **Use the host's native image tool (Path B)** — if the host (Claude Code /
     Codex / etc.) has its own image-generation tool, generate directly from the
     prompts in `image_prompts.json` and save to `images/<filename>`, skipping
     this script entirely. See `references/image-generator.md` §7 Path B.
3. **When invoked by another skill** that already confirmed the image source:
   honor it — if the caller confirmed `web` or host-native, do **not** prompt for
   an AI backend; only run this gate when AI generation is actually about to run
   with nothing configured.

Once a backend is configured this gate never fires again for the session.

## Two ways to drive it

### One-off — a single image right now

```bash
# AI generation
python3 <skill-dir>/scripts/image_gen.py "a serene alpine lake at dawn, soft mist, painterly" \
  --aspect_ratio 16:9 --image_size 2K --output /abs/out/dir --filename hero

# Web search (openly-licensed, downloads one best match + records the source)
python3 <skill-dir>/scripts/image_search.py "diverse engineering team in a modern office" \
  --orientation landscape --output /abs/out/team.jpg
```

The positional-prompt form skips the manifest and leaves no audit trail — reserve
it for quick fixups and standalone requests.

### Batch — an `image_prompts.json` manifest (audit trail)

The manifest is the shared contract when a caller has many images and wants a
written-back status per item. Write it, then run generation; the CLI runs every
`Pending`/`Failed` item and writes `Generated` / `Failed` / `Needs-Manual` back
into the same file as each completes.

```jsonc
{
  "project": "my-deck",
  "deck_rendering": "vector-illustration",   // one rendering shared by all items
  "deck_palette": "cool-corporate",          // one palette shared by all items
  "color_scheme": { "primary": "#1E3A5F", "secondary": "#F8F9FA", "accent": "#D4AF37" },
  "items": [
    { "filename": "cover.png", "prompt": "...", "aspect_ratio": "16:9",
      "image_size": "2K", "page_role": "hero_page", "text_policy": "none",
      "status": "Pending" }
  ]
}
```

```bash
# Render the read-only Markdown sidecar for review (no network):
python3 <skill-dir>/scripts/image_gen.py --render-md /abs/images/image_prompts.json
# Generate every Pending/Failed item in parallel, writing status back:
python3 <skill-dir>/scripts/image_gen.py --manifest /abs/images/image_prompts.json
```

Full field reference (`page_role`, `text_policy`, `type`, `slice_grid`/
`slice_names`, back-compat) is in `references/image-generator.md` §6.

### Slice a generated sheet into elements

When several small spot illustrations should share one coherent style, generate
one grid sheet (a single AI item), then cut it:

```bash
python3 <skill-dir>/scripts/slice_images.py /abs/images/spot_sheet.png \
  --grid 2x3 --names icon_a icon_b icon_c icon_d icon_e icon_f --trim --alpha
```

`--trim` tight-crops each cell to its content; `--alpha` knocks out the flat
background to transparency. Geometry rules: `references/image-generator.md` §4.3.

## Prompt quality — consult the craft library before writing an AI prompt

The quality of an AI-generated image is set almost entirely by the prompt. Before
authoring or repairing any `ai` prompt, **read the prompt-craft library** — a
distilled checklist plus a 160-prompt exemplar atlas (adapted from the MIT-licensed
[GPT-Image2-Skill](https://github.com/wuyoscar/GPT-Image2-Skill), tuned for
`gpt-image-2` but the principles carry across backends):

1. **`references/prompt-craft/craft.md`** — the 18-point checklist: put exact text in
   quotes, declare canvas/aspect/layout before subject, JSON/config-style prompts,
   fixed-region schemas for infographics, diagram grammar for data figures, UI-as-spec,
   multi-panel consistency, camera context for photorealism, scene density over
   adjectives, bounded style anchors, material/lighting/palette as separate controls,
   edit-endpoint invariants, dense Chinese/multilingual layouts. Load it whenever a
   prompt involves readable text, diagrams/data, UI, multi-panel layouts, or is weak.
2. **`references/prompt-craft/gallery.md`** — routing index to per-category exemplar
   prompts (`gallery-*.md`). Find the closest category, read 3–8 nearby `**Prompt**`
   entries, and remix rather than writing from scratch. (Preview PNGs aren't bundled;
   the prompt text is what matters.)
3. **`references/prompt-craft/openai-cookbook.md`** — official `gpt-image` API/model
   parameter semantics; load for capability or parameter questions.

This lifts output quality across every backend; it is not gpt-image-only.

## References — load on demand

| Need | Read |
|---|---|
| **Prompt-craft checklist (read before writing any AI prompt)** | `references/prompt-craft/craft.md` |
| **Exemplar prompt atlas by category** | `references/prompt-craft/gallery.md` → `gallery-<category>.md` |
| Common framework: resource-list format, path dispatch, status enum | `references/image-base.md` |
| AI path: prompt assembly, page roles, sheet/slice geometry, manifest schema, path selection | `references/image-generator.md` |
| Web path: license tiers, provider selection, attribution, `--strict-no-attribution` | `references/image-searcher.md` |
| Palette vocabulary (color behavior for generated images) | `references/image-palettes/_index.md` |
| Rendering styles (flat, watercolor, 3d-isometric, …) | `references/image-renderings/_index.md` |
| Composition types (infographic, flowchart, framework, …) | `references/image-type-templates/_index.md` |

Lazy-load only what the job needs: an all-search job never opens
`image-generator.md`, and an all-generate job never opens `image-searcher.md`.

## Being invoked by another skill

A caller (such as `ppt-master`) hands off by writing an `image_prompts.json`
manifest into its own project and asking this skill to run it. Read the manifest,
run the path each item declares (`image_gen.py --manifest` for `ai`,
`image_search.py` for `web`, `slice_images.py` for `slice`), and the status
written back into the manifest is the caller's signal that the files are ready.
Honor a caller's confirmed generation path — a manifest existing does **not** by
itself mean `--manifest` should run (it is the AI-API path only); see
`references/image-generator.md` §7.
