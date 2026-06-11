---
name: implement
description: Implement a technical design by decomposing it into dependency-ordered vertical slices, executing each with TDD red-green, reviewing each via an isolated sub-agent, and persisting progress to a state file so work survives session restarts. Use when the user has a tech design (doc or settled conversation) and says "implement", "start building", "let's code this", "实现吧", "开始实现", "按这个方案做", "把设计落地".
---

Take a technical design and turn it into working, reviewed code — slice by
slice, test-first, with progress checkpointed to disk so a new session can
resume exactly where the last one stopped.

## Inputs

One of:
- A path to a tech design document
- "implement the design" — use the design settled in conversation context

If neither exists, stop and ask for the design first.

## State persistence

State file: `.octo/implement-state.json` in the repository root (create
`.octo/` if needed; add it to `.gitignore` if not ignored — the state file
is session machinery, never committed).

**On startup, always check for this file first.**

- **Exists** → read it, summarize progress, ask: "Found an in-progress
  implementation. Resume from where we left off?" Resume honors each
  slice's status; "no" means ask whether to start fresh (overwrite) or
  abort.
- **Missing** → start at Phase 1.

```json
{
  "tech_design_path": "path or '(conversation)'",
  "branch": "feat/...",
  "updated_at": "RFC3339",
  "waves": [
    { "wave": 1, "mode": "sequential",
      "slices": [
        { "slice": 1, "title": "...", "status": "pending|in_progress|review|done|skipped",
          "acceptance_criteria": ["..."], "files_owned": ["..."],
          "tests_added": 0, "review_summary": "", "deviations": "", "commit_sha": "" }
      ] }
  ]
}
```

Update the file on every status transition (slice starts, enters review,
done with commit SHA + review summary + deviations). Delete it when
everything is done — a clean exit leaves no state behind.

Resuming: `done`/`skipped` skip; `review` re-runs or finishes the review;
`in_progress` checks `git log` for partial commits and continues from them
or restarts the slice; `pending` starts normally.

## Phase 1 — readiness gate, then decompose

**Readiness gate.** Before slicing, verify the design is concrete enough to
code from: every API has method + path + request/response shape, every
schema has fields + types, every external call names its counterpart. If
something is too vague to implement without guessing, list exactly what's
missing and ask the user (use `ask_user_question` for each decision) —
do NOT decompose on top of vague specs; slices built on guesses produce
guessed code.

**Decompose into vertical slices.** Each slice cuts through all layers
end-to-end (schema → logic → surface → tests), never a horizontal slab.
Group slices into waves by dependency:

- **Wave 1 is always a tracer bullet**: one thin end-to-end slice that
  proves the architecture, run inline so its lessons inform the rest.
- Dependencies before dependents; data layer before consumers.
- Slices within a wave must own **disjoint file sets** — that's what makes
  a wave parallelizable. If two slices need the same file, different waves.
- Honesty over parallelism: a tightly-coupled feature is often one
  sequential wave per slice. Say so instead of forcing a fan-out.

Present the breakdown (slice titles, scope, acceptance criteria, wave
grouping) and confirm with the user before writing the state file —
unless the user has already told you to proceed autonomously.

## Phase 2 — execute wave by wave

### Branch discipline

Work on a fresh branch off the latest default branch. Never start a new
piece of work on a branch whose PR already has auto-merge armed — after a
PR is created, the next slice batch gets a new branch.

### The TDD cadence (every slice, no exceptions)

(Test-quality standards — behavior over implementation, what to mock,
wire-contract tests for boundary structs — live in the `tdd` skill; this
section is the rhythm, that skill is the craft.)

For EACH behavior in the slice:

1. Write ONE failing test — actual test code, not a description.
2. Run it; confirm RED (and that it fails for the right reason).
3. Write the minimal implementation.
4. Run it; confirm GREEN.
5. Run the package's full tests; refactor if needed; still GREEN.
6. Commit immediately — one behavior, one commit, bisectable.

