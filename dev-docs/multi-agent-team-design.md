# 技术方案：Multi-Agent Team

## 背景与目标

### 背景

当前 octo-agent 是**单 agent 多会话**架构：所有 IM 私聊/群聊、Web 端、TUI 共享同一个 system prompt、同一套 skills、同一组 MCP server。消息到 agent 的路由仅按 chat 维度绑定 session，不存在按"专家领域"分发消息的能力。

随着用户场景拓宽（代码审查、运维排障、文档撰写需要在不同群/群里并行处理），一个 agent 同时承载过多领域知识和工具会导致 system prompt 膨胀、tools 列表过长、上下文窗口被无关信息挤占。

### 目标

1. 将 octo 从**单 agent** 升级为**多 agent 平台**：引入 Master / Sub-Agent 模型，每个 agent 拥有独立的 system prompt、工具集、skills、MCP、session pool。
2. Master agent 是**唯一**管理入口：创建/修改/删除 agent、增删 skill、配置 MCP 都只能在 Master 视图下进行。
3. Sub-agent 只能从已有资源池中**启用/禁用**，不能新增 skill/MCP（"定义"归 Master，"使用配置"归子 agent）。
4. 切换 agent 时，Web UI 上的会话列表、skill 面板、MCP 面板全部切换为该 agent 的命名空间。
5. IM 路由支持频道绑定（私聊严格一对一，群聊多 agent 共存 @ 触发）。
6. CLI/TUI 支持启动时 `--agent` 指定 agent。

### 不在范围内

- Agent 之间的互相通信（直接消息传递、共享黑板）
- 自动意图识别路由（用 LLM 分类消息决定路由目标）
- 集群级多用户 agent（每个用户的 Master 仍然是本地的）
- Sub-agent 的 tool 白名单动态扩充（只能在 Master 已定义的范围内开关）

---

## 命名表

| 术语 | 含义 |
|------|------|
| **Agent Profile** | 描述一个 expert agent 完整配置的声明式对象，包含 ID、名称、描述、系统提示词、模型、工具白名单、mention 别名、频道绑定 |
| **Master Agent** | 代码内建的顶级 agent，system prompt 由代码 base prompt + onboard 注入的 `~/.octo/soul.md` 和 `~/.octo/user.md` 构成，**不可通过 profile 编辑器修改**。拥有所有 skill/MCP 的定义权 |
| **Sub-Agent** | 用户在 Master 视图下创建的 Agent Profile，只能从 Master 已有的资源池中启用/禁用，不能新增 |
| **Profile Store** | `~/.octo/agents/` 目录，每个 sub-agent 一个 `<id>.json` 文件 |
| **Agent Router** | 根据 InboundEvent（平台、chat ID、消息内容中的 @ 提及）决定消息路由到哪个 agent profile |
| **Session Key** | `platform:chat_id:user_id`（私聊时）或 `platform:chat_id`（群聊时），标识一个 session pool 中的唯一会话 |
| **Agent 切换** | Web 顶部 agent 头像点击后切换当前 agent，所有 UI 面板切换到该 agent 隔离的命名空间 |

---

## 架构

### 总体架构

```
                         InboundEvent (IM 消息)
                              │
                    ┌─────────▼─────────┐
                    │    AgentRouter     │
                    │  (profile 选择器)  │
                    └─────────┬─────────┘
                              │ 路由决策：
                              │ 1. 频道绑定（最高优先）
                              │ 2. @ 提及
                              │ 3. fallback → Master
            ┌─────────────────┼─────────────────┐
            ▼                 ▼                 ▼
   ┌──────────────┐  ┌──────────────┐  ┌──────────────┐
   │ Manager[Mstr]│  │ Manager[code]│  │ Manager[ops] │
   │ Master       │  │ code-review  │  │ ops-helper   │
   │ 独立session   │  │ 独立session   │  │ 独立session   │
   │ pool         │  │ pool         │  │ pool         │
   └──────────────┘  └──────────────┘  └──────────────┘
            │                 │                 │
            ▼                 ▼                 ▼
      Agent(Master)     Agent(code)       Agent(ops)
```

### 改造后的 Server 结构

`internal/server/server.go` 的 `Server` 结构需要从单一 `channelMgr` 扩展为一组 manager：

