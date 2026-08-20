// Evaluates a node's `visibleWhen` condition against the panel's current
// field values. See dev-docs/genui-interactive-panels-design.md, "Local
// interaction".
//
// There is no parser and no evaluator here in the expression sense — a
// condition is a small validated object (guard.ts's sanitizeCondition) and
// this file is a fixed set of comparisons over it. That is the whole reason
// conditions are shaped the way they are: the "no eval / no new Function /
// no hand-written expression parser" property from the original GenUI
// design holds by construction rather than by review.

import type { GenuiCondition, GenuiFieldValue } from './types'

/**
 * True when the node carrying this condition should render. An absent
 * condition always renders.
 *
 * Family resolution mirrors the guard: if any of equals/in/not survived
 * sanitization the equality family decides, otherwise every present range
 * predicate must hold. The guard already drops all but one member of the
 * equality family, so the order below only matters for hand-built conditions
 * in tests.
 */
export function evaluateCondition(
  cond: GenuiCondition | undefined,
  fields: Record<string, GenuiFieldValue>
): boolean {
  if (!cond) return true
  const raw = fields[cond.field]

  if (cond.equals !== undefined) return looseEquals(raw, cond.equals)
  if (cond.in !== undefined) return cond.in.some(v => looseEquals(raw, v))
  if (cond.not !== undefined) return !looseEquals(raw, cond.not)

  const n = toComparableNumber(raw)
  // Fail closed: a range predicate over a value that is not a number — an
  // untouched slider, or a condition pointed at a text field — hides the node
  // rather than showing it. A range-gated section should stay folded away
  // until the control that drives it has actually been moved.
  if (n === null) return false
  if (cond.gt !== undefined && !(n > cond.gt)) return false
  if (cond.gte !== undefined && !(n >= cond.gte)) return false
  if (cond.lt !== undefined && !(n < cond.lt)) return false
  if (cond.lte !== undefined && !(n <= cond.lte)) return false
  return true
}

/**
 * An unset field compares as the empty string, so `{field: "mode", equals:
 * "advanced"}` is hidden until the user picks something — the useful default.
 *
 * Values of differing types are compared by string form. A `select` stores
 * its option values as strings, so a model that writes `equals: 30` next to
 * an option `value: "30"` means the obvious thing; failing that match on a
 * type technicality would be a trap with no upside.
 */
function looseEquals(raw: GenuiFieldValue | undefined, expected: string | number | boolean): boolean {
  const actual: GenuiFieldValue = raw === undefined ? '' : raw
  if (typeof actual === typeof expected) return actual === expected
  return String(actual) === String(expected)
}

/** Numeric view of a field value, or null when there isn't one. Booleans are
 * deliberately excluded: `Number(true)` is 1, but "is this switch greater
 * than zero" is not a question worth answering. */
function toComparableNumber(raw: GenuiFieldValue | undefined): number | null {
  if (typeof raw === 'number') return Number.isFinite(raw) ? raw : null
  if (typeof raw === 'string' && raw.trim() !== '') {
    const n = Number(raw)
    return Number.isFinite(n) ? n : null
  }
  return null
}
