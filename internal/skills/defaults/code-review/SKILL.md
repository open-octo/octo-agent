---
name: code-review
description: Review local code changes. Uses an isolated sub-agent for unbiased analysis when the main agent wrote the code; reviews directly in fresh review-only sessions. Checks correctness, conventions, performance, tests, security, and tech design compliance. Use when the user wants a code review or says "review the diff" / "review my changes" / "代码评审" / "review 一下改动".
---

Review local code changes. Choose the reviewer based on where the implementation context lives:

- **You implemented the changes in this session** (or took part in the design/implementation conversation): dispatch an **isolated sub-agent** — a fresh agent with no knowledge of this session's implementation decisions. This eliminates the bias of reviewing your own work.
- **Fresh session, review-only request** (the user opened this session just to have you review a diff/PR you had no part in): you already ARE the unbiased fresh pair of eyes — review the code **yourself**, following the same template in [code-reviewer.md](code-reviewer.md). Spawning a sub-agent here only adds latency and a context hand-off with zero isolation benefit. Skip step 2 and produce the review report directly.

## Process

### 1. Gather context for the review

Collect everything the reviewer needs:

```bash
# What changed
git log --oneline main..HEAD
git diff main...HEAD
git diff
git diff --cached
```

Also identify:
- Tech design doc path (if one exists in conversation context or commit messages)
- Which services/modules are touched
- Test files added or modified

### 2. Dispatch the sub-agent (only when you wrote the code)

Call the `sub_agent` tool with `subagent_type: "code-review"` — a read-only reviewer that starts with zero context. Pass it the diff range, file list, and tech design reference in the prompt — but NOT your implementation reasoning or conversation history.

If you need to review multiple independent changes (e.g., several PRs), dispatch each sub-agent with `run_in_background: true` so they run in parallel. Wait for the completion notifications and use the results from those notifications. Do not poll `sub_agent_status` while waiting; use `sub_agent_status` only if you suspect an agent is stuck or need to list running agents. For a single short review, a synchronous `sub_agent` call is fine.

The sub-agent prompt should follow the template in [code-reviewer.md](code-reviewer.md).

Fill in:
- `{DESCRIPTION}` — brief summary of what these changes do
- `{TECH_DESIGN_PATH}` — path to tech design doc (or "none")
- `{BASE_SHA}` / `{HEAD_SHA}` — git range
- `{DIFF_STAT}` — output of `git diff --stat`

If the `sub_agent` tool is not available in this session, perform the review yourself following the same template — and say explicitly in the report that the review was not isolated. (No such disclaimer is needed when you review directly because the request came in a fresh session — that review IS isolated.)

### 3. Act on the review

Whether the findings come from the sub-agent or from your own review:

**Receiving feedback — rules:**
- Verify before implementing. Don't blindly agree.
- If any item is unclear, clarify ALL unclear items before fixing any.
- Implementation order: Critical → Important → Minor, test each fix individually.
- Push back with technical reasoning if the reviewer is wrong (missing context, doesn't apply to this codebase, YAGNI).
- No performative agreement ("Great point!", "You're absolutely right!"). Just fix and show the result.

**Priority:**
- **Critical**: fix immediately, no exceptions
- **Important**: fix before proceeding
- **Minor**: note for later or fix if quick

### 4. Report to user

Present the review to the user. For sub-agent findings, add your own assessment of each — agree, disagree with reasoning, or need discussion.
