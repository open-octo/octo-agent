import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { get } from 'svelte/store'
import { artifacts, panelContent } from './stores'
import { observeArtifact, resetArtifacts } from './artifacts'

// Nothing a preview document references can authenticate: the srcdoc iframe has
// no allow-same-origin, so its subresource requests are cross-site and the
// SameSite=Strict access-key cookie is withheld. These tests pin the two halves
// of that constraint — images stay out of the iframe, and no preview document
// smuggles an /api/ reference back in.

const SID = 'sess-1'

function payload(path: string) {
  return { type: 'write', path }
}

// Image bytes must come back as the test environment's own Blob. Node's fetch
// Response mints undici's Blob, which jsdom's FileReader brand-checks and
// rejects on Node 22 (Node 26 unifies the classes, so it passes there) — the
// inliner's catch would swallow that and the tests would assert on an
// un-rewritten document. A real browser mints fetch blobs and FileReader in
// the same realm, so this is also the truer stub.
function imageResponse(bytes: Uint8Array<ArrayBuffer>): Response {
  return { ok: true, blob: async () => new Blob([bytes], { type: 'image/png' }) } as Response
}

beforeEach(() => {
  resetArtifacts(SID)
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('observeArtifact — image artifacts', () => {
  it('carries a src for the host document and never fetches the bytes', async () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)

    await observeArtifact(SID, payload('/tmp/shot.png'), false)

    const [entry] = get(artifacts)
    expect(entry.type).toBe('Image')
    expect(entry.src).toBe(
      `/api/sessions/${encodeURIComponent(SID)}/artifacts?path=${encodeURIComponent('/tmp/shot.png')}&rev=1`,
    )
    // The <img> pulls the bytes lazily; observing must not download them.
    expect(fetchMock).not.toHaveBeenCalled()
    // No sandboxed preview document at all, so nothing to 401.
    expect(entry.preview).toBe('')
    // `code` is the on-disk path — the one text form worth copying.
    expect(entry.code).toBe('/tmp/shot.png')
  })

  it('changes the src when the same path is written again', async () => {
    vi.stubGlobal('fetch', vi.fn())

    await observeArtifact(SID, payload('/tmp/shot.png'), false)
    const first = get(artifacts)[0].src
    await observeArtifact(SID, payload('/tmp/shot.png'), true)
    const second = get(artifacts)[0].src

    // An identical src would let Svelte skip the update and leave the old bytes
    // on screen after the agent overwrote the file.
    expect(get(artifacts)).toHaveLength(1)
    expect(second).not.toBe(first)
  })

})

describe('observeArtifact — markdown copy button', () => {
  it('binds the handler to an element the preview document actually has', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('```js\nlet a = 1\n```\n')))

    await observeArtifact(SID, payload('/tmp/notes.md'), false)

    const { preview } = get(artifacts)[0]
    // '.body' matched nothing here, so querySelector returned null and the whole
    // handler threw on load — the button never worked.
    expect(preview).not.toContain(".querySelector('.body')")
    expect(preview).toContain('document.body.addEventListener')
    // The other half of the contract: the markup the handler looks for.
    expect(preview).toContain('class="code-block"')
    expect(preview).toContain('class="copy-btn"')
    expect(preview).toContain('<pre><code')
  })
})

// Code-kind artifacts fetch their body like markdown does and preview as
// escaped monospace text. The server serves these extensions as text/plain
// (#1895) — before that, the fetch 404ed and the artifact silently vanished.
describe('observeArtifact — code artifacts', () => {
  it('previews a code file as escaped monospace text', async () => {
    const source = 'if x < 1 && y > 2:\n    print("ok")\n'
    vi.stubGlobal('fetch', vi.fn(async () => new Response(source)))

    await observeArtifact(SID, payload('/tmp/script.py'), false)

    const [entry] = get(artifacts)
    expect(entry.type).toBe('PY')
    expect(entry.code).toBe(source)
    // The source lands inside a <pre>, so its markup-significant characters
    // must arrive escaped.
    expect(entry.preview).toContain('<pre')
    expect(entry.preview).toContain('x &lt; 1 &amp;&amp; y &gt; 2')
    expect(entry.preview).not.toContain('x < 1')
  })

  it('never auto-opens the panel for a live code write', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('x = 1\n')))

    await observeArtifact(SID, { type: 'write', path: '/tmp/script.py' }, true)

    // Source-file writes are the routine bulk of a coding session; the panel
    // must not pop open on them.
    expect(get(panelContent)).toBe(null)

    // And the code write must not consume the once-per-session flag: a later
    // rich-kind artifact still auto-opens.
    await observeArtifact(SID, { type: 'write', path: '/tmp/report.md' }, true)
    expect(get(panelContent)).toBe('session')
  })
})

