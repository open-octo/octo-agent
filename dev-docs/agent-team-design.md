# 技术方案：Agent Team

## 1. 背景与目标

### 1.1 背景

octo 现有的 `sub_agent` 系统支持主 Agent 临时 spawn 一个子 Agent 协作完成任务。当前模型是 **ad-hoc 模式**：主 Agent 即兴决定 spawn 谁、给它什么角色、用什么工具。每次都是单次、不可复用的。

但在实际场景中，用户经常需要面对 **重复性的多角色协作任务**——例如 code review 需要 correctness / security / 性能三个角度并行审查，debug 需要 日志分析 / 代码定位 / 根因假设 三个角色协作。这类任务有固定的角色组合模式，每次重新让主 Agent 即兴编排效率低、质量不稳定。

### 1.2 目标

新增 **Agent Team** 能力：

1. 允许用户预定义 **Team Template**（声明式描述角色组合、每个角色的 model/tools/system prompt）
2. 主 Agent 可以 **按模板实例化一个 Team**，并行或串行编排多个 member 协作完成任务
3. 用户通过与主 Agent 对话触发 team，**主 Agent 是唯一对话入口**，内部 member 对用户不可见
4. 用户提供 **Activity Feed**，实时看到每个 member 在做什么（状态 + 工具调用 + 输入输出）
5. Member 之间通过 **Blackboard**（共享空间）通信协作

### 1.3 与 Multi-Agent System 的关系

Agent Team 与 Multi-Agent System 是 octo Agent 生态中两个 **完全独立、互不依赖** 的功能：

| 维度 | Multi-Agent System | Agent Team |
|------|-------------------|------------|
| 用户交互 | 多个 expert agent 平权暴露，用户直接选 | 唯一入口是主 Agent，内部 member 隐藏 |
| 路由 | @ 提及 / 频道绑定 | 主 Agent 编排 |
| 成员身份 | 面向用户的"专家" | 主 Agent 背后的"工人" |
| 配置 | `agents.yml` 定义 profile | `teams.yml` 定义模板 |

### 1.4 范围

**包含**：
- Team Template 声明式定义（YAML + 内置模板）
- Template 实例化（按模板 spawn member）
- Orchestration 引擎（parallel / sequential 策略）
- Blackboard 共享空间（文件系统实现）
- Activity Feed（member 实时活动推送，全量粒度）
- 触发模型：显式 @ + 主 Agent 判断确认（混合模式）
- Member 完成聚合（LLM summary / vote / concat）

**不包含**（后续迭代）：
- Web UI 模板创建/编辑界面（MVP 只做 YAML + 内置模板）
- 主 Agent 自动判断的可配开关（MVP 默认开启确认流程）
- Member 间星型直接通信（MVP 只做 blackboard）
- Team 间的嵌套/编排

## 2. 名词表

| 术语 | 定义 |
|------|------|
| **Team Template** | 声明式定义的角色组合，描述"一个团队长什么样"（哪些角色、各用什么模型工具和 prompt） |
| **Team Instance** | 模板的运行时实例，主 Agent 一次 `team_spawn` 创建一个 instance |
| **Member** | Team 中的一个角色，对应一个被 spawn 出的 sub-agent |
| **Blackboard** | 共享工作目录，member 通过文件系统读写共享上下文，实现间接通信 |
| **Activity Feed** | 向用户实时推送的 member 活动流（状态 + 工具调用 + 输入输出） |
| **Orchestrator** | 主 Agent 内部的编排逻辑：按模板创建 member → 分发任务 → 监控 → 汇总 |
| **Aggregator** | 成员完成后的产出合成策略（LLM 汇总 / 投票 / 拼接） |
| **Main Agent / 前台 Agent** | 用户直接对话的 Agent，Agent Team 的唯一入口 |
| **Member Agent / 隐藏 Agent** | 主 Agent 背后 spawn 的 sub-agent，用户不可直接对话 |

## 3. 业务流程

### 3.1 触发流程（混合模式）

