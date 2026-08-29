import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { get } from 'svelte/store'
import { artifacts, artifactSel, panelContent, panelExpanded } from './stores'
import { lightAppSource, observeArtifact, hydrateArtifact, resetArtifacts, pathIsInside } from './artifacts'

// Nothing a preview document references can authenticate: the srcdoc iframe has
// no allow-same-origin, so its subresource requests are cross-site and the
// SameSite=Strict access-key cookie is withheld. These tests pin the two halves
// of that constraint — images stay out of the iframe, and no preview document
// smuggles an /api/ reference back in.

const SID = 'sess-1'

function payload(path: string) {
  return { type: 'write', path }
}

// Observing is metadata-only since #1893 — the body fetch and preview build run
// on first selection. Most cases below assert on the preview document, so this
// drives that second step the way the panel's $effect does.
async function observeHydrated(path: string) {
  observeArtifact(SID, payload(path), false)
  await hydrateArtifact(get(artifacts).at(-1))
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

describe('resetArtifacts', () => {
  it('closes an expanded panel so the main column is never hidden with nothing beside it', () => {
    panelContent.set('lightapps')
    panelExpanded.set(true)

    resetArtifacts('sess-2')

    expect(get(panelContent)).toBe(null)
    expect(get(panelExpanded)).toBe(false)
  })
})

describe('observeArtifact — image artifacts', () => {
  it('carries a src for the host document and never fetches the bytes', () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)

    observeArtifact(SID, payload('/tmp/shot.png'), false)

    const [entry] = get(artifacts)
    expect(entry.type).toBe('Image')
    expect(entry.src).toBe(
      `/api/sessions/${encodeURIComponent(SID)}/artifacts?path=${encodeURIComponent('/tmp/shot.png')}&rev=1`,
    )
    // The <img> pulls the bytes lazily; observing must not download them.
    expect(fetchMock).not.toHaveBeenCalled()
    // No sandboxed preview document at all, so nothing to 401 — and nothing
    // for hydrateArtifact to build either.
    expect(entry.preview).toBe('')
    expect(entry.loaded).toBe(true)
    // `code` is the on-disk path — the one text form worth copying.
    expect(entry.code).toBe('/tmp/shot.png')
  })

  it('changes the src when the same path is written again', () => {
    vi.stubGlobal('fetch', vi.fn())

    observeArtifact(SID, payload('/tmp/shot.png'), false)
    const first = get(artifacts)[0].src
    observeArtifact(SID, payload('/tmp/shot.png'), true)
    const second = get(artifacts)[0].src

    // An identical src would let Svelte skip the update and leave the old bytes
    // on screen after the agent overwrote the file.
    expect(get(artifacts)).toHaveLength(1)
    expect(second).not.toBe(first)
  })

})

