---
name: workflow-creator
description: Turn a repeatable multi-step task into a runnable, reusable saved workflow through guided conversation, in either of two shapes — chaining existing skills/recordings (wiring each one's output into the next's input), or composing the workflow script's own primitives (agent/parallel/pipeline) directly when the task needs fresh sub-agent orchestration and no existing skill covers it. Figure out which shape fits, generate the Ruby workflow, dry-run it, and save it with workflow_save. Use when the user wants to combine / chain / orchestrate work into one repeatable flow — whether that's several EXISTING skills, or a from-scratch multi-agent script (parallel review, fan-out research, a pipeline over a list) — e.g. "chain these skills", "把这几个技能连起来", "串个流程", "做个 workflow", "编排一下", "build a workflow", "写个并行跑多个 agent 的脚本". Do NOT use to author a single new skill (that is skill-creator), to run one skill/agent a single time (just call it), or to design a recurring/self-triggering loop with its own trigger and state file (that is loop-engineering — a saved workflow is just the one-shot script such a loop calls, not the loop itself).
---

# Build a saved workflow

A **saved workflow** is a small Ruby (mruby) script that runs on demand or on a
schedule. Your job is to guide the user from a one-off description of what they
want repeated to a saved, validated workflow. It takes one of two shapes,
sometimes mixed in the same script:

- **Skill chain** — the script calls *existing* skills/recordings in order via
  `skill(...)`, passing each one's output to the next's input. You are composing
  things that already exist.
- **Primitive-composed** — the script calls `agent(...)` directly (alone, or
  fanned out with `parallel`/`pipeline`) to do fresh sub-agent work that has no
  matching existing skill — e.g. "review this diff across 3 dimensions in
  parallel" or "run this check over every file in the list." There is nothing to
  inventory here; you're writing the orchestration from scratch, just not the
  agent-level logic (that's still an LLM call inside `agent(...)`, not new Go/tool
  code).

Figure out which shape (or mix) fits **before** drafting anything — see Step 0.

## The pieces you use

- `agent(prompt, opts = {})` — run one sub-agent to completion, returns a String.
  `opts[:schema]` (a JSON-schema string) makes the reply come back as a JSON
  *string* matching it — parse it yourself with `JSON.parse`, unlike `skill()`'s
  schema results, which arrive already parsed.
- `skill(name, params = {}, opts = {})` — runs one *existing* skill and returns
  its outputs as a Ruby `Hash`. A **browser recording** replays deterministically;
  a **SKILL.md skill** runs as a sub-agent. `opts[:schema]` makes a SKILL.md step
  return structured (already-parsed) output.
- `args` — the workflow's input, a Ruby `Hash`, so the saved workflow is
  parameterizable and reusable.
- `parallel(items) { |it| ... }` / `pipeline(items, *stages)` — for fan-out or
  staged flows, over either `agent()` or `skill()` calls.
- `log(msg)` / `phase(title)` — progress output, no effect on scheduling.
- The `workflow` tool — runs a script (in the **background**: returns a run id).
- `workflow_save` — persists the script as a named workflow.

## Steps

### 0. Decide the shape
First check this isn't actually a **loop** in disguise: if the user describes
something that keeps running on its own (daily/hourly cadence, "keep checking
until X", needs to remember state between runs, needs a done-condition or a
human gate) — that's `loop-engineering`'s job, not this skill's. A saved
workflow is a single deterministic script; it has no opinion on when or how
often it's invoked. Redirect there instead of scoping cadence/state yourself.

Otherwise, ask what the workflow should do — its steps and their order, not the
broader scenario or how often it runs — then place it:

- Every step maps to a skill/recording the user already has → **skill chain**,
  go to Step 1.
- The work is fresh sub-agent orchestration with no matching skill (a review
  fanned out across dimensions, a search run in parallel over several sources, a
  per-item pipeline) → **primitive-composed**, skip straight to Step 3 and write
  `agent`/`parallel`/`pipeline` calls directly — there's nothing to inventory or
  infer an output schema for, since you're writing the prompts yourself.
- Some steps are existing skills and others are ad hoc agent work → do both:
  inventory only the skill steps (Step 1), and for the primitive steps just
  decide the prompt and, if a later step needs structured output from it, a
  `schema`.

### 1. Inventory what's available (skill-chain steps only)
Read the system prompt's `# Available skills` (SKILL.md skills) and
`# Browser recordings` (recordings) sections. List the candidates relevant to the
user's goal — with their params, and, for recordings, their outputs — and ask
which ones, in what order.

