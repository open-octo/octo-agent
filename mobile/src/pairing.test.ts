import { describe, it, expect } from 'vitest'
import { parsePairingURL } from './pairing'

describe('parsePairingURL', () => {
  // relay is percent-encoded exactly as Go's url.Values.Encode() writes it.
  const good =
    'octo-pair://v1?relay=wss%3A%2F%2Frelay.octo.dev&tid=deadbeef&hk=QUJD%2B%2Fw%3D%3D&tok=tok123'

  it('parses a valid pairing URL and decodes the values', () => {
    const info = parsePairingURL(good)
    expect(info.relay).toBe('wss://relay.octo.dev')
    expect(info.tunnelId).toBe('deadbeef')
    expect(info.hostKey).toBe('QUJD+/w==') // base64 with +, /, = survives decode
    expect(info.token).toBe('tok123')
  })

  it('rejects a wrong scheme', () => {
    expect(() => parsePairingURL('https://v1?relay=a&tid=b&hk=c&tok=d')).toThrow(/scheme/)
  })

  it('rejects an unsupported version', () => {
    expect(() => parsePairingURL('octo-pair://v2?relay=a&tid=b&hk=c&tok=d')).toThrow(/version/)
  })

  it('rejects a missing field', () => {
    expect(() => parsePairingURL('octo-pair://v1?relay=a&tid=b&hk=c')).toThrow(/missing tok/)
  })

  it('rejects a non-URL', () => {
    expect(() => parsePairingURL('not a url')).toThrow()
  })
})
