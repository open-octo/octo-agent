// Turns ```mermaid fences in rendered chat markdown into diagrams.
//
// markdown.ts renders a closed mermaid fence as an ordinary code block tagged
// `mermaid-block`; setupMermaid() then swaps a diagram in beside the source
// once the block is in the DOM. The source stays in place (hidden) so the
// block's Copy button still copies it, and any block that fails to render is
// simply left as the code block it already is — a model writing invalid
// diagram syntax never blanks the reply.
//
// The SVG reaches the document through innerHTML, so it is paid for twice:
//   1. mermaid runs with securityLevel: 'strict', which sanitizes the labels
//      it renders, and startOnLoad: false so nothing auto-executes against
//      the surrounding document.
//   2. The SVG it produces is then run through DOMPurify under an SVG profile
//      before insertion — our policy, not only mermaid's.
//
// mermaid is imported dynamically: it is by far the heaviest thing the
// frontend can pull in, and a session that never shows a diagram should never
// parse it.
import DOMPurify from 'dompurify'

export const MERMAID_BLOCK_CLASS = 'mermaid-block'
const RENDERED_ATTR = 'data-mermaid-rendered'

// mermaid.render(id, …) builds a temporary DOM node under that id and writes
// it into both the SVG's `id` and the `#id …` selectors of the <style> it
// embeds, so two diagrams rendered under the same id would cross-contaminate
// each other's styles. Module-level so every diagram on the page gets its own.
let seq = 0

// Keyed by theme + source. A streamed reply re-renders its markdown every few
// dozen milliseconds, and each pass replaces the block's DOM; a cached SVG is
// swapped back in synchronously, so the diagram doesn't flicker or re-run.
// null records a source mermaid rejected, so it isn't retried on every pass.
const cache = new Map<string, string | null>()
const inflight = new Map<string, Promise<string | null>>()

function currentTheme(): 'dark' | 'default' {
  return document.documentElement.getAttribute('data-theme') === 'dark' ? 'dark' : 'default'
}

// mermaid.initialize() sets global config, and render() reads it while it
// works, so a render with one theme must not overlap one with another — a
// theme switch mid-render would otherwise cache a diagram under the wrong key.
let queue: Promise<unknown> = Promise.resolve()

// Resolves to null when mermaid rejects the source; rejects when mermaid
// itself can't be loaded, which says nothing about the source.
async function renderSvg(code: string, theme: 'dark' | 'default'): Promise<string | null> {
  const { default: mermaid } = await import('mermaid')
  const run = queue.then(async () => {
    mermaid.initialize({
      startOnLoad: false,
      securityLevel: 'strict',
      theme,
      // Without this, flowchart labels render inside a foreignObject and the
      // SVG-only sanitize below drops the element *and its contents*
      // (foreignObject is in DOMPurify's svgDisallowed AND its
      // FORBID_CONTENTS), leaving a diagram of empty boxes and arrows — no
      // error, just silently wordless. Off, mermaid emits a native SVG text
      // element instead, which survives sanitizing untouched.
      // securityLevel does NOT imply this: getEffectiveHtmlLabels() defaults
      // it to true independently.
      htmlLabels: false,
      // On a syntax error mermaid otherwise draws its own error diagram and
      // leaves it attached to document.body, below the whole app.
      suppressErrorRendering: true,
      // themeCSS/themeVariables are settable from inside the diagram source
      // via a %%{init: …}%% directive and are NOT in mermaid's own `secure`
      // list, so strict mode does not cover them. They end up in a style
      // element that survives sanitizing, which would let a diagram pull an
      // external url(). `theme` is listed so a directive can't override the
      // one matching the app.
      secure: ['secure', 'securityLevel', 'startOnLoad', 'maxTextSize', 'suppressErrorRendering', 'maxEdges', 'htmlLabels', 'theme', 'themeCSS', 'themeVariables'],
    })
    try {
      const { svg } = await mermaid.render(`md-mermaid-${seq++}`, code)
      return DOMPurify.sanitize(svg, { USE_PROFILES: { svg: true, svgFilters: true } })
    } catch (err) {
      console.warn('mermaid: diagram not rendered:', err)
      return null
    }
  })
  // A rejected link would skip every later render queued behind it.
  queue = run.catch(() => {})
  return run
}