Check for a **name collision**: if one name is *both* a recording and a SKILL.md
skill, `skill("that-name")` fails as ambiguous. Flag it and disambiguate with a
`browser:` or `md:` prefix (e.g. `skill("browser:export")`).

### 2. Wire outputs → inputs (skill-chain steps only)
For each adjacent pair, propose how the earlier step's output feeds the next
step's params, then have the user confirm *before* you generate anything. Be
honest that the two skill types give you different amounts to work with:

- **Upstream is a browser recording** — its `outputs` are in the manifest
  (`[outputs: files (file[])]`). Wire directly, e.g. `dl["files"]` → the next
  step's `inputs`. This is manifest-driven and reliable.
- **Upstream is a SKILL.md skill** — the manifest does **not** list its outputs.
  Infer the likely output shape from the skill's description/body and **propose a
  call-site `schema`** for that step so its reply comes back structured. Show the
  user the shape you assumed and ask them to confirm or fix it. You are
  proposing, not reading a declared contract — don't present it as certain.

For a **primitive-composed** step feeding another primitive-composed step, the
same idea applies but there's no manifest at all: you decide both the upstream
`agent()`'s `schema` (if you need structured output) and how the result is used
downstream, and confirm your choice with the user the same way.

### 3. Generate the Ruby workflow
The script is **Ruby (mruby), not JavaScript**. Read inputs via `args`. Show it
to the user before running. Shape for a skill chain:

```ruby
# invoked as: workflow name=monthly-report, args={ "month" => "2026-06" }
dl  = skill("download-excels", { "month" => args["month"] })        # recording → {"files"=>[...]}
tbl = skill("merge-excels", { "inputs" => dl["files"] },            # SKILL.md, proposed schema
            schema: '{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}')
ppt = skill("excels-to-ppt", { "table" => tbl["path"] })            # SKILL.md
ppt                                                                 # last expression = the result
```

Shape for a primitive-composed workflow — no `skill()` call anywhere:

```ruby
# invoked as: workflow name=diff-review
findings = parallel(["correctness", "security", "perf"]) do |dimension|
  agent("Review the current diff for #{dimension} issues", read_only: true)
end
findings.each { |f| log(f) }
findings
```

A failed `agent()`/`skill()` raises and halts the run, so you don't have to check
each result for errors.

### 4. Dry-run to validate the wiring — it is asynchronous
The `workflow` tool runs in the **background**: it returns a run id, and you must
poll `workflow_status(id)` until the run reports done before judging it. Don't
declare success before results land; cap how many times you poll.

A dry-run **actually executes** the steps, and a browser recording drives a real
Chrome *right now*. So if the chain contains a recording:
- confirm Chrome is attached (port 9222) before dry-running, **or**
- validate only the SKILL.md tail by hand-feeding a sample value for the
  recording's output (e.g. pass a couple of file paths as `inputs`), and validate
  the recording itself separately with the `browser` tool's `run_skill`.

Fix any wiring error and re-run before saving.

### 5. Save it and show how to run it
Call `workflow_save(name, script, description, scope)`. `scope` defaults to
`project` (`.octo/workflows`) and **errors when you're not inside a git repo** —
if there's no repo, use `scope: "user"` (`~/.octo/workflows`, available across
projects). Ask the user if it's unclear. Confirm the name with them first.

Then tell them the three ways to run it:
- **In chat** — ask to run the workflow by name (passing `args`).
- **CLI / headless** — run it by name.
- **On a schedule** — use the `cron-task-creator` skill.

### 6. State the constraints
- A workflow that includes a browser recording needs a live Chrome **every time
  it runs** — it errors clearly in a headless/cron run without one. Say so when
  the flow has a recording in it. A pure primitive-composed workflow has no such
  constraint.
- A SKILL.md or `agent()` step's structured handoff depends on the call-site
  `schema` you proposed in step 2; without one, that step returns free text.

## Don't
- Don't author a new SKILL.md — that is `skill-creator`.
- Don't design cadence/state/trigger for a recurring loop — that is
  `loop-engineering`; don't ask "定时跑还是手动触发" or "这个 workflow 解决什么场景"
  as scoping questions, since scheduling is only ever a *consumer* of a saved
  workflow (mentioned once, in Step 5, as one of three ways to run it by name).
- Don't save before the user has confirmed the name and scope.
- Don't report the dry-run as passing before `workflow_status` says done.
