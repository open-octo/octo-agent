import { describe, it, expect, vi } from 'vitest'
import { marked } from 'marked'
import { renderMarkdown } from './markdown'

describe('renderMarkdown: blockquote contents', () => {
  it('renders bold inside a quote instead of leaking asterisks', () => {
    const out = renderMarkdown('> quoted **bold**')
    expect(out).toContain('<blockquote')
    expect(out).toContain('<strong>bold</strong>')
    expect(out).not.toContain('**bold**')
  })

  it('renders inline code inside a quote', () => {
    const out = renderMarkdown('> use `npm ci` first')
    expect(out).toContain('<code>npm ci</code>')
    expect(out).not.toContain('`npm ci`')
  })

  it('renders links inside a quote through the custom link renderer', () => {
    const out = renderMarkdown('> see [docs](https://example.com)')
    expect(out).toContain('href="https://example.com"')
    expect(out).toContain('>docs</a>')
  })

  it('keeps a multi-line quote as one quote with both lines rendered', () => {
    const out = renderMarkdown('> first **line**\n> second *line*')
    expect(out.match(/<blockquote/g)).toHaveLength(1)
    expect(out).toContain('<strong>line</strong>')
    expect(out).toContain('<em>line</em>')
  })

  it('still renders markdown outside a quote', () => {
    const out = renderMarkdown('Outside **bold** and `code`.')
    expect(out).toContain('<strong>bold</strong>')
    expect(out).toContain('<code>code</code>')
  })
})

describe('renderMarkdown: tables', () => {
  it('wraps the table in a .table-scroll container for horizontal scrolling', () => {
    const out = renderMarkdown('| A | B |\n|---|---|\n| x | 1 |')
    expect(out).toContain('<div class="table-scroll"><table>')
    expect(out).toContain('<thead>')
    expect(out).toContain('<tbody>')
  })

  it('renders header cells as th and data cells as td', () => {
    const out = renderMarkdown('| A | B |\n|---|---|\n| x | y |')
    expect(out).toContain('<th>A</th>')
    expect(out).toContain('<th>B</th>')
    expect(out).toContain('<td>x</td>')
    expect(out).toContain('<td>y</td>')
  })

  it('preserves inline markup inside table cells', () => {
    const out = renderMarkdown('| A |\n|---|\n| **bold** |')
    expect(out).toContain('<td><strong>bold</strong></td>')
  })

  it('preserves per-column alignment', () => {
    const out = renderMarkdown('| A | B |\n|:---|:---:|\n| x | y |')
    expect(out).toContain('<th align="left">A</th>')
    expect(out).toContain('<th align="center">B</th>')
  })

  it('wraps header cells in a tr (matching marked default output)', () => {
    const out = renderMarkdown('| A | B |\n|---|---|\n| x | y |')
    // The <th>s must sit inside a <tr> (marked emits newlines inside the row).
    expect(out).toMatch(/<thead><tr>\s*<th>A<\/th>\s*<th>B<\/th>\s*<\/tr>\s*<\/thead>/)
    expect(out).not.toMatch(/<thead>\s*<th\b/)
  })

  it('renders an escaped pipe as a literal single cell', () => {
    const out = renderMarkdown('| A |\n|---|\n| a\\|b |')
    expect(out).toContain('<td>a|b</td>')
    // The escaped pipe must not create a spurious second column.
    expect(out.match(/<td>/g)).toHaveLength(1)
  })
})

