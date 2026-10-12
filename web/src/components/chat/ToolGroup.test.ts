// The terminal card's command block. The header line ellipsizes the command
// to one 11px line and terminal commands carry their meaning at the END
// (`cd <long path> && the-thing-that-matters`), so the expanded body has to
// carry the command itself — otherwise a card shows output with no way to
// tell what produced it.
//
// Render-level coverage is possible because vitest.config.ts sets
// resolve.conditions: ['browser'] — see src/lib/genui/components.test.ts.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { mount, unmount } from 'svelte'
import { get } from 'svelte/store'
import ToolGroup from './ToolGroup.svelte'
import { toasts } from '../../lib/stores'

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

  // Every producer sends args as an object today (agent.Event.Input and
  // ContentBlock.Input are both map[string]any), but argSummary also accepts a
  // JSON string; this pins that branch so the body block survives either shape.
  it('accepts args as a JSON string', () => {
    const el = render([tool({ args: JSON.stringify({ command: 'ls -la' }) })])
    expect(el.querySelector('.cmd-block .cmd-text')?.textContent).toBe('ls -la')
  })

  it('is terminal-only', () => {
    const el = render([tool({ name: 'read_file', args: { path: '/tmp/a.txt' } })])
    expect(el.querySelector('.cmd-block')).toBeNull()
  })

  it('covers the bash alias too', () => {
    const el = render([tool({ name: 'bash', args: { command: 'ls -la' } })])
    expect(el.querySelector('.cmd-block .cmd-text')?.textContent).toBe('ls -la')
  })

  it('shows the command while the command is still running', () => {
    const el = render([tool({ done: false, stdout: ['building…'] })])
    expect(el.querySelector('.cmd-block .cmd-text')?.textContent).toBe(LONG)
    expect(el.querySelector('.blink-caret')).not.toBeNull()
  })

  // No command means no dark strip with a bare "$" in it.
  it('renders nothing when there is no command', () => {
    const el = render([tool({ args: {} })])
    expect(el.querySelector('.cmd-block')).toBeNull()
  })

  // A replayed turn carries no stdout — history sends tool_call + tool_result
  // only — so a finished terminal card renders from `result`. It has to stay on
  // the terminal surface, or the same card is dark live and light after reload.
  it('keeps replayed output on the terminal surface', () => {
    const el = render([tool({ stdout: [], result: '3 files changed' })])
    expect(el.querySelector('.terminal-output')?.textContent).toContain('3 files changed')
    expect(el.querySelector('.tool-output')).toBeNull()
  })

  it('leaves a non-terminal tool\'s result on the plain surface', () => {
    const el = render([tool({ name: 'read_file', args: { path: '/tmp/a.txt' }, result: 'hello' })])
    expect(el.querySelector('.tool-output')?.textContent).toContain('hello')
    expect(el.querySelector('.terminal-output')).toBeNull()
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

// jsdom ships no navigator.clipboard at all, which is also what a browser
// hands a page served over plain HTTP — so the failure path is the real
// out-of-the-box state, not a contrived one.
describe('terminal command copy button', () => {
  function stubClipboard(impl: () => Promise<void>) {
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText: vi.fn(impl) }, configurable: true, writable: true,
    })
    return (navigator.clipboard as any).writeText
  }

  afterEach(() => {
    Reflect.deleteProperty(navigator, 'clipboard')
    toasts.set([])
    vi.restoreAllMocks()
  })

  it('copies the command and acknowledges it', async () => {
    const writeText = stubClipboard(() => Promise.resolve())
    const el = render([tool()])
    const btn = el.querySelector('.cmd-copy') as HTMLButtonElement
    expect(btn.querySelector('iconify-icon')?.getAttribute('icon')).toBe('lucide:copy')

    btn.click()
    await vi.waitFor(() => {
      expect(btn.querySelector('iconify-icon')?.getAttribute('icon')).toBe('lucide:check')
    })
    expect(writeText).toHaveBeenCalledWith(LONG)
    expect(get(toasts)).toHaveLength(0)
  })

  it('toasts instead of throwing when the clipboard is unavailable', async () => {
    // No stubClipboard call: navigator.clipboard is undefined, so the handler
    // throws a TypeError on property access rather than rejecting a promise.
    const el = render([tool()])
    const btn = el.querySelector('.cmd-copy') as HTMLButtonElement

    btn.click()
    await vi.waitFor(() => expect(get(toasts)).toHaveLength(1))
    expect(get(toasts)[0].type).toBe('error')
    expect(btn.querySelector('iconify-icon')?.getAttribute('icon')).toBe('lucide:copy')
  })

  it('toasts when the clipboard write is denied', async () => {
    stubClipboard(() => Promise.reject(new Error('denied')))
    const el = render([tool()])
    ;(el.querySelector('.cmd-copy') as HTMLButtonElement).click()

    await vi.waitFor(() => expect(get(toasts)).toHaveLength(1))
    expect(get(toasts)[0].type).toBe('error')
  })
})

// The header count is derived from the result text, so a zero-hit grep's
// "(no matches)" sentinel used to read as "1 matches" (#2612).
describe('tool header count', () => {
  const meta = (over: Record<string, unknown>) =>
    render([tool(over)]).querySelector('.tool-submeta')?.textContent

  it('shows 0 matches for a grep with no hits', () => {
    expect(meta({ name: 'grep', args: { pattern: 'x' }, result: '(no matches)' })).toBe('0 matches')
  })

  it('uses the singular for one', () => {
    expect(meta({ name: 'grep', args: { pattern: 'x' }, result: 'a.go:1:x' })).toBe('1 match')
  })

  // The cap marker isn't a match, and the real total is in it.
  it('reports the full total for a truncated grep', () => {
    const res = 'a.go:1:x\nb.go:2:x\n\n[truncated to first 2 of 812 lines — narrow the pattern]'
    expect(meta({ name: 'grep', args: { pattern: 'x' }, result: res })).toBe('812 matches')
  })

  it('counts files in files_with_matches mode', () => {
    expect(meta({ name: 'grep', args: { pattern: 'x', mode: 'files_with_matches' }, result: 'a.go\nb.go' })).toBe('2 files')
  })

  it('sums the per-file tallies in count mode', () => {
    expect(meta({ name: 'grep', args: { pattern: 'x', mode: 'count' }, result: 'a.go:3\nb.go:1' })).toBe('4 matches')
  })

  // A single-file count search prints the bare number, no path.
  it('reads a bare tally in count mode', () => {
    expect(meta({ name: 'grep', args: { pattern: 'x', mode: 'count', path: 'a.go' }, result: '3' })).toBe('3 matches')
  })

  it('reports lines, not matches, when context is on', () => {
    const res = 'a.go:1:x\na.go-2-y\n--\nb.go-1-z\nb.go:2:x'
    expect(meta({ name: 'grep', args: JSON.stringify({ pattern: 'x', context_lines: 1 }), result: res })).toBe('4 lines')
  })

  it('keeps the plural for many', () => {
    expect(meta({ name: 'read_file', args: { path: '/tmp/a' }, result: 'a\nb' })).toBe('2 lines')
  })
})
