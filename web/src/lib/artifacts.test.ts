import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { get } from 'svelte/store'
import { artifacts } from './stores'
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
      `/api/sessions/${encodeURIComponent(SID)}/artifacts?path=${encodeURIComponent('/tmp/shot.png')}`,
    )
    // The <img> pulls the bytes lazily; observing must not download them.
    expect(fetchMock).not.toHaveBeenCalled()
    // No sandboxed preview document at all, so nothing to 401.
    expect(entry.preview).toBe('')
    // `code` is the on-disk path — the one text form worth copying.
    expect(entry.code).toBe('/tmp/shot.png')
  })

  it('keeps transcript order during replay, since nothing awaits', async () => {
    vi.stubGlobal('fetch', vi.fn())

    await observeArtifact(SID, payload('/tmp/a.png'), false)
    await observeArtifact(SID, payload('/tmp/b.png'), false)

    expect(get(artifacts).map(a => a.path)).toEqual(['/tmp/a.png', '/tmp/b.png'])
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
      return new Response(png, { headers: { 'Content-Type': 'image/png' } })
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