```flowchart TB
    A[用户发送消息] --> B{是否显式 @ team?}
    B -->|是| C[team_spawn 对应模板]
    B -->|否| D{主 Agent 判断是否适合用 team?}
    D -->|是| E[建议用户: 用 XX team 处理更好，要启动吗?]
    E -->|用户确认| C
    E -->|用户拒绝| F[正常单 agent 处理]
    D -->|否| F
    C --> G[Orchestration 编排执行]
```

### 3.2 Orchestration 完整流程

```flowchart TB
    S0[team_spawn 模板名 + 任务] --> S1[加载模板]
    S1 --> S2{策略 = parallel?}
    S2 -->|是| S3[并行 spawn 所有 member]
    S2 -->|否| S4[串行: spawn 第一个 member]
    S3 --> S5[创建 Blackboard 共享目录]
    S4 --> S5
    S5 --> S6[给每个 member 发送任务 prompt]
    S6 --> S7[member 各自工作]
    S7 --> S8[member 完成 → 结果写入 blackboard]
    S8 --> S9{completion 条件满足?}
    S9 -->|否| S7
    S9 -->|是| S10[Aggregator 汇总所有产出]
    S10 --> S11[主 Agent 回复用户]
```

### 3.3 Activity Feed 推送流程

```flowchart LR
    A[Member 调工具] --> B[SubAgentEvent sink]
    B --> C[Team Activity Bus]
    C --> D[WebSocket/SSE]
    D --> E[Web UI 活动面板]
    D --> F[IM 消息推送]
```

## 4. 架构设计

### 4.1 系统架构图

```
┌──────────────────────────────────────────────────────────┐
│                      用户 (Web UI / IM)                    │
│                          ↕                                │
│                 ┌────────┴────────┐                       │
│                 │   主 Agent 会话   │                       │
│                 │  (唯一对话入口)   │                       │
│                 └────────┬────────┘                       │
│                          │                                │
│    ┌─────────────────────┼─────────────────────┐          │
│    │  tools layer        │                     │          │
│    │  ┌─────────────────┐│ ┌─────────────────┐│          │
│    │  │ TeamTool        ││ │ sub_agent tool  ││  ← 共存  │
│    │  │ team_list       ││ │ (已有)          ││          │
│    │  │ team_spawn      ││ └─────────────────┘│          │
│    │  │ team_send       ││                     │          │
│    │  │ team_status     ││                     │          │
│    │  │ team_kill       ││                     │          │
│    │  └────────┬────────┘│                     │          │
│    └───────────┼─────────┘─────────────────────┘          │
│                │                                           │
│    ┌───────────▼───────────────────────────────────────┐  │
│    │              Team Orchestrator                     │  │
│    │  - 加载模板 → 生成 SpawnRequest                    │  │
│    │  - 调用 Spawner (已有)                             │  │
│    │  - 监控 member 状态                                │  │
│    │  - 触发 Aggregator                                 │  │
│    └───────────┬───────────────────────────────────────┘  │
│                │                                           │
│    ┌───────────▼───────────────────────────────────────┐  │
│    │           SubAgentManager (已有)                   │  │
│    │  Spawn / Send / ContinueSync / Read / Kill        │  │
│    └───────────┬───────────────────────────────────────┘  │
│                │                                           │
│    ┌───────────▼───────────────────────────────────────┐  │
│    │          Activity Feed Engine (新增)               │  │
│    │  - 订阅 SubAgentEvent sink                         │  │
│    │  - 推送 member 活动到 WebSocket                    │  │
│    └───────────────────────────────────────────────────┘  │
│                                                           │
│    ┌────────────────────────────────────────────────────┐ │
│    │               Blackboard (文件系统)                  │ │
│    │  ~/.octo/teams/<team-id>/shared/                   │ │
│    │    ├── context.md     (共享任务上下文)              │ │
│    │    ├── findings/      (各 member 产出)              │ │
│    │    │    ├── reviewer-correctness.md                 │ │
│    │    │    ├── reviewer-security.md                    │ │
│    │    │    └── reviewer-perf.md                        │ │
│    │    └── summary.md      (aggregator 汇总产出)        │ │
│    └────────────────────────────────────────────────────┘ │
└───────────────────────────────────────────────────────────┘
```

