// mermaid itself cannot render under jsdom (it needs getBBox /
// getComputedTextLength), so it is mocked here; what is under test is which
// fences get tagged and how a tagged block is swapped for a diagram.
import { describe, it, expect, vi, beforeEach } from 'vitest'

const render = vi.fn()
vi.mock('mermaid', () => ({ default: { initialize: vi.fn(), render } }))

import { renderMarkdown } from './markdown'
import { setupMermaid, resetMermaidCache } from './mermaid'

const flush = () => new Promise((r) => setTimeout(r, 0))

function mount(md: string): HTMLElement {
  const el = document.createElement('div')
  el.innerHTML = renderMarkdown(md)
  document.body.appendChild(el)
  return el
}

beforeEach(() => {
  resetMermaidCache()
  render.mockReset()
  document.body.innerHTML = ''
})

describe('renderMarkdown: mermaid fences', () => {
  it('tags a closed mermaid fence and keeps its source', () => {
    const out = renderMarkdown('```mermaid\ngraph TD\nA-->B\n```\n')
    expect(out).toContain('mermaid-block')
    expect(out).toContain('A--&gt;B')
  })

  it('leaves an unclosed fence as a plain code block', () => {
    expect(renderMarkdown('```mermaid\ngraph TD\nA-->B')).not.toContain('mermaid-block')
  })

  it('treats a shorter closing run as unclosed', () => {
    expect(renderMarkdown('````mermaid\ngraph TD\n```')).not.toContain('mermaid-block')
  })

  it('tags a tilde fence, an info string after the name, and any case', () => {
    expect(renderMarkdown('~~~mermaid\ngraph TD\n~~~')).toContain('mermaid-block')
    expect(renderMarkdown('```mermaid title\ngraph TD\n```')).toContain('mermaid-block')
    expect(renderMarkdown('```Mermaid\ngraph TD\n```')).toContain('mermaid-block')
  })

  it('treats a closing run indented four spaces as content', () => {
    expect(renderMarkdown('```mermaid\ngraph TD\n    ```')).not.toContain('mermaid-block')
  })

  it('does not tag other languages', () => {
    expect(renderMarkdown('```js\nx\n```')).not.toContain('mermaid-block')
  })
})

describe('setupMermaid', () => {
  it('adds the sanitized diagram beside the source', async () => {
    render.mockResolvedValue({ svg: '<svg><text>A</text><script>alert(1)</script></svg>' })
    const el = mount('```mermaid\ngraph TD\nA-->B\n```')
    setupMermaid(el)
    await flush()
    const block = el.querySelector('.mermaid-block')!
    expect(block.hasAttribute('data-mermaid-rendered')).toBe(true)
    expect(block.querySelector('.mermaid-diagram svg text')?.textContent).toBe('A')
    expect(block.innerHTML).not.toContain('<script')
    expect(block.querySelector('pre code')?.textContent).toBe('graph TD\nA-->B')
  })

  it('leaves the code block alone when mermaid rejects the source', async () => {
    render.mockRejectedValue(new Error('parse error'))
    const el = mount('```mermaid\nnot a diagram\n```')
    setupMermaid(el)
    await flush()
    const block = el.querySelector('.mermaid-block')!
    expect(block.hasAttribute('data-mermaid-rendered')).toBe(false)
    expect(block.querySelector('.mermaid-diagram')).toBeNull()
  })

  it('reuses the cached diagram when the markdown re-renders', async () => {
    render.mockResolvedValue({ svg: '<svg><text>A</text></svg>' })
    const md = '```mermaid\ngraph TD\nA-->B\n```'
    const el = mount(md)
    setupMermaid(el)
    await flush()
    // {@html} replaces the nodes on every streamed update.
    el.innerHTML = renderMarkdown(md + '\n\nmore text')
    await flush()
    expect(el.querySelector('.mermaid-diagram svg')).not.toBeNull()
    expect(render).toHaveBeenCalledTimes(1)
  })
})

describe('setupMermaid: theme and failures', () => {
  it('redraws a finished diagram when the theme changes', async () => {
    render.mockImplementation(async () => ({ svg: `<svg><text>${document.documentElement.getAttribute('data-theme') ?? 'light'}</text></svg>` }))
    const el = mount('```mermaid\ngraph TD\nA-->B\n```')
    setupMermaid(el)
    await flush()
    expect(el.querySelector('.mermaid-diagram text')?.textContent).toBe('light')
    document.documentElement.setAttribute('data-theme', 'dark')
    await flush()
    await flush()
    expect(el.querySelectorAll('.mermaid-diagram')).toHaveLength(1)
    expect(el.querySelector('.mermaid-diagram text')?.textContent).toBe('dark')
    document.documentElement.removeAttribute('data-theme')
  })

  it('renders one source once when two blocks show it', async () => {
    render.mockResolvedValue({ svg: '<svg><text>A</text></svg>' })
    const el = mount('```mermaid\ngraph TD\nA-->B\n```\n\n```mermaid\ngraph TD\nA-->B\n```')
    setupMermaid(el)
    await flush()
    expect(el.querySelectorAll('.mermaid-diagram')).toHaveLength(2)
    expect(render).toHaveBeenCalledTimes(1)
  })
})
