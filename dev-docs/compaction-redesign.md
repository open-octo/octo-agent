# Compaction redesign — token-budget retention, cheap reclamation, deficit-aware recovery

## Problem

In agentic sessions the compactor folds almost nothing per run and re-triggers
nearly every turn. Observed: repeated `compacted context · folded 1 message(s) ·
~170k → ~132k tokens` on consecutive turns (kimi-for-coding, 200k window, trigger
150k). Each run reclaims a sliver, context climbs back over the trigger, and it
compacts again — wasting an LLM summarize call almost every turn while never
getting the context down.

### Root cause

`safeSplitIndex(msgs, compactKeepTurns=4)` (`internal/agent/compaction.go`)
chooses what to keep verbatim by **plain user turns** — it keeps the last 4
messages with `Role==user && !hasToolResult`, summarizing everything before. In
an agentic session the token bulk is `tool_use`/`tool_result` pairs, and they
all live *inside* the most recent few user turns. So the split lands at the
oldest user turn (folding a tiny prefix) and the ~150k of recent tool output is
never foldable. There is also no guard against compacting when the projected
reduction is trivial, so it thrashes.

## How other agents do it (research summary)

| | retention unit | anti-thrash | cheap (no-LLM) tier | recall |
|---|---|---|---|---|
| octo (today) | last **4 user turns** | none | per-result 40KB cap at write time | none |
| octo Ruby | last **≤20 messages** (tool pairs kept) | skip if reduction <10% | — | chunk MD + index, `file_reader` |
| Claude Code | summarize whole body, keep ~0 verbatim | headroom trigger + 3-fail breaker | microcompact: stale tool results → cold storage, no LLM, cache-aware | re-read last 5 files |
| Codex | last user messages up to **≤20k tokens** | none (hard-bounded result) | per-result middle-truncate (10KB) | — |

Takeaway: every mature implementation bounds the kept-verbatim tail by **tokens
(or message count)**, summarizes tool/assistant bulk regardless of recency, and
has a cheap pre-pass and/or anti-thrash guard. octo's turn-count unit is the
outlier and the bug.

## Design

Three parts, shipped as stacked PRs in order. Parts 1 and 2 are self-contained
in `internal/agent`; part 3 spans the agent ↔ session layers.

### Part 1 — token-budget retention + anti-thrash (the fix)

Replace turn-count retention with token-budget retention, still splitting only on
safe boundaries so `tool_use`/`tool_result` pairs are never severed and the kept
tail always begins on a real user message.

- New `safeSplitIndexByBudget(msgs, keepBudget int) int`: walk plain-user-turn
  boundaries newest→oldest, accumulating the tail's estimated tokens; keep adding
  turns while the tail stays ≤ `keepBudget`; split at the oldest kept boundary.
  Always keep at least the most recent user turn (never split inside it).
- `keepBudget = CompactKeepFraction × contextWindow(model)`, default
  **0.30** (new `Agent.CompactKeepFraction`, 0 ⇒ default). kimi 200k ⇒ ~60k tail;
  after a fold the context is `summary(~1–2k) + ~60k ≈ 62k` vs the 150k trigger —
  a wide margin, so re-compaction is rare. Floor at 8k so tiny windows still keep
  one real turn.
