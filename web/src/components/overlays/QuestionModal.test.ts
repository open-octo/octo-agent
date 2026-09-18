// Fold/unfold coverage for the question banner. Possible because
// vitest.config.ts sets resolve.conditions: ['browser'] — see
// src/lib/genui/components.test.ts.
//
// The banner replaced an expand-into-modal layout, so its toggle is now the
// only thing standing between a pending question and a readable transcript.
// These pin the three states that used to be spread across two layouts.
import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { mount, unmount, flushSync } from 'svelte'
import QuestionModal from './QuestionModal.svelte'
import { questionModals, activeSessionId } from '../../lib/stores'
import { setLocale } from '../../lib/i18n'

let target: HTMLElement
let app: Record<string, unknown> | null = null

function entry(questionId: string) {
  return {
    questionId,
    sessionId: 's1',
    dismissed: false,
    questions: [{
      question: 'Change the upload params?',
      header: 'Plan',
      multi_select: false,
      options: [
        { label: 'Go ahead', description: 'Apply the plan as written' },
        { label: 'Hold off', description: 'Explain the details first' },
      ],
    }],
  }
}

const inner = () => target.querySelector('.banner-inner') as HTMLElement
const toggle = () => target.querySelector('.banner-toggle') as HTMLButtonElement
const rows = () => target.querySelectorAll('.row').length

function render() {
  app = mount(QuestionModal, { target }) as Record<string, unknown>
  flushSync()
}

beforeEach(() => {
  // jsdom stops at layout: the banner's option list resets its scroll offset
  // on every tab change, and Element.scrollTo is not implemented there.
  Element.prototype.scrollTo = () => {}
  setLocale('en')
  activeSessionId.set('s1')
  questionModals.set({ s1: entry('q1') as never })
  target = document.createElement('div')
  document.body.appendChild(target)
})

afterEach(() => {
  if (app) unmount(app)
  app = null
  target.remove()
  questionModals.set({})
  activeSessionId.set(null)
})

describe('question banner folding', () => {
  it('starts open and folds down to the question line', () => {
    render()
    expect(rows()).toBeGreaterThan(0)
    expect(target.querySelector('.banner-actions')).not.toBe(null)
    expect(toggle().getAttribute('aria-expanded')).toBe('true')

    toggle().click()
    flushSync()

    // Folded: the question line and its toggle survive, everything else goes.
    expect(rows()).toBe(0)
    expect(target.querySelector('.banner-actions')).toBe(null)
    expect(target.querySelector('.banner-question')?.textContent).toContain('Change the upload params?')
    expect(inner().classList.contains('collapsed')).toBe(true)
    expect(toggle().getAttribute('aria-expanded')).toBe('false')

    toggle().click()
    flushSync()
    expect(rows()).toBeGreaterThan(0)
  })

  // A question folded away must not swallow the NEXT one: the model would be
  // left waiting on an answer the user never saw a prompt for.
  it('unfolds when a new question arrives', () => {
    render()
    toggle().click()
    flushSync()
    expect(inner().classList.contains('collapsed')).toBe(true)

    questionModals.set({ s1: entry('q2') as never })
    flushSync()

    expect(inner().classList.contains('collapsed')).toBe(false)
    expect(rows()).toBeGreaterThan(0)
  })

  it('folds on Escape from inside the banner', () => {
    render()
    const row = target.querySelector('.row') as HTMLElement
    row.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    flushSync()

    expect(inner().classList.contains('collapsed')).toBe(true)
  })
})