### 4.2 模块职责

| 模块 | 职责 | 已有/新增 |
|------|------|----------|
| `internal/tools/team.go` | Team 工具 handler（team_list/spawn/send/status/kill） | 新增 |
| `internal/team/orchestrator.go` | 编排引擎：加载模板 → spawn members → 监控 → 汇总 | 新增 |
| `internal/team/template.go` | Team Template 加载/解析/校验 | 新增 |
| `internal/team/blackboard.go` | Blackboard 共享空间生命周期管理 | 新增 |
| `internal/team/activity.go` | Activity Feed 事件聚合与推送 | 新增 |
| `internal/team/aggregator.go` | 产出汇总（LLM summary / vote / concat） | 新增 |
| `internal/app/spawner.go` | sub-agent 生命周期管理 | **已有，不改** |
| `internal/tools/subagent_manager.go` | async sub-agent 状态跟踪 | **已有，不改** |

### 4.3 和现有 sub-agent 系统的关系

Agent Team 是现有 `Spawner` + `SubAgentManager` 的 **上层编排层**，不修改已有 sub-agent 基础设施。具体来说：

- `team_spawn` 调用 `SubAgentManager.Spawn()` 创建每个 member（复用现有路径）
- member 间通信通过 Blackboard 文件系统（不经过 SubAgentManager 的 Send）
- Activity Feed 通过扩展 `SubAgentEventSink` 实现（在已有 `tools.SubAgentEventSink(ctx)` 基础上增加 team 维度）

## 5. 详细设计

### 5.1 Team Template

#### 5.1.1 数据结构

```go
type TeamTemplate struct {
    Name        string           `json:"name" yaml:"name"`
    Description string           `json:"description" yaml:"description"`
    Members     []MemberSpec     `json:"members" yaml:"members"`
    Orchestrator OrchestratorCfg `json:"orchestrator" yaml:"orchestrator"`
}

type MemberSpec struct {
    Role         string   `json:"role" yaml:"role"`
    Description  string   `json:"description" yaml:"description"`
    Model        string   `json:"model,omitempty" yaml:"model,omitempty"`
    SystemPrompt string   `json:"system_prompt" yaml:"system_prompt"`
    Tools        []string `json:"tools,omitempty" yaml:"tools,omitempty"`
    DisallowedTools []string `json:"disallowed_tools,omitempty" yaml:"disallowed_tools,omitempty"`
    LeanContext  bool     `json:"lean_context,omitempty" yaml:"lean_context,omitempty"`
    Schema       string   `json:"schema,omitempty" yaml:"schema,omitempty"`
}

type OrchestratorCfg struct {
    Strategy    string `json:"strategy" yaml:"strategy"`           // "parallel" | "sequential"
    Completion  string `json:"completion" yaml:"completion"`       // "all_done" | "any_done"
    Aggregation string `json:"aggregation" yaml:"aggregation"`     // "llm_summary" | "vote" | "concat"
    SharedSpace bool   `json:"shared_space" yaml:"shared_space"`   // 是否启用 Blackboard
}
```

#### 5.1.2 模板加载优先级

1. 内置模板（嵌入 binary，提供 `code-review-team`、`full-stack-team`、`debug-team` 等默认模板）
2. `~/.octo/teams/<name>.yml` — 用户自定义（Web UI 创建的模板也存入此目录）

同名时：用户自定义覆盖内置。

#### 5.1.3 内置模板示例