```go
type Server struct {
    // ─── 多 agent 核心 ───
    agentStore   *agentprofile.Store          // ~/.octo/agents/ 的加载与热管理
    multiMgr     *channel.MultiManager        // 替代单一 channelMgr，内持多个 per-agent Manager
    masterMgr    *channel.Manager             // Master agent 的 Manager（快捷引用）

    // ─── 以下字段基本不变 ───
    cfg          Config
    sender       agent.Sender
    model        string
    skillReg     *skills.Registry             // Master 的 skill registry（全局定义）
    // MCP 通过 tools.ActiveMCPRegistry() / app.SwapMCP() 进程全局管理，无单字段引用

    // ─── 子 agent 资源隔离（新增） ───
    // skills/MCP 定义是 Master 全局的（skillReg + 进程全局 mcpRegistry），
    // 此处记录每个 sub-agent 禁用的资源列表
    disabledSkills map[string][]string // agentID → []skillName
    disabledMCPs   map[string][]string // agentID → []mcpServerName
    // ... turnLocks, sessionAgents, wsHub, 等保持不变
}
```

`MultiManager` 负责：
- 按 agent ID 持有各自的 `*channel.Manager`
- 路由 InboundEvent 到对应 manager 的 session pool
- 提供按 agent 维度列出/管理 session 的方法

```go
// internal/channel/multi_manager.go
type MultiManager struct {
    mu       sync.RWMutex
    managers map[string]*Manager  // agentID → Manager
    router   *AgentRouter
    store    *agentprofile.Store
}

// Resolve 根据 InboundEvent 找到应处理的 Manager
func (mm *MultiManager) Resolve(ev InboundEvent) *Manager {
    profile := mm.router.Route(ev)
    mm.mu.RLock()
    defer mm.mu.RUnlock()
    return mm.managers[profile.ID]
}

// Master 返回 Master agent 的 Manager（始终存在）
func (mm *MultiManager) Master() *Manager
```

---

## 详细设计

### 1. Agent Profile 数据模型

#### 1.1 文件存储格式

Profile 存储在 `~/.octo/agents/<profile-id>.json`，每个文件一个 profile：

```json
{
  "id": "code-review",
  "name": "代码审查专家",
  "description": "专责 review PR、发现 bug、建议改进",
  "model": "claude-sonnet-4-20250514",
  "system_prompt": "你是代码审查专家。当用户发来 PR 链接或代码变更时...",
  "tools": ["read_file", "write_file", "edit_file", "grep", "glob", "terminal", "code-review"],
  "tool_skills": ["code-review"],
  "mention_as": ["@review", "@CR"],
  "channel_bindings": [
    {"platform": "weixin", "chat_id": "dev-group-xxx"}
  ],
  "created_at": "2026-07-15T10:00:00Z",
  "updated_at": "2026-07-15T10:00:00Z"
}
```

#### 1.2 Go 结构

```go
// internal/agentprofile/profile.go
package agentprofile

type Profile struct {
    ID             string           `json:"id"`
    Name           string           `json:"name"`
    Description    string           `json:"description"`
    Model          string           `json:"model"`
    SystemPrompt   string           `json:"system_prompt"`
    Tools          []string         `json:"tools"`
    ToolSkills     []string         `json:"tool_skills"`  // 这些 skill 以工具形式暴露
    MentionAs      []string         `json:"mention_as"`
    ChannelBindings []ChannelBinding `json:"channel_bindings"`
    CreatedAt      time.Time        `json:"created_at"`
    UpdatedAt      time.Time        `json:"updated_at"`
}

type ChannelBinding struct {
    Platform string `json:"platform"`
    ChatID   string `json:"chat_id"`
}

// IsMaster 判断该 profile 是否为 Master
// Master 没有 JSON 文件，是代码内建的
func (p *Profile) IsMaster() bool { return p.ID == "master" }
```

#### 1.3 Profile Store

Store 负责从 `~/.octo/agents/` 加载所有 profile，提供增删改查 API，并支持热加载。

```go
// internal/agentprofile/store.go
type Store struct {
    mu       sync.RWMutex
    dir      string           // ~/.octo/agents/
    profiles map[string]*Profile
    watcher  *fsnotify.Watcher // 监听文件变更
}

func New(dir string) (*Store, error)
func (s *Store) Load() error                    // 全量加载
func (s *Store) Get(id string) (*Profile, bool)
func (s *Store) List() []*Profile               // 返回所有 profile（不含 Master）
func (s *Store) Create(p *Profile) error
func (s *Store) Update(p *Profile) error
func (s *Store) Delete(id string) error
func (s *Store) Watch(ctx context.Context)     // fsnotify 热加载循环
func (s *Store) ByChannel(platform, chatID string) []*Profile  // 按频道绑定索引
func (s *Store) ByMention(alias string) (*Profile, bool)       // 按 @ 别名索引
```

