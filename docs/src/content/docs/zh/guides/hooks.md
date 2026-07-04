---
title: 用 Hooks 自动化
description: 在 agent 生命周期的固定节点运行你自己的 shell 命令。
---

Hooks 会在生命周期的固定节点执行一条外部命令——这套模型来自 Claude Code，octo 把它搬到了每一种
传输方式上（CLI、Web、IM），背后是同一套引擎。

## 七个事件

| 事件 | 触发时机 | 能否拦截 |
|---|---|---|
| `SessionStart` | 每次打开一个逻辑会话时（一次） | stdout 会折叠进上下文 |
| `UserPromptSubmit` | 每一轮用户消息之前 | stdout 会折叠进上下文 |
| `PreToolUse` | 每次工具调度之前 | 可以——能允许/拦截这次调用 |
| `PostToolUse` | 每次工具成功返回之后 | stdout 会折叠进那次工具结果的文本里 |
| `Stop` | 一轮 assistant 回复结束时，无论成功还是出错 | 仅副作用 |
| `SubagentStop` | 一个子代理结束时 | 仅副作用 |
| `PreCompact` | 历史压缩之前 | 仅副作用 |

`PreToolUse` 是严格意义上唯一的门控点。其余六个都是观察/副作用节点；`SessionStart`、
`UserPromptSubmit`、`PostToolUse` 还会额外把 stdout 折叠回模型的上下文——另外三个
（`Stop`、`SubagentStop`、`PreCompact`）的 stdout 会被直接丢弃。

## Hooks 配置在哪

两个 YAML 文件，都会加载、都会跑（不是后者覆盖前者）：

- `~/.octo/hooks.yml` —— 用户级，始终加载。
- `.octo/hooks.yml` —— 项目级。第一次遇到会提示你要不要信任它（做指纹校验），毕竟一个项目文件
  可能是你 clone 下来的仓库自带的。

```yaml
hooks:
  PreToolUse:
    - matcher: "terminal"                # 对工具名做正则匹配；仅 PreToolUse/PostToolUse 支持
      command: "./scripts/guard.sh"
      timeout: 5s                        # Go duration 字符串；默认 5s，上限 30s

  PostToolUse:
    - matcher: "terminal"
      command: "audit-logger"            # stdout 折叠进那次工具结果的文本

  Stop:
    - command: "./scripts/notify-on-commit.sh"
      async: true                        # 只有 Stop / SubagentStop / PreCompact 能设
```

| 字段 | 是否必填 | 说明 |
|---|---|---|
| `command` | 是 | 通过平台 shell 运行（`sh -c` / PowerShell）；JSON payload 从 stdin 传入 |
| `matcher` | 否 | 对工具名的正则；不填默认匹配所有。在 `PreToolUse`/`PostToolUse` 之外的事件上会被忽略（不是报错） |
| `timeout` | 否 | duration 字符串；不合法或不填就用默认的 5s，不管你写多大都会被压到 30s 封顶 |
| `async` | 否 | 默认 `false`。在 `SessionStart`/`UserPromptSubmit`/`PostToolUse`——这三个会往上下文里注入内容的事件——上设为 `true` 是加载期的硬报错：这三个必须同步跑，否则没东西可折叠 |

不认识的事件名同样是加载期硬报错——不会有拼错了却悄悄忽略的情况。

:::note[老的环境变量写法]
`OCTO_HOOK_PRE_TURN` / `OCTO_HOOK_POST_TURN` / `OCTO_HOOK_TIMEOUT` 依然管用，会被自动转换成一个
`UserPromptSubmit` hook（PRE_TURN）和一个 `Stop` hook（POST_TURN，强制异步）。如果不只是想挂一条
简单命令，优先用 `hooks.yml`。
:::

## `PreToolUse` 的拦截契约

同一个工具匹配到的多个 `PreToolUse` hook 按注册顺序依次跑；第一个给出拦截结论的胜出：

- **退出码 `2`** → 拦截。原因取 stdout 里的 `{"decision":"block","reason":"..."}`（如果有），
  否则取 stderr 最后 500 个字符，都没有就用一句通用的"blocked by PreToolUse hook"。
- **退出码 `0`** 且 stdout 能解析成 `{"decision":"approve"|"block","reason":"..."}` → 直接采用
  这个结论——`approve` 会**完全跳过正常的权限引擎**。
- **退出码 `0`**，解析不出结论 → 不表态，交给正常的权限引擎决定。
- **超时，或其他任何非零退出码** → 当作非阻塞性错误处理；工具调用照常执行，就像这个 hook
  什么都没说一样。

`PreToolUse` 只跑 shell hook——这个事件没有进程内 hook 这条路。

stdin 传入的 payload（以 `PreToolUse` 为例；其他事件的信封结构一样，只是带的字段不同）：

```json
{
  "event": "PreToolUse",
  "session_id": "sess_abc123",
  "cwd": "/repo",
  "transcript_path": "~/.octo/sessions/sess_abc123.json",
  "model": "claude-sonnet-5",
  "transport": "cli",
  "tool_name": "terminal",
  "tool_input": { "command": "rm -rf /" }
}
```

一个会拦截的例子 —— `scripts/guard.sh`：

```bash
#!/bin/sh
payload=$(cat)
cmd=$(echo "$payload" | jq -r '.tool_input.command // empty')
case "$cmd" in
  *"rm -rf"*)
    echo "refusing destructive rm -rf" >&2
    exit 2                 # 退出码 2 = 拦截；stderr 就是原因
    ;;
esac
exit 0                      # 不表态——落到权限引擎那一层
```

## 注入的内容是怎么到模型手上的

对 `SessionStart`、`UserPromptSubmit`、`PostToolUse` 这三个事件，每个 hook 的 stdout 要么是
`{"additional_context": "..."}` 这个 JSON 对象里的 `additional_context` 字段，要么——如果 stdout
不是这个形状——就直接用原始文本。同一个事件挂了多个 hook 时，它们的输出之间用一个空行拼接起来。

## `octo hooks list`

```bash
octo hooks list
```

依次打印：环境变量兼容写法配出来的 hook（如果有）、`~/.octo/hooks.yml` 里的每一条用户级 hook
（事件、命令、matcher、是否异步）、每一条项目级 hook 及其信任状态（`trusted` /
`UNTRUSTED — run octo in this repo and approve to enable`），以及一行固定文字，说明 octo 自带
的两个内置 hook（`UserPromptSubmit` 上的记忆提醒、`PostToolUse` 上的保存提醒，两者都只在存在记忆
目录时生效）。如果什么都没配，会直接说明这一点，不会打印一个空区块。

下一步：一个常见搭配是在 `terminal` 上挂一个 `PostToolUse` hook，在 `git commit` 之后提醒保存记忆——
见[让它拥有记忆](/docs/zh/guides/memory/)。
