import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { get } from 'svelte/store'
import { artifacts, artifactSel, panelContent, panelExpanded } from './stores'
import { observeArtifact, hydrateArtifact, resetArtifacts, markArtifactOriginUnavailable, probeArtifactOrigin } from './artifacts'
import type { Artifact } from './types'

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
    // empty body (copy, download).
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

// Source, config, and data files are not artifacts: the panel lists only what
// it can render, and the Git Diff mode is where code changes are shown.
describe('observeArtifact — source files', () => {
  it('ignores source, config, and data file writes', () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)

    for (const p of ['/tmp/script.py', '/tmp/main.go', '/tmp/app.js', '/tmp/config.json', '/tmp/data.csv', '/tmp/notes.txt']) {
      observeArtifact(SID, { type: 'write', path: p }, true)
    }

    // Nothing lands in the list, nothing is fetched, and the panel must not
    // pop open on a source-file write.
    expect(get(artifacts)).toEqual([])
    expect(fetchMock).not.toHaveBeenCalled()
    expect(get(panelContent)).toBe(null)

    // And an ignored write must not consume the once-per-session flag: a
    // later rich-kind artifact still auto-opens.
    observeArtifact(SID, { type: 'write', path: '/tmp/report.md' }, true)
    expect(get(panelContent)).toBe('session')
  })
})

// The Light App view renders through the same rule as the artifact preview,
// via this one helper — a page the preview strips must not come alive once it
// is saved and opened as a Light App.
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
// HTML artifacts render from the artifact origin: hydration asks the server
// for a grant instead of building a preview document, and nothing beside the
// page is ever fetched by the host — the origin serves those files to the
// frame directly.
describe('hydrateArtifact — html artifacts on the artifact origin', () => {
  const html = '<h1>hi</h1><img src="chart.png">'

  function stubFetch(grantStatus = 200) {
    const fetchMock = vi.fn(async (u: string) => {
      if (u.endsWith('/artifacts/grant')) {
        if (grantStatus !== 200) {
          return new Response(JSON.stringify({ error: 'artifact origin unavailable' }), { status: grantStatus })
        }
        return new Response(JSON.stringify({ url: 'http://tok.artifacts.localhost:8088/', expires_at: '2026-01-01T00:00:00Z' }))
      }
      return new Response(html)
    })
    vi.stubGlobal('fetch', fetchMock)
    return fetchMock
  }

  function grantCalls(fetchMock: ReturnType<typeof vi.fn>) {
    return fetchMock.mock.calls.filter(c => String(c[0]).endsWith('/artifacts/grant'))
  }

  it('records the granted origin URL and builds no preview document', async () => {
    const fetchMock = stubFetch()

    await observeHydrated('/tmp/page.html')

    const [entry] = get(artifacts)
    expect(entry.loaded).toBe(true)
    expect(entry.originURL).toBe('http://tok.artifacts.localhost:8088/')
    expect(entry.originUnavailable).toBeFalsy()
    expect(entry.preview).toBe('')
    expect(entry.code).toBe(html)
    expect(entry.rev).toBe(1)
    const [grant] = grantCalls(fetchMock)
    expect(grant[0]).toBe(`/api/sessions/${SID}/artifacts/grant`)
    expect((grant[1] as RequestInit).method).toBe('POST')
    expect((grant[1] as RequestInit).body).toBe(JSON.stringify({ path: '/tmp/page.html' }))
    expect(fetchMock.mock.calls.some(c => String(c[0]).includes('chart.png'))).toBe(false)
  })

  it('marks the origin unavailable when the server refuses the grant, keeping the code view', async () => {
    stubFetch(409)

    await observeHydrated('/tmp/page.html')

    const [entry] = get(artifacts)
    expect(entry.loaded).toBe(true)
    expect(entry.loadFailed).toBeFalsy()
    expect(entry.originUnavailable).toBe(true)
    expect(entry.originURL).toBeUndefined()
    expect(entry.code).toBe(html)
  })

  it('bumps rev on a re-write so the frame reloads the unchanged origin', () => {
    stubFetch()
    observeArtifact(SID, payload('/tmp/page.html'), false)
    observeArtifact(SID, payload('/tmp/page.html'), false)
    expect(get(artifacts)).toHaveLength(1)
    expect(get(artifacts)[0].rev).toBe(2)
  })

  it('a frame that finds the origin unreachable flips only that entry; later artifacts still ask', async () => {
    const fetchMock = stubFetch()
    await observeHydrated('/tmp/page.html')

    markArtifactOriginUnavailable(get(artifacts)[0])

    expect(get(artifacts)[0].originUnavailable).toBe(true)
    expect(get(artifacts)[0].originURL).toBeUndefined()
    const before = grantCalls(fetchMock).length
    await observeHydrated('/tmp/other.html')
    // A transient failure must not lock the whole page into the notice.
    expect(grantCalls(fetchMock).length).toBe(before + 1)
    expect(get(artifacts).at(-1)!.originURL).toBe('http://tok.artifacts.localhost:8088/')
  })

  it('probes an origin URL once, and marks the entry when the host does not resolve', async () => {
    const probe = vi.fn(async (url: string) => {
      if (url.startsWith('http://dead.')) throw new TypeError('Failed to fetch')
      return new Response(null, { status: 200 })
    })
    vi.stubGlobal('fetch', probe)
    const live = { path: '/tmp/a.html', originURL: 'http://live.artifacts.localhost:8088/' } as Artifact
    const dead = { path: '/tmp/b.html', originURL: 'http://dead.artifacts.localhost:8088/' } as Artifact
    artifacts.set([live, dead])

    probeArtifactOrigin(live)
    probeArtifactOrigin(live) // a theme switch or re-render: no second fetch
    probeArtifactOrigin(dead)
    await new Promise(r => setTimeout(r, 0))

    expect(probe.mock.calls.filter(c => String(c[0]).startsWith('http://live.')).length).toBe(1)
    expect(get(artifacts)[0].originUnavailable).toBeFalsy()
    expect(get(artifacts)[1].originUnavailable).toBe(true)
    expect(get(artifacts)[1].originURL).toBeUndefined()
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
      { id: 'a3', name: 'page.html', path: '/tmp/page.html', type: 'HTML', icon: '',
        loaded: true, code: '<h1>hi</h1>', preview: '', originURL: 'http://t.artifacts.localhost:8088/' } as any,
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
    // An HTML entry on the artifact origin has no baked theme: the frame
    // passes the theme in the URL, so nothing here needs rebuilding.
    expect(get(artifacts)[2].loaded).toBe(true)
    expect(get(artifacts)[2].originURL).toBe('http://t.artifacts.localhost:8088/')
  })
})
