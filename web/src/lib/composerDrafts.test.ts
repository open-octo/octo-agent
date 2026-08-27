import { describe, it, expect } from 'vitest'
import { parkDraft, takeDraft, patchParkedAttachments, type Attachment } from './composerDrafts'

const file = (id: string, over: Partial<Attachment> = {}): Attachment =>
  ({ id, name: `${id}.pdf`, ...over })

describe('composerDrafts', () => {
  it('hands a parked draft back to whoever takes it', () => {
    parkDraft('s1', 'half a sentence', [file('a')])
    const d = takeDraft('s1')
    expect(d.text).toBe('half a sentence')
    expect(d.attachments.map(a => a.id)).toEqual(['a'])
  })

  it('keeps sessions apart', () => {
    parkDraft('s1', 'for one', [])
    parkDraft('s2', 'for two', [])
    expect(takeDraft('s2').text).toBe('for two')
    expect(takeDraft('s1').text).toBe('for one')
  })

  it('reads a session that parked nothing as an empty draft', () => {
    const d = takeDraft('never-seen')
    expect(d.text).toBe('')
    expect(d.attachments).toEqual([])
  })

  // The composer takes ownership on mount; leaving a copy behind would let a
  // message that was typed in a not-yet-created chat ('') and then sent come
  // back in the next new chat.
  it('removes the draft it hands out', () => {
    parkDraft('', 'sent already', [file('a')])
    takeDraft('')
    expect(takeDraft('')).toEqual({ text: '', attachments: [] })
  })

  it('stores nothing for an empty box, and clears what was there', () => {
    parkDraft('s1', 'typed', [file('a')])
    parkDraft('s1', '', [])
    expect(takeDraft('s1')).toEqual({ text: '', attachments: [] })
  })

  it('keeps a draft that is attachments only', () => {
    parkDraft('s1', '', [file('a')])
    expect(takeDraft('s1').attachments.map(a => a.id)).toEqual(['a'])
  })

  describe('patchParkedAttachments', () => {
    // An upload that resolves after the composer is unmounted: the placeholder
    // must clear, or the chip comes back with no remove button and send()
    // refuses to send past it.
    it('lands a late upload result on the parked draft', () => {
      parkDraft('s1', 'note', [file('a', { uploading: true })])
      patchParkedAttachments('s1', list =>
        list.map(a => a.id === 'a' ? { ...a, path: '/api/uploads/a.pdf', uploading: false } : a))
      const d = takeDraft('s1')
      expect(d.text).toBe('note')
      expect(d.attachments[0]).toMatchObject({ path: '/api/uploads/a.pdf', uploading: false })
    })

    it('lands a late failure by dropping the placeholder', () => {
      parkDraft('s1', 'note', [file('a', { uploading: true }), file('b')])
      patchParkedAttachments('s1', list => list.filter(a => a.id !== 'a'))
      expect(takeDraft('s1').attachments.map(a => a.id)).toEqual(['b'])
    })

    it('drops the entry when the patch empties it', () => {
      parkDraft('s1', '', [file('a', { uploading: true })])
      patchParkedAttachments('s1', list => list.filter(a => a.id !== 'a'))
      expect(takeDraft('s1')).toEqual({ text: '', attachments: [] })
    })

    it('ignores a session with nothing parked', () => {
      expect(() => patchParkedAttachments('gone', list => [...list, file('a')])).not.toThrow()
      expect(takeDraft('gone').attachments).toEqual([])
    })
  })
})
