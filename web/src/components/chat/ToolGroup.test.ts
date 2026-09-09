// The terminal card's command block. The header line ellipsizes the command
// to one 11px line and terminal commands carry their meaning at the END
// (`cd <long path> && the-thing-that-matters`), so the expanded body has to
// carry the command itself — otherwise a card shows output with no way to
// tell what produced it.
//
// Render-level coverage is possible because vitest.config.ts sets
// resolve.conditions: ['browser'] — see src/lib/genui/components.test.ts.
import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { mount, unmount } from 'svelte'
import ToolGroup from './ToolGroup.svelte'

let target: HTMLElement
let app: Record<string, unknown> | null = null

beforeEach(() => {
  target = document.createElement('div')
  document.body.appendChild(target)
})

afterEach(() => {
  if (app) unmount(app)
  app = null
  target.remove()
})

function tool(over: Record<string, unknown> = {}) {
  return {
    id: 't1', toolId: 'tu1', name: 'terminal',
    args: { command: 'cd /very/long/path && git commit -m "x"' },
    summary: '', done: true, error: null, result: '', stdout: [], diff: null,
    ...over,
  }
}

function render(tools: Record<string, unknown>[]) {
  app = mount(ToolGroup, { target, props: { tools } })
  return target
}

const LONG = 'cd /very/long/path && git commit -m "x"'

describe('terminal command block', () => {
  it('shows the full command above the output', () => {
    const el = render([tool({ stdout: ['3 files changed'] })])
    const cmd = el.querySelector('.cmd-block .cmd-text')
    expect(cmd?.textContent).toBe(LONG)
    expect(el.querySelector('.terminal-output')?.textContent).toContain('3 files changed')
  })

  // A command that printed nothing falls through to a different body branch;
  // the command must survive that, since "what ran?" is then the only thing
  // the card can answer at all.
  it('shows the command when the command produced no output', () => {
    const el = render([tool({ stdout: [], result: '' })])
    expect(el.querySelector('.cmd-block .cmd-text')?.textContent).toBe(LONG)
  })

  it('shows the command when the tool errored', () => {
    const el = render([tool({ error: 'timed out', done: true })])
    expect(el.querySelector('.cmd-block .cmd-text')?.textContent).toBe(LONG)
    expect(el.querySelector('.error-output')?.textContent).toContain('timed out')
  })

  // args arrives as a JSON string over some paths and as an object over
  // others (see ChatView's tool_call handlers) — argSummary absorbs both.
  it('accepts args as a JSON string', () => {
    const el = render([tool({ args: JSON.stringify({ command: 'ls -la' }) })])
    expect(el.querySelector('.cmd-block .cmd-text')?.textContent).toBe('ls -la')
  })

  it('is terminal-only', () => {
    const el = render([tool({ name: 'read_file', args: { path: '/tmp/a.txt' } })])
    expect(el.querySelector('.cmd-block')).toBeNull()
  })

  // The output pre used to special-case a leading "$ " line as a prompt, but
  // nothing ever emits one — internal/tools/terminal.go streams raw output
  // lines only. Output that happens to start with "$ " is just output.
  it('does not treat a leading "$ " output line as a prompt', () => {
    const el = render([tool({ stdout: ['$ not a prompt'] })])
    const out = el.querySelector('.terminal-output')
    expect(out?.textContent).toContain('$ not a prompt')
    expect(out?.querySelector('.term-prompt')).toBeNull()
  })
})
