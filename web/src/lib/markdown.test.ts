import { describe, it, expect } from 'vitest'
import { renderMarkdown } from './markdown'

// These assert on what a quote's CONTENTS render to, never on the surrounding
// <blockquote> tag. Under happy-dom, DOMPurify strips block wrappers it keeps
// in a real browser — `sanitize('<div class="q">x</div>')` returns bare `x`
// here, and blockquote goes the same way, while the <p>/<strong> inside
// survive. That is a test-environment artifact, not the shipped behavior (the
// code-block renderer emits a <div> that is plainly present in the UI), so
// asserting on the wrapper would pin the artifact instead of the fix.
describe('renderMarkdown: blockquote contents', () => {
  it('renders bold inside a quote instead of leaking asterisks', () => {
    const out = renderMarkdown('> quoted **bold**')
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
    expect(out).toContain('<strong>line</strong>')
    expect(out).toContain('<em>line</em>')
  })

  it('still renders markdown outside a quote', () => {
    const out = renderMarkdown('Outside **bold** and `code`.')
    expect(out).toContain('<strong>bold</strong>')
    expect(out).toContain('<code>code</code>')
  })
})