// Entries land in the store metadata-only and the body is built on first
// selection, so history replay costs no network and a session's unopened
// artifacts hold no data: URIs (#1893).
describe('hydrateArtifact — lazy body build', () => {
  it('observing a text artifact fetches nothing', () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)

    observeArtifact(SID, payload('/tmp/report.md'), false)

    const [entry] = get(artifacts)
    expect(fetchMock).not.toHaveBeenCalled()
    expect(entry.loaded).toBe(false)
    expect(entry.preview).toBe('')
    expect(entry.code).toBe('')
  })

  it('builds the body once on first selection', async () => {
    const fetchMock = vi.fn(async () => new Response('# hello'))
    vi.stubGlobal('fetch', fetchMock)

    observeArtifact(SID, payload('/tmp/report.md'), false)
    await hydrateArtifact(get(artifacts)[0])
    // Re-selecting a loaded entry must not refetch.
    await hydrateArtifact(get(artifacts)[0])

    const [entry] = get(artifacts)
    expect(entry.loaded).toBe(true)
    expect(entry.code).toBe('# hello')
    expect(entry.preview).toContain('hello')
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('drops a stale build when a re-write replaced the entry', async () => {
    let release!: (r: Response) => void
    const gate = new Promise<Response>(r => { release = r })
    vi.stubGlobal('fetch', vi.fn(() => gate))

    observeArtifact(SID, payload('/tmp/report.md'), false)
    const stale = hydrateArtifact(get(artifacts)[0])
    // The agent re-wrote the file while the old body was still being fetched.
    observeArtifact(SID, payload('/tmp/report.md'), false)
    release(new Response('# v1'))
    await stale

    // The build began against the replaced entry; the fresh one must not
    // inherit those bytes — it hydrates itself when next selected.
    expect(get(artifacts)).toHaveLength(1)
    expect(get(artifacts)[0].loaded).toBe(false)
  })

  it('discards a build that resolves after a session switch', async () => {
    let release!: (r: Response) => void
    const gate = new Promise<Response>(r => { release = r })
    vi.stubGlobal('fetch', vi.fn(() => gate))

    observeArtifact(SID, payload('/tmp/report.md'), false)
    const inflight = hydrateArtifact(get(artifacts)[0])
    resetArtifacts('sess-2')
    release(new Response('# hello'))
    await inflight

    expect(get(artifacts)).toHaveLength(0)
  })

  it('shows a placeholder instead of loading forever when the fetch fails', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('', { status: 404 })))

    observeArtifact(SID, payload('/tmp/report.md'), false)
    await hydrateArtifact(get(artifacts)[0])

    const [entry] = get(artifacts)
    // loaded, or the panel would spin forever; a re-write replaces the entry
    // and retries. loadFailed disables the actions that would persist the
    // empty body (copy, download, Save to Light App).
    expect(entry.loaded).toBe(true)
    expect(entry.loadFailed).toBe(true)
    expect(entry.preview).toContain('could not be loaded')
  })
})

