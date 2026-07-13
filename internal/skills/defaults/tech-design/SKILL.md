---
name: tech-design
license: MIT
description:
  Produce a complete backend technical design document from a PRD or feature
  description — understand, explore the codebase, grill on decisions, write, and
  self-review. Use when the user wants to write a tech design, technical
  proposal, or backend design doc, e.g. "写技术方案", "出个设计文档", "tech design",
  "technical proposal", "帮我写后端设计". For pressure-testing decisions first, use
  the grill-me skill; to build from a finished design, use the implement skill.
metadata:
  origin: adapted for octo — generic stack, no org-internal KB/lint tooling
---

# Skill: tech-design

Take a PRD (or feature description) and produce a complete backend technical
design document through a structured process: **understand → explore → grill →
write → self-review**.

## Rules (the reason this skill exists)

These six rules run through the whole process. Follow them while writing
(Phase 4), then grep for violations in the self-review. Most rework on a design
doc traces back to breaking one of them.

### R1. Never invent placeholders (API / field / service / URL / enum)

Every concrete technical name must be **grepped from real code, looked up in
docs, or asked of the user** — never a plausible-sounding placeholder left for
review to fix. Inventing `svc.GetBookingNotes` when no such method exists, or
guessing a column is `matter_type` when it's really `ticket_type`, is the single
most common source of production bugs that pass mocked tests.

### R2. Never invent phasing

The design scope is strictly equal to the PRD scope. There are only two legal
sources of phasing: (a) the PRD itself marks priority (P1/P2 in the stories), or
(b) the user explicitly said "do X first, not Y" during grilling. Otherwise, if
the PRD lists N sub-scenarios, design N.

**Do not use `P1/P2/P3` (or `Phase 1/2/3`) prefixes to label features, sections,
or upstream links** — even as "neutral numbering". A P-prefix is always read as
priority/phasing and manufactures an ordering the PRD never stated. Use the PRD's
own section names instead. Real execution order belongs only in a "release order"
section, and only when driven by a dependency chain, not by scope-cutting.

### R3. Technical facts must be traceable