```yaml
# code-review-team（内置）
name: code-review-team
description: "代码审查团队 — 从正确性、安全性、性能三个角度并行 review"
members:
  - role: reviewer-correctness
    description: "正确性与逻辑审查"
    model: claude-sonnet-4-20250514
    system_prompt: |
      你是一个代码审查者，专注于：
      1. 逻辑正确性 — 算法是否正确实现了需求
      2. 边界条件 — 输入验证、错误路径、并发安全
      3. 潜在 bug — 空指针、越界、竞态
      不关心代码风格、命名和性能问题。
    tools: [read_file, grep, glob, terminal]

  - role: reviewer-security
    description: "安全审查"
    model: claude-sonnet-4-20250514
    system_prompt: |
      你是一个安全审查者，专注于：
      1. 注入漏洞 — SQL/NoSSQL/命令注入
      2. XSS / CSRF / SSRF
      3. 权限越权 — 水平/垂直越权
      4. 敏感信息泄露 — 日志、错误消息
    tools: [read_file, grep, glob]

  - role: reviewer-perf
    description: "性能审查"
    model: claude-haiku-4-20250514
    system_prompt: |
      你是一个性能审查者，专注于：
      1. 时间复杂度 — 是否有不必要的嵌套循环
      2. N+1 查询 — 数据库/网络调用是否有批量优化空间
      3. 内存泄漏 — 未释放的资源、大对象
      4. 并发效率 — 锁粒度、goroutine 泄漏
    tools: [read_file, grep, glob]
    lean_context: true  # 性能用 haiku + lean system

orchestrator:
  strategy: parallel
  completion: all_done
  aggregation: llm_summary
  shared_space: true
```

### 5.2 Orchestration 引擎

#### 5.2.1 编排流程

```
team_spawn("code-review-team", "review PR #123")
  │
  ├─ 1. 加载模板 → 校验完整性
  ├─ 2. 创建 Blackboard 目录 ~/.octo/teams/t_<random>/shared/
  ├─ 3. 写入 context.md (任务上下文: "review PR #123" + diff 内容)
  ├─ 4. 对每个 member 调用 Spawner.Spawn()
  │     └─ SpawnRequest{
  │          Prompt:  "<任务上下文>\n\n你的角色: <role description>\n\n请把你的产出写到: <blackboard>/findings/<role>.md",
  │          Model:   <member.model 或继承主 agent>,
  │          SystemSuffix: <member.system_prompt>,
  │          Tools:   <member.tools 或继承全部>,
  │          LeanContext: <member.lean_context>,
  │          Schema:  <member.schema>,
  │        }
  ├─ 5. 等待 completion 条件
  │     ├─ parallel + all_done: 等全部 member 完成
  │     └─ sequential: 链式替换 prompt
  ├─ 6. 调用 Aggregator 汇总
  │     └─ llm_summary: 读取各 member 的 findings → 用 LLM 生成结构化 summary
  └─ 7. 汇总结果作为 team_spawn tool_result 返回给主 Agent
```

#### 5.2.2 并行等待实现

并行策略下，`team_spawn` 采用 **同步阻塞** 路径（类似现有的 `SetSynchronous(true)` 模式）：

- 调用方（主 Agent 的 runLoop）阻塞等待 team_spawn tool_result
- Orchestrator 内部并发等待所有 member 的 `SubAgentNotification`
- 每个 member 退出时触发 `onExit` 回调 → Orchestrator 记录完成状态
- 全部完成后触发 Aggregator → 返回最终结果

超时：默认 10 分钟，可在模板中配置 `orchestrator.timeout_seconds`。

#### 5.2.3 串行策略

串行策略下，member 按模板列表顺序执行：

```
spawn member_1 → 等待完成 → 把 member_1 产出附加到 prompt → spawn member_2 → ...
```

适用于 "调研 → 设计 → 实现 → 测试" 的链式任务。

### 5.3 Blackboard 共享空间

#### 5.3.1 目录结构

```
~/.octo/teams/
└── t_<16位随机hex>/              # Team Instance 目录
    ├── meta.json                 # TeamInstance 运行时数据
    └── shared/                   # Blackboard 根目录
        ├── context.md            # 任务上下文（主 Agent 写入）
        ├── findings/             # 各 member 产出
        │   ├── reviewer-correctness.md
        │   ├── reviewer-security.md
        │   └── reviewer-perf.md
        └── summary.md            # Aggregator 最终产出
```

#### 5.3.2 生命周期

