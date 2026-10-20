import { describe, it, expect } from 'vitest'
import { parseConnectLink } from './connection'

describe('parseConnectLink', () => {
  it('parses the bootstrap link octo serve prints', () => {
    expect(parseConnectLink('http://192.168.1.5:8088/?access_key=abc%2B1')).toEqual({
      base: 'http://192.168.1.5:8088',
      key: 'abc+1',
    })
  })

  it('keeps https and drops any path', () => {
    expect(parseConnectLink(' https://box.example.ts.net/chat?access_key=k ')).toEqual({
      base: 'https://box.example.ts.net',
      key: 'k',
    })
  })

  it('takes a bare address as http', () => {
    expect(parseConnectLink('100.64.0.5:8088/?access_key=k').base).toBe('http://100.64.0.5:8088')
  })

  it('rejects a link without a key', () => {
    expect(() => parseConnectLink('http://192.168.1.5:8088/')).toThrow(/access_key/)
  })

  it('rejects other schemes', () => {
    expect(() => parseConnectLink('ftp://host/?access_key=k')).toThrow(/scheme/)
  })
})
