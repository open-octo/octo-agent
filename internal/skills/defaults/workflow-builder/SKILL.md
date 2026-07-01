---
name: workflow-builder
description: Chain existing skills and browser recordings into a runnable, reusable saved workflow through guided conversation — inventory the available skills, wire each one's output into the next's input, generate the Ruby workflow, dry-run it, and save it with workflow_save. Use when the user wants to combine / chain / orchestrate several EXISTING skills or recordings into one repeatable flow, e.g. "chain these skills", "把这几个技能连起来", "串个流程", "做个 workflow", "编排一下", "build a workflow". Do NOT use to author a single new skill (that is skill-creator) or to run one skill a single time (just call it).
---

# Build a saved workflow from existing skills

A **saved workflow** is a small Ruby (mruby) script that calls existing skills in
order, passing each one's outputs to the next, and reruns on demand or on a
schedule. Your job is to guide the user from "chain these" to a saved, validated
workflow. You are *composing skills that already exist* — you are not writing a
new skill or new code.

## The pieces you use

- `skill(name, params = {}, opts = {})` — the workflow primitive: runs one skill
  and returns its outputs as a Ruby `Hash`. A **browser recording** replays
  deterministically; a **SKILL.md skill** runs as a sub-agent. `opts[:schema]`
  (a JSON-schema string) makes a SKILL.md step return structured output.
- `args` — the workflow's input, a Ruby `Hash`, so the saved workflow is
  parameterizable and reusable.
- `parallel` / `pipeline` — for fan-out or staged flows.
- The `workflow` tool — runs a script (in the **background**: returns a run id).
- `workflow_save` — persists the script as a named workflow.

## Steps

### 1. Inventory what's available
Read the system prompt's `# Available skills` (SKILL.md skills) and
`# Browser recordings` (recordings) sections. List the candidates relevant to the
user's goal — with their params, and, for recordings, their outputs — and ask
which ones, in what order.

Check for a **name collision**: if one name is *both* a recording and a SKILL.md
skill, `skill("that-name")` fails as ambiguous. Flag it and disambiguate with a
`browser:` or `md:` prefix (e.g. `skill("browser:export")`).

### 2. Wire outputs → inputs (the important step)
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

### 3. Generate the Ruby workflow
The script is **Ruby (mruby), not JavaScript**. Read inputs via `args`. Show it
to the user before running. Shape:

```ruby
# invoked as: workflow name=monthly-report, args={ "month" => "2026-06" }
dl  = skill("download-excels", { "month" => args["month"] })        # recording → {"files"=>[...]}
tbl = skill("merge-excels", { "inputs" => dl["files"] },            # SKILL.md, proposed schema
            schema: '{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}')
ppt = skill("excels-to-ppt", { "table" => tbl["path"] })            # SKILL.md
ppt                                                                 # last expression = the result
```

A failed `skill()` raises and halts the run, so you don't have to check each
result for errors.

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
  the flow has a recording in it.
- A SKILL.md step's structured handoff depends on the call-site `schema` you
  proposed in step 2; without one, that step returns free text.

## Don't
- Don't author a new SKILL.md — that is `skill-creator`.
- Don't save before the user has confirmed the name and scope.
- Don't report the dry-run as passing before `workflow_status` says done.
