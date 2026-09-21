# Themes (Web UI palettes)

A theme is `~/.octo/themes/<id>/` holding `manifest.json` + `theme.css` (plus any assets the CSS
references). No build step: write the files, reload the Web UI, pick it under **Settings → Theme**.
Deleting the folder uninstalls; anyone still on that theme falls back to the default pack.

- `id` must equal the folder name (lowercase letters, digits, hyphens) and must not be `azure`
  (the default pack inside the app).
- `manifest.json`: `{"id", "name", "author"?, "homepage"?, "names"?: {"zh": "…"}, "swatch"?: ["#accent", "#surface"]}`.
  `swatch` feeds the picker chip and is hex-only — anything else is dropped.
- octo's built-in themes (Ocean, Blossom, Vogue) are seeded into the same directory on first run —
  same format, same API. Copy one, rename folder + `id`, and edit. User edits survive upgrades.

## theme.css — always TWO blocks

Colours are CSS custom properties keyed off two `<html>` attributes: `data-theme` (`light`|`dark`)
and `data-theme-pack` (the palette id, absent for the default pack):

```css
:root[data-theme-pack="ocean"] { /* light values */ }
:root[data-theme-pack="ocean"][data-theme="dark"] { /* dark values */ }
```

The default dark palette has the **same specificity** as your light block, and yours is injected
afterwards — so a light-only theme leaks into dark mode and renders unreadable. Whatever you
redefine for light, redefine for dark.

## Traps that fail silently

- `--chat-bg` / `--chat-bg-image` are **background-image layers**, not colours — use a
  `linear-gradient(...)`; a bare `#hex` does nothing.
- Asset URLs must be **absolute API paths** — `url('/api/themes/<id>/wallpaper.webp')`, never a
  relative `url()`: relative resolves where the property is *used* (the app's own bundle), not
  where it is declared, and quietly 404s. Same for `@font-face` sources. Servable types:
  `.css .webp .png .jpg .jpeg .gif .avif .woff .woff2 .ttf .otf` — **no SVG** (carries script;
  these files are served from the app's own origin).
- Set `--on-accent` (text/icons on accent fill) in every theme — white over a light or mid-tone
  accent fails WCAG AA; check 4.5:1 in both modes.
- `--font-zoom` is written by the font-size setting; do not set it.

## Variable map (63 total — redefine any subset, the rest inherit the default pack)

The families that make a theme look like yours:

- **Accent**: `--blue-1 --blue-2 --blue-5 --blue-6 --blue-7` (`--blue-6` is the main accent)
- **Text**: `--text --text-secondary --text-tertiary --text-quaternary --text-heading`
- **Backgrounds**: `--bg-layout --bg-container --bg-sidebar --bg-table-header --bg-zebra`
- **Borders**: `--border --border-secondary --border-table`
- **Semantic**: `--success/--warning/--error` each with `-bg -border -text` (plus `--error-dark`);
  `--info-bg --info-border --info-text` — note `--info-*` belongs to the **accent** family in the
  shipped packs: tint your accent, tint these too
- **Interaction**: `--hover-neutral --row-hover --active-blue-bg --focus-ring --scrim`
- **Chat surface**: `--chat-bg --chat-bg-image`; **on-accent**: `--on-accent`
- **Radius**: `--radius-pill --radius-card --radius-modal --radius-sm --radius-xs`
- **Chrome**: `--sidebar-frost --panel-frost --titlebar-frost --frost-blur` (opaque/flat by
  default; translucent frost + blur for a glassy shell)
- **Misc**: `--card-shadow`, `--terminal-bg/--terminal-text` (intentionally dark in BOTH modes),
  `--surface-info`, `--control-track`, `--search-bg/--search-hover`, `--scrollbar-thumb`,
  `--placeholder`, `--font-mono/--font-sans`

## Dev loop

Edit `theme.css` → reload the page → **Settings**: the **Theme** row picks the palette family, the
**Appearance** row picks light/dark within it — check both. Theme not listed? Folder name ≠ `id`,
or `manifest.json` doesn't parse.

Full guide (worked minimal theme, publish checklist):
**https://octo-agent.dev/docs/guides/themes/**
