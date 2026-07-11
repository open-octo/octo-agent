# SVG Icon Library — not bundled with the octo default skill

Upstream ppt-master ships **11,600+ SVG icons** across five libraries in this
directory. The octo default skill **omits them** (≈10 MB / 11k+ files would
bloat the embedded binary), so this directory arrives empty except for this
note. Everything else about icons still works — you just have to populate the
library once, or supply your own icons per project.

| Library | Style | viewBox | `data-icon` prefix |
|---------|-------|---------|--------------------|
| `chunk-filled` | fill · straight-line geometry (sharp, rectilinear) | `0 0 16 16` | `chunk-filled/` |
| `tabler-filled` | fill · bezier curves (smooth, rounded) | `0 0 24 24` | `tabler-filled/` |
| `tabler-outline` | stroke / line art | `0 0 24 24` | `tabler-outline/` |
| `phosphor-duotone` | duotone (color + 20%-opacity backplate) | `0 0 256 256` | `phosphor-duotone/` |
| `simple-icons` | brand logos (single-color marks) | `0 0 24 24` | `simple-icons/` |

## Populate the full library (one-time)

Fetch this directory from upstream and drop the per-library folders in here:

```bash
# Option A — sparse-clone just the icon set from the source repo:
git clone --filter=blob:none --no-checkout https://github.com/hugohe3/ppt-master.git /tmp/ppt-src
git -C /tmp/ppt-src sparse-checkout set skills/ppt-master/templates/icons
git -C /tmp/ppt-src checkout
# then copy into this directory (the one holding this README):
cp -R /tmp/ppt-src/skills/ppt-master/templates/icons/. <ppt-master-skill-dir>/templates/icons/

# Option B — install upstream ppt-master as a skill and copy its icons dir:
#   octo skills add hugohe3/ppt-master/skills/ppt-master
#   then copy that skill's templates/icons/* into here.
```

After populating, the layout is `templates/icons/<library>/<name>.svg`, and
`ls templates/icons/<library>/ | grep <keyword>` finds icons on demand.

## Without the full library — custom icons still work

`scripts/icon_sync.py` copies chosen icons from this global directory into a
project's own `<project>/icons/<lib>/`, and **`finalize_svg.py embed-icons`
resolves project-first**. So you can skip the global library entirely and drop
your own `.svg` files into `<project>/icons/<lib>/` (any `<lib>`, e.g.
`custom/`), then reference them as `data-icon="<lib>/<name>"`. A name already
present in the project counts as satisfied — `icon_sync.py` only reports names
it cannot find in either the project or this (possibly empty) global library.

## Usage (unchanged)

Placeholder syntax during SVG generation:

```xml
<use data-icon="tabler-outline/home" x="100" y="200" width="48" height="48" fill="#005587"/>
```

- `data-icon` — `<library>/<icon-name>` (filename without `.svg`)
- `x` / `y` — position; `width` / `height` — size (32–48px recommended); `fill` — color

**One presentation = one stylistic library** for generic icons (`chunk-filled` /
`tabler-filled` / `tabler-outline` / `phosphor-duotone` — never mix). `simple-icons`
is the sole exception, used alongside the chosen library for real brand marks only.
Full selection guidance lives in `references/executor-base.md` §4.