1. **创建**：`team_spawn` 时创建目录 + meta.json
2. **读写**：member 通过 `read_file` / `write_file` / `edit_file` 工具读写 shared/ 目录（filesystem 天然是并发安全的，Go 的 `os.WriteFile` 在大多数 FS 上是原子的）
3. **销毁**：team 完成后保留 24 小时（供用户查看 Activity Feed 历史），过期由 GC 清理
4. **手动清理**：`team_kill` 触发后立即标记为 `failed`，目录保留但不自动清理

#### 5.3.3 并发安全

- member 写入各自独立的 `findings/<role>.md`（无写冲突）
- `context.md` 只由主 Agent 写入（member 只读）
- `summary.md` 由 Aggregator 在所有 member 完成后写入（唯一写）
- 无共享可变状态 → 不需要加锁

### 5.4 Activity Feed

#### 5.4.1 推送粒度（全量）

每个 member 的工具调用都实时推送：

```json
{
  "team_id": "t_a1b2c3d4",
  "member_id": "agent_5",
  "role": "reviewer-correctness",
  "event": "tool_started",
  "tool_name": "terminal",
  "tool_input": {"command": "go test ./auth/..."},
  "timestamp": "2026-07-16T10:30:00Z"
}
```

```json
{
  "team_id": "t_a1b2c3d4",
  "member_id": "agent_5",
  "role": "reviewer-correctness",
  "event": "tool_done",
  "tool_name": "terminal",
  "tool_output": "PASS\nok  0.031s",
  "timestamp": "2026-07-16T10:30:05Z"
}
```

```json
{
  "team_id": "t_a1b2c3d4",
  "member_id": "agent_5",
  "role": "reviewer-correctness",
  "event": "member_done",
  "result_summary": "Found 2 potential bugs: ...",
  "tokens_in": 3200,
  "tokens_out": 1800,
  "timestamp": "2026-07-16T10:31:00Z"
}
```

#### 5.4.2 事件来源

复用现有 `SubAgentEventSink`：

- `EventToolStarted` → `{"event": "tool_started", ...}`
- `EventToolError` → `{"event": "tool_error", ...}`
- `EventTurnDone` 里如果 agent 在 sub-agent context 中 → `{"event": "member_done", ...}`

新增事件类型：

- `EventMemberSpawned` — member 创建完成
- `EventTeamSpawned` — team 实例创建
- `EventTeamCompleted` — team 整体完成
- `EventTeamFailed` — team 失败/超时

#### 5.4.3 传输协议

- **Web UI**：通过现有 WebSocket 通道（扩展 `ws` 消息类型增加 `team_activity`）
- **IM**：在 team 完成前推送关键节点消息（member_done, team_completed），不推送每个 tool 级别事件（避免刷屏）
- **IM 配置**：用户可通过配置 `team.activity.im_granularity` 控制 IM 推送粒度（`none` | `milestone` | `all`，默认 `milestone`）

### 5.5 Aggregator

#### 5.5.1 策略

| 策略 | 语义 | 适用场景 |
|------|------|----------|
| `llm_summary` | 把所有 member 的产出丢给 LLM 生成结构化汇总 | review 汇总、调研综合 |
| `vote` | 对 boolean 类产出投票（"是否有安全问题？"） | 是/否判定类 |
| `concat` | 原样拼接所有产出，不做 LLM 加工 | raw data 收集 |

#### 5.5.2 llm_summary 实现

```go
func llmSummary(ctx context.Context, model string, findings []string) (string, error) {
    prompt := fmt.Sprintf(`You are a synthesis assistant. Below are findings from %d reviewers.
Combine them into a structured summary with sections:
## Critical (must fix)
## Suggestions (should consider)
## Positive (what's good)

Findings:
%s`, len(findings), strings.Join(findings, "\n---\n"))
    // 进行一次 LLM Turn（非 agent loop，单次 Turn）
    reply, err := agent.New(sender, model).Turn(ctx, prompt)
    return reply.Content, err
}
```

汇总模型：默认使用主 Agent 的 model，可在模板中指定 `orchestrator.aggregation_model`。

### 5.6 工具接口

#### 5.6.1 team_list

