import { describe, it, expect } from 'vitest'
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
})