Banned in any plan, prompt, or code you produce: "TODO", "TBD",
"implement later", "add proper error handling", "similar to slice N",
steps that describe WHAT without showing the code. If you can't write the
code, the design decision isn't made yet — go back and make it.

### Sequential slices (the common case)

Run inline, full cadence above, then review (below), then next slice.

### Parallel waves (only when file sets are truly disjoint)

Spawn one `sub_agent` per slice. Each sub-agent works in its own git
worktree — follow the absolute-path rule from the `worktree-isolate`
skill: octo has no session working directory, so every command is
`git -C "$WT" …` / `cd "$WT" && …` in a single `terminal` call, and every
file tool gets the worktree's absolute path. The sub-agent prompt must be
self-contained: slice scope, acceptance criteria, owned files (absolute
paths), interface contracts, the design doc path, the TDD cadence, and the
deviation rules below — sub-agents have no conversation context.

After a wave: review each slice, merge each worktree branch back, resolve
any conflict (a conflict means the slices weren't independent — note it),
run the full suite on the merged result.

If `sub_agent` is unavailable in this session, run the slices sequentially
inline and say so.

### Verify external contracts before writing boundary code

Any code that talks to something outside this repo — an HTTP/RPC API, a
DB column, a wire format — must be verified against ground truth before
the struct or query is written, and the evidence shown (file:line or
fetched doc, with the verbatim field names):

- **External API**: read an existing client of the same service in this
  codebase, or the upstream handler/spec itself. Prose descriptions in
  the design doc are not evidence — they rot.
- **DB column comparisons**: grep the WRITE path (where the column is
  assigned), not just the read path. A filter that compares a column to a
  value no writer ever stores compiles, passes unit tests against fake
  data, and matches zero rows in production.
- If no ground truth can be found, STOP and flag it rather than guess —
  wrong contracts pass mocked tests and fail only in production.

### Review (every slice, non-negotiable)

After a slice's code is complete, dispatch an isolated reviewer with the
`code-review` skill's sub-agent pattern: zero conversation context, given
only the git range, the design doc path, what the slice claims to do, and
any intentional deviations (so they aren't re-reported). Ask it to check
correctness, races, conventions, tests, security, and design compliance,
with severity-ranked findings.

Then: verify each finding before acting — reviewers are sometimes wrong;
push back with technical reasoning when they are. Fix Critical now,
Important before the next wave, Minor if cheap. Record the review summary
in the state file. No performative agreement — fix and show the result.

### Deviation rules

| Situation | Action |
|---|---|
| Bug found while implementing | Fix now, note in the slice report |
| Design is missing a small detail | Decide, implement, note it |
| Environment/dependency blocker | Work around, note it, surface at the checkpoint |
| **Architectural change** (new boundary, changed contract/schema) | **STOP and ask the user** |

Update the design doc in the same branch when the implementation
legitimately diverges — the doc describes current state, and a doc that
lies is worse than no doc.

## Phase 3 — verification before the PR

Four levels, in order:

1. **Exists** — everything the design names is present.
2. **Substantive** — no stubs: scan the diff for empty bodies,
   `panic("unimplemented")`, tests without assertions, leftover TODOs.
3. **Wired** — every new surface is reachable: handlers registered, tools
   advertised, hooks attached, config read. Unwired code is a bug.
4. **Functional** — the project's full test suite (with the race detector
   if it's a Go project), formatter, and vet/linter all clean; every
   acceptance criterion from Phase 1 checked off. Where feasible, one
   real end-to-end smoke (run the binary, hit the endpoint, observe the
   behavior) — unit-green is not the same as works.

Then: push the branch, open a PR whose description covers what landed,
review findings fixed, and deviations from the design. Delete the state
file. Report: slices completed, tests added, review findings (any
patterns?), deviations, anything left for manual verification.

## Key principles

- Vertical over horizontal; tracer bullet first; learn before fanning out.
- One behavior = one commit; review every slice; verify, don't trust.
- The state file is always current — any session can crash and resume.
- Auto-fix bugs and blockers; stop and ask before architectural change.
- Match the codebase's conventions — comment density, naming, test style —
  not your own defaults.
