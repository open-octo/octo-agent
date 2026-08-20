// Pins the assumption GenuiMermaid.svelte is built on.
//
// mermaid cannot be rendered under jsdom (it needs getBBox /
// getComputedTextLength, which jsdom does not implement), so the diagram
// pipeline itself is not testable here. What IS testable — and what actually
// broke — is which label shape survives the sanitizer. If DOMPurify ever
// changes its SVG profile, this fails and points at the reason the mermaid
// component sets htmlLabels: false.
import { describe, it, expect } from 'vitest'
import DOMPurify from 'dompurify'

const SVG_PROFILE = { USE_PROFILES: { svg: true, svgFilters: true } } as const

// What mermaid emits for a flowchart label with htmlLabels on (its default).
const FOREIGN_OBJECT_LABEL = `<svg xmlns="http://www.w3.org/2000/svg"><g><foreignObject width="100" height="20"><div xmlns="http://www.w3.org/1999/xhtml"><span class="nodeLabel">NodeText</span></div></foreignObject></g></svg>`

// What it emits with htmlLabels off.
const TEXT_LABEL = `<svg xmlns="http://www.w3.org/2000/svg"><g><text x="1" y="2"><tspan>NodeText</tspan></text></g></svg>`

describe('mermaid label survival through the SVG sanitizer', () => {
  it('drops a foreignObject label entirely — element and contents', () => {
    // foreignObject is in DOMPurify's svgDisallowed AND its FORBID_CONTENTS,
    // so a flowchart rendered with html labels comes out as empty boxes and
    // arrows: no error, just silently wordless. This is why the component
    // turns html labels off rather than allowing the element back in.
    const out = DOMPurify.sanitize(FOREIGN_OBJECT_LABEL, SVG_PROFILE)
    expect(out).not.toContain('NodeText')
  })

  it('keeps a native <text> label', () => {
    const out = DOMPurify.sanitize(TEXT_LABEL, SVG_PROFILE)
    expect(out).toContain('NodeText')
  })

  it('strips script and event handlers from an SVG', () => {
    const hostile = `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)<\/script><rect onclick="alert(2)" onload="alert(3)"/><a href="javascript:alert(4)">x</a></svg>`
    const out = DOMPurify.sanitize(hostile, SVG_PROFILE)
    expect(out).not.toMatch(/<script/i)
    expect(out).not.toMatch(/onclick|onload/i)
    expect(out).not.toMatch(/javascript:/i)
  })
})