describe('observeArtifact — markdown copy button', () => {
  it('binds the handler to an element the preview document actually has', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('```js\nlet a = 1\n```\n')))

    await observeHydrated('/tmp/notes.md')

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

// Code-kind artifacts hydrate their body like markdown does and preview as
// escaped monospace text. The server serves these extensions as text/plain
// (#1895) — before that, the fetch 404ed and the artifact silently vanished.
describe('observeArtifact — code artifacts', () => {
  it('previews a code file as escaped monospace text', async () => {
    const source = 'if x < 1 && y > 2:\n    print("ok")\n'
    vi.stubGlobal('fetch', vi.fn(async () => new Response(source)))

    await observeHydrated('/tmp/script.py')

    const [entry] = get(artifacts)
    expect(entry.type).toBe('PY')
    expect(entry.code).toBe(source)
    // The source lands inside a <pre>, so its markup-significant characters
    // must arrive escaped.
    expect(entry.preview).toContain('<pre')
    expect(entry.preview).toContain('x &lt; 1 &amp;&amp; y &gt; 2')
    expect(entry.preview).not.toContain('x < 1')
  })

  it('never auto-opens the panel for a live code write', () => {
    vi.stubGlobal('fetch', vi.fn())

    observeArtifact(SID, { type: 'write', path: '/tmp/script.py' }, true)

    // Source-file writes are the routine bulk of a coding session; the panel
    // must not pop open on them.
    expect(get(panelContent)).toBe(null)

    // And the code write must not consume the once-per-session flag: a later
    // rich-kind artifact still auto-opens.
    observeArtifact(SID, { type: 'write', path: '/tmp/report.md' }, true)
    expect(get(panelContent)).toBe('session')
  })
})

describe('observeArtifact — preview documents', () => {
  it('builds the markdown preview without referencing /api/', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('# hello')))

    await observeHydrated('/tmp/notes.md')

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

    await observeHydrated('/tmp/report.md')

    const [entry] = get(artifacts)
    expect(entry.preview).toContain('src="data:image/png;base64,')
    expect(entry.preview).not.toContain('img/shot.png')
    // Resolved relative to /tmp/report.md, not to the host page.
    expect(fetchMock).toHaveBeenCalledWith(
      `/api/sessions/${SID}/artifacts?path=${encodeURIComponent('/tmp/img/shot.png')}`,
    )
  })

  it('inlines a raw <img> tag the document wrote itself', async () => {
    // The document's own HTML is content inside the sandboxed preview iframe,
    // so it survives rendering — chat bubbles escape it instead (markdown.ts).
    // Without that, the tag never reaches the inliner and the image silently
    // does not render.
    const fetchMock = stubFetch('# Report\n\n<img src="chart.png" width="400">\n')

    await observeHydrated('/tmp/report.md')

    const [entry] = get(artifacts)
    expect(entry.preview).toContain('src="data:image/png;base64,')
    expect(entry.preview).not.toContain('&lt;img')
    expect(fetchMock).toHaveBeenCalledWith(
      `/api/sessions/${SID}/artifacts?path=${encodeURIComponent('/tmp/chart.png')}`,
    )
  })

  it('resolves "." and ".." segments', async () => {
    const fetchMock = stubFetch('![shot](../shots/./a.png)\n')

    await observeHydrated('/tmp/docs/report.md')

    expect(fetchMock).toHaveBeenCalledWith(
      `/api/sessions/${SID}/artifacts?path=${encodeURIComponent('/tmp/shots/a.png')}`,
    )
  })

  it('refuses to inline a non-image, even though the endpoint serves it', async () => {
    // .md is previewable server-side, so only this gate stops an artifact from
    // naming a sibling document and having the host page fetch it.
    const fetchMock = stubFetch('![oops](secrets.md)\n')

    await observeHydrated('/tmp/report.md')

    const [entry] = get(artifacts)
    expect(entry.preview).toContain('secrets.md')
    expect(entry.preview).not.toContain('data:')
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('leaves remote and data references untouched', async () => {
    const fetchMock = stubFetch('![a](https://example.com/a.png)\n\n![b](data:image/gif;base64,R0lGOD)\n')

    await observeHydrated('/tmp/report.md')

    const [entry] = get(artifacts)
    expect(entry.preview).toContain('https://example.com/a.png')
    // Only the markdown body itself was fetched.
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('keeps the artifact when an image is not fetchable', async () => {
    stubFetch('![gone](missing.png)\n', 404)

    await observeHydrated('/tmp/report.md')

    const [entry] = get(artifacts)
    // The reference stays as written: a broken image beats losing the preview.
    expect(entry.preview).toContain('missing.png')
    expect(get(artifacts)).toHaveLength(1)
  })
})

// An HTML artifact previews as its own document, so the same references need the
// same treatment.
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

    await observeHydrated('/tmp/page.html')

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

    await observeHydrated('/tmp/page.html')

    const [entry] = get(artifacts)
    expect(entry.preview).toContain('data:image/png;base64,')
    // A surviving <source> outranks the src, so the browser would go back to
    // the unreachable relative path and the image would still be broken.
    expect(entry.preview).not.toContain('chart.webp')
    expect(entry.preview).not.toContain('<source')
  })

  it('rewrites an SVG <image href>', async () => {
    stubFetch('<html><body><svg><image href="d.png" width="10" height="10"></image></svg></body></html>')

    await observeHydrated('/tmp/page.html')

    const [entry] = get(artifacts)
    expect(entry.preview).toContain('data:image/png;base64,')
    expect(entry.preview).not.toContain('d.png')
  })

  it('keeps a doctype\'s public and system identifiers', async () => {
    stubFetch(
      '<!DOCTYPE html PUBLIC "-//W3C//DTD HTML 4.01//EN" "http://www.w3.org/TR/html4/strict.dtd">' +
        '<html><body><img src="chart.png"></body></html>',
    )

    await observeHydrated('/tmp/page.html')

    // A name-only `<!DOCTYPE html>` would switch this file to standards mode and
    // the preview would lay out differently from the real thing.
    expect(get(artifacts)[0].preview).toContain('PUBLIC "-//W3C//DTD HTML 4.01//EN"')
  })

  it('hands back the exact source when there is nothing to inline', async () => {
    const html = '<!DOCTYPE html><html><body><h1>hi</h1></body></html>'
    stubFetch(html)

    await observeHydrated('/tmp/page.html')

    // No parse/serialize round-trip on a document with no local references.
    expect(get(artifacts)[0].preview).toBe(html)
  })

  it('strips an unreachable local script and still inlines the image', async () => {
    stubFetch('<html><head><script src="app.js"></script></head><body><img src="chart.png"></body></html>')

    await observeHydrated('/tmp/page.html')

    const [entry] = get(artifacts)
    expect(entry.preview).not.toContain('app.js')
    expect(entry.preview).toContain('removed')
    expect(entry.preview).toContain('data:image/png')
  })
})

