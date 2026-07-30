import { describe, it, expect, afterEach, vi } from 'vitest'
import { imagePreviewError } from './artifact-actions'

// A failed <img> fires onerror with no reason attached; imagePreviewError
// re-requests the src to read the endpoint's error body (#1896).

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('imagePreviewError', () => {
  it("returns the server's error body for a failing src", async () => {
    vi.stubGlobal('fetch', vi.fn(async () =>
      new Response(JSON.stringify({ error: 'artifact exceeds the 10 MB preview cap' }), { status: 413 }),
    ))

    expect(await imagePreviewError('/api/x')).toBe('artifact exceeds the 10 MB preview cap')
  })

  it('falls back to the status line when the body is not JSON', async () => {
    vi.stubGlobal('fetch', vi.fn(async () =>
      new Response('<html>gateway error</html>', { status: 502, statusText: 'Bad Gateway' }),
    ))

    expect(await imagePreviewError('/api/x')).toBe('502 Bad Gateway')
  })

  it('returns empty when the refetch succeeds (decode failure) or throws', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(new Uint8Array([1, 2, 3]))))
    expect(await imagePreviewError('/api/x')).toBe('')

    vi.stubGlobal('fetch', vi.fn(async () => { throw new TypeError('network down') }))
    expect(await imagePreviewError('/api/x')).toBe('')
  })
})
