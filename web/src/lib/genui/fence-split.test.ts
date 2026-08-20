import { describe, it, expect } from 'vitest'
import { splitOctoUiFences } from './fence-split'

const VALID_BODY = '{"items":[{"type":"text","text":"hi"}]}'

describe('splitOctoUiFences: no-op path', () => {
  it('returns exactly one markdown segment, byte-identical to the input, when there is no fence', () => {
    // Every pre-existing message in every pre-existing session must take
    // this path unchanged.
    const text = 'Just plain markdown **bold** text with no fence at all.\n\nSecond paragraph.'
    expect(splitOctoUiFences(text)).toEqual([{ kind: 'markdown', text }])
  })

  it('is a no-op for empty text', () => {
    expect(splitOctoUiFences('')).toEqual([])
  })

  it('does not treat an ordinary ```json fence as an octo-ui fence', () => {
    const text = 'before\n```json\n{"a":1}\n```\nafter'
    expect(splitOctoUiFences(text)).toEqual([{ kind: 'markdown', text }])
  })
})

describe('splitOctoUiFences: a complete fence surrounded by text', () => {
  it('splits into markdown / octo-ui / markdown, in order', () => {
    const text = `before\n\`\`\`octo-ui\n${VALID_BODY}\n\`\`\`\nafter`
    const segs = splitOctoUiFences(text)
    expect(segs).toHaveLength(3)
    expect(segs[0].kind).toBe('markdown')
    expect(segs[1].kind).toBe('octo-ui')
    expect(segs[2].kind).toBe('markdown')

    const md0 = segs[0] as { kind: 'markdown'; text: string }
    const ui = segs[1] as { kind: 'octo-ui'; raw: string; complete: boolean; spec: unknown }
    const md2 = segs[2] as { kind: 'markdown'; text: string }
    expect(md0.text).toBe('before\n')
    expect(ui.complete).toBe(true)
    expect(ui.spec).toEqual({ items: [{ type: 'text', text: 'hi' }] })
    expect(md2.text).toBe('after')

    // Reassembling the markdown/raw text of every segment recovers the
    // original message losslessly (modulo the fence delimiters themselves).
    expect(md0.text + '```octo-ui\n' + ui.raw + '```\n' + md2.text).toBe(text)
  })

  it('handles a fence with no surrounding text at all', () => {
    const text = `\`\`\`octo-ui\n${VALID_BODY}\n\`\`\``
    const segs = splitOctoUiFences(text)
    expect(segs).toHaveLength(1)
    expect(segs[0]).toMatchObject({ kind: 'octo-ui', complete: true })
    expect((segs[0] as any).spec).toEqual({ items: [{ type: 'text', text: 'hi' }] })
  })
})

describe('splitOctoUiFences: multiple complete fences', () => {
  it('yields both fences in order with the markdown between/around them', () => {
    const bodyA = '{"items":[{"type":"text","text":"a"}]}'
    const bodyB = '{"items":[{"type":"stat","label":"Revenue","value":"$1"}]}'
    const text = `intro\n\`\`\`octo-ui\n${bodyA}\n\`\`\`\nmiddle\n\`\`\`octo-ui\n${bodyB}\n\`\`\`\noutro`
    const segs = splitOctoUiFences(text)
    const kinds = segs.map((s) => s.kind)
    expect(kinds).toEqual(['markdown', 'octo-ui', 'markdown', 'octo-ui', 'markdown'])
    expect((segs[1] as any).spec).toEqual({ items: [{ type: 'text', text: 'a' }] })
    expect((segs[3] as any).spec).toEqual({ items: [{ type: 'stat', label: 'Revenue', value: '$1' }] })
    expect((segs[0] as any).text).toBe('intro\n')
    expect((segs[2] as any).text).toBe('middle\n')
    expect((segs[4] as any).text).toBe('outro')
  })
})

describe('splitOctoUiFences: unclosed trailing fence (streaming)', () => {
  it('yields one markdown segment plus one incomplete octo-ui segment', () => {
    const text = 'intro\n```octo-ui\n{"items":[{"type":"text","text":"a"}'
    const segs = splitOctoUiFences(text)
    expect(segs).toHaveLength(2)
    expect(segs[0]).toEqual({ kind: 'markdown', text: 'intro\n' })
    expect(segs[1].kind).toBe('octo-ui')
    expect((segs[1] as any).complete).toBe(false)
  })

  it('recovers a partial spec from a prefix that already has one complete node', () => {
    const text = '```octo-ui\n{"items":[{"type":"text","text":"a"},{"type":"text","text":"b'
    const segs = splitOctoUiFences(text)
    expect(segs).toHaveLength(1)
    const ui = segs[0] as any
    expect(ui.complete).toBe(false)
    expect(ui.spec).toEqual({ items: [{ type: 'text', text: 'a' }] })
  })

  it('yields spec: null when nothing in the buffer is safe to render yet', () => {
    const text = '```octo-ui\n{"items":[{"type":"text","text":"a"'
    const segs = splitOctoUiFences(text)
    const ui = segs[0] as any
    expect(ui.complete).toBe(false)
    expect(ui.spec).toBeNull()
  })
})

describe('splitOctoUiFences: malformed content inside a closed fence', () => {
  it('yields spec: null, complete: true for malformed JSON', () => {
    const text = '```octo-ui\nnot json\n```'
    const segs = splitOctoUiFences(text)
    expect(segs).toHaveLength(1)
    expect(segs[0]).toMatchObject({ kind: 'octo-ui', complete: true, spec: null })
  })

  it('yields spec: null for valid JSON the guard entirely rejects (no items array)', () => {
    const text = '```octo-ui\n{}\n```'
    const segs = splitOctoUiFences(text)
    expect(segs).toHaveLength(1)
    expect(segs[0]).toMatchObject({ kind: 'octo-ui', complete: true, spec: null })
  })
})
