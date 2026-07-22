import { describe, it, expect } from 'vitest'
import { encodeFrame, decodeFrame, type ShimFrame } from './frames'

describe('frame codec', () => {
  const samples: ShimFrame[] = [
    { kind: 'http-req', id: '1', method: 'GET', path: '/api/sessions', headers: { accept: 'application/json' }, body: null },
    { kind: 'http-resp', id: '1', status: 200, headers: { 'content-type': 'application/json' }, body: '{"ok":true}' },
    { kind: 'ws-open', id: '2', path: '/ws' },
    { kind: 'ws-msg', id: '2', data: '{"type":"subscribe"}' },
    { kind: 'ws-close', id: '2', code: 1000, reason: 'bye' },
    { kind: 'ws-error', id: '2', message: 'dial failed' },
  ]

  it('round-trips every frame kind', () => {
    for (const f of samples) {
      expect(decodeFrame(encodeFrame(f))).toEqual(f)
    }
  })

  it('rejects invalid JSON', () => {
    expect(() => decodeFrame('{not json')).toThrow(/JSON/)
  })

  it('rejects an unknown kind', () => {
    expect(() => decodeFrame(JSON.stringify({ kind: 'bogus', id: '1' }))).toThrow(/unknown kind/)
  })

  it('rejects a missing id', () => {
    expect(() => decodeFrame(JSON.stringify({ kind: 'ws-msg', data: 'x' }))).toThrow(/missing id/)
  })
})
