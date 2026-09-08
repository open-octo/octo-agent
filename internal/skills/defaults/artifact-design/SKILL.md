---
name: artifact-design
description: Design guidance for any HTML/Markdown file shown in octo's Artifacts panel — reports, dashboards, architecture/system diagrams, generated UIs, slide-style pages, 3D scenes. Read this BEFORE writing the file, not after — it calibrates how much design effort the request warrants and covers the panel's real constraints (the page runs on its own origin and can reference files beside it, external resources gated to an allowlist of CDN hosts, narrow default width, no live theme push). Use when the user asks to "画架构图" / "generate a diagram" / "make a dashboard" / "produce a report page" / "visualize this as a page" / build any artifact meant to be looked at rather than edited. If the page contains a chart, graph, plot, heatmap, or stat tile, also read references/charts.md — chart-type selection, color systems, legend/axis/tooltip conventions.
---

# Artifact design

An artifact is any `.html`/`.htm`/`.md`/`.markdown`/`.png`/`.jpg`/`.jpeg`/`.gif`/`.svg`/`.webp`
file the agent produces. Writing one through `write_file`/`edit_file` surfaces it
automatically in the web UI's Artifacts panel; a file built some other way (a
script, a build step, a download) needs one `show_artifact` call with its
absolute path. This skill is about what to put *inside* the HTML — read it
before writing the first line.

If the page contains any chart, graph, plot, heatmap, sparkline, or stat
tile, read `references/charts.md` before writing the chart — chart-type
selection, the color system (with a validated default palette in
`references/palette.md`), legend/axis/tooltip conventions, and chart
legibility at the panel's docked width.

## How the panel actually works — design within these constraints

- **The page runs on its own origin — a real one, not the app's.** HTML
  renders from `http://<token>.artifacts.localhost:<port>/` inside a frame, so
  everything a normal web page can do locally works: `localStorage` and
  IndexedDB persist, `<a download>` saves a file, `requestFullscreen()` and
  pointer lock work, WebGL and Web Audio work. Its network is fenced by a
  Content-Security-Policy: the page may load from and talk to its own origin
  and the allowlisted CDN hosts below, and nothing else — no `fetch` to other
  APIs, no images or media from other hosts, no reaching octo's `/api`. Data
  the page needs must be in the page or in a file beside it; do not write code
  that calls external services or octo's API from inside the page.
- **Files beside the page load by relative path.** `<script src="./app.js">`,
  `<link href="./style.css">`, `<img src="./chart.png">`,
  `loader.load('./model.glb')`, fonts, audio and video in the same directory
  (or a subdirectory) are served with the page. Only page-asset types are:
  images, `.css`/`.js`/`.mjs`/`.json`/`.wasm`/`.csv`/`.txt`/`.xml`, `.glb`/
  `.gltf`/`.bin`/`.obj`/`.mtl`/`.hdr`, `.woff`/`.woff2`/`.ttf`/`.otf`,
  `.mp3`/`.wav`/`.ogg`/`.mp4`/`.webm`. A second `.html` is not — one entry
  page per artifact. A single file is still the simplest artifact; split into
  sibling files when the page has a real script or a binary asset (a model, a
  font, a recording) that would be absurd to inline.
- **External references are allowlist-gated, and the allowlist is enforced.**
  A `<script src=…>` / `<link rel="stylesheet" href=…>` pointing at another
  host may only use these CDN hosts; anything else is stripped before
  rendering, under a banner saying how many were removed:
  `cdnjs.cloudflare.com`, `cdn.jsdelivr.net`, `unpkg.com`,
  `fonts.googleapis.com`, `fonts.gstatic.com`, and the mainland-China mirrors
  `cdn.bootcdn.net`, `cdn.staticfile.org`, `cdn.staticfile.net`,
  `registry.npmmirror.com`. Pin exact versions; if the user is in mainland
  China, prefer the CN mirrors. Reach for a CDN only when the page needs a
  real library (React, ECharts, Chart.js, three.js, …) — a page that depends
  on one shows nothing when that host is unreachable. Relative references are
  not external and are never stripped.
- **The default viewport is narrow.** The panel is a **420px-wide docked
  sidebar** by default; the user can maximize it to `min(900px, 75vw)`, but
  don't design for that as the common case. Build the layout to read cleanly
  at ~380–420px first, then let it use extra space gracefully above that —
  not the other way around. This is the opposite of most artifact platforms,
  where the canvas starts wide. Multi-column layouts, wide tables, and
  side-by-side diagram lanes need an explicit `@media (max-width: 720px)` (or
  tighter) fallback to a single column, or they'll clip or force horizontal
  scroll in the default view.
- **Theme support is one-directional.** Only `@media (prefers-color-scheme:
  dark)` is a live signal in octo today — the panel does not push its own
  light/dark toggle state into the iframe (unlike some other artifact
  hosts). Still write `:root[data-theme="dark"]` / `:root[data-theme="light"]`
  overrides alongside the media query — they're free, harmless if unused, and
  correct if that wiring ever lands — but don't rely on them being live; the
  media query is what actually renders today for most users.