**创建规则**：
- ID 由 Web UI 或 `octo agents create` 生成，格式为 8 位随机小写字母（与 session ID 短格式一致）
- `id` 字段 immutable，创建后不可改
- 所有字段在 `Create`/`Update` 时做非空校验
- `model` 必须在 `~/.octo/config.yml` 的 `models` 列表中

**热加载**：
- `Watch()` 监听 `~/.octo/agents/` 目录的 `CREATE` / `WRITE` / `REMOVE` 事件
- 文件改动后：`Load()` 全量重读（文件量小，O(N) 可接受）
- 触发 `MultiManager` 重载（重建受影响的 per-agent Manager）
- API 路径 `POST /api/agents/:id/reload` 提供手动触发入口

### 2. Agent Router

Router 根据 InboundEvent 决定消息路由到哪个 agent profile。

```go
// internal/agentprofile/router.go
type Router struct {
    store *Store
}

// Route 返回应处理该消息的 agent profile
// 返回 *Profile（总不为 nil：fallback 到 Master）
func (r *Router) Route(ev InboundEvent) *Profile {
    // 1. 检查 @ 提及（群聊场景）
    if mentioned := extractMentionAlias(ev.Text); mentioned != "" {
        if p, ok := r.store.ByMention(mentioned); ok {
            return p
        }
        // @ 了不存在的 alias，静默返回 Master（不报错）
        return MasterProfile()
    }

    // 2. 频道绑定
    if p := r.store.ByChannel(ev.Platform, ev.ChatID); len(p) > 0 {
        if len(p) == 1 {
            return p[0]
        }
        // 群聊多绑定但未 @：静默（返回 Master 也不响应，由 dispatcher 直接 drop）
        // 见下方 "路由行为总结"
        return nil
    }

    // 3. 私聊未绑定 → Master
    return MasterProfile()
}
```

#### 路由行为总结

| 场景 | @ 提及 | 频道绑定 | 路由结果 |
|------|--------|----------|----------|
| 私聊（未绑定） | — | 无 | **Master** |
| 私聊（绑定 code-review） | — | 有（唯一） | **code-review** |
| 群聊（绑定 code-review） | @ops 但 ops 未绑定此群 | 有 | **静默**（drop，不响应） |
| 群聊（绑定 code-review） | @review | 有 | **code-review** |
| 群聊（绑定 code + ops） | @ops | 有（多） | **ops-helper** |
| 群聊（绑定 code + ops） | 无 @ | 有（多） | **静默**（drop） |
| 群聊（无绑定） | — | 无 | **Master**（但无频道绑定，实际不走路由） |

**注意**：AgentRouter 返回 nil 时，MultiManager 应 drop 该消息（不路由到任何 agent），这与"群聊未 @ 完全静默"的决策一致。

### 3. Multi-Manager Session Pool

每个 agent profile 拥有独立的 `*channel.Manager`，即独立的 `sync.Map[SessionKey, *Session]`。

```
Channel.MultiManager
├── managers["master"]  → *channel.Manager (Master)
├── managers["code"]    → *channel.Manager (code-review)
└── managers["ops"]     → *channel.Manager (ops-helper)
```

**Session Key** 生成规则不变（`channel.sessionKeyFor(mode, ev)` = `platform:chat_id[:user_id]`），但 manager 按 agent ID 隔离，不同 agent 可以有相同 Session Key 的不同 session。

**关键行为**：
- Session ID 本身已全局唯一（由 `agent.NewSession` 的时间戳 + 随机后缀保证），无需添加 agent 前缀
- Session 文件保持现名 `~/.octo/sessions/<session_id>.jsonl`，仅按 manager 隔离 in-memory 引用
- 首次加载时，所有已有 session 自动归属 Master（session 文件名不带 agent 信息）

### 4. 改造 `buildChannelFactory`

当前 `Server.buildChannelFactory()` 返回无参闭包。需要改为按 profile 参数化：

```go
// 改造前
func (s *Server) buildChannelFactory() func() *agent.Agent {
    return func() *agent.Agent {
        defaultSender, model := s.defaultSenderAndModel()
        a := agent.New(defaultSender, model)
        // ... 设置 LiteSender 等
        return a
    }
}

// 改造后：每个 Manager 在创建 session 时传入 profile
func (s *Server) buildChannelFactoryFor(profile *agentprofile.Profile) func() *agent.Agent {
    return func() *agent.Agent {
        sender, model := s.senderForProfile(profile)
        a := agent.New(sender, model)
        a.MaxTokens = s.cfg.MaxTokens

        // profile 覆盖默认 system prompt
        if profile.SystemPrompt != "" {
            a.System = profile.SystemPrompt
        }

        // profile 的 working_dir
        if profile.WorkingDir != "" {
            a.CWD = profile.WorkingDir  // 或设到 Session 上
        }

        return a
    }
}
```