// Only a <link> whose rel the document needs in order to render gets stripped.
// A favicon or manifest failing to load changes nothing, and treating those as
// external references once kept the whole file from rendering and from the
// image inliner entirely (#1896).
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

    await observeHydrated('/tmp/page.html')

    const [entry] = get(artifacts)
    expect(entry.preview).not.toContain('removed')
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

    await observeHydrated('/tmp/page.html')

    expect(get(artifacts)[0].preview).toBe(html)
  })

  it('strips an external stylesheet and renders the rest under a banner', async () => {
    stubFetch('<html><head><link rel="stylesheet" href="https://cdn.example.com/x.css"></head><body><h1>hi</h1></body></html>')

    await observeHydrated('/tmp/page.html')

    const { preview } = get(artifacts)[0]
    expect(preview).not.toContain('cdn.example.com')
    expect(preview).toContain('<h1>hi</h1>')
    expect(preview).toContain('1 external script/stylesheet was removed')
    // The banner sits inside the body, before the page's own content.
    expect(preview.indexOf('removed')).toBeLessThan(preview.indexOf('<h1>hi</h1>'))
  })

  it('strips a link whose rel attribute is unquoted or trails the href', async () => {
    stubFetch('<html><head><link href="style.css" rel=stylesheet></head><body></body></html>')

    await observeHydrated('/tmp/page.html')

    const { preview } = get(artifacts)[0]
    expect(preview).not.toContain('style.css')
    expect(preview).toContain('removed')
  })

  it('treats rel="preload" as render-affecting', async () => {
    // The rel="preload" onload="this.rel='stylesheet'" idiom makes it one.
    stubFetch('<html><head><link rel="preload" as="style" href="x.css"></head><body></body></html>')

    await observeHydrated('/tmp/page.html')

    const { preview } = get(artifacts)[0]
    expect(preview).not.toContain('x.css')
    expect(preview).toContain('removed')
  })

  it('strips external scripts but keeps inline ones, and counts what it removed', async () => {
    stubFetch(
      '<html><head><script src="https://cdn.example.com/lib.js"></script>' +
        '<script>window.ok = 1</script>' +
        '<link rel="stylesheet" href="https://cdn.example.com/x.css"></head>' +
        '<body><p>content</p><script src="app.js"></script></body></html>',
    )

    await observeHydrated('/tmp/page.html')

    const { preview } = get(artifacts)[0]
    expect(preview).not.toContain('lib.js')
    expect(preview).not.toContain('app.js')
    expect(preview).not.toContain('x.css')
    expect(preview).toContain('<script>window.ok = 1</script>')
    expect(preview).toContain('<p>content</p>')
    expect(preview).toContain('3 external scripts/stylesheets were removed')
  })

  it('still inlines the local images of a page that lost its stylesheet', async () => {
    // Before, an external reference routed the whole file to a warning page
    // and the inliner never ran; now the surviving content gets its images.
    stubFetch('<html><head><link rel="stylesheet" href="https://cdn.example.com/x.css"></head><body><img src="chart.png"></body></html>')

    await observeHydrated('/tmp/page.html')

    expect(get(artifacts)[0].preview).toContain('data:image/png;base64,')
  })

  it('puts the banner at the top when the page has no <body> tag', async () => {
    stubFetch('<link rel="stylesheet" href="https://cdn.example.com/x.css"><h1>hi</h1>')

    await observeHydrated('/tmp/page.html')

    const { preview } = get(artifacts)[0]
    expect(preview.startsWith('<div')).toBe(true)
    expect(preview).toContain('<h1>hi</h1>')
  })

  it('leaves a data: stylesheet alone, same as before', async () => {
    const html = '<html><head><link rel="stylesheet" href="data:text/css,body{margin:0}"></head><body></body></html>'
    stubFetch(html)

    await observeHydrated('/tmp/page.html')

    expect(get(artifacts)[0].preview).toBe(html)
  })
})