- Anti-thrash guard in `maybeCompact`: if the projected fold would reclaim less
  than **15%** of `before` (i.e. the bulk is all inside the kept tail and a
  summarize wouldn't help), skip this round rather than burn an LLM call. Part 2
  is what actually shrinks that case.
- `CompactStats.KeptTurns` stays meaningful (report the number of user turns
  kept); `FoldedMsgs` stays the split index (count of folded leading messages).
  The TUI line `compacted context · folded N message(s)` (`cmd/octo/tuirepl.go`)
  needs no change.

Files: `internal/agent/compaction.go` (+ `overflow.go` uses the same split),
`internal/agent/agent.go` (field), `cmd/octo/chat.go` + `config` (optional flag).

### Part 2 — no-LLM stale-tool-result reclamation (cheap tier)

A pre-pass that crushes the bulky-recent-turn case without an LLM call — the
single-giant-turn case Part 1 alone can't fold.

- New `reclaimStaleToolResults() (reclaimed int)`: keep the most recent
  `hotToolResults` (default **6**) `tool_result` blocks inline; for older ones
  whose content exceeds `staleToolResultMinBytes` (default **4_000**), replace the
  block content with a placeholder `[elided N bytes — <tool> result; re-run to
  view]`. IDs are preserved so tool_use/tool_result pairing stays valid. No model
  call.
- Wire into `maybeCompact` / between-batch path: when over trigger, run
  reclamation **first**; recompute tokens; if now under `trigger − margin`, skip
  the LLM summarize entirely. Otherwise fall through to Part 1's summarize.
- Cache: rewriting old blocks invalidates the prompt-cache prefix from the first
  rewritten message — still far cheaper than an LLM summarize + full rebuild.
- Data-loss caveat: the elided original is gone from history. Mitigated by Part 3
  archival when the hook is set; without it the placeholder says "re-run to view".

Files: new `internal/agent/reclaim.go`, wired in `compaction.go`.

### Part 3 — deficit-aware overflow recovery + chunk archival/recall

- **3a. Parse the 413 deficit.** New `parseOverflowTokens(err) (have, max int,
  ok bool)` for Anthropic `prompt is too long: N tokens > M maximum`, OpenAI
  `maximum context length is M ... you requested N`, Qwen variants. In
  `overflowRecovery.tryRecover`, when parseable, reclaim/fold to cover
  `have − max + margin` instead of the current blind half pull-back.
- **3b. Chunk archival + recall.** New `Agent.ArchiveHook func(msgs []Message)
  (ref string, err error)`, set by the CLI/server (which own the session dir;
  the agent layer must not). On summarize (Part 1) and on reclamation (Part 2),
  the folded/elided originals are written to a session chunk file; the summary
  message footer carries `[archived at <ref> — use the read tool to recall]`.
  Recall reuses the existing `read` tool — no new tool. Hook nil (tests/headless)
  ⇒ archival skipped, behaviour unchanged.

Files: `internal/agent/overflow.go`, `internal/agent/agent.go` (hook field),
`internal/app` + `internal/server` + `cmd/octo` (set the hook; chunk file I/O).

## Acceptance criteria

- A session that today logs `folded 1 message · 170k → 132k` repeatedly folds to
  ~60k once and does not re-compact for many turns (Part 1).
- A single huge agentic turn (one user message, 150k of tool output) is brought
  under the trigger by reclamation with **no** summarize call (Part 2).
- A real 413 is recovered by folding at least the parsed deficit, once, and the
  turn retries successfully (Part 3a).
- With an archive hook set, folded originals land in a chunk file and the summary
  references it; `read`ing that path returns them (Part 3b).
- `go test -race ./...`, `go vet`, `gofmt -l` all clean.

## Test plan

- `safeSplitIndexByBudget`: budget larger than whole history ⇒ split 0; budget
  smaller than the most recent turn ⇒ keeps exactly that turn; mixed turns ⇒
  splits at the right boundary; never splits inside a tool pair.
- anti-thrash: bulk-in-tail history ⇒ `maybeCompact` is a no-op (no summarize call
  on the fake sender).
- `reclaimStaleToolResults`: hot tail preserved, old large results elided, IDs
  intact, reclaimed-token count correct, small results untouched.
- `parseOverflowTokens`: the real error strings in `overflow.go`'s comment table.
- archival: hook receives exactly the folded messages; footer ref present; nil
  hook ⇒ no-op.

## Rollout / compatibility

- Defaults preserve today's external knobs (`CompactThreshold` semantics
  unchanged); only the retention *unit* changes. `compactKeepTurns` constant is
  removed; `CompactKeepFraction` (new, default 0.30) replaces it.
- Session JSON is unaffected (history shape unchanged; elided blocks are ordinary
  tool_result content).
- Part 2 reclamation and Part 3 archival are independently revertible.

## Settled parameters

1. **keepBudget** = `CompactKeepFraction` × window, default **0.30**, independent
   of the trigger but capped at half the trigger so a fold reliably clears it.
2. **Reclamation** keeps the most recent **6** tool results inline; elides older
   ones over **4KB**.
3. **Reclamation recoverability** — reclamation elides in place with a "re-run to
   view" placeholder, the same lossy-but-regenerable contract the write-time
   `microCompact` backstop already ships, so it doesn't depend on archival.
   Archival (3b) covers the genuinely-lost content: the turns the summarize path
   folds away (their lossy summary is otherwise the only trace). The overflow
   fallback path and reclamation placeholders are not archived.
4. **Chunk format** — per-session `<id>.chunks/chunk-NNN.md`, a readable
   Markdown transcript (so the recalled text is plain, not wire JSON). Archival
   is best-effort: a write failure never breaks a compaction. CLI (persisted TUI
   sessions) and the web server (`buildAgent`) set `Agent.ArchiveDir` via
   `Session.ChunkDir`; one-shot/headless and the IM factory leave it unset.
