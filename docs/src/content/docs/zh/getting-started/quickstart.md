---
title: 快速开始
description: 从安装到跑完第一个任务，不到五分钟。
---

```bash
export ANTHROPIC_API_KEY=sk-ant-...      # 或 OPENAI_API_KEY=...

# 一次性设置：保存默认 provider/model（下次就不用重复上面的 export 了）
octo config
```

## Headless 单发

在脚本或 CI 里，`octo` 是 `claude -p` 风格的单发模式：一个 prompt，一次完整的 agentic 工具循环，然后退出。
内置工具（shell、读写改文件、搜索）、MCP 服务、skills 默认全部开启，所以一句话就能真正把事情做完。

```bash
octo "给 octo config show 加一个 --json 参数并跑测试"

# prompt 也可以来自管道或文件——写脚本 / CI 时很方便：
echo "总结一下上一次提交改了什么" | octo
octo --prompt-file ./task.md
```

## 交互式多轮对话

在终端里不带消息直接运行 `octo` 就会进入 TUI——富工具卡片、会话自动保存。

```bash
octo
octo sessions        # 列出已保存的会话
octo -c              # 从列表里选一个最近的会话
octo -c <session-id>
```

## 流式输出与推理

默认开启流式输出；`--stream=false` 改成缓冲模式，只打印最终回复文本——适合重定向到文件里捕获。

```bash
octo --stream=false "..."

# 扩展推理：设置强度。终端从不渲染思考轨迹；--show-reasoning 只控制
# 是否把轨迹提供给 Web UI。
octo --reasoning-effort high "..."
octo --show-reasoning=false "..."   # 保留推理，但不给 Web UI 显示轨迹
```

## 纯聊天，不带工具

```bash
octo --no-tools "..."
```

## 仓库约定

```bash
octo init            # 为当前仓库生成一份 .octorules
```

下一步：[选择 Provider](/docs/zh/getting-started/choose-a-provider/)，或者直接跳到某篇
[指南](/docs/zh/guides/connect-mcp-servers/)。