describe('renderMarkdown: link labels (#2088)', () => {
  it('renders bold inside a link label instead of leaking asterisks', () => {
    const out = renderMarkdown('See [**PR #123**](https://example.com)')
    expect(out).toContain('href="https://example.com"')
    expect(out).toContain('<strong>PR #123</strong>')
    expect(out).not.toContain('**')
  })

  it('renders inline code inside a link label instead of leaking backticks', () => {
    const out = renderMarkdown('Open [`foo.ts`](https://example.com/foo)')
    expect(out).toContain('<code>foo.ts</code>')
    expect(out).not.toContain('`')
  })

  it('sanitizes dangerous HTML in link labels', () => {
    // Inline HTML in the label flows through parseInline like everywhere else
    // in the document, so renderer.html escapes it into text: the tag never
    // becomes an element, and the onerror text that survives is inert (the
    // DOMPurify pass behind it stays as the second line of defence).
    const out = renderMarkdown('[a <img src=x onerror=alert(1)> c](https://example.com)')
    expect(out).not.toContain('<img')
    expect(out).toContain('&lt;img src=x onerror=alert(1)&gt;')
    const out2 = renderMarkdown('[x <script>alert(1)</script>](https://example.com)')
    expect(out2).not.toContain('<script>')
  })

  it('still blanks unsafe hrefs', () => {
    const out = renderMarkdown('[click](javascript:alert(1))')
    expect(out).not.toContain('javascript:')
    expect(out).toContain('>click</a>')
  })
})

describe('renderMarkdown: renderer installed once (#2089)', () => {
  it('does not re-wrap the global renderer methods on every render', () => {
    // marked.use() wraps already-installed renderer methods in a new closure
    // per call; installing per render grew an unbounded wrapper chain. With a
    // single module-scope install, the installed method references must stay
    // identical across renders.
    renderMarkdown('warm-up **render**')
    const installed = (marked.defaults as any).renderer
    const before = { code: installed.code, link: installed.link, blockquote: installed.blockquote, table: installed.table }
    for (let i = 0; i < 5; i++) renderMarkdown(`render pass ${i} with \`code\` and [a link](https://example.com)`)
    const after = (marked.defaults as any).renderer
    expect(after).toBe(installed)
    expect(after.code).toBe(before.code)
    expect(after.link).toBe(before.link)
    expect(after.blockquote).toBe(before.blockquote)
    expect(after.table).toBe(before.table)
  })
})

describe('renderMarkdown: raw HTML in reply text', () => {
  // A model wrote GitDiffModal.svelte and narrated it inline, without a code
  // fence. The component's own markup and <style> reached the chat DOM, where
  // the app's global CSS turned them into a live full-screen overlay.
  const modalSrc = [
    '写 GitDiffModal.svelte（全屏弹窗）。',
    '',
    '<div class="backdrop" role="presentation" onclick={close}>',
    '  <div class="modal" role="dialog" aria-modal="true" tabindex="-1">',
    "    <button class=\"btn\" onclick={() => act('revert')} disabled={busy}>Revert</button>",
    '  </div>',
    '</div>',
    '',
    '<style>.backdrop { position: fixed; inset: 0; z-index: 9999; }</style>',
  ].join('\n')

  it('escapes block-level HTML instead of building real nodes', () => {
    const out = renderMarkdown(modalSrc)
    // No element survives — only escaped text. Attribute-looking substrings
    // (role="dialog") are fine inside a text node; what must not appear is a
    // tag that opens one.
    expect(out).not.toContain('<div')
    expect(out).not.toContain('<button')
    expect(out).not.toContain('<style')
    expect(out).toContain('&lt;div class=')
    expect(out).toContain('&lt;style&gt;')
  })

  it('escapes HTML inside the overridden table and blockquote renderers', () => {
    // renderer.table / renderer.blockquote are this module's own overrides, so
    // they are the likeliest place for a future change to let markup back in.
    const table = renderMarkdown('| A |\n|---|\n| <div class="modal">x</div> |')
    expect(table).toContain('<table>')
    // The wrapper <div class="table-scroll"> is this renderer's own output; the
    // cell's markup is what must not have become an element.
    expect(table).not.toContain('<div class="modal"')
    expect(table).toContain('&lt;div class="modal"&gt;')

    const quote = renderMarkdown('> quoted <div class="backdrop">x</div>')
    expect(quote).toContain('<blockquote')
    expect(quote).not.toContain('<div class="backdrop"')
    expect(quote).toContain('&lt;div class=')
  })

  it('escapes HTML inside a think block', () => {
    // The same overlay payload can arrive in reasoning text, which is parsed by
    // a separate marked.parse call.
    const out = renderMarkdown('<think>trying <style>.backdrop{position:fixed}</style></think>done')
    expect(out).toContain('<details class="think-block"')
    expect(out).not.toContain('<style')
    expect(out).toContain('&lt;style&gt;')
  })

  it('escapes inline formatting tags too, by design', () => {
    // <br>/<kbd>/<details> are harmless on their own, but the rule is "no
    // element from reply text reaches the app's document" rather than a
    // per-tag allowlist. Pinned so it is a decision, not an oversight.
    const out = renderMarkdown('| A |\n|---|\n| one<br>two |')
    expect(out).not.toContain('<br>')
    expect(out).toContain('&lt;br&gt;')
  })

  it('escapes inline HTML too', () => {
    const out = renderMarkdown('the wrapper is <span class="chat-overlay">here</span> ok')
    expect(out).not.toContain('<span')
    expect(out).toContain('&lt;span class="chat-overlay"&gt;here&lt;/span&gt;')
  })

  it('keeps escaping tags DOMPurify would have allowed', () => {
    // <img>/<a> survive sanitization, so only the renderer-level escape keeps
    // them out of the DOM.
    const out = renderMarkdown('<img src="x"> and <a href="https://example.com">link</a>')
    expect(out).not.toContain('<img')
    expect(out).not.toContain('<a href=')
    expect(out).toContain('&lt;img src="x"&gt;')
  })

  it('still renders markdown-authored structure around escaped HTML', () => {
    const out = renderMarkdown('**bold** then <div>raw</div> then `code`')
    expect(out).toContain('<strong>bold</strong>')
    expect(out).toContain('<code>code</code>')
    expect(out).toContain('&lt;div&gt;raw&lt;/div&gt;')
  })

  it('leaves think blocks working — they are extracted before marked runs', () => {
    const out = renderMarkdown('<think>weighing **options**</think>answer')
    expect(out).toContain('<details class="think-block"')
    expect(out).toContain('<strong>options</strong>')
    expect(out).not.toContain('&lt;think&gt;')
  })

  it('renders fenced HTML as a code block, not as markup', () => {
    const out = renderMarkdown('```html\n<div class="modal">x</div>\n```')
    expect(out).toContain('class="code-block"')
    expect(out).not.toContain('<div class="modal">x</div>')
  })
})

