---
title: Extending octo
description: Writing a new tool or a new chat channel adapter.
---

octo is distributed as a binary, so `internal/` carries no compatibility promise — but the shapes
below are stable enough in practice to extend from, and are the actual interfaces the built-in
tools and adapters implement.

## Writing a tool

A tool is a `ToolDefinition` (its JSON-Schema-described interface to the model) plus a
`ToolExecutor` (what actually runs):

```go
type ToolDefinition struct {
    Name        string
    Description string
    Parameters  map[string]any // JSON Schema object
}

type ToolExecutor interface {
    Execute(ctx context.Context, name string, input map[string]any) (ToolResult, error)
}
```

`ToolResult.Text` is the required textual summary — shown in the UI and sent back to the model.
`ToolResult.Blocks` carries optional rich content (images, structured data) that the provider
adapter serializes into the vendor-specific wire format. A denied permission never reaches
`Execute` at all: the agent loop synthesizes an error `ToolResult` itself, so the model sees the
denial and can adapt instead of the run aborting.

Register a new tool in `internal/tools/` next to the existing implementations (`terminal.go`,
`edit_file.go`, …) and add it to `DefaultTools()` if it should ship enabled by default.

## Writing a channel adapter

Every IM bridge implements one interface:

```go
type Adapter interface {
    Platform() string
    Start(ctx context.Context, onMessage func(InboundEvent)) error
    Stop() error
    SendText(chatID, text string, replyTo string) SendResult
    SendFile(...) SendResult
}
```

`Start` blocks, calling `onMessage` for every inbound event, until the context is cancelled or
`Stop` is called — the same shape for a polling adapter and a websocket-push one. Look at
`internal/channel/` for the six shipped adapters (WeChat iLink, Feishu, DingTalk, WeCom, Discord,
Telegram); a new one plugs into `octo serve`'s Channels panel the same way they do, with no changes
needed above `internal/channel/`.

## Adding a provider protocol

A third wire format (beyond Anthropic Messages and OpenAI Chat Completions) is a new package under
`internal/provider/`, implementing the `Sender`/`StreamingSender`/`ToolSender`/`ToolStreamingSender`
stack described in [Provider protocols](/docs/architecture/provider-protocols/) — the agent layer
doesn't need to change at all, since it's written against those interfaces, not a concrete client.

See [Contributing](/docs/community/contributing/) for the PR workflow, test conventions, and what
reviewers look for.