列出所有可用模板。

```
Parameters: none
Response:
{
  "teams": [
    {
      "name": "code-review-team",
      "description": "...",
      "member_count": 3,
      "source": "builtin" | "custom"
    }
  ]
}
```

#### 5.6.2 team_spawn

实例化一个 team。

```json
{
  "name": "string (required) — 模板名",
  "task": "string (required) — 具体任务描述"
}
```

**行为**：
1. 加载模板
2. 创建 Blackboard
3. 并行/串行 spawn members
4. 等待完成
5. 聚合
6. 返回 summary

**返回值**（tool_result.Text）：

```
Team "code-review-team" completed. 3/3 members finished.

## Critical (must fix)
- [reviewer-correctness] auth.go:42 — Token validation bypass when input is empty
- [reviewer-security] middleware.go:15 — Missing CSRF token validation on POST

## Suggestions (should consider)
- [reviewer-perf] db.go:88 — N+1 query in user listing, consider JOIN

## Positive
- [reviewer-correctness] Good error handling in payment flow
```

**同步/异步**：

默认同步阻塞（`team_spawn` 作为工具调用阻塞主 Agent 的 runLoop）。当主 Agent 判断需要异步时，用 `run_in_background: true` 参数：

```json
{
  "name": "code-review-team",
  "task": "review PR #123",
  "run_in_background": true
}
```

异步模式下返回 `team_id`，主 Agent 通过 `team_status` 查询进度。

#### 5.6.3 team_send

给运行中的 member 发后续消息。

```json
{
  "team_id": "string (required)",
  "member_id": "string (required) — agent_N 形式",
  "message": "string (required)"
}
```

内部调用 `SubAgentManager.Send()`，复用现有路径。

#### 5.6.4 team_status

查询 team 运行状态。

```json
{
  "team_id": "string (required)"
}
```

**返回值**：

```json
{
  "team_id": "t_a1b2c3d4",
  "template": "code-review-team",
  "status": "running",
  "created_at": "2026-07-16T10:30:00Z",
  "members": [
    {
      "member_id": "agent_5",
      "role": "reviewer-correctness",
      "status": "done",
      "tokens_in": 3200,
      "tokens_out": 1800,
      "completed_at": "2026-07-16T10:31:00Z"
    },
    {
      "member_id": "agent_6",
      "role": "reviewer-security",
      "status": "running",
      "last_tool": "grep"
    }
  ]
}
```

#### 5.6.5 team_kill

终止整个 team。

```json
{
  "team_id": "string (required)"
}
```

对所有 running member 调用 `SubAgentManager.Kill()`。

### 5.7 主 Agent 如何判断是否用 team

在主 Agent 的 system prompt 中注入 team 使用指南。具体做法：

1. `team_list` 工具始终在工具列表中（如果 module 启用）
2. 在主 Agent system prompt 中加入指导：

```
## Agent Team Guidance

When a user's task matches one of the available team patterns, you may
suggest using a team for better quality. Available teams:
- code-review-team: for reviewing code changes from multiple perspectives
- debug-team: for systematic debugging with log/code/hypothesis roles
- (when more teams are added, they will be listed here)

When suggesting a team, say something like:
"I can use the <team-name> team for this — it'll run N specialized
reviewers in parallel. Want me to start it?"

When the user @-mentions a team by name, call team_spawn directly.
```

### 5.8 触发：混合模式

#### 5.8.1 显式触发

用户在消息中 @ team 名：

```
@code-review-team review PR #123
```

主 Agent 的 system prompt 中包含 team 名 → 识别到 → 直接调用 `team_spawn`。

#### 5.8.2 隐式触发（主 Agent 判断 + 确认）

```
用户: "帮我 review 这个 PR"
主 Agent: "这个任务适合用 code-review-team 来处理 — 它会并行运行 3 个专项审查员
          （正确性、安全、性能）。要启动吗？"
用户: "启动"
主 Agent: [调用 team_spawn]
```

**确认流程的必要性**：team 涉及多个 member 并行执行，token 消耗是单 agent 的 N 倍。确认步骤避免用户无意中触发高成本操作。

