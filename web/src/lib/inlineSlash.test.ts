import { describe, it, expect } from 'vitest'
import { inlineSlashCommand } from './inlineSlash'

describe('inlineSlashCommand', () => {
  it('recognises the no-arg commands, case-insensitively and around whitespace', () => {
    expect(inlineSlashCommand('/clear')).toBe('clear')
    expect(inlineSlashCommand('  /compact  ')).toBe('compact')
    expect(inlineSlashCommand('/RELOAD')).toBe('reload')
  })

  it('recognises /goal with and without arguments', () => {
    expect(inlineSlashCommand('/goal')).toBe('goal')
    expect(inlineSlashCommand('/goal ')).toBe('goal')
    expect(inlineSlashCommand('/goal ship the release')).toBe('goal')
    expect(inlineSlashCommand('/goal pause')).toBe('goal')
  })

  // The server matches /goal case-sensitively (the ToLower switch covers only
  // the no-arg three), so "/Goal x" falls through to the model there.
  it('leaves /goal case-sensitive, like the server', () => {
    expect(inlineSlashCommand('/Goal ship it')).toBeNull()
  })

  it('rejects anything the server would hand to the model', () => {
    expect(inlineSlashCommand('/goalpost matters')).toBeNull()
    expect(inlineSlashCommand('/clear the deck')).toBeNull()
    expect(inlineSlashCommand('/loop 5m check CI')).toBeNull()
    expect(inlineSlashCommand('what does /goal do?')).toBeNull()
    expect(inlineSlashCommand('')).toBeNull()
  })

  // Attachments take the message off the inline path server-side (the switch is
  // guarded on there being no blocks and no notes), so it runs as a real turn
  // and does get a history_user_message echo.
  it('is off when the message carries files', () => {
    expect(inlineSlashCommand('/goal ship it', true)).toBeNull()
    expect(inlineSlashCommand('/compact', true)).toBeNull()
  })
})
