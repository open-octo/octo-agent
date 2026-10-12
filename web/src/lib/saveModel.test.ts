import { describe, it, expect, afterEach, vi } from 'vitest'
import { saveModel } from './api'

// saveModel backs the first-run wizard. A Custom model's context window has to
// reach the endpoint on both of its paths — a fresh endpoint (create) and a
// re-run over an existing one (add-model upsert) — and an unset window must be
// omitted so a re-run keeps one already in config.yml.

type Call = { url: string; method: string; body: any }

function stubServer(existing: string[]): Call[] {
  const calls: Call[] = []
  vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
    const method = init?.method ?? 'GET'
    calls.push({ url, method, body: init?.body ? JSON.parse(String(init.body)) : undefined })
    const payload = url === '/api/config/endpoints' && method === 'GET'
      ? { endpoints: existing.map((id) => ({ id, provider: 'custom', base_url: 'http://x.example/v1', models: [] })) }
      : {}
    return new Response(JSON.stringify(payload), { status: 200 })
  }))
  return calls
}

const req = { model: 'big-model', base_url: 'http://x.example/v1', provider: 'custom', vision: true }

afterEach(() => vi.unstubAllGlobals())

describe('saveModel context window', () => {
  it('sends the window with the initial model of a new endpoint', async () => {
    const calls = stubServer([])
    await saveModel({ ...req, context_window: 1_000_000 })
    const create = calls.find((c) => c.url === '/api/config/endpoints' && c.method === 'POST')
    expect(create?.body.models).toEqual([{ model: 'big-model', vision: true, context_window: 1_000_000 }])
  })

  it('sends the window when re-running over an existing endpoint', async () => {
    const calls = stubServer(['custom'])
    await saveModel({ ...req, context_window: 200_000 })
    const add = calls.find((c) => c.url === '/api/config/endpoints/custom/models')
    expect(add?.body).toEqual({ model: 'big-model', vision: true, context_window: 200_000 })
  })

  it('omits an unset window on both paths', async () => {
    let calls = stubServer([])
    await saveModel(req)
    const create = calls.find((c) => c.url === '/api/config/endpoints' && c.method === 'POST')
    expect(create?.body.models[0]).not.toHaveProperty('context_window')

    calls = stubServer(['custom'])
    await saveModel(req)
    const add = calls.find((c) => c.url === '/api/config/endpoints/custom/models')
    expect(add?.body).not.toHaveProperty('context_window')
  })
})
