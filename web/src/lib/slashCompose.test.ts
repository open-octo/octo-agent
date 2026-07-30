import { describe, it, expect } from 'vitest'
import { composeSlashCommand } from './slashCompose'

describe('composeSlashCommand', () => {
  it('returns the command unchanged when there is no draft', () => {
    expect(composeSlashCommand('/code-review ', '')).toBe('/code-review ')
    expect(composeSlashCommand('/code-review ', '   ')).toBe('/code-review ')
  })

  it('keeps the draft as the command argument', () => {
    expect(composeSlashCommand('/code-review ', 'check the diff')).toBe('/code-review check the diff')
  })

  it('separates command and draft when the command has no trailing space', () => {
    expect(composeSlashCommand('Run the "deploy" workflow', 'to staging')).toBe('Run the "deploy" workflow to staging')
  })

  it('trims the draft', () => {
    expect(composeSlashCommand('/goal ', '  ship it  ')).toBe('/goal ship it')
  })
})
