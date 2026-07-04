---
title: Sessions & history
description: Persistence, resume, and crash durability.
---

Every interactive conversation is a session, persisted as JSON under `~/.octo/sessions/` after
each round.

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

Next: sessions across the web UI and IM channels share the same store — see
[Bridge to chat apps](/docs/guides/channels/).
