// Logic behind the ask_user_question picker, kept out of the components so it
// can be unit-tested the way the rest of web/src/lib is. The components stay
// presentational: they render what these functions decide.
//
// The model mirrors Claude Code's picker. A question set is navigated as tabs
// with a review/submit tab, and each question renders in one of two mutually
// exclusive layouts — see usesPreviewLayout.

export interface AskOption {
  label: string
  description?: string
  preview?: string
}

export interface AskQuestion {
  question: string
  header: string
  multi_select?: boolean
  options?: AskOption[]
}

/** One question's in-progress answer. Drafts live client-side until submit. */
export interface AskDraft {
  choices: string[]
  custom: string
  notes: string
  /** True while the "Other" row's input is revealed. */
  otherOpen: boolean
}

export type AskOutcome = 'submitted' | 'clarify' | 'rejected'

/** What one answer looks like on the wire. Preview is filled server-side. */
export interface AskAnswerPayload {
  choices: string[]
  custom: string
  notes: string
}

export function emptyDraft(): AskDraft {
  return { choices: [], custom: '', notes: '', otherOpen: false }
}

export function emptyDrafts(questions: AskQuestion[]): AskDraft[] {
  return questions.map(() => emptyDraft())
}

/**
 * A question uses the preview layout when it is single-select and at least one
 * option carries a preview. That layout REPLACES the flat one: no "Other" row
 * (its text slot holds notes instead) and no descriptions (the preview stands
 * in for them).
 */
export function usesPreviewLayout(q: AskQuestion | undefined): boolean {
  if (!q || q.multi_select) return false
  return (q.options ?? []).some(o => !!o.preview)
}

/**
 * A lone single-select question needs no review tab: picking an option IS the
 * submission. A lone multi-select one does — toggling never advances.
 */
export function hasReviewTab(questions: AskQuestion[]): boolean {
  if (questions.length === 1) return !!questions[0]?.multi_select
  return questions.length > 1
}

export function tabCount(questions: AskQuestion[]): number {
  return questions.length + (hasReviewTab(questions) ? 1 : 0)
}

export function isReviewTab(questions: AskQuestion[], index: number): boolean {
  return index >= questions.length
}

/** Where picking on question `index` lands: the next index, then the review tab. */
export function advanceIndex(questions: AskQuestion[], index: number): number {
  const next = index + 1
  if (next < questions.length) return next
  return hasReviewTab(questions) ? questions.length : -1 // -1 = submit now
}

export function stepTab(questions: AskQuestion[], index: number, delta: number): number {
  const n = tabCount(questions)
  return (index + delta + n) % n
}

/** Toggling a choice: single-select replaces, multi-select accumulates. */
export function toggleChoice(draft: AskDraft, q: AskQuestion, label: string): AskDraft {
  if (q.multi_select) {
    const has = draft.choices.includes(label)
    return {
      ...draft,
      choices: has ? draft.choices.filter(c => c !== label) : [...draft.choices, label],
    }
  }
  return { ...draft, choices: [label], custom: '', otherOpen: false }
}

/** One answer as display text — used by the tab ticks and the review list. */
export function answerSummary(draft: AskDraft | undefined): string {
  if (!draft) return ''
  const parts = [...draft.choices]
  if (draft.custom.trim()) parts.push(draft.custom.trim())
  return parts.join(', ')
}

export function isAnswered(draft: AskDraft | undefined): boolean {
  return answerSummary(draft) !== ''
}

export function allAnswered(questions: AskQuestion[], drafts: AskDraft[]): boolean {
  return questions.every((_, i) => isAnswered(drafts[i]))
}

/** True when at least one question has something to submit. */
export function anyAnswered(drafts: AskDraft[]): boolean {
  return drafts.some(d => isAnswered(d) || d.notes.trim() !== '')
}

/**
 * The answers array for the user_question_answer frame. `preview` is
 * deliberately absent: the server copies the chosen option's preview out of
 * the request it still holds, so a large preview never travels back.
 */
export function answersPayload(drafts: AskDraft[]): AskAnswerPayload[] {
  return drafts.map(d => ({
    choices: [...d.choices],
    custom: d.custom.trim(),
    notes: d.notes.trim(),
  }))
}

/** The focused option's preview, or nothing when it has none. */
export function focusedPreview(q: AskQuestion | undefined, label: string): string {
  if (!q) return ''
  return (q.options ?? []).find(o => o.label === label)?.preview ?? ''
}
