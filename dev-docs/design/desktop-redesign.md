# Desktop redesign — design reference

All 8 implementation phases below shipped and merged; this doc is kept for the
design tokens and scope decisions. The original interactive design mock
(`desktop-redesign-mock.html`, covering all 12 views, the settings modal, the
endpoint/model modals, onboarding, both platforms, and both agent states) has
been removed now that the real implementation supersedes it.

## Design tokens

The mock is light-only. The dark column is our derived mapping (macOS dark-mode
conventions), approved 2026-07-31. `web/src/app.css` is the source of truth in
code; this table records the intent.

| Token (app.css)      | Light                 | Dark                   |
| -------------------- | --------------------- | ---------------------- |
| window / layout bg   | `#F5F5F7`             | `#1E1E20`              |
| card / container bg  | `#FFFFFF`             | `#2C2C2E`              |
| accent               | `#007AFF`             | `#0A84FF`              |
| accent subtle bg     | `rgba(0,122,255,.10)` | `rgba(10,132,255,.18)` |
| text primary         | `#1D1D1F`             | `#F5F5F7`              |
| text secondary       | `#86868B`             | `#98989D`              |
| text tertiary        | `#A9A9AF`             | `#636366`              |
| border               | `rgba(0,0,0,.08)`     | `rgba(255,255,255,.10)`|
| border secondary     | `rgba(0,0,0,.05)`     | `rgba(255,255,255,.06)`|
| hover fill           | black 4–6%            | white 6–8%             |
| success              | `#34C759` / `#248a3d` text | `#30D158`         |
| warning              | `#FF9500` / `#b25e00` text | `#FF9F0A`         |
| error                | `#FF3B30` / `#c9302c` text | `#FF453A`         |
| sidebar frost        | `rgba(245,245,247,.6)` + blur | `rgba(34,34,36,.6)` + blur |
| panel frost (artifact) | `rgba(255,255,255,.7)` + blur(20px) | `rgba(36,36,38,.72)` + blur(20px) |
| font stack           | SF Pro (`-apple-system, BlinkMacSystemFont, "SF Pro Text", …`) | same |
| card radius          | 14px                  | same                   |

Hardcoded colors in the mock that must be tokenized when implementing (found
during the dark derivation): inline-code text `rgb(58,58,60)`, icon-well bg
`#f0f0f2`, hover surface `#fbfbfd`, artifact callout text `rgb(28,78,143)`.

## Scope decisions (2026-07-31)

- Dark theme ships together with the redesign; every new style goes through tokens.
- Settings moves from a full-page view to a modal (category rail: general /
  endpoints / connect / agent defaults / mobile / about).
- Multi-backend switcher: **not built** (mock shows it gated behind a settings
  toggle; we don't render it at all).
- Artifact diff view: **not built** (preview tab only).
- Chat export: **will be built** (the only net-new feature kept from the mock).

## Implementation phases

0. Token foundation + dark mapping + components-gallery view (token acceptance page)
1. Window shell: remove 56px header → full-height sidebar + top-right toolbelt +
   mac/win window controls; 3 columns with draggable right panel
2. Sidebar rebuild (pinned/grouped/ungrouped sessions, config & data nav sections)
3. Chat view: slim chat header (compact/clear/artifacts/export), message avatars +
   meta, reasoning fold, tool cards
4. Composer: compact two-row card + @agent picker + endpoint-grouped model menu +
   permission-mode menu (keep stop button, goal chip, attachments, slash menu)
5. Artifact panel restyle (copy/export/open-in-editor)
6. Secondary views reskin (10 views, page primitives)
7. Settings modal + onboarding redesign
8. Chat export
