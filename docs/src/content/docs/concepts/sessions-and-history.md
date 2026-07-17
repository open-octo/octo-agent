---
title: Sessions & history
description: Persistence, resume, and crash durability.
---

Every interactive conversation is a session, persisted as JSONL (one record per line, a meta
header first) under `~/.octo/sessions/` after each round.

```bash
octo sessions        # list saved sessions
octo -c              # pick a recent session from a list
octo -c <session-id> # resume a specific one
```

## Crash durability

History is persisted at round granularity, so a crash mid-turn loses at most the in-flight round,
not the session. A replay buffer covers the gap between the last persisted round and whatever was
streaming when the process died, so resuming picks up cleanly rather than replaying stale state.

## Message format

Internally, a message's content is a union of blocks — text, tool-use, tool-result — rather than a
single string, which is what lets a session round-trip tool calls faithfully across a resume.
Older sessions saved before this existed still load: a nil block list falls back to the plain-string
form.

## Branching a session

From the Web UI, any user message can be branched: hover the message, click **Branch**, optionally edit
the prompt, and a new session is created with the history up to and including that message. The original
session is untouched. This is useful for testing prompt variants — rewrite a question and compare the
side-by-side results without polluting the original conversation.

You can also **edit** a user message in place: hover and click **Edit**, and the message turns into an
input you can modify. Saving truncates the history past that point and resends the modified prompt, all
within the current session — no new session created. Branch and edit differ only in whether a new session
is spawned.

The new session carries a `branched_from` field pointing back to its source, shown in the session header
as "Branched from \<title\>". Branching is available via `POST /api/sessions/{id}/branch` with a
`message_index` and an optional `prompt_override`; editing via `POST /api/sessions/{id}/edit_message`.

Next: sessions across the web UI and IM channels share the same store — see
[Bridge to chat apps](/docs/guides/channels/).