function svgFor(code: string, theme: 'dark' | 'default'): Promise<string | null> {
  const key = `${theme}\x00${code}`
  if (cache.has(key)) return Promise.resolve(cache.get(key) ?? null)
  let p = inflight.get(key)
  if (!p) {
    p = renderSvg(code, theme).then(
      (svg) => {
        cache.set(key, svg)
        inflight.delete(key)
        return svg
      },
      (err) => {
        // Not cached: a chunk that failed to load (say, after an upgrade
        // replaced it) may load on a later attempt.
        console.warn('mermaid: failed to load:', err)
        inflight.delete(key)
        return null
      },
    )
    inflight.set(key, p)
  }
  return p
}

function apply(block: HTMLElement, svg: string) {
  if (!block.isConnected) return
  attach(block, svg)
}

function attach(block: HTMLElement, svg: string) {
  if (block.hasAttribute(RENDERED_ATTR)) return
  const diagram = document.createElement('div')
  diagram.className = 'mermaid-diagram'
  diagram.innerHTML = svg
  block.appendChild(diagram)
  block.setAttribute(RENDERED_ATTR, '')
}

function hydrate(root: HTMLElement) {
  const blocks = root.querySelectorAll<HTMLElement>(`.${MERMAID_BLOCK_CLASS}:not([${RENDERED_ATTR}])`)
  if (blocks.length === 0) return
  const theme = currentTheme()
  for (const block of blocks) {
    const code = block.querySelector('pre code')?.textContent ?? ''
    if (!code.trim()) continue
    const key = `${theme}\x00${code}`
    if (cache.has(key)) {
      const svg = cache.get(key)
      if (svg) apply(block, svg)
      continue
    }
    void svgFor(code, theme).then((svg) => {
      if (svg) apply(block, svg)
    })
  }
}

/** Svelte action for a container whose {@html} markdown may hold mermaid
 * blocks. Watches for re-rendered content, since {@html} replaces the nodes
 * on every change. */
export function setupMermaid(el: HTMLElement): { destroy: () => void } {
  hydrate(el)
  const obs = new MutationObserver(() => hydrate(el))
  obs.observe(el, { childList: true, subtree: true })
  // A finished message never re-renders, so a theme switch has to redraw its
  // diagrams itself: mermaid's light lines vanish on a dark background and
  // the reverse.
  const themeObs = new MutationObserver(() => {
    for (const block of el.querySelectorAll<HTMLElement>(`[${RENDERED_ATTR}]`)) {
      block.querySelector('.mermaid-diagram')?.remove()
      block.removeAttribute(RENDERED_ATTR)
    }
    hydrate(el)
  })
  themeObs.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] })
  return {
    destroy: () => {
      obs.disconnect()
      themeObs.disconnect()
    },
  }
}

/** Draws the mermaid blocks in a rendered-markdown string and returns the
 * string with each diagram in place — for a document that renders somewhere
 * no action can reach, such as a Markdown artifact's sandboxed srcdoc frame.
 * The SVG is produced here, in the app, so the frame needs no script. */
export async function renderMermaidBlocks(html: string): Promise<string> {
  if (!html.includes(MERMAID_BLOCK_CLASS)) return html
  const tpl = document.createElement('template')
  tpl.innerHTML = html
  const blocks = tpl.content.querySelectorAll<HTMLElement>(`.${MERMAID_BLOCK_CLASS}`)
  const theme = currentTheme()
  await Promise.all(Array.from(blocks, async (block) => {
    const code = block.querySelector('pre code')?.textContent ?? ''
    if (!code.trim()) return
    const svg = await svgFor(code, theme)
    if (svg) attach(block, svg)
  }))
  return tpl.innerHTML
}

/** Test seam. */
export function resetMermaidCache(): void {
  cache.clear()
  inflight.clear()
  queue = Promise.resolve()
  seq = 0
}
