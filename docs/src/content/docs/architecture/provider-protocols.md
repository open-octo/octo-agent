---
title: Provider protocols
description: What the anthropic, openai and responses adapters each normalize away.
---

octo speaks three wire formats. Provider quirks are encapsulated entirely in
`internal/provider/{anthropic,openai,responses}/` — the agent layer never branches on which
protocol it's talking to. Named vendors pin their protocol; a Custom endpoint picks one with
`protocol: openai | anthropic | openai-responses`.

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

## OpenAI Responses

- `POST /v1/responses`, auth via `Authorization: Bearer`. Only Custom endpoints use it
  (`protocol: openai-responses`); named vendors keep Chat Completions.
- Every request is stateless (`store: false`): the full history is sent each turn.
- `system` goes in the top-level `instructions` field.
- Function tools are flat (`{type: "function", name, parameters, strict: false}`), not nested
  under `function`. `strict` is sent as `false` because omitting it makes the API try to
  rewrite the schema into strict mode.
- Tool calls and results are standalone input items (`function_call` / `function_call_output`).
- There is no finish reason: `status: "incomplete"` with `max_output_tokens` maps to the
  agent's `max_tokens`, and any `function_call` in the output makes it a `tool_use` turn.
- The stream is dispatched on each event's `type`. A function call's `call_id` and name arrive
  only on `response.output_item.added`; the argument deltas after it carry just the item id.
- Reasoning models return opaque reasoning items. They are kept in history as `thinking` blocks
  that carry the item id, the encrypted content and the model that produced them, and are sent
  back only to that same model. Anything the request can't be sure the backend accepts is
  dropped instead, and the Anthropic adapter drops these blocks too.

## Cache token accounting

Cache semantics differ by *protocol*, not by vendor:

- **Anthropic-protocol** endpoints (real Anthropic, and Anthropic-compatible gateways) report
  `input_tokens` as the *uncached remainder*, with `cache_read_input_tokens` reported separately.
- **OpenAI-protocol** endpoints (DeepSeek's default endpoint, DashScope) report `prompt_tokens` as
  the *whole* input, with `cached_tokens` as a subset of it.
- **Responses** endpoints report `input_tokens` as the whole input, with `cached_tokens` and
  `cache_write_tokens` inside it.

The agent treats input tokens and cache-read tokens as non-overlapping buckets — context occupancy
is their sum — so the OpenAI and Responses adapters subtract the cached parts before handing
counts to the agent layer; the Anthropic adapter needs no adjustment.

## Extended reasoning

Anthropic `thinking` blocks and OpenAI `reasoning_content` are unified behind
`--reasoning-effort` / `--show-reasoning`: OpenAI-protocol backends receive the effort level
directly as `reasoning_effort` (`reasoning.effort` on Responses); Anthropic-protocol backends map it to adaptive thinking or an
explicit token budget, normalized per model family so older models and newer ones both get a
sensible mapping from the same five-level scale.

Next: adding another protocol, or a new tool, follows the pattern in
[Extending octo](/docs/architecture/extending-octo/).