describe('observeArtifact — preview documents', () => {
  it('builds the markdown preview without referencing /api/', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('# hello')))

    await observeArtifact(SID, payload('/tmp/notes.md'), false)

    const [entry] = get(artifacts)
    expect(entry.preview).not.toBe('')
    expect(entry.preview).not.toContain('/api/')
  })
})

// A markdown artifact's own image references can't load from inside the iframe
// either, so their bytes are inlined as data: URIs.
describe('observeArtifact — markdown image references', () => {
  const png = new Uint8Array([137, 80, 78, 71])

  function stubFetch(md: string, imageStatus = 200) {
    const fetchMock = vi.fn(async (u: string) => {
      if (u.includes('report.md')) return new Response(md)
      if (imageStatus !== 200) return new Response('', { status: imageStatus })
      return imageResponse(png)
    })
    vi.stubGlobal('fetch', fetchMock)
    return fetchMock
  }

  it('inlines a relative reference, resolved against the markdown file', async () => {
    const fetchMock = stubFetch('![shot](img/shot.png)\n')

    await observeArtifact(SID, payload('/tmp/report.md'), false)

    const [entry] = get(artifacts)
    expect(entry.preview).toContain('src="data:image/png;base64,')
    expect(entry.preview).not.toContain('img/shot.png')
    // Resolved relative to /tmp/report.md, not to the host page.
    expect(fetchMock).toHaveBeenCalledWith(
      `/api/sessions/${SID}/artifacts?path=${encodeURIComponent('/tmp/img/shot.png')}`,
    )
  })

  it('resolves "." and ".." segments', async () => {
    const fetchMock = stubFetch('![shot](../shots/./a.png)\n')

    await observeArtifact(SID, payload('/tmp/docs/report.md'), false)

    expect(fetchMock).toHaveBeenCalledWith(
      `/api/sessions/${SID}/artifacts?path=${encodeURIComponent('/tmp/shots/a.png')}`,
    )
  })

  it('refuses to inline a non-image, even though the endpoint serves it', async () => {
    // .md is previewable server-side, so only this gate stops an artifact from
    // naming a sibling document and having the host page fetch it.
    const fetchMock = stubFetch('![oops](secrets.md)\n')

    await observeArtifact(SID, payload('/tmp/report.md'), false)

    const [entry] = get(artifacts)
    expect(entry.preview).toContain('secrets.md')
    expect(entry.preview).not.toContain('data:')
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('leaves remote and data references untouched', async () => {
    const fetchMock = stubFetch('![a](https://example.com/a.png)\n\n![b](data:image/gif;base64,R0lGOD)\n')

    await observeArtifact(SID, payload('/tmp/report.md'), false)

    const [entry] = get(artifacts)
    expect(entry.preview).toContain('https://example.com/a.png')
    // Only the markdown body itself was fetched.
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('keeps the artifact when an image is not fetchable', async () => {
    stubFetch('![gone](missing.png)\n', 404)

    await observeArtifact(SID, payload('/tmp/report.md'), false)

    const [entry] = get(artifacts)
    // The reference stays as written: a broken image beats losing the preview.
    expect(entry.preview).toContain('missing.png')
    expect(get(artifacts)).toHaveLength(1)
  })
})

// An HTML artifact previews as its own document, so the same references need the
// same treatment — but only when hasExternalRefs hasn't already routed the file
// to the warning page.
describe('observeArtifact — html local references', () => {
  const png = new Uint8Array([137, 80, 78, 71])

  function stubFetch(html: string, imageStatus = 200) {
    const fetchMock = vi.fn(async (u: string) => {
      if (u.includes('page.html')) return new Response(html)
      if (imageStatus !== 200) return new Response('', { status: imageStatus })
      return imageResponse(png)
    })
    vi.stubGlobal('fetch', fetchMock)
    return fetchMock
  }

  it('inlines an <img> and a CSS url(), keeping the doctype', async () => {
    stubFetch(
      '<!DOCTYPE html><html><head><style>body{background:url("bg.png")}</style></head>' +
        '<body><img src="chart.png"></body></html>',
    )

    await observeArtifact(SID, payload('/tmp/page.html'), false)

    const [entry] = get(artifacts)
    expect(entry.preview).toMatch(/^<!DOCTYPE html>/i)
    expect(entry.preview).not.toContain('chart.png')
    expect(entry.preview).not.toContain('bg.png')
    expect(entry.preview.match(/data:image\/png;base64,/g)).toHaveLength(2)
  })

  it('drops a <picture>\'s <source> so the inlined <img src> actually wins', async () => {
    stubFetch(
      '<html><body><picture><source srcset="chart.webp" type="image/webp">' +
        '<img src="chart.png"></picture></body></html>',
    )

    await observeArtifact(SID, payload('/tmp/page.html'), false)

    const [entry] = get(artifacts)
    expect(entry.preview).toContain('data:image/png;base64,')
    // A surviving <source> outranks the src, so the browser would go back to
    // the unreachable relative path and the image would still be broken.
    expect(entry.preview).not.toContain('chart.webp')
    expect(entry.preview).not.toContain('<source')
  })

  it('rewrites an SVG <image href>', async () => {
    stubFetch('<html><body><svg><image href="d.png" width="10" height="10"></image></svg></body></html>')

    await observeArtifact(SID, payload('/tmp/page.html'), false)

    const [entry] = get(artifacts)
    expect(entry.preview).toContain('data:image/png;base64,')
    expect(entry.preview).not.toContain('d.png')
  })

  it('keeps a doctype\'s public and system identifiers', async () => {
    stubFetch(
      '<!DOCTYPE html PUBLIC "-//W3C//DTD HTML 4.01//EN" "http://www.w3.org/TR/html4/strict.dtd">' +
        '<html><body><img src="chart.png"></body></html>',
    )

    await observeArtifact(SID, payload('/tmp/page.html'), false)

    // A name-only `<!DOCTYPE html>` would switch this file to standards mode and
    // the preview would lay out differently from the real thing.
    expect(get(artifacts)[0].preview).toContain('PUBLIC "-//W3C//DTD HTML 4.01//EN"')
  })

  it('hands back the exact source when there is nothing to inline', async () => {
    const html = '<!DOCTYPE html><html><body><h1>hi</h1></body></html>'
    stubFetch(html)

    await observeArtifact(SID, payload('/tmp/page.html'), false)

    // No parse/serialize round-trip on a document with no local references.
    expect(get(artifacts)[0].preview).toBe(html)
  })

  it('leaves the warning page alone when a script is unreachable', async () => {
    stubFetch('<html><head><script src="app.js"></script></head><body><img src="chart.png"></body></html>')

    await observeArtifact(SID, payload('/tmp/page.html'), false)

    const [entry] = get(artifacts)
    expect(entry.preview).toContain('cannot be previewed here')
    expect(entry.preview).not.toContain('data:image/png')
  })
})

// Only a <link> whose rel the document needs in order to render may force the
// warning page. A favicon or manifest failing to load changes nothing, and
// routing those files to the warning page also kept them from the image
// inliner entirely (#1896).
describe('observeArtifact — link rel discrimination', () => {
  const png = new Uint8Array([137, 80, 78, 71])

  function stubFetch(html: string) {
    vi.stubGlobal('fetch', vi.fn(async (u: string) => {
      if (u.includes('page.html')) return new Response(html)
      return imageResponse(png)
    }))
  }

  it('previews past a favicon link and still inlines the images', async () => {
    stubFetch(
      '<html><head><link rel="icon" href="favicon.png"></head>' +
        '<body><img src="chart.png"></body></html>',
    )

    await observeArtifact(SID, payload('/tmp/page.html'), false)

    const [entry] = get(artifacts)
    expect(entry.preview).not.toContain('cannot be previewed here')
    // The whole point: this file used to skip the inliner along with the preview.
    expect(entry.preview).toContain('data:image/png;base64,')
  })

  it('previews past preconnect, manifest, and canonical links', async () => {
    const html =
      '<html><head><link rel="preconnect" href="https://fonts.googleapis.com">' +
      '<link rel="manifest" href="manifest.json">' +
      '<link rel="canonical" href="https://example.com/page"></head>' +
      '<body><h1>hi</h1></body></html>'
    stubFetch(html)

    await observeArtifact(SID, payload('/tmp/page.html'), false)

    expect(get(artifacts)[0].preview).toBe(html)
  })

  it('still warns for an external stylesheet', async () => {
    stubFetch('<html><head><link rel="stylesheet" href="https://cdn.example.com/x.css"></head><body></body></html>')

    await observeArtifact(SID, payload('/tmp/page.html'), false)

    expect(get(artifacts)[0].preview).toContain('cannot be previewed here')
  })

  it('still warns when the rel attribute is unquoted or trails the href', async () => {
    stubFetch('<html><head><link href="style.css" rel=stylesheet></head><body></body></html>')

    await observeArtifact(SID, payload('/tmp/page.html'), false)

    expect(get(artifacts)[0].preview).toContain('cannot be previewed here')
  })

  it('treats rel="preload" as render-affecting', async () => {
    // The rel="preload" onload="this.rel='stylesheet'" idiom makes it one.
    stubFetch('<html><head><link rel="preload" as="style" href="x.css"></head><body></body></html>')

    await observeArtifact(SID, payload('/tmp/page.html'), false)

    expect(get(artifacts)[0].preview).toContain('cannot be previewed here')
  })

  it('leaves a data: stylesheet alone, same as before', async () => {
    const html = '<html><head><link rel="stylesheet" href="data:text/css,body{margin:0}"></head><body></body></html>'
    stubFetch(html)

    await observeArtifact(SID, payload('/tmp/page.html'), false)

    expect(get(artifacts)[0].preview).toBe(html)
  })
})
