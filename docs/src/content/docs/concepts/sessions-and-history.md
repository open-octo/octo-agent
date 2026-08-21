---
title: Sessions & history
description: Persistence, resume, and crash durability.
---

Every interactive conversation is a session, persisted as JSONL (one record per line, a meta
header first) under `~/.octo/sessions/` after each round.

```bash
octo sessions        # list this directory's sessions
octo sessions --all  # every session on the machine, grouped by directory
octo -c              # pick one of this directory's sessions from a list
octo -c <session-id> # resume a specific one
```

## Sessions belong to a directory

A session started in the terminal records the directory it was started in, and that directory is
part of its identity. `octo -c` resolves only against the sessions belonging to the directory you
are standing in — the picker, `last`, and an explicit ID alike. Naming a session that belongs
somewhere else tells you where, so you can `cd` there and resume it.

This is what lets every surface agree on where a session's tools run. Continuing a terminal session
in the Web UI runs its tools in the directory it was started in, rather than wherever `octo serve`
happened to be launched from.

The first turn of a new terminal session also files it under a project for that directory — the
sidebar grouping that a directory plus its sessions makes — creating one named after the directory
if none exists. That is what scopes the session's [memory](/docs/guides/memory/) to the directory on
every surface rather than only in the terminal. The workspace directory is the exception: accepting
the default is not choosing a directory, so it never becomes a project.

Sessions that never chose a directory get the workspace (`~/Octo` unless `workspace_dir` says
otherwise): new Web UI sessions are seeded with it, and IM sessions record it when they are created,
since a chat message carries no directory to start from. The directory `octo serve` was launched
from is never used — where the server happened to be started is nobody's choice.

Sessions written before any of this belong to no directory, so no directory resumes them — the
terminal refuses them by ID as well, since running one in whatever directory you happen to be in is
the same drift this removes. `octo sessions --all` lists them under their own heading, and they open
normally in the Web UI, where the server resolves the workspace for them.

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
