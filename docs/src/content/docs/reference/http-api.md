---
title: HTTP API
description: The REST surface behind octo serve — the same API the embedded Web UI uses.
---

`octo serve` exposes one REST + WebSocket API; the embedded Web UI is just its first client.
All routes below are prefixed `/api` and require the [access key](/docs/reference/security/) on
any non-loopback bind. `GET /api/health` and `GET /api/version` are the two unauthenticated routes.

This API is **Best-effort** (see [Compatibility](/docs/reference/compatibility/)): scripting it
with `curl` is documented, supported behavior, but routes and fields may change in a minor release,
called out in the release notes — there's no versioned `/api/v1` yet.

## Chat & streaming

| Route | Purpose |
|---|---|
| `POST /api/chat` | create a chat |
| `POST /api/chat/{id}/turn` | send a turn |
| `GET /ws` | WebSocket — live session/task/workflow events for the Web UI |

## Sessions

| Route | Purpose |
|---|---|
| `GET/POST /api/sessions` | list / create |
| `GET /api/sessions/{id}` | fetch one, or `/messages`, `/artifacts` |
| `DELETE`, `PATCH /api/sessions/{id}` | delete, or update (model, reasoning effort, show-reasoning, permission mode, working dir) |
| `GET/PUT/DELETE /api/sessions/{id}/goal` | read, set, or clear the session's goal |

## Tools, skills, workflows

| Route | Purpose |
|---|---|
| `GET /api/tools` | list available tools |
| `GET /api/skills`, `PATCH /api/skills/{name}/toggle`, `DELETE`, `POST /api/skills/import`, `GET .../export` | manage skills |
| `GET /api/workflows` | list workflows |
| `GET/POST/PATCH/DELETE /api/mcp/servers` | manage MCP servers |

## Channels (IM bridge)

| Route | Purpose |
|---|---|
| `GET /api/channels`, `/available` | list configured / connectable platforms |
| `GET/POST/DELETE /api/channels/{platform}` | read, save, or remove a platform's config |
| `POST /api/channels/{platform}/test` | send a test message |
| `POST /api/channels/{platform}/send`, `/send-file` | send from the API |
| `POST/GET/DELETE /api/channels/weixin/login` | WeChat QR login flow |

## Tasks, profile, memory, trash

| Route | Purpose |
|---|---|
| `GET/POST/PATCH/DELETE /api/tasks` | the task graph (`task_create`/`task_update`/`task_list` mirrored over HTTP) |
| `GET /api/profile/soul`, `/api/profile/user` | read `soul.md` / `user.md` |
| `GET /api/memories` | read the memory index |
| `GET /api/trash`, `POST .../empty`, `POST .../{id}/restore`, `DELETE /api/trash/{id}` | the recoverable-delete trash panel |

## Onboarding, config, providers

| Route | Purpose |
|---|---|
| `GET/POST /api/onboard/status`, `/complete` | first-run onboarding state |
| `GET /api/providers`, `GET /api/config` | available providers; effective config |
| `POST /api/config/test` | verify a provider/model/key combination |
| `POST/PATCH/DELETE /api/config/models{,/{id}}` | manage model entries; `/default`, `/lite` set the two special slots |

## Browser & uploads

| Route | Purpose |
|---|---|
| `GET /api/browser/status`, `POST /api/browser/verify` | CDP connection state |
| `GET/PUT/DELETE /api/browser/recordings{,/{name}}` | manage record/replay recordings |
| `POST /api/upload`, `GET /api/uploads/{name}` | file upload used by chat attachments |

## Server lifecycle

| Route | Purpose |
|---|---|
| `POST /api/restart` | request a [restart](/docs/guides/self-host/#restarting); returns `202` immediately, drains in-flight turns in the background |

Next: the [security model](/docs/reference/security/) covers exactly what the access key protects
and what doesn't need it (loopback).