`agent.Agent` 不需要大改。Profile 的 `Tools` 白名单在 per-turn 的 `tools.DefaultToolsForProfile(ctx, profile)` 处过滤，不在 factory 中处理。

### 5. Agent 工具白名单注入

Tool registry 在构建 tool list 时传入 profile，只包含 profile 声明的 tools：

```go
// internal/tools/registry.go
func DefaultToolsForProfile(ctx context.Context, profile *agentprofile.Profile, serverModel string) []agent.ToolDefinition {
    all := DefaultToolsForCtx(ctx, serverModel)
    if profile == nil || len(profile.Tools) == 0 {
        return all  // Master：返回全部
    }
    allowed := make(map[string]bool, len(profile.Tools))
    for _, t := range profile.Tools {
        allowed[t] = true
    }
    filtered := make([]agent.ToolDefinition, 0, len(all))
    for _, t := range all {
        if allowed[t.Name] {
            filtered = append(filtered, t)
        }
    }
    return filtered
}
```

同理，skill manifest 只包含 profile 声明的 skills：

```go
// internal/skills/skills.go
func ManifestForProfile(reg *Registry, profile *agentprofile.Profile) string {
    if profile == nil || len(profile.ToolSkills) == 0 {
        return RenderManifest(reg)  // Master：全部
    }
    // 仅渲染指定 skill
    return renderFilteredManifest(reg, profile.ToolSkills)
}
```

### 6. Per-Agent Web 会话隔离

Web 端当前使用全局 `skillReg` 和 `mcpRegistry`。改造后，服务器需要为每个 agent 维护独立的 skill/MCP 状态：

#### 6.1 服务端数据结构

```go
// internal/server/server.go（改造后新增字段）
// skillReg、disabledSkills、disabledMCPs 是在现有 Server 结构上的新增字段；
// MCP 的"定义"走进程全局 tools.ActiveMCPRegistry()，不另设字段
type Server struct {
    // Master 资源：全局唯一，定义所有可用资源
    skillReg       *skills.Registry    // Master：拥有全部 skill 定义（已存在）

    // 以下两个是新增字段
    disabledSkills map[string][]string // agentID → 在该 agent 中禁用的 skill 名称列表
    disabledMCPs   map[string][]string // agentID → 在该 agent 中禁用的 MCP 名称列表

    // 每个 agent 独立的 per-turn 状态（已有，按 session 隔离）
    // turnLocks, sessionAgents 等保持不变，因为不同 manager 的 session key 已是隔离的
}
```

**资源管理权限矩阵**：

| 操作 | Master 视图 | Sub-Agent 视图 |
|------|-----------|--------------|
| 新增 skill 定义 | ✅ | ❌ |
| 删除 skill 定义 | ✅ | ❌ |
| 开关 skill | ✅ | ✅（仅影响本 agent） |
| 新增 MCP server | ✅ | ❌ |
| 删除 MCP server | ✅ | ❌ |
| 开关 MCP server | ✅ | ✅（仅影响本 agent） |
| 新建会话 | ✅（session pool 归 master） | ✅（session pool 归该 agent） |
| 查看会话列表 | 仅 master 的 session | 仅该 agent 的 session |

#### 6.2 会话列表 API 改造

当前 `GET /api/sessions` 列全部 session（不分 agent）。需要：

```
GET /api/sessions              — 返回 master 的 sessions（向后兼容）
GET /api/agents/:id/sessions   — 返回指定 agent 的 sessions
```

前端按需请求当前选中 agent 的 session 列表。

### 7. Web UI 布局

#### 7.1 Agent 选择器（右侧顶部）

在 `web/src/components/layout/Header.svelte` 的右侧区域（现有设置按钮左侧）添加 agent 头像：

```
┌────────────────────────────────────────────────────┐
│  [搜索 pill]  ···          [🚀 Master ▼] [⚙] [−] │
└────────────────────────────────────────────────────┘
```

点击头像展开 AgentList 下拉：
- 当前选中 agent 高亮
- Master 在最顶部，显示 🚀 Master + 描述
- 每个 sub-agent 显示头像首字母 + name + 标记"绑定到 n 个频道"
- 列表底部有 `[+ 新建 Agent]`（仅在 Master 视图下显示）

#### 7.2 Agent 管理面板

