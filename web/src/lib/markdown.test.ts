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
