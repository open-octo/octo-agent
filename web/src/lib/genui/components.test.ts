// Render-level coverage for the GenUI component tree. These are possible
// because vitest.config.ts sets resolve.conditions: ['browser'] — without it
// Vitest resolves Svelte's server build and mount() throws
// lifecycle_function_unavailable.
import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { mount, unmount, flushSync } from 'svelte'
import GenuiBlock from '../../components/genui/GenuiBlock.svelte'
import type { GenuiSpec } from './types'

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

function render(spec: GenuiSpec) {
  app = mount(GenuiBlock, { target, props: { spec } })
  flushSync()
  return target
}

describe('conditional visibility', () => {
  it('hides a node until its field matches', () => {
    const el = render({
      items: [
        { type: 'select', field: 'mode', options: [{ label: 'Basic', value: 'basic' }, { label: 'Advanced', value: 'advanced' }] },
        { type: 'text', text: 'expert options', visibleWhen: { field: 'mode', equals: 'advanced' } },
      ],
    })
    // The select seeds itself with its first option, so the gated text is out.
    expect(el.textContent).not.toContain('expert options')

    const select = el.querySelector('select') as HTMLSelectElement
    select.value = 'advanced'
    select.dispatchEvent(new Event('change', { bubbles: true }))
    flushSync()
    expect(el.textContent).toContain('expert options')
  })

  it('keeps a range-gated node hidden until the slider is moved', () => {
    const el = render({
      items: [
        { type: 'slider', field: 'n', min: 0, max: 100 },
        { type: 'text', text: 'over fifty', visibleWhen: { field: 'n', gt: 50 } },
      ],
    })
    // A slider reports its seeded value on mount, which is min — so the node
    // is hidden, but for the right reason rather than by failing to evaluate.
    expect(el.textContent).not.toContain('over fifty')

    const slider = el.querySelector('input[type=range]') as HTMLInputElement
    slider.value = '80'
    slider.dispatchEvent(new Event('input', { bubbles: true }))
    flushSync()
    expect(el.textContent).toContain('over fifty')
  })
})

describe('collapsible', () => {
  it('starts folded and toggles', () => {
    const el = render({
      items: [{ type: 'collapsible', title: 'details', children: [{ type: 'text', text: 'inner' }] }],
    })
    expect(el.textContent).toContain('details')
    expect(el.textContent).not.toContain('inner')

    const head = el.querySelector('.genui-collapsible-head') as HTMLButtonElement
    head.click()
    flushSync()
    expect(el.textContent).toContain('inner')
  })

  it('honours a declared open state', () => {
    const el = render({
      items: [{ type: 'collapsible', title: 'd', open: true, children: [{ type: 'text', text: 'inner' }] }],
    })
    expect(el.textContent).toContain('inner')
  })
})

describe('code', () => {
  it('highlights a registered language', () => {
    const el = render({ items: [{ type: 'code', code: 'const x = 1', lang: 'javascript' }] })
    // highlight.js wraps tokens in spans; the source text survives either way.
    expect(el.querySelector('.genui-code')?.textContent).toContain('const x = 1')
    expect(el.querySelector('.genui-code span')).not.toBeNull()
  })

  it('falls back to plain text for an unregistered language', () => {
    const el = render({ items: [{ type: 'code', code: 'SELECT 1', lang: 'brainfuck' }] })
    expect(el.querySelector('.genui-code')?.textContent).toContain('SELECT 1')
    expect(el.querySelector('.genui-code span')).toBeNull()
  })

  it('never renders model text as markup', () => {
    const el = render({ items: [{ type: 'code', code: '<img src=x onerror=alert(1)>', lang: 'javascript' }] })
    expect(el.querySelector('.genui-code img')).toBeNull()
    expect(el.querySelector('.genui-code')?.textContent).toContain('<img src=x onerror=alert(1)>')
  })
})