// The #1888 review collected reference kinds the inliner skipped; these pin
// the client-side-fixable ones (#1892). What stays out: file kinds the
// artifact endpoint deliberately doesn't serve (video, audio, fonts).
describe('observeArtifact — inliner gaps (#1892)', () => {
  const png = new Uint8Array([137, 80, 78, 71])

  function stubFetch(html: string) {
    const fetchMock = vi.fn(async (u: string) => {
      if (u.includes('page.html')) return new Response(html)
      return imageResponse(png)
    })
    vi.stubGlobal('fetch', fetchMock)
    return fetchMock
  }

  it('inlines a srcset-only <img> via its first candidate', async () => {
    const fetchMock = stubFetch('<html><body><img srcset="chart.png 1x, chart@2x.png 2x"></body></html>')

    await observeHydrated('/tmp/page.html')

    const [entry] = get(artifacts)
    expect(entry.preview).toContain('src="data:image/png;base64,')
    expect(entry.preview).not.toContain('srcset')
    expect(fetchMock).toHaveBeenCalledWith(
      `/api/sessions/${SID}/artifacts?path=${encodeURIComponent('/tmp/chart.png')}`,
    )
  })

  it('does not second-guess a remote src just because the srcset is local', async () => {
    const html = '<html><body><img src="https://example.com/c.png" srcset="chart.png 1x"></body></html>'
    const fetchMock = stubFetch(html)

    await observeHydrated('/tmp/page.html')

    // The remote src is a load the iframe performs fine; replacing it with a
    // sibling file's bytes would show the wrong image.
    expect(get(artifacts)[0].preview).toBe(html)
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('resolves references against a local <base href>', async () => {
    const fetchMock = stubFetch(
      '<html><head><base href="assets/"></head><body><img src="chart.png"></body></html>',
    )

    await observeHydrated('/tmp/page.html')

    expect(get(artifacts)[0].preview).toContain('data:image/png;base64,')
    expect(fetchMock).toHaveBeenCalledWith(
      `/api/sessions/${SID}/artifacts?path=${encodeURIComponent('/tmp/assets/chart.png')}`,
    )
  })

  it('inlines nothing under a remote <base href>', async () => {
    const html = '<html><head><base href="https://cdn.example.com/"></head><body><img src="chart.png"></body></html>'
    const fetchMock = stubFetch(html)

    await observeHydrated('/tmp/page.html')

    // chart.png resolves against the remote base — again a load the iframe
    // performs itself — so the document rides through untouched.
    expect(get(artifacts)[0].preview).toBe(html)
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('keeps a comment above <html>', async () => {
    stubFetch('<!-- built 2026-07-30 --><!DOCTYPE html><html><body><img src="chart.png"></body></html>')

    await observeHydrated('/tmp/page.html')

    const [entry] = get(artifacts)
    expect(entry.preview).toContain('<!-- built 2026-07-30 -->')
    expect(entry.preview).toContain('<!DOCTYPE html>')
    expect(entry.preview).toContain('data:image/png;base64,')
  })

  it('inlines a quoted CSS url() whose file name contains a parenthesis', async () => {
    const fetchMock = stubFetch(
      '<html><head><style>body{background:url("bg (1).png")}</style></head><body></body></html>',
    )

    await observeHydrated('/tmp/page.html')

    expect(get(artifacts)[0].preview).toContain('data:image/png;base64,')
    expect(fetchMock).toHaveBeenCalledWith(
      `/api/sessions/${SID}/artifacts?path=${encodeURIComponent('/tmp/bg (1).png')}`,
    )
  })

  it('leaves a quoted data: url() with parentheses alone', async () => {
    const html =
      `<html><head><style>body{background:url("data:image/svg+xml,<svg fill='rgb(1,2,3)'/>")}</style></head><body></body></html>`
    const fetchMock = stubFetch(html)

    await observeHydrated('/tmp/page.html')

    expect(get(artifacts)[0].preview).toBe(html)
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })
})

// The list must come out in transcript order regardless of how long any body
// takes to load (#1894). Observing is synchronous, so entries land in call
// order by construction; these pin the other half — a slow hydration writes
// back in place and never reorders the list or steals the selection.
describe('observeArtifact — transcript ordering', () => {
  it('keeps order and selection while an earlier artifact is still hydrating', async () => {
    // report.md needs a fetch we hold open; the image needs none.
    let releaseMd!: (r: Response) => void
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>(res => { releaseMd = res })))

    observeArtifact(SID, payload('/tmp/report.md'), false)
    const md = hydrateArtifact(get(artifacts)[0])
    observeArtifact(SID, payload('/tmp/late.png'), false)

    // Both entries are present in transcript order before any fetch resolves.
    expect(get(artifacts).map(a => a.name)).toEqual(['report.md', 'late.png'])
    expect(get(artifactSel)).toBe(1)

    releaseMd(new Response('# report'))
    await md

    // The finished build lands in place — order and selection unchanged.
    expect(get(artifacts).map(a => a.name)).toEqual(['report.md', 'late.png'])
    expect(get(artifacts)[0].code).toBe('# report')
    expect(get(artifactSel)).toBe(1)
  })

  it('keeps the newer write when a stale build of the same path resolves last', async () => {
    const resolvers: Array<(r: Response) => void> = []
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>(res => { resolvers.push(res) })))

    observeArtifact(SID, payload('/tmp/a.md'), false)
    const stale = hydrateArtifact(get(artifacts)[0])
    // Re-write replaces the entry while the first build is still fetching.
    observeArtifact(SID, payload('/tmp/a.md'), false)
    const fresh = hydrateArtifact(get(artifacts)[0])
    resolvers[1](new Response('v2'))
    await fresh
    resolvers[0](new Response('v1'))
    await stale

    expect(get(artifacts)).toHaveLength(1)
    expect(get(artifacts)[0].code).toBe('v2')
    // The dropped stale build must not touch the selection either.
    expect(get(artifactSel)).toBe(0)
  })
})