- **Update in place, not by versioning.** Re-running `write_file`/`edit_file`
  against the *same absolute path* updates that same panel entry rather than
  creating a new one. If you're iterating on a diagram, keep writing the same
  file.
- **No title/gallery metadata to set.** The panel derives the display name
  from the file's basename and its type label from the extension — there is
  no favicon or description field to populate. A `<title>` tag is harmless
  but cosmetically inert here.
- **Markdown gets code-block styling for free** (the panel inlines a
  highlight.js theme for `.md` previews) — don't hand-roll code-fence CSS in
  a Markdown artifact; that's only a concern for HTML artifacts.

## Calibrate effort to the ask

Don't build a dashboard when a status note was asked for, and don't ship a
bare unstyled div when the user asked for something they'll actually look at
and share. Match investment to what's being requested:

- A one-off answer, a small table, a short report → a clean, readable page.
  Spend your effort on typography and spacing, not on custom components.
- A named artifact meant to be referred back to (an architecture diagram, a
  dashboard, a generated tool UI) → invest in layout structure, a real color
  system, and responsive behavior — this is the case the rest of this skill
  is written for.

## Before you write

Before calling `write_file`/`show_artifact`, confirm:

- [ ] Every `<script src>` / `<link rel="stylesheet" href>` is either a
      relative path to a file you also wrote beside the page, or an
      allowlisted CDN host (see above) with a pinned version
- [ ] Every relative reference names a file that really exists in the page's
      directory, with an asset extension from the list above
- [ ] Web fonts only from `fonts.googleapis.com` or a `.woff2` beside the
      page, always with a system-stack fallback: `-apple-system,
      BlinkMacSystemFont, "Segoe UI", system-ui, sans-serif` — or skip the web
      font and use the stack alone
- [ ] Any image is a file beside the page or a `data:` URI, never a network
      URL at the network's mercy
- [ ] `@media (prefers-color-scheme: dark)` covers every color used, and every
      color has a light-mode default that isn't just "assume light"
- [ ] The narrowest layout (~380px) has no fixed-pixel widths wider than the
      viewport and no unintended horizontal scroll on the page body — wrap
      any table/code block that must be wide in its own
      `overflow-x: auto` container instead

## The box-and-arrow diagram pattern

For architecture/system/flow diagrams, hand-written CSS beats reaching for a
charting or graph-layout library — you get exact visual control, real theme
support, and no library to inline. This is the same technique behind
well-made "layered boxes with a few connectors" diagrams:

- **Zones** — a `<div>` per architectural layer/boundary, colored via a CSS
  variable per zone (`--accent`, `--serve`, `--agent`, …), laid out with
  `display:flex`/`grid`, not absolute positioning
- **Cards** inside a zone — one per component, a title + one or two lines of
  description, not a paragraph
- **Connectors** — Unicode arrow glyphs (`↕ ↓ ↑ → ←`) centered in their own
  small `<div>`, not actual line-drawing; this keeps everything reflow-safe
  when the panel width changes, which real SVG connectors are not
- **Legend** — a row of colored dots (`<span class="dot">` with
  `background: var(--accent)`) mapped 1:1 to the zone colors, so readers
  decode color without following a line
- **Numbered steps** — `<ol>` with CSS counters
  (`counter-increment`/`content: counter(s)`) rendered as a small filled
  circle, cheaper and crisper than an actual numbered-badge image

Skeleton:

```html
<style>
  :root { --bg:#fafaf9; --ink:#1c1917; --line:#d6d3d1; --accent:#2563eb; --accent-soft:#eff6ff; }
  @media (prefers-color-scheme: dark) {
    :root { --bg:#0c0a09; --ink:#f5f5f4; --line:#292524; --accent:#60a5fa; --accent-soft:#172033; }
  }
  :root[data-theme="dark"] { --bg:#0c0a09; --ink:#f5f5f4; --line:#292524; --accent:#60a5fa; --accent-soft:#172033; }
  :root[data-theme="light"] { --bg:#fafaf9; --ink:#1c1917; --line:#d6d3d1; --accent:#2563eb; --accent-soft:#eff6ff; }
  * { box-sizing: border-box; }
  body { margin:0; background:var(--bg); color:var(--ink); font:14px/1.5 -apple-system,BlinkMacSystemFont,sans-serif; }
  .wrap { padding: 20px 16px; }
  .zone { background:var(--accent-soft); border:1px solid var(--line); border-radius:12px; padding:14px; }
  .cards { display:grid; gap:10px; }
  .card { background:var(--bg); border:1px solid var(--line); border-radius:8px; padding:10px 12px; }
  .connector { text-align:center; color:var(--line); font-size:20px; margin:6px 0; }
  @media (min-width: 640px) { .cards.two { grid-template-columns: 1fr 1fr; } }
</style>
<div class="wrap">
  <div class="zone">
    <div class="cards">
      <div class="card"><b>Component</b><br><span style="color:var(--line)">one line of description</span></div>
    </div>
  </div>
  <div class="connector">↓</div>
</div>
```

Reach for real SVG or an inlined graph library only when the diagram has
many interconnected nodes needing automatic layout, or edges that genuinely
cross at arbitrary points — most system/architecture diagrams are layered
boxes and don't need that.
