---
title: Provider 协议
description: anthropic 和 openai 两个适配器各自屏蔽掉了什么。
---

octo 说两种协议的"母语"。Provider 各自的特殊之处完全封装在
`internal/provider/{anthropic,openai}/` 里——agent 层从不关心自己在跟哪种协议打交道。

## Anthropic Messages

- 认证走 `x-api-key` + `anthropic-version` 请求头。
- `system` 是一个顶层字段，不是一条消息。
- 内容 block：`[{type: "text", text}]`。
- SSE 聚合器按 `message_start` / `content_block_delta` / `message_delta` 分发。
- 工具调用会先出现一个类型为 `tool_use` 的 `content_block_start`，后面跟着若干 `input_json_delta`
  片段，累积成最终的参数。
- `stop_reason: "tool_use"` 是 agent 层用来判断"这一轮想调用工具"的信号。

## OpenAI Chat Completions

- 认证走 `Authorization: Bearer`。
- `system` 放在 `messages[0]` 里，而不是单独的字段。
- SSE 聚合器解析 `chat.completion.chunk` 对象，并且能容忍缺失的 `[DONE]` 哨兵——部分第三方
  OpenAI 兼容服务会省略它。
- 工具调用出现在 `delta.tool_calls[]` 里，按 `tool_calls[i].index` 分片；聚合器先按 index
  拼接片段，再解析出 JSON 参数。
- `finish_reason: "tool_calls"` 会被归一化成 agent 层看到的 `"tool_use"`，所以无论后端是谁，
  agent 循环看到的拼写都只有一种。
- 每个流式请求都会带上 `stream_options.include_usage: true`——DashScope（百炼）和真正的 OpenAI
  在没有这个字段时根本不会发 usage chunk，流式的一轮就会报出零 token。DeepSeek 无论有没有这个
  字段都会发 usage。

## Cache token 统计

Cache 的语义是按*协议*区分的，不是按 vendor：

- **Anthropic 协议**端点（真正的 Anthropic，以及 Anthropic 兼容网关）把 `input_tokens` 报告为
  *未命中缓存的剩余部分*，`cache_read_input_tokens` 单独报告。
- **OpenAI 协议**端点（DeepSeek 默认端点、DashScope）把 `prompt_tokens` 报告为*完整输入*，
  `cached_tokens` 是它的一个子集。

agent 把输入 token 和缓存命中 token 当成两个不重叠的桶——上下文占用是两者之和——所以
OpenAI 适配器在把计数交给 agent 层之前会先从 prompt 里减去 cached 部分；Anthropic 适配器
不需要这个调整。

## 扩展推理

Anthropic 的 `thinking` block 和 OpenAI 的 `reasoning_content` 被统一到了
`--reasoning-effort` / `--show-reasoning` 背后：OpenAI 协议后端直接把强度值原样作为
`reasoning_effort` 传过去；Anthropic 协议后端会把它映射成自适应思考，或者一个明确的
token 预算，并按模型 family 做归一化，让新旧模型都能从同一套五档强度里得到合理的映射。

下一步：加一个第三种协议，或者新增一个工具，都遵循
[扩展 octo](/docs/zh/architecture/extending-octo/) 里的模式。