#### 5.8.3 可配置性

在 `config.yml` 中提供配置选项：

```yaml
team:
  enabled: true
  auto_suggest: true        # 主 Agent 是否主动建议（默认 true）
  require_confirmation: true # 隐式触发是否需要确认（默认 true）
  max_concurrent_teams: 3    # 同时运行的最大 team 数
  activity:
    web_granularity: full    # web UI 推送粒度
    im_granularity: milestone # IM 推送粒度（none | milestone | all）
    history_retention_hours: 24  # team 完成后保留时间
```

## 6. 数据模型

### 6.1 运行时无持久化

Team Instance 的运行时数据仅存在于 **文件系统 + 内存** 中：

- 文件系统：`~/.octo/teams/<team-id>/` (meta.json + shared/)
- 内存：`map[team_id]*TeamInstance`（Orchestrator 维护，进程重启丢失）

不引入 DB 表。原因：
- Team 是 ephemeral（临时）资源，运行完即弃
- 文件系统的 blackboard 已经承载了所有状态
- 进程重启时 running members 也会被 SubAgentManager 清理

### 6.2 meta.json

```json
{
  "team_id": "t_a1b2c3d4",
  "template": "code-review-team",
  "status": "running",
  "created_at": "2026-07-16T10:30:00Z",
  "config": {
    "strategy": "parallel",
    "completion": "all_done",
    "aggregation": "llm_summary",
    "shared_space": true
  },
  "members": [
    {
      "member_id": "agent_5",
      "role": "reviewer-correctness",
      "model": "claude-sonnet-4-20250514",
      "status": "running",
      "spawned_at": "2026-07-16T10:30:01Z"
    }
  ]
}
```

## 7. API 设计

### 7.1 工具 API（主 Agent 调用）

所有 team 能力通过新增工具暴露给主 Agent，不引入新的 HTTP endpoint。具体 tool 定义见第 5.6 节。

### 7.2 WebSocket 扩展

在现有 WebSocket 消息流中增加 `team_activity` 消息类型：

```json
{
  "type": "team_activity",
  "session_id": "s_xxx",
  "team_id": "t_a1b2c3d4",
  "payload": {
    "event": "tool_started",
    "member_id": "agent_5",
    "role": "reviewer-correctness",
    "tool_name": "read_file",
    "tool_input": {"path": "src/auth.go"}
  }
}
```

### 7.3 配置 API

团队模板的管理通过文件系统操作实现（读/写 YAML），MVP 阶段不引入 CRUD API。后续 Web UI 模板编辑器需要时再增加：

```
GET    /api/teams              — 列出模板
POST   /api/teams              — 创建模板
PUT    /api/teams/:name        — 更新模板
DELETE /api/teams/:name        — 删除模板
```

## 8. 外部依赖接口

本设计不引入新的外部 RPC/HTTP/MQ 依赖。所有新增能力基于：
- 文件系统（Blackboard）
- 已有 Spawner / SubAgentManager
- 已有 SubAgentEventSink

## 9. 测试计划

### 9.1 单元测试
- 模板加载/校验（YAML 解析、内置模板覆盖逻辑）
- Orchestrator（parallel/sequential 策略、completion 检测）
- Blackboard（创建/读写/GC）
- Aggregator（llm_summary、vote、concat）
- TeamTool（team_list/spawn/send/status/kill handler）

### 9.2 集成测试
- 端到端：team_spawn → member blackboard 写入 → aggregator 产出
- Activity Feed：mock SubAgentEventSink → 验证 WebSocket 推送
- 超时：member 超时 → team 进入 failed 状态
- 并发：多个 member 同时写 blackboard → 文件无冲突

### 9.3 兼容性
- 已有 sub_agent 工具：Agent Team 通过新工具暴露，不修改 sub_agent tool → 无影响
- 主 Agent system prompt：team 指南是追加内容，不删除现有指导 → 无影响
- 已有 Activity Event（SubAgentEventSink）：team activity 是扩展事件类型 → 无影响