在 Sidebar 中添加 `AgentsView.svelte` 入口，展示 Master 下的 agent 管理界面：

```
┌─────────────────────────────────────────────┐
│  🤖 Agent Team                              │
├─────────────────────────────────────────────┤
│                                             │
│  [+ 新建 Agent]                             │
│                                             │
│  ┌─────────────────────────────────────┐    │
│  │ 🔍 code-review                      │    │
│  │    代码审查专家                      │    │
│  │    Model: claude-sonnet-4           │    │
│  │    Bound: feishu/dev-group          │    │
│  │    Mention: @review, @CR            │    │
│  │                              [编辑] │    │
│  └─────────────────────────────────────┘    │
│                                             │
│  ┌─────────────────────────────────────┐    │
│  │ 🚀 ops-helper                       │    │
│  │    运维助手                          │    │
│  │    Model: claude-sonnet-4           │    │
│  │    Bound: weixin/ops-group          │    │
│  │                              [编辑] │    │
│  └─────────────────────────────────────┘    │
│                                             │
└─────────────────────────────────────────────┘
```

#### 7.3 Agent 编辑视图

点击编辑进入 `AgentEditView.svelte`，表单字段：

| 字段 | 类型 | 可编辑 |
|------|------|--------|
| ID | 只读文本 | ❌ |
| 名称 | 文本输入 | ✅ |
| 描述 | 文本输入 | ✅ |
| Model | 下拉选择（来自 config models） | ✅ |
| System Prompt | 多行文本 | ✅ |
| Tools | 多选 checkbox（来自 Master tools） | ✅（仅开关） |
| Skills | 多选 checkbox（来自 Master skills） | ✅（仅开关） |
| Mention Aliases | 标签输入 | ✅ |
| 频道绑定 | 频道多选 + chat_id 输入 | ✅ |

**权限控制**：仅 Master 视图下可进入编辑视图。切换到 sub-agent 后，新建按钮隐藏且无编辑入口（仅开关 skill/MCP）。

#### 7.4 切换 Agent 后的 UI 行为

切换 agent 时，前端 `activeAgentId` store 变更，触发：

| UI 面板 | 行为 |
|---------|------|
| 会话列表 | 调用 `GET /api/agents/:id/sessions`，清空当前列表并展示新列表 |
| 聊天视图 | 当前 session 变为 null；用户需新建或选择一个该 agent 的 session |
| Skill 面板 | 展示该 agent 的 skill 列表（开关禁用/启用）；无"新增"按钮 |
| MCP 面板 | 展示该 agent 的 MCP 列表；无"新增 server"按钮 |
| 顶部头像 | 更新为新 agent 的首字母/图标 |

### 8. CLI/TUI 入口指定 Agent

#### 8.1 Flag 传递

`octo` 的默认行为（无显式子命令时）既是 TUI 也是单轮 chat。`--agent` 参数仅加在 `cmd/octo/chat.go` 的默认入口（即 `octo` 命令本身），不在 `octo serve` 或其他子命令上：

- `octo` 不接受子命令时 → 默认 entry（chat/TUI），支持 `--agent`
- `octo serve` → 服务器，**不接受** `--agent`（serve 同时托管所有 agent，加此 flag 与多 agent 架构矛盾）

```go
// cmd/octo/chat.go
var agentName = flag.String("agent", "", "Start session bound to a specific agent (by ID or name)")
```

用法示例：

```bash
octo --agent code-review           # TUI，直接使用 code-review agent
octo --agent ops-helper "查日志"    # 单轮 chat，走 ops-helper
```

**为什么 `octo chat` 不存在？** `octo` 没有名为 `chat` 的显式子命令 — 直接 `octo <message>` 就是 chat 模式，`octo`（无参数）就是 TUI。`--agent` 在这个默认 entry 上，不影响 `octo serve`、`octo skills`、`octo config` 等管理子命令。

#### 8.2 后端处理

`octo` 启动后，`activeAgentId` 由 flag 决定：

- 未指定：默认 `master`
- 指定：检查 `agentStore.Get(id)` 是否存在。不存在则启动报错 `agent "xxx" not found (available: master, code-review, ops-helper)`

**`-c` 会话列表按 agent 过滤**：`octo -c`（无 ID 时）弹出的会话选择列表，只展示 `activeAgentId` 对应的 session pool 中的会话。例如 `octo --agent code-review -c` 只展示 `code-review` 的 session，不展示 master 或其他 agent 的 session。因为 session pool 已经按 agent 隔离，`-c` picker 只需要从当前 agent 的 pool 读取列表即可。

