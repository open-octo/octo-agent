# Unified Agent Tool Design

## 目标

把 `launch_agent` + `send_message` + `agent_status` + `kill_agent` + 4 个 preset agent 合并成一个统一的 `sub_agent` 工具，参考 Claude Code 的设计。

## 当前问题

1. **工具太多**：5 个独立工具（launch_agent, send_message, agent_status, kill_agent, 4 presets）
2. **同步/异步两套机制**：`SubAgentManager` 的 `Start`/`Send` vs `RunSync`/`ContinueSync`
3. **没有 Fork 模式**：每次 spawn 都是全新 agent，无法继承上下文
4. **Preset 硬编码**：agent 类型写在 Go 代码里，用户无法自定义

## 新设计

### sub_agent 工具（统一）

```go
type AgentTool struct{}

func (AgentTool) Definition() agent.ToolDefinition {
    return agent.ToolDefinition{
        Name: "sub_agent",
        Description: "Launch an autonomous sub-agent to handle a focused sub-task. ...",
        Parameters: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "description": { "type": "string", ... },
                "prompt": { "type": "string", ... },
                "subagent_type": { "type": "string", "optional": true,
                    "description": "Agent type: 'explore', 'plan', 'general', 'code-review', or omit to fork yourself" },
                "run_in_background": { "type": "boolean", "optional": true,
                    "description": "Run asynchronously. You'll be notified when it completes." },
                "model": { "type": "string", "optional": true, ... },
                "tools": { "type": "array", "optional": true, ... },
            },
            "required": []string{"description", "prompt"},
        },
    }
}
```

### 行为

| 参数组合 | 行为 |
|---|---|
| `run_in_background: true` | 异步启动，返回 `async_launched` 状态 |
| `run_in_background: false/omitted` | 同步运行，返回完整结果 |
| `subagent_type: "explore"` | 使用预设 persona + read-only 工具 |
| `subagent_type: omitted` | **Fork 模式**：继承父 agent 完整上下文 |

### Fork 模式（新）

当 `subagent_type` 省略时：
1. 子 agent 继承父 agent 的完整 history
2. 子 agent 共享父 agent 的 system prompt
3. 子 agent 使用相同的 model（除非显式覆盖）
4. 子 agent 可以访问相同的工具（递归防护：移除 sub_agent 工具）
5. 同步模式下直接返回结果；异步模式下通过通知返回

### 删除的工具

- `launch_agent` → 合并到 `sub_agent`
- `send_message` → **删除**。旧设计需要模型先 `launch_agent` 再 `send_message` 才能继续对话，交互复杂且容易出错。新设计中同步模式直接返回结果，异步模式通过通知返回，模型只需一次调用即可完成任务，无需手动续聊。
- `agent_status` → 删除（异步任务通过通知系统管理）
- `kill_agent` → 删除（异步任务通过通知系统管理）
- `explore_agent`, `plan_agent`, `general_agent`, `code_review_agent` → 合并到 `sub_agent` 的 `subagent_type` 参数

### Agent 定义系统（新）

从 `~/.octo/agents/*.md` 加载 agent 定义：

```markdown
---
name: explore
description: Read-only exploration agent
tools: ["read_file", "grep", "glob", "terminal"]
read_only: true
model: sonnet
---

You are a read-only exploration sub-agent. Your job is to locate and understand code...
```

### 实现步骤

1. 创建 `internal/tools/agent.go`（新的统一 sub_agent 工具）
2. 创建 `internal/tools/agent_presets.go`（agent 预设定义）
3. 创建 `internal/tools/spawner.go`（Spawner 接口）
4. 删除 `launch_agent.go`, `send_message.go`, `agent_status.go`, `kill_agent.go`, `preset_agents.go`
5. 更新 `registry.go`
6. 更新 `subagent_manager.go`
7. 更新测试

## 兼容性

- CLI 行为不变：默认同步运行
- Server/IM 行为不变：默认同步运行（`run_in_background` 为 false）
- 异步模式通过 `run_in_background: true` 显式开启