## 10. 兼容性

### 10.1 与现有 sub-agent 的关系

Agent Team 是 sub-agent 的上层编排模式：
- 不修改 `internal/app/spawner.go`
- 不修改 `internal/tools/subagent_manager.go`
- 不修改 `internal/tools/agent_followup.go`
- 新增 `internal/tools/team.go` 和 `internal/team/` 包

### 10.2 主 Agent system prompt

Team 指南追加到 system prompt 中（仅在 `team.enabled: true` 时注入）。不影响现有 prompt 结构。

### 10.3 配置

新增 `team:` 顶级配置块。不修改现有 config key。

### 10.4 数据

- Team 数据仅存在文件系统和内存，无 DB 变更
- 模板 YAML 文件是新增文件，与现有数据无交集

## 11. 高可用

### 11.1 超时控制

- Member 级别：复用现有 `MaxTurns` 机制
- Team 级别：`orchestrator.timeout_seconds`（默认 600s），超时后未完成的 member 被 kill，已完成的产出仍然进入 aggregation

### 11.2 并发控制

- 全局 team 并发上限：`team.max_concurrent_teams`（默认 3）
- Member 级别：复用现有 `maxConcurrentSubAgents = 16`

### 11.3 降级

- 单个 member 失败不影响其他 member 运行（parallel 模式下）
- 如果 >50% member 失败，team 标记 `failed`，已完成的产出仍尝试 aggregation（如果 enough data）
- 主 Agent 可以调用 `team_kill` 终止整个 team

### 11.4 Token 预算

- Team 消耗的 token 是各 member token 之和（rolled into parent 的 SessionTokens）
- 主 Agent 可配置 `team.max_tokens_per_team`，超过时终止尚未完成的 member

## 12. 监控与告警

### 12.1 关键指标

| 指标 | 类型 | 说明 |
|------|------|------|
| `team.spawned_total` | counter | team 创建总数 |
| `team.completed_total` | counter | team 完成总数 |
| `team.failed_total` | counter | team 失败总数 |
| `team.duration_ms` | histogram | team 执行时长分布 |
| `team.member_count` | histogram | 每次 team 的 member 数量分布 |
| `team.tokens_total` | counter | team 消耗 token 总量 |

### 12.2 日志

- team 创建/完成/失败：INFO 级别
- member 创建/完成/失败：DEBUG 级别
- 工具调用：使用现有 SubAgentEvent 日志

## 13. 发布顺序

1. **Step 1**：核心能力 — 模板加载 + Orchestrator + Blackboard + 3 个内置模板
2. **Step 2**：Activity Feed — WebSocket 推送 + Web UI 活动面板
3. **Step 3**：Web UI 模板编辑器（如不做则跳过）

MVP 阶段交付 Step 1 + 2，实现最小可用闭环。

## 14. 回滚

### 14.1 代码回滚

Agent Team 通过 `team.enabled` 配置开关控制。设置 `enabled: false` 后：
- Team 工具从主 Agent 工具列表中移除
- Orchestrator 不初始化
- 已有 sub_agent 功能完全不受影响

纯配置变更即可回滚，无代码部署。

### 14.2 数据回滚

Team 数据仅存在文件系统和内存，无 DB。回滚无数据迁移负担。

### 14.3 残留文件清理

回滚后 `~/.octo/teams/` 目录可手动删除或等 GC 清理。

## 15. 安全与权限

### 15.1 Blackboard 隔离

- 每个 team 实例有独立目录，不可跨 team 访问
- member 的 tool allowlist 限制为模板指定范围，不可逃逸到系统其他路径

### 15.2 权限继承

- member 的 permission_mode（interactive/auto/strict）继承主 Agent 会话
- member 不能 escalate 权限

### 15.3 敏感信息

- Blackboard 目录在 `~/.octo/teams/` 下，与 session 文件同级（受相同文件系统权限保护）
- Activity Feed 推送的 tool_input 可能含敏感数据（如 API Key），需要在推送前进行 **敏感字段脱敏**（与现有 sub_agent 工具事件的脱敏策略一致）