TUI 会话的 `source` 字段标记为 `cli`，但 `agent_id` 标记为所选 profile。

#### 8.3 TUI 内的 Agent 切换

TUI 中增加 `/agent` 命令：

```
/agent                  — 显示当前 agent 名称
/agent list             — 列出所有可用 agents（名称 + 绑定频道数）
/agent use <name|id>    — 切换当前会话的 agent（创建新 session，旧 session 归属原 agent）
```

### 9. IM 频道绑定 API

#### 9.1 REST API

```
GET    /api/agents               — 列出所有 profiles
POST   /api/agents               — 创建 profile
PUT    /api/agents/:id           — 更新 profile
DELETE /api/agents/:id           — 删除 profile
POST   /api/agents/:id/bind      — 绑定频道   body: {"platform": "weixin", "chat_id": "xxx"}
DELETE /api/agents/:id/bind      — 解绑频道   body: {"platform": "weixin", "chat_id": "xxx"}
POST   /api/agents/:id/reload    — 热加载指定 profile
```

#### 9.2 Agent Profile 校验规则

- `name`：1-32 字符，同一 Store 内唯一
- `id`：创建时由服务端生成（8 位随机小写），创建后 immutable
- `system_prompt`：最大 10000 字符
- `tools`：必须是Master `skillReg` + `tools.DefaultRegistry` 中存在名的子集
- `model`：必须在 `~/.octo/config.yml` 的 `models` 列表中
- `mention_as`：每个 alias 必须以 `@` 开头，且全局唯一（同一 alias 不能被两个 profile 声明）

### 10. 数据迁移

#### 10.1 Master Profile（无文件）

Master 没有 JSON 文件。代码中硬编码：

```go
func MasterProfile() *Profile {
    return &Profile{
        ID:           "master",
        Name:         "Master",
        Description:  "Default agent with full access",
        Model:        "",  // 从 config 读取
        SystemPrompt: "",  // 由代码 base + onboard 注入
        Tools:        nil, // nil = 全部工具
        ToolSkills:   nil, // nil = 全部 skill
    }
}
```

#### 10.2 已有 Session 归属

服务器首次启动多 agent 改造后：
- 所有已有 session 归属于 Master agent（它们都用旧格式存储，不带 agent 前缀）
- `~/.octo/sessions/` 中的文件直接划入 Master 的 session pool
- 这导致 Master 首次加载时 session 列表包含所有历史 session — 符合预期（用户视角无感）

---

## HTTP API 完整清单

### Agent Profile CRUD

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/agents` | 列出所有 profiles（不含 master） |
| POST | `/api/agents` | 创建新 profile（body: profile json） |
| GET | `/api/agents/:id` | 获取单个 profile |
| PUT | `/api/agents/:id` | 更新 profile |
| DELETE | `/api/agents/:id` | 删除 profile |
| POST | `/api/agents/:id/bind` | 绑定频道 |
| DELETE | `/api/agents/:id/bind` | 解绑频道 |
| POST | `/api/agents/:id/reload` | 热加载 |

### Per-Agent 资源

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/agents/:id/sessions` | 列出该 agent 的 session |
| GET | `/api/agents/:id/skills` | 列出该 agent 的 skill 状态 |
| PUT | `/api/agents/:id/skills/:skill` | 启用/禁用 skill |
| GET | `/api/agents/:id/mcp` | 列出该 agent 的 MCP 状态 |
| PUT | `/api/agents/:id/mcp/:server` | 启用/禁用 MCP server |
| GET | `/api/agents/:id/tools` | 列出该 agent 的工具白名单 |