describe('quiz', () => {
  it('scores locally and locks after answering', () => {
    const el = render({
      items: [
        {
          type: 'quiz',
          field: 'q1',
          question: 'pick b',
          options: [{ label: 'A', value: 'a' }, { label: 'B', value: 'b' }],
          correct: 'b',
          explanation: 'because b',
        },
      ],
    })
    const buttons = Array.from(el.querySelectorAll('.genui-quiz-option')) as HTMLButtonElement[]
    buttons[1].click()
    flushSync()
    expect(el.textContent).toContain('because b')
    expect(el.querySelector('.genui-quiz-verdict')?.classList.contains('right')).toBe(true)
    // Locked: every option is disabled once answered.
    expect(buttons.every(b => b.disabled)).toBe(true)
  })

  it('marks a wrong answer without hiding the right one', () => {
    const el = render({
      items: [
        {
          type: 'quiz',
          field: 'q1',
          question: 'pick b',
          options: [{ label: 'A', value: 'a' }, { label: 'B', value: 'b' }],
          correct: 'b',
        },
      ],
    })
    ;(el.querySelectorAll('.genui-quiz-option')[0] as HTMLButtonElement).click()
    flushSync()
    expect(el.querySelector('.genui-quiz-verdict')?.classList.contains('right')).toBe(false)
    expect(el.querySelector('.genui-quiz-option.right')).not.toBeNull()
  })
})

describe('table', () => {
  it('filters from a field and sorts on header click', () => {
    const el = render({
      items: [
        { type: 'input', field: 'q' },
        {
          type: 'table',
          columns: ['name', 'n'],
          rows: [['alpha', 3], ['beta', 1], ['alphabet', 2]],
          filterBy: { field: 'q', column: 'name' },
          sortable: true,
        },
      ],
    })
    expect(el.querySelectorAll('tbody tr').length).toBe(3)

    const input = el.querySelector('input[type=text]') as HTMLInputElement
    input.value = 'alpha'
    input.dispatchEvent(new Event('input', { bubbles: true }))
    flushSync()
    expect(el.querySelectorAll('tbody tr').length).toBe(2)

    const header = el.querySelectorAll('th')[1] as HTMLElement
    header.click()
    flushSync()
    const firstCell = el.querySelector('tbody tr td:nth-child(2)')?.textContent
    expect(firstCell).toBe('2')
  })
})

describe('plot', () => {
  it('draws one polyline per series and a legend when there are several', () => {
    const el = render({
      items: [
        {
          type: 'plot',
          plot: 'line',
          series: [
            { name: 'a', points: [{ label: 'x', value: 1 }, { label: 'y', value: 2 }] },
            { name: 'b', points: [{ label: 'x', value: 2 }, { label: 'y', value: 1 }] },
          ],
        },
      ],
    })
    expect(el.querySelectorAll('polyline').length).toBe(2)
    expect(el.textContent).toContain('a')
    expect(el.textContent).toContain('b')
  })

  it('breaks a line at a gap instead of drawing through zero', () => {
    const el = render({
      items: [
        {
          type: 'plot',
          plot: 'line',
          series: [{ points: [{ label: 'x', value: 1 }, { label: 'z', value: 3 }] }, { points: [{ label: 'y', value: 2 }] }],
        },
      ],
    })
    // Series one has no point at 'y', so its single run is x…z with a hole:
    // two separate segments rather than one continuous line through zero.
    const lines = el.querySelectorAll('polyline')
    expect(lines.length).toBeGreaterThanOrEqual(2)
  })

  it('renders a pie as arcs', () => {
    const el = render({
      items: [{ type: 'plot', plot: 'pie', series: [{ points: [{ label: 'a', value: 1 }, { label: 'b', value: 1 }] }] }],
    })
    expect(el.querySelectorAll('path').length).toBe(2)
  })
})

describe('link', () => {
  it('opens in a new tab without handing over window.opener', () => {
    const el = render({ items: [{ type: 'link', text: 'docs', href: 'https://a.test/x' }] })
    const a = el.querySelector('a.genui-link') as HTMLAnchorElement
    expect(a.getAttribute('href')).toBe('https://a.test/x')
    expect(a.getAttribute('target')).toBe('_blank')
    // Without noopener the opened page can navigate this one through opener.
    expect(a.getAttribute('rel')).toContain('noopener')
    expect(a.textContent).toContain('docs')
  })
})

describe('divider', () => {
  it('renders a rule', () => {
    const el = render({ items: [{ type: 'divider' }] })
    expect(el.querySelector('hr.genui-divider')).not.toBeNull()
  })
})
