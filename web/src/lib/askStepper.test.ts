import { describe, it, expect } from 'vitest'
import {
  advanceIndex,
  allAnswered,
  answerSummary,
  answersPayload,
  anyAnswered,
  emptyDraft,
  emptyDrafts,
  focusedPreview,
  hasReviewTab,
  isReviewTab,
  stepTab,
  tabCount,
  toggleChoice,
  usesPreviewLayout,
  type AskQuestion,
} from './askStepper'

const q = (over: Partial<AskQuestion> = {}): AskQuestion => ({
  question: 'Which one?',
  header: 'pick',
  options: [{ label: 'A' }, { label: 'B' }],
  ...over,
})

describe('usesPreviewLayout', () => {
  it('needs a preview and single-select', () => {
    expect(usesPreviewLayout(q())).toBe(false)
    expect(usesPreviewLayout(q({ options: [{ label: 'A', preview: 'x' }, { label: 'B' }] }))).toBe(true)
  })

  // Previews are single-select only, matching Claude Code — a multi-select
  // question stays in the flat layout even if the model sent previews.
  it('ignores previews on a multi-select question', () => {
    const multi = q({ multi_select: true, options: [{ label: 'A', preview: 'x' }] })
    expect(usesPreviewLayout(multi)).toBe(false)
  })

  it('tolerates a missing question', () => {
    expect(usesPreviewLayout(undefined)).toBe(false)
  })
})

describe('review tab', () => {
  it('is skipped for a lone single-select question', () => {
    expect(hasReviewTab([q()])).toBe(false)
    expect(tabCount([q()])).toBe(1)
  })

  // Toggling never advances, so a lone multi-select question needs a submit step.
  it('exists for a lone multi-select question', () => {
    expect(hasReviewTab([q({ multi_select: true })])).toBe(true)
    expect(tabCount([q({ multi_select: true })])).toBe(2)
  })

  it('exists whenever there are several questions', () => {
    expect(tabCount([q(), q()])).toBe(3)
    expect(isReviewTab([q(), q()], 2)).toBe(true)
    expect(isReviewTab([q(), q()], 1)).toBe(false)
  })
})

describe('navigation', () => {
  // Claude Code advances to the next index, not the next unanswered question.
  it('advances to the next index, then the review tab', () => {
    const qs = [q(), q()]
    expect(advanceIndex(qs, 0)).toBe(1)
    expect(advanceIndex(qs, 1)).toBe(2)
  })

  it('submits straight away when there is no review tab', () => {
    expect(advanceIndex([q()], 0)).toBe(-1)
  })

  it('wraps tabs in both directions', () => {
    const qs = [q(), q()] // 3 tabs incl. review
    expect(stepTab(qs, 0, 1)).toBe(1)
    expect(stepTab(qs, 2, 1)).toBe(0)
    expect(stepTab(qs, 0, -1)).toBe(2)
  })
})

describe('toggleChoice', () => {
  it('replaces the pick on a single-select question and clears free text', () => {
    let d = { ...emptyDraft(), custom: 'typed', otherOpen: true }
    d = toggleChoice(d, q(), 'A')
    expect(d.choices).toEqual(['A'])
    expect(d.custom).toBe('')
    expect(d.otherOpen).toBe(false)
    d = toggleChoice(d, q(), 'B')
    expect(d.choices).toEqual(['B'])
  })

  it('accumulates and removes on a multi-select question', () => {
    const multi = q({ multi_select: true, options: [{ label: 'A' }, { label: 'B' }] })
    let d = toggleChoice(emptyDraft(), multi, 'A')
    d = toggleChoice(d, multi, 'B')
    expect(d.choices).toEqual(['A', 'B'])
    d = toggleChoice(d, multi, 'A')
    expect(d.choices).toEqual(['B'])
  })
})

describe('answers', () => {
  it('summarises picks and free text together', () => {
    expect(answerSummary({ ...emptyDraft(), choices: ['A', 'B'] })).toBe('A, B')
    expect(answerSummary({ ...emptyDraft(), choices: ['A'], custom: ' mine ' })).toBe('A, mine')
    expect(answerSummary(undefined)).toBe('')
  })

  it('reports completeness across the set', () => {
    const qs = [q(), q()]
    const drafts = emptyDrafts(qs)
    expect(allAnswered(qs, drafts)).toBe(false)
    expect(anyAnswered(drafts)).toBe(false)
    drafts[0] = { ...drafts[0], choices: ['A'] }
    expect(anyAnswered(drafts)).toBe(true)
    expect(allAnswered(qs, drafts)).toBe(false)
    drafts[1] = { ...drafts[1], choices: ['B'] }
    expect(allAnswered(qs, drafts)).toBe(true)
  })

  // A note alone is worth submitting: the result reports it as
  // "(no option selected)" plus the note.
  it('counts a note as something to submit', () => {
    expect(anyAnswered([{ ...emptyDraft(), notes: 'hm' }])).toBe(true)
  })

  it('trims on the way to the wire and never sends a preview', () => {
    const payload = answersPayload([{ choices: ['A'], custom: ' x ', notes: ' n ', otherOpen: true }])
    expect(payload).toEqual([{ choices: ['A'], custom: 'x', notes: 'n' }])
    expect(payload[0]).not.toHaveProperty('preview')
  })
})

describe('focusedPreview', () => {
  it('returns the focused option preview, empty when it has none', () => {
    const withPreview = q({ options: [{ label: 'A', preview: 'left' }, { label: 'B' }] })
    expect(focusedPreview(withPreview, 'A')).toBe('left')
    expect(focusedPreview(withPreview, 'B')).toBe('')
    expect(focusedPreview(undefined, 'A')).toBe('')
  })
})