// "Save to Light App" persists a copy that renders through the same kind of
// sandboxed iframe as the panel preview, so it must save the inlined preview —
// the raw source's relative image paths resolve against nothing there (#1890).
describe('lightAppSource — what Save to Light App persists', () => {
  const png = new Uint8Array([137, 80, 78, 71])

  function stubFetch(html: string) {
    vi.stubGlobal('fetch', vi.fn(async (u: string) => {
      if (u.includes('page.html')) return new Response(html)
      return imageResponse(png)
    }))
  }

  it('hands over the inlined preview when the document has local images', async () => {
    stubFetch('<!DOCTYPE html><html><body><img src="chart.png"></body></html>')

    await observeHydrated('/tmp/page.html')

    const saved = lightAppSource(get(artifacts)[0])
    expect(saved).toContain('data:image/png;base64,')
    expect(saved).not.toContain('chart.png')
  })

  it('hands over the exact source when there was nothing to inline', async () => {
    const html = '<!DOCTYPE html><html><body><h1>hi</h1></body></html>'
    stubFetch(html)

    await observeHydrated('/tmp/page.html')

    expect(lightAppSource(get(artifacts)[0])).toBe(html)
  })

  it('never persists the stripped rendering — the source is the faithful copy', async () => {
    const html = '<html><head><script src="app.js"></script></head><body><img src="chart.png"></body></html>'
    stubFetch(html)

    await observeHydrated('/tmp/page.html')

    const entry = get(artifacts)[0]
    expect(entry.preview).toContain('removed')
    expect(lightAppSource(entry)).toBe(html)
  })

  it('never persists the load-failure placeholder either', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('', { status: 404 })))

    await observeHydrated('/tmp/page.html')

    const entry = get(artifacts)[0]
    expect(entry.loadFailed).toBe(true)
    // The save button is disabled for a failed entry; if a save fires anyway,
    // the placeholder note must not become a Light App.
    expect(lightAppSource(entry)).not.toContain('could not be loaded')
  })

  it('hands over the source for a non-HTML artifact', () => {
    const a = { type: 'Markdown', code: '# hi', preview: '<h1>hi</h1>' }
    expect(lightAppSource(a as any)).toBe('# hi')
  })

  it('falls back to the source when the preview is empty', () => {
    const a = { type: 'HTML', code: '<html><body>hi</body></html>', preview: '' }
    expect(lightAppSource(a as any)).toBe(a.code)
  })
})