### 切换

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/agent-current` | 获取当前 web 端的 active agent（存 cookie 或返回 master） |
| PUT | `/api/agent-current` | 切换当前 agent |

---

## 改造文件清单

### 新增文件

| 文件 | 说明 |
|------|------|
| `internal/agentprofile/profile.go` | Profile 结构 + 校验 |
| `internal/agentprofile/store.go` | Store（加载/保存/热加载/索引） |
| `internal/agentprofile/router.go` | AgentRouter（按事件路由到 profile） |
| `internal/channel/multi_manager.go` | MultiManager（per-agent Manager 容器） |
| `web/src/views/AgentsView.svelte` | Agent 管理主面板 |
| `web/src/views/AgentEditView.svelte` | Agent 编辑表单 |
| `web/src/components/layout/AgentAvatar.svelte` | 顶部头像选择器 |
| `web/src/components/agents/AgentList.svelte` | 下拉 agent 列表 |

### 修改文件

| 文件 | 改动 |
|------|------|
| `internal/server/server.go` | `Server` 增加 `agentStore`、`multiMgr`；`initChannels()` 改为 `initMultiManager()` |
| `internal/server/server.go` | `buildChannelFactory()` 改为按 profile 创建 |
| `internal/server/handlers.go` | 新增 agents handlers（CRUD + 资源子路由） |
| `internal/channel/manager.go` | 接受 profile 参数注入 system prompt / model |
| `internal/tools/registry.go` | `DefaultToolsForProfile()` — 按 profile 过滤工具 |
| `internal/skills/skills.go` | `ManifestForProfile()` — 按 profile 过滤 skill manifest |
| `internal/config/config.go` | `Server.Config` 新增 `agentName` 字段 |
| `cmd/octo/chat.go` | 新增 `--agent` flag |
| `cmd/octo/repl.go` | 新增 `--agent` flag；`/agent` 命令 |
| `cmd/octo-desktop/main.go` | desktop 启动传 `agentName` 给 server |
| `web/src/lib/stores.ts` | 新增 `activeAgentId`, `agentList` stores |
| `web/src/lib/api.ts` | 新增 agents API 调用 |
| `web/src/components/layout/Header.svelte` | 集成 AgentAvatar 下拉 |
| `web/src/views/SkillsView.svelte` | 技能列表根据 active agent 过滤；不在 master 时隐藏新增 |
| `web/src/views/McpView.svelte` | MCP 列表根据 active agent 过滤 |
| `web/src/views/ChatView.svelte` | 会话列表根据 active agent 拉取 |

---

## 测试策略

### 单元测试

1. `store.go`：CRUD、热加载事件、并发安全
2. `router.go`：各种路由场景（绑定冲突、@ 别名、私聊、群聊）
3. `multi_manager.go`：不同 manager 的 session 隔离
4. tools/skills 过滤逻辑

### 集成测试

1. 创建 profile → assign → 重启 → 热加载 → 删除，全流程
2. Master 的 skill/MCP 定义新增后，sub-agent 立即可见（在启用列表中）
3. Web 切换 agent → 会话列表、skill 面板、MCP 面板同步切换
4. IM 频道绑定：发消息到绑定群 → 只有被绑 agent 响应；@ 提及 → 被 @ agent 响应

### 兼容性测试

1. Master profile 缺失（无 agent 文件）→ 所有已有行为不变
2. `~/.octo/agents/` 目录不存在 → fallback 到 master
3. 已有 session 文件照常归属 master

---

## 安全

1. **Agent Profile 文件写入限制**：Profile JSON 只能写入 `~/.octo/agents/` 目录，路径校验拒绝 `..` 和绝对路径（与 `session.resolveSessionPath` 同策略）
2. **Tools 白名单**：Sub-agent 不能通过 API 添加 Master 未声明的工具；`PUT /api/agents/:id` 中 `tools` 字段必须在 `tools.DefaultRegistry` 和 `skillReg` 名单位有交集
3. **Mention Alias 全局唯一**：创建/更新时校验 alias 不与其他 profile 冲突（防"身份冒充"）
4. **删除保护**：有频道绑定或有活跃 session 的 profile 删除时返回错误，需先解绑/关闭 session

---

## 高可用

1. **Profile 热加载失败**：文件写入过程中读取到半写状态 → 跳过本次变更，保留旧 profile 并打印警告日志（不崩溃）
2. **Profile 文件损坏（JSON 解析失败）**：跳过该文件，其他 profile 正常加载
3. **Master 不可用时**：Master 是代码内建的，不会"不可用"。agentStore 为空时 fallback 到 Master（向后兼容）
4. **MultiManager 部分 agent 启动失败**：隔离故障 — 单个 Manager 启动失败不影响其他 Manager（与现在单 Manager 启动失败导致 channel 全挂不同，这次是 per-agent 隔离的）

---

## 监控与告警

| 指标 | 说明 |
|------|------|
| `octo.agents.total` | 当前加载的 profile 数量 |
| `octo.agents.route_hit_total` | 按 agent 维度的路由命中计数 |
| `octo.agents.route_fallback_total` | 路由 fallback 到 master 的次数（应该很少） |
| `octo.agents.reload_total` | profile 热加载次数 |
| `octo.agents.reload_error_total` | profile 热加载失败次数 |
| `octo.sessions.total` | 按 agent 维度的 session 数量（label: agent_id） |

---

## 发布顺序

按依赖链从底向上：

1. **`internal/agentprofile/`** — Profile / Store / Router 包（纯新代码，无依赖）
2. **`internal/channel/` — MultiManager** — 引用 agentprofile
3. **`internal/server/server.go`** — 重构 initChannels → initMultiManager，替换 channelMgr 为 multiMgr
4. **`internal/tools/registry.go` + `internal/skills/skills.go`** — 过滤函数
5. **Web UI** — AgentViews + 改造 Header/Skills/Mcp/Chat
6. **`cmd/octo/`** — `--agent` flag + `/agent` 命令
7. **测试 + 文档**

### 发布后验证清单

- [ ] 无 profile 文件时行为与改造前完全一致（向后兼容）
- [ ] 创建 profile → 绑定频道 → IM 发消息 → 正确路由
- [ ] Web 切换 agent → 会话列表、skill 面板、MCP 面板正确隔离
- [ ] 热加载后新配置生效（无需重启）
- [ ] Master 的 system prompt 不可通过 profile 编辑器修改（只读显示"由 onboard 管理"）
- [ ] `octo chat --agent code-review` 正常启动
- [ ] `octo serve` 桌面版 master 和新 profile 共存

---

## 兼容性

以下逐项说明对现有行为的影响：

| 项目 | 影响 | 原因 |
|------|------|------|
| **已有 session 文件** | 无影响 | 旧 session 文件位于 `~/.octo/sessions/<session_id>.jsonl`，不带 agent 前缀；改造后全部划归 Master 的 session pool |
| **`octo` / `octo chat` 交互** | 无影响 | 未传 `--agent` 时默认使用 Master；Master 的 behavior 与改造前完全一致 |
| **Web 端会话列表** | 无影响 | 未创建 profile 时，`GET /api/sessions` 返回 Master 的所有 session（同改造前） |
| **IM channel 路由** | 无影响 | 未创建 profile 时，所有 channel 消息路由到 Master，行为与改造前一致 |
| **Skills** | 无影响 | Master 拥有全部 skill 定义（默认开启所有 skill）；不存在 skill/MCP 定义丢失的可能 |
| **MCP** | 无影响 | MCP server 的连接管理路径不变（仍走 `tools.ActiveMCPRegistry()`），仅新增 per-agent 禁用开关 |
| **`~/.octo/config.yml`** | 无影响 | Agent profile 是新引入的存储路径 (`~/.octo/agents/`)，不改动现有配置文件 |
| **`onboard` 流程** | 无影响 | onboard 仍操作 `~/.octo/soul.md` 和 `~/.octo/user.md`，不引入新路径 |
| **API 端点** | 向后兼容 | 新增 `/api/agents/*` 路由组；旧端点 `/api/sessions`、`/api/channels/*` 行为不变 |
| **WebSocket 事件** | 无影响 | WS 事件类型不变（still broadcast per-session）；但创建新 session 时需带 `agent_id` 字段（可选，默认 master） |
| **`octo serve` CLI** | 无影响 | 无需新增 flag；桌面版和 serve 均自动加载 profile |

---

## 回滚

### 代码回滚

本次改造的所有代码改动集中在以下几个独立包中：
- `internal/agentprofile/` — 全新包，可整体删除
- `internal/channel/multi_manager.go` — 全新文件，可删除
- `internal/server/` — 改动集中在 `initChannels()` 重构为 `initMultiManager()` + 新增 handlers，撤销后恢复旧 `initChannels()` 即可
- `web/src/views/AgentsView.svelte` + `AgentEditView.svelte` + `AgentAvatar.svelte` — 全新文件，可删除

回滚步骤：
1. 删除新增文件
2. 恢复 `server.go` 中的 `initChannels()` 方法（从 git 历史取旧版本恢复）
3. 恢复 `server.go` 中的 `Server` 结构体（移除新增字段）
4. 恢复 `cmd/octo/chat.go` 移除 `--agent` flag
5. 恢复 `cmd/octo/repl.go` 移除 `--agent` flag 和 `/agent` 命令

### 数据回滚

- **`~/.octo/agents/` 目录**：直接删除即可。profile 定义不写入任何其他位置，删除后系统行为恢复为单 agent（Master）
- **已有 session 文件**：不需要迁移或删除；回滚后它们自动归属 Master（读取时默认 agent_id = "master"），行为不变

### 回滚安全性

回滚**无数据丢失风险**：
- Session 文件仅用于读取，改造期间不会被重写
- Profile 文件是纯新增数据，删除不影响既有配置
- 回滚后唯一"丢失"的是用户创建的自定义 profile 定义（但 `~/.octo/agents/` 目录可备份）

---

## 未解决问题

无。所有分支已在拷问阶段 resolve。