Every concrete technical claim (DB column, URL prefix, field, API signature, enum
value, cache key pattern, MQ topic) must come from one of: a **code location**
(`file:line`), **project docs**, or a **grill answer** (quote the decision). Can't
find a source → **ask the user** (they're online; asking is cheap). Never paper
over a gap with a TODO.

Decision-level choices (sync vs async, new table vs extend column) can rest on a
grill answer plus explicit trade-off reasoning — but **factual assertions** must
be greppable.

**High-guess areas** (grilling usually misses these; handle by "check docs → check
code → ask user"): compatibility design, security design, circuit-breaker
thresholds, rollback plan, external dependency interface tables.

### R4. The document must read context-free

Write for a future reader who wasn't in this conversation. **Banned phrases**:
grill-question references (`the Q3 decision`, `per the original Q3`), grill-option
letters (`go with option A`, `path A` — the reader has no idea what A was; inline
the actual content instead), conversation references (`after we discussed…`,
`the earlier path`), and editing-process narration (`revised plan`, `originally
we…`).

Replace with the direct technical reason: "We use X rather than Y because … (name
the actual X and Y, never bare option letters)."

**Also context-bound: undefined jargon.** Any domain term or coined abbreviation
must be defined on first use or in a glossary. Test: would someone who only reads
this doc, wasn't in the conversation, and isn't deep in this domain know what the
word means? If not, define it.

Exception: a version-history changelog may say "X changed to Y", but must not
reference a grill question.

### R5. Examples must be empirical

A concrete example ("service X runs on cluster Y", "call chain A→B→C", "this
field is usually N") must be grepped / measured / confirmed from logs. Ask "can I
reproduce this example?" — if not, don't write it. An honest "needs verification"
beats a wrong example that a later reader treats as truth. An old default config
existing ≠ the current fact being correct; infrastructure migrates.

### R6. Resolve every uncertainty now — never defer it to the implementation phase

The finished design must be accurate and complete: an implementer builds from it
without discovering that a decision was left open. A "待实现阶段确认 / to be
confirmed during implementation / TBD" item is a **design defect**, not a legal
hand-off — it is exactly the gap this skill exists to close. Every uncertainty
you hit while writing (Phase 4) or in self-review (Phase 5) must be closed before
the doc is done. There are two kinds, and each has one correct way to close it:

- **A code-verifiable fact** — does this RPC actually return field X? what is the
  struct's real shape? which column / tag / enum? → **resolve it yourself by
  reading the code** (grep / codegraph / trace the call chain to the source).
  Never turn a checkable fact into an open question for someone else.
- **A genuine decision** — which of data-source A/B/C, sync vs async, extend an
  existing struct vs add a new RPC, each with different costs → **re-enter the
  grill-me skill** and drive it to a decision with the user, one question at a
  time. Then write the chosen approach in as settled fact (per R4 — the actual
  choice and its reason, never a bare option letter).

If you catch yourself writing an "open questions" / "实现阶段需确认" list that
shifts a decision downstream: stop. Check the code, or grill the user, then write
the answer. The only thing that may remain unresolved is something genuinely
outside the design's control (e.g. an upstream team owes a field that does not
exist yet) — and that is recorded as a **blocking external dependency with an
owner**, not as a decision the implementer is expected to make.

## Inputs

The user provides one of: a doc URL containing the PRD, a local file path, or a
prose description. If a URL is given, fetch and read it first.

## Process

### Phase 1 — Understand the problem

Read the PRD thoroughly. Extract: **problem statement** (what pain is solved),
**core user flow** (the happy path end-to-end), **scope boundaries** (explicitly
in/out), **success metrics**. Summarize back to the user in 5-8 bullets and ask:
"Did I get this right? Anything missing?"

### Phase 2 — Explore the codebase

Identify which existing services, modules, and data models are involved. Explore
to understand current architecture and boundaries, existing schemas that will be
touched, APIs to modify or depend on, relevant MQ topics / cache keys / cron
jobs, and prior art — similar features already implemented that can inform the
design. Use codegraph if indexed, otherwise search directly. Report key findings
concisely (module names, key files, current behavior).

### Phase 3 — Fill the write-specific gaps

The general decision tree (architecture / data / API / MQ / config / rollout) is
resolved by a prior **grill-me** session, and those decisions are already in
context — **do not re-grill here**. This step only fills the concrete items a
document needs but grilling usually misses — exactly the R3 high-guess areas:

- **Compatibility**: old/new data, old/new interfaces, dual-run during grayscale
- **Security**: auth, privilege escalation, sensitive fields
- **High availability**: timeout / degradation / rate-limit thresholds (concrete
  numbers)
- **Rollback**: can code and data roll back independently?
- **External dependencies**: for each HTTP/RPC/MQ upstream, the canonical
  `file:line` plus verbatim field names (with serialization tags)

Handle in R3 order — **check docs → check code → ask user**. Read from code what
you can (upstream fields, URL prefixes); ask the user only for what's neither in
code nor docs, one question at a time. When gaps are filled, confirm: "Decisions
and the facts needed to write are all in — ready to write?"

### Phase 4 — Write the tech design

Structure to the change, not a fixed template. A **project-level** design (new
feature/service/flow, multi-service, needs an architecture diagram and grayscale
rollout) covers the full skeleton below. A **small iteration** (a field added, a
branch extended, single service, architecture unchanged) keeps only the sections
that actually change and drops the rest.

Typical skeleton:

- **Background & goals**, **Out of scope**, **Naming glossary** (if any jargon)
- **Business flow** — a `flowchart TB` when the flow is non-trivial
- **Architecture** — service boundaries and responsibilities
- **Detailed design & sequence diagrams** — one `sequenceDiagram` per flow, not
  one giant diagram; skip for pure field additions
- **Data model** — table/collection schema, indexes, data-volume estimate,
  backfill/migration if any
- **API design** — every endpoint named, request/response with verbatim fields
- **MQ design** — topic, schema, consumer group, retry/DLQ
- **Cache design** — key format, TTL, eviction, invalidation
- **Config design** — every config / feature-flag key, per-environment defaults,
  dynamic vs restart-to-apply
- **External dependency interfaces** — see below
- **Test plan**, **Compatibility**, **Security**, **High availability**
  (circuit-breaking / degradation / concurrency), **Monitoring & alerting**
- **Release order** (when multiple repos), **Rollback** (code / data / config)

**Empty sections**: in a project-level design, keep the section and write "N/A —
[why]" so a reviewer sees each sanity-check area was considered; in a small
iteration, delete sections that don't change. **The compatibility section is the
exception** — always spell out *why* it's not affected, item by item, never a
bare "N/A"; it's the most-missed area in self-review.

**Style**: tables over paragraphs; concrete over abstract — real key formats,
real SQL, real JSON, every cache key / MQ topic / API endpoint / DB column named,
no hand-waving. Write in the user's language; keep code, field names, and types
in their original form.

**The external-dependency table is the worst offender for R1 + R3.** Every
HTTP/RPC/MQ upstream call gets a row: canonical file path + line range + verbatim
fields (with serialization tags). Do **not** write prose like "reads status /
type / amount" — an implementer seeing a prose field name will guess the tag
(`status` → `json:"status"` or `json:"status_text"`?), and one wrong character is
a production bug that fake-client unit tests never catch. Filling this table with
verbatim fields at design time is the only place that prevents the whole class.

### Phase 5 — Self-review, then hand off

Before showing the user, grep for rule violations:

| Rule | Check | Fix |
|---|---|---|
| **R1** | Every API name / DB column / enum / URL has a `file:line` or doc source | Grep real code to verify; ask the user if not found — no plausible placeholders |
| **R2** | `grep -E 'P1\|P2\|P3\|Phase\|阶段'` — any hit is suspect phasing | Can it point back to a PRD priority or an explicit "do X first" decision? If not, remove it and restore full-PRD scope. Even if it can, drop the P/Phase prefix and use the PRD's section name |
| **R3** | `grep -E '通常\|一般\|应该是\|usually\|should be\|probably\|likely'` | Stop & verify: add `file:line` / doc reference, or ask the user — no TODO fallback |
| **R4** | `grep -E 'Q\d+\|grill\|option [ABCD]\|方案 ?[ABCD]\|走 ?[ABCD] ?路'` | Rewrite as a direct technical statement; replace bare option letters with their actual content. Changelog is exempt |
| **R4-jargon** | List domain jargon / coined abbreviations; confirm each is defined in the glossary or on first use | Define the undefined ones |
| **R5** | Every concrete example (service/cluster/field value/call chain) is empirical | Can't reproduce → change to "needs verification" or delete |
| **R6** | `grep -E '实现阶段\|待实现\|待确认\|to be confirmed\|during implementation\|open question'` — any hit is a deferred decision | Close it before hand-off: a code-checkable fact → read the code; a genuine decision → re-enter grill-me and ask the user. Only a real external blocker may remain, recorded with an owner |
| **Completeness** | `grep -E 'TBD\|TODO\|待定'`; empty tables, placeholders | Every cache key / MQ topic / API endpoint / DB column must be concrete |
| **Consistency** | Sequence-diagram field names vs API response fields vs DB columns aligned? MQ topic name producer == consumer? | Rename to align |

Fix issues in place, including every R6 deferral: close each one before you
present the draft — a code-checkable fact by reading the code, a genuine decision
by re-entering **grill-me** to resolve it with the user. The draft you hand off
carries no "confirm during implementation" items; the only thing that may remain
is a real external blocker, recorded with an owner. Present the draft, iterate on
feedback until approved.

## Output

Write the final document to the location the user specifies (default:
`tech-design.md` in the current directory). Once approved, the natural next step
is the **implement** skill, which builds from the design slice by slice.