describe('renderMarkdown: rawHtml opt-out', () => {
  // The markdown artifact preview renders into a sandboxed srcdoc iframe with
  // its own stylesheet, where a document's own tags are content — and
  // artifacts.ts's local-ref inlining needs to see <img src="…"> to rewrite it.
  it('keeps the source tags as markup when asked', () => {
    const out = renderMarkdown('<img src="chart.png">', true, { rawHtml: true })
    expect(out).toContain('<img src="chart.png"')
    expect(out).not.toContain('&lt;img')
  })

  it('keeps <details> usable in a rendered document', () => {
    const out = renderMarkdown('<details><summary>more</summary>body</details>', true, { rawHtml: true })
    expect(out).toContain('<details>')
    expect(out).toContain('<summary>')
  })

  it('still drops scripts and event handlers in rawHtml mode', () => {
    // rawHtml relaxes the escape, not DOMPurify.
    const out = renderMarkdown('<script>alert(1)</script><img src=x onerror=alert(1)>', true, { rawHtml: true })
    expect(out).not.toContain('<script')
    expect(out).not.toContain('onerror')
  })

  it('does not leak the mode into the next render', () => {
    // The renderer is a module-level singleton; the flag has to be per-call or
    // one artifact preview would disable escaping for every chat bubble after
    // it.
    renderMarkdown('<div class="backdrop">x</div>', true, { rawHtml: true })
    const out = renderMarkdown('<div class="backdrop">x</div>')
    expect(out).not.toContain('<div')
    expect(out).toContain('&lt;div class=')
  })

  it('restores escaping even when the parse throws', () => {
    const spy = vi.spyOn(marked, 'parse').mockImplementation(() => {
      throw new Error('boom')
    })
    expect(() => renderMarkdown('x', true, { rawHtml: true })).toThrow('boom')
    spy.mockRestore()
    const out = renderMarkdown('<div class="backdrop">x</div>')
    expect(out).not.toContain('<div')
    expect(out).toContain('&lt;div class=')
  })
})
