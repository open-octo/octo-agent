---
title: 扩展 octo
description: 如何写一个新工具，或者一个新的聊天渠道适配器。
---

octo 以二进制形式分发，所以 `internal/` 不承诺任何兼容性——但下面这些形状在实践中足够稳定，
值得基于它们做扩展，而且就是内置工具和适配器实际实现的接口。

## 写一个工具

一个工具由一个 `ToolDefinition`（它对模型暴露的、用 JSON Schema 描述的接口）加上一个
`ToolExecutor`（真正执行的部分）组成：

```go
type ToolDefinition struct {
    Name        string
    Description string
    Parameters  map[string]any // JSON Schema 对象
}

type ToolExecutor interface {
    Execute(ctx context.Context, name string, input map[string]any) (ToolResult, error)
}
```

`ToolResult.Text` 是必填的文本摘要——会显示在 UI 里，也会发回给模型。`ToolResult.Blocks`
携带可选的富内容（图片、结构化数据），由 provider 适配器序列化成对应厂商的 wire 格式。
被权限拒绝的调用根本不会走到 `Execute`：agent 循环会自己合成一个错误的 `ToolResult`，
让模型看到拒绝原因、能够调整策略，而不是整次运行直接中断。

在 `internal/tools/` 里挨着已有实现（`terminal.go`、`edit_file.go` 等）注册新工具，
如果它应该默认开启，再加进 `DefaultTools()`。

## 写一个渠道适配器

每一个 IM 桥接都实现同一个接口：

```go
type Adapter interface {
    Platform() string
    Start(ctx context.Context, onMessage func(InboundEvent)) error
    Stop() error
    SendText(chatID, text string, replyTo string) SendResult
    SendFile(...) SendResult
}
```

`Start` 会阻塞，对每一个入站事件调用 `onMessage`，直到 context 被取消或者 `Stop` 被调用——
轮询式的适配器和 websocket 推送式的适配器用的是同一套形状。参考 `internal/channel/`
里已经实现的六个适配器（微信 iLink、飞书、钉钉、企微、Discord、Telegram）；新写一个
接入 `octo serve` 的 Channels 面板的方式和它们完全一样，`internal/channel/` 之上的代码
不需要任何改动。

## 新增一个 Provider 协议

第三种 wire 格式（在 Anthropic Messages 和 OpenAI Chat Completions 之外）就是
`internal/provider/` 下的一个新包，实现 `provider.Provider`（必须），以及可选的
`provider.StreamingProvider` / `provider.ToolProvider` / `provider.ToolStreamingProvider`。
[Provider 协议](/docs/zh/architecture/provider-protocols/)里描述的、面向 agent 的 `Sender`
接口栈由 `internal/app` 的适配器统一实现一次——你的 provider 包接在它下面即可，agent 层完全
不需要改动。

PR 流程、测试约定、以及 reviewer 关注什么，见[贡献指南](/docs/zh/community/contributing/)。