describe('installArtifactThemeRefresh — theme switch busts baked previews', () => {
  it('drops loaded text previews and leaves image artifacts alone', async () => {
    const { installArtifactThemeRefresh } = await import('./artifacts')
    document.documentElement.setAttribute('data-theme', 'light')
    installArtifactThemeRefresh()
    artifacts.set([
      { id: 'a1', name: 'doc.md', path: '/tmp/doc.md', type: 'Markdown', icon: '',
        loaded: true, code: '# hi', preview: '<h1>hi</h1>' } as any,
      { id: 'a2', name: 'shot.png', path: '/tmp/shot.png', type: 'Image', icon: '',
        loaded: true, src: '/api/x.png', preview: '' } as any,
    ])

    document.documentElement.setAttribute('data-theme', 'dark')
    // MutationObserver delivers on a microtask; yield until it has run.
    await vi.waitFor(() => {
      expect(get(artifacts)[0].loaded).toBe(false)
    })
    expect(get(artifacts)[0].preview).toBe('')
    // The image entry keeps its src and stays loaded — hydrate would refuse
    // to rebuild it, so resetting it would strand a permanent spinner.
    expect(get(artifacts)[1].loaded).toBe(true)
    expect(get(artifacts)[1].src).toBe('/api/x.png')
  })
})

describe('pathIsInside — Light Apps directory detection', () => {
  const LA_DIR = '/Users/qiao/.octo/light-apps'

  it('matches files inside the directory, including nested ones', () => {
    expect(pathIsInside(`${LA_DIR}/my-app/index.html`, LA_DIR)).toBe(true)
    expect(pathIsInside(`${LA_DIR}/my-app/sub/asset.js`, LA_DIR)).toBe(true)
  })

  it('rejects siblings that merely share a prefix', () => {
    expect(pathIsInside('/Users/qiao/.octo/light-apps2/x.html', LA_DIR)).toBe(false)
    expect(pathIsInside('/Users/qiao/.octo/light-apps.js', LA_DIR)).toBe(false)
    expect(pathIsInside('/Users/qiao/.octo/light-apps-other/x.html', LA_DIR)).toBe(false)
  })

  it('rejects unrelated paths and relative paths', () => {
    expect(pathIsInside('/Users/qiao/Downloads/report.html', LA_DIR)).toBe(false)
    expect(pathIsInside('my-app/index.html', LA_DIR)).toBe(false)
    expect(pathIsInside('', LA_DIR)).toBe(false)
  })

  it('normalizes backslashes (Windows)', () => {
    expect(pathIsInside('C:\\Users\\qiao\\.octo\\light-apps\\my-app\\index.html', 'C:\\Users\\qiao\\.octo\\light-apps')).toBe(true)
    expect(pathIsInside('C:\\Users\\qiao\\.octo\\light-apps2\\x.html', 'C:\\Users\\qiao\\.octo\\light-apps')).toBe(false)
  })

  it('folds case so Windows drive letters and spelling differences match', () => {
    expect(pathIsInside('/Users/QIAO/.OCTO/Light-Apps/my-app/index.html', LA_DIR)).toBe(true)
    expect(pathIsInside('C:/Users/Qiao/.octo/light-apps/my-app/index.html', 'c:\\users\\qiao\\.octo\\light-apps')).toBe(true)
  })

  it('tolerates a trailing slash on either side', () => {
    expect(pathIsInside(`${LA_DIR}/my-app/index.html`, `${LA_DIR}/`)).toBe(true)
    expect(pathIsInside(`${LA_DIR}/my-app/index.html/`, LA_DIR)).toBe(true)
  })

  it('matches nothing when the directory is unknown', () => {
    expect(pathIsInside(`${LA_DIR}/my-app/index.html`, '')).toBe(false)
  })
})
