---
name: skill-creator
description: Create new skills, modify existing skills, and help users turn repeatable workflows into reusable instructions. Use when the user wants to create a skill from scratch, edit or improve an existing skill, capture a workflow as a skill, or optimize a skill's description for better triggering accuracy. Do NOT use to chain or orchestrate several EXISTING skills/recordings into a runnable saved workflow — that is workflow-builder.
---

# Skill Creator

A skill for creating and iteratively improving skills.

## The Skill Lifecycle

1. **Capture intent** — understand what the skill should do and when it should trigger
2. **Write a draft** — compose the SKILL.md with frontmatter + instructions
3. **Test it** — run realistic prompts through the skill and evaluate the results
4. **Iterate** — rewrite based on feedback and test again
5. **Polish** — optimize the description for triggering accuracy

Your job is to figure out where the user is in this process and help them progress.

## Communicating with the user

Pay attention to context cues. Not every user is a programmer — some may be using octo for the first time. Briefly explain terms if you're in doubt.

---

## Creating a skill

### Capture Intent

Start by understanding the user's intent. The current conversation may already contain a workflow they want to capture.

Ask:
1. What should this skill enable octo to do?
2. When should this skill trigger? (what user phrases / contexts)
3. What's the expected output format?
4. Should we set up test cases? Skills with objectively verifiable outputs (file transforms, code generation, fixed workflow steps) benefit from tests. Skills with subjective outputs (writing style, creative tasks) often don't.

### Interview and Research

Proactively ask about edge cases, input/output formats, example files, success criteria, and dependencies.

Check available skills and tools — if useful for research, use subagents to search docs or find similar skills in parallel.

### Where the skill lives — user-level vs project-level

A skill is a directory holding a `SKILL.md`. The loader scans two roots:

- **User-level** `~/.octo/skills/<name>/SKILL.md` — available in every session.
  The default home for a personal skill.
- **Project-level** `<repo>/.octo/skills/<name>/SKILL.md` — discovered only when
  octo runs inside that working directory, and it **takes precedence** over a
  user-level skill of the same name. Use this when the skill is repo-specific or
  should travel with the code and be shared with the team (commit it to the
  repo). Ask which the user wants; default to user-level unless the skill is
  clearly tied to one project.

(`octo skills add` and the web Skills panel install to the user-level root; a
project-level skill is just a directory you create under the repo.)

### Write the SKILL.md

Both roots use the same format — a `SKILL.md` with YAML frontmatter:

```markdown
---
name: my-skill
description: When to trigger, what it does. Be specific — include both what the skill does AND the contexts where it should be used. Make descriptions slightly "pushy" to combat undertriggering.
---

# Title

Instructions go here...
```

**Anatomy of a skill:**

```
skill-name/
├── SKILL.md (required)
│   ├── YAML frontmatter (name, description required)
│   └── Markdown instructions
└── Bundled Resources (optional)
    ├── scripts/    - Executable scripts for deterministic tasks
    ├── references/ - Docs loaded into context as needed
    └── templates/  - Files used in output
```

**Progressive Disclosure:**
- **Metadata** (name + description) — Always in the system prompt (~100 words)
- **SKILL.md body** — Loaded on demand when the skill triggers (<500 lines ideal)
- **Bundled resources** — Read on demand with file tools

**Key patterns:**
- Keep SKILL.md under 500 lines
- Reference bundled files clearly with guidance on when to read them
- For large reference files (>300 lines), include a table of contents
- Use imperative form in instructions
- Explain the **why** behind instructions, not just the **what**
- Avoid ALL-CAPS MUSTs — explain reasoning instead

**Defining output formats:**
```markdown
## Report structure
Always use this exact template:
# [Title]
## Executive summary
## Key findings
## Recommendations
```

### Test the skill

After writing the draft, come up with 2–3 realistic test prompts. Share them with the user for approval, then test.

**How to test in octo:**
1. Create a test workspace directory
2. For each test prompt, spawn a subagent:
   - Give it the skill path (`~/.octo/skills/<name>/`)
   - Give it the test prompt as a task
   - Have it save outputs to a designated directory
3. Review outputs — both qualitatively and, if possible, with simple assertions
4. Collect feedback and iterate

If the user says "don't overthink it, just vibe with me," skip formal testing and iterate conversationally instead.

---

## Improving a skill

### How to think about improvements

1. **Generalize from feedback** — skills are meant to be used thousands of times. Don't overfit to test cases; fix the underlying pattern.
2. **Keep the prompt lean** — remove instructions that aren't pulling their weight. Read the full execution trace, not just final outputs.
3. **Explain the why** — today's LLMs are smart. Give them reasoning and they'll go beyond rote instructions.
4. **Look for repeated work** — if test runs all independently wrote similar helper scripts, bundle one in `scripts/`.

### The iteration loop

1. Apply improvements to the skill
2. Re-run all test cases (and baselines if applicable)
3. Collect feedback
4. Read feedback, improve again, repeat

Keep going until the user is happy, feedback is empty (everything looks good), or you're not making meaningful progress.

---

## Description optimization

The `description` field is the primary triggering mechanism. After creating or improving a skill, offer to optimize it.

### How to test triggering

1. Generate 10–15 realistic user queries — a mix of:
   - **Should-trigger** (5–7): queries where the skill would help. Vary formality, include edge cases, some without explicitly naming the skill.
   - **Should-not-trigger** (5–7): near-miss queries that share keywords but don't need this skill. Make them genuinely tricky, not obviously irrelevant.

2. Present them to the user for review and editing.

3. Mentally simulate: for each query, would the current description cause the skill to be selected? Identify failures.

4. Rewrite the description to fix misses and false positives. A good description:
   - Covers the skill's domain broadly
   - Includes specific trigger phrases
   - Is slightly "pushy" (mentions related concepts the user might not explicitly name)
   - Stays under ~200 words

---

## Modifying an existing skill

When updating an existing skill:
- **Preserve the original name** — use the existing directory name and frontmatter `name`
- **Copy before editing** — if the installed path may be read-only, copy to `/tmp/`, edit there, then write back
- **Read the current version first** — use `read_file` to see what's there before changing anything

---

## Packaging and delivery

After the skill is done:
1. Ensure the SKILL.md is well-formed (valid frontmatter, clear instructions)
2. If bundled resources exist, verify they're referenced correctly from SKILL.md
3. Present the final skill path to the user so they can use or share it

---

## Reference: SKILL.md frontmatter schema

```yaml
---
name: skill-name          # Display name (directory name is the trigger key)
description: "..."        # Trigger description — the most important field
---
```

Only `name` and `description` are required. The loader ignores all other frontmatter keys for Claude Code compatibility.
