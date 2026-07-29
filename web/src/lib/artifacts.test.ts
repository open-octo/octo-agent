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
  // Covers the chrome we generate, not references inside the artifact's own
  // body: a hand-written `![shot](shot.png)` in a markdown file still resolves
  // against the host page and still fails inside the iframe.
  it('builds the markdown preview without referencing /api/', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('# hello')))

    await observeArtifact(SID, payload('/tmp/notes.md'), false)

    const [entry] = get(artifacts)
    expect(entry.preview).not.toBe('')
    expect(entry.preview).not.toContain('/api/')
  })
})
