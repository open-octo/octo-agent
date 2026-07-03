---
title: Provider protocols
description: What the anthropic and openai adapters each normalize away.
---

octo speaks two wire formats. Provider quirks are encapsulated entirely in
`internal/provider/{anthropic,openai}/` — the agent layer never branches on which protocol it's
talking to.

## Anthropic Messages

- Auth via `x-api-key` + `anthropic-version` headers.
- `system` is a top-level field, not a message.
- Content blocks: `[{type: "text", text}]`.
- The SSE aggregator dispatches on `message_start` / `content_block_delta` / `message_delta`.
- Tool calls land as a `content_block_start` of type `tool_use`, followed by `input_json_delta`
  fragments that accumulate into the final arguments.
- `stop_reason: "tool_use"` is the agent-facing signal that a turn wants to call a tool.

## OpenAI Chat Completions

- Auth via `Authorization: Bearer`.
- `system` is carried as `messages[0]`, not a separate field.
- The SSE aggregator parses `chat.completion.chunk` objects, and tolerates a missing `[DONE]`
  sentinel — some third-party OpenAI-compatible servers omit it.
- Tool calls arrive in `delta.tool_calls[]`, chunked by `tool_calls[i].index`; the aggregator
  concatenates fragments by index before parsing the JSON arguments.
- `finish_reason: "tool_calls"` is normalized to `"tool_use"` on the agent-facing surface, so the
  agent loop only ever sees one spelling regardless of backend.
- `stream_options.include_usage: true` is sent on every streaming request — DashScope (Bailian) and
  real OpenAI emit no usage chunk at all without it, so a streamed turn would otherwise report zero
  tokens. DeepSeek sends usage either way.

## Cache token accounting

Cache semantics differ by *protocol*, not by vendor:

- **Anthropic-protocol** endpoints (real Anthropic, and Anthropic-compatible gateways) report
  `input_tokens` as the *uncached remainder*, with `cache_read_input_tokens` reported separately.
- **OpenAI-protocol** endpoints (DeepSeek's default endpoint, DashScope) report `prompt_tokens` as
  the *whole* input, with `cached_tokens` as a subset of it.

The agent treats input tokens and cache-read tokens as non-overlapping buckets — context occupancy
is their sum — so the OpenAI adapter subtracts cached from prompt before handing counts to the
agent layer; the Anthropic adapter needs no adjustment.

## Extended reasoning

Anthropic `thinking` blocks and OpenAI `reasoning_content` are unified behind
`--reasoning-effort` / `--show-reasoning`: OpenAI-protocol backends receive the effort level
directly as `reasoning_effort`; Anthropic-protocol backends map it to adaptive thinking or an
explicit token budget, normalized per model family so older models and newer ones both get a
sensible mapping from the same five-level scale.

Next: adding a third protocol, or a new tool, follows the pattern in
[Extending octo](/docs/architecture/extending-octo/).
