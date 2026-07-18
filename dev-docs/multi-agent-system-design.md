# 技术方案：Multi-Agent System

## 背景与目标

### 背景

当前 octo 是**单 agent 多会话**架构：所有 IM 私聊/群聊、Web 端、TUI 共享同一个 system prompt、同一套 skills、同一组 MCP server。消息到 agent 的路由仅按 chat 维度绑定 session，不存在按"专家领域"分发消息的能力。

随着用户场景拓宽（代码审查、运维排障、文档撰写需要在不同群/群里并行处理），一个 agent 同时承载过多领域知识和工具会导致 system prompt 膨胀、tools 列表过长、上下文窗口被无关信息挤占。

### 目标

1. 将 octo 从**单 agent** 升级为**多 agent 平台**：引入 Default Agent / Expert Agent 模型，每个 agent 拥有独立的 system prompt、工具集、skills、MCP、session pool。
2. Default Agent 是**唯一**管理入口：创建/修改/删除 agent、增删 skill、配置 MCP 都只能在 Default Agent 视图下进行。
3. Expert Agent 只能从已有资源池中**启用/禁用**，不能新增 skill/MCP（"定义"归 Default Agent，"使用配置"归各 agent）。
4. 每个 agent 拥有独立的会话 pool，会话一旦建立 agent 不可切换。
5. IM 路由支持频道绑定（私聊严格一对一，群聊多 agent 共存 @ 触发）。
6. CLI/TUI 支持启动时 `--agent` 指定 agent。
7. 所有 agent（无论是 Default 还是 Expert）都可以通过 `sub_agent` 工具在会话内部调度匿名子 agent 执行隔离任务。

### 不在范围内

- agent 之间的互相通信（直接消息传递、共享黑板）
- 自动意图识别路由（用 LLM 分类消息决定路由目标）
- 集群级多用户 agent（每个用户的 Default Agent 仍然是本地的）
- Expert Agent 的 tool 白名单动态扩充（只能在 Default Agent 已定义的范围内开关）

---

## 命名表

| 术语 | 含义 |
|------|------|
| **Agent Profile** | 描述一个 expert agent 完整配置的声明式对象，包含 ID、名称、描述、系统提示词、模型、工具白名单、mention 别名、频道绑定 |
| **Default Agent** | 代码内建的顶级 agent，system prompt 由代码 base prompt + onboard 注入的 `~/.octo/soul.md` 和 `~/.octo/user.md` 构成，**不可通过 profile 编辑器修改**。拥有所有 skill/MCP 的定义权。没有对应的 JSON 文件 |
| **Expert Agent** | 用户在 Default Agent 视图下创建的 Agent Profile，只能从 Default Agent 已有的资源池中启用/禁用，不能新增 |
| **sub_agent** | 工具名。所有 agent 都可通过此工具在会话内部调度匿名子 agent 执行隔离任务。子 agent 的生命周期、上下文、工具集完全独立，运行结束后结果回传给主 agent |
| **Profile Store** | `~/.octo/agents/` 目录，每个 expert agent 一个 `<id>.json` 文件 |
| **Agent Router** | 根据 InboundEvent（平台、chatID、消息内容中的 @ 提及）决定消息路由到哪个 agent profile |
| **Session Key** | `platform:chat_id:user_id`（私聊时）或 `platform:chat_id`（群聊时），标识一个 session pool 中的唯一会话 |
| **Agent 绑定** | 会话创建时绑定 agent，一旦建立不可切换；通过新建会话选择其他 agent |

---

## 架构

### 总体架构

```
                         InboundEvent (IM 消息)
                              │
                    ┌─────────▼─────────┐
                    │    AgentRouter     │
                    │  (profile 选择器)   │
                    └─────────┬─────────┘
                              │ 路由决策：
                              │ 1. 频道绑定（最高优先）
                              │ 2. @ 提及
                              │ 3. fallback → Default Agent
            ┌─────────────────┼─────────────────┐
            ▼                 ▼                 ▼
   ┌──────────────┐  ┌──────────────┐  ┌──────────────┐
   │ Manager[def] │  │ Manager[code]│  │ Manager[ops] │
   │ Default      │  │ code-review  │  │ ops-helper   │
   │ 独立 session  │  │ 独立 session  │  │ 独立 session  │
   │ pool         │  │ pool         │  │ pool         │
   └──────────────┘  └──────────────┘  └──────────────┘
            │                 │                 │
            ▼                 ▼                 ▼
      Agent(Default)    Agent(code)       Agent(ops)
```

### 改造后的 Server 结构

`internal/server/server.go` 的 `Server` 结构需要从单一 `channelMgr` 扩展为一组 manager：

```go
type Server struct {
    // ─── 多 agent 核心 ───
    // channelMgr 已完全移除，由 multiMgr 统一派发所有 IM 事件
    agentStore   *agentprofile.Store          // ~/.octo/agents/ 的加载与热管理
    multiMgr     *channel.MultiManager        // 替代 channelMgr，内持多个 per-agent Manager
    defaultMgr   *channel.Manager             // Default Agent 的 Manager（快捷引用，冗余但省去每次 map 查找）
```

### IM 事件流

废弃 `Server.channelMgr` 后，`multiMgr` 成为 IM 事件的唯一分派点：

```
Channel Adapter (IM 消息)
    │
    ▼
Server.handleChannelMessage(ev)         ← 入口不变，body 改造
    │
    ▼
mgr := multiMgr.Resolve(ev)             ← 路由决策：拿到正确的 per-agent Manager
if mgr == nil {
    // 群聊多绑定无 @：drop，不路由到任何 agent
    return
}
    │
    ▼
mgr.GetOrCreateSession(ev) & run turn    ← 跟原有单 Manager 路径一致，但隔离到目标 agent 的 pool
```

**Resolve 返回 nil 的 drop 逻辑**：当群聊绑定多个 agent 但消息无 @ 时，`Router.Route()` 返回 nil，`Resolve` 透传返回 nil，handler 直接 return 不响应。

**迁移期无并存**：没有 `channelMgr` / `multiMgr` 双轨。`initChannels()` 改为 `initMultiManager()`，一次切干净。

**adapter 注入改造**：每个 channel adapter 启动时的回调从 `func(ev)` 改为内部直接调 `multiMgr.Resolve`。由于 Resolve 是纯计算（路由查找 + map 读取），adapter 不需要感知 agent 数量。

    // ─── 以下字段基本不变 ───
    cfg          Config
    sender       agent.Sender
    model        string
    provider     string
    skillReg     *skills.Registry             // Default Agent 的 skill registry（全局定义）
    // MCP 通过 tools.ActiveMCPRegistry() / app.SwapMCP() 进程全局管理，无单字段引用
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

// Resolve 根据 InboundEvent 找到应处理的 Manager。
// profile 为 nil 时（群聊多绑定无 @）返回 nil，caller 应 drop 该消息。
func (mm *MultiManager) Resolve(ev InboundEvent) *Manager {
    profile := mm.router.Route(ev)
    if profile == nil {
        return nil
    }
    mm.mu.RLock()
    defer mm.mu.RUnlock()
    return mm.managers[profile.ID]
}

// Default 返回 Default Agent 的 Manager（始终存在）
func (mm *MultiManager) Default() *Manager
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
    WorkingDir     string           `json:"working_dir,omitempty"`
    MentionAs      []string         `json:"mention_as"`
    ChannelBindings []ChannelBinding `json:"channel_bindings"`
    CreatedAt      time.Time        `json:"created_at"`
    UpdatedAt      time.Time        `json:"updated_at"`
}

type ChannelBinding struct {
    Platform string `json:"platform"`
    ChatID   string `json:"chat_id"`
}

// IsDefault 判断该 profile 是否为 Default Agent
// Default Agent 没有 JSON 文件，是代码内建的
func (p *Profile) IsDefault() bool { return p.ID == "default" }
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
func (s *Store) List() []*Profile               // 返回所有 profile（不含 Default Agent）
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
// 返回 *Profile（总不为 nil：fallback 到 Default Agent）
func (r *Router) Route(ev InboundEvent) *Profile {
    // 1. 检查 @ 提及（群聊场景）
    if mentioned := extractMentionAlias(ev.Text); mentioned != "" {
        if p, ok := r.store.ByMention(mentioned); ok {
            return p
        }
        // @ 了不存在的 alias，回退到 Default Agent（Default 也可能 drop，取决于上下文）
        return DefaultProfile()
    }

    // 2. 频道绑定
    if p := r.store.ByChannel(ev.Platform, ev.ChatID); len(p) > 0 {
        if len(p) == 1 {
            return p[0]
        }
        // 群聊多绑定但未 @：静默（Default Agent 也不响应，由 dispatcher 直接 drop）
        // 见下方 "路由行为总结"
        return nil
    }

    // 3. 私聊未绑定 → Default Agent
    return DefaultProfile()
}
```

#### 路由行为总结

| 场景 | @ 提及 | 频道绑定 | 路由结果 |
|------|--------|----------|----------|
| 私聊（未绑定） | — | 无 | **Default Agent** |
| 私聊（绑定 code-review） | — | 有（唯一） | **code-review** |
| 群聊（绑定 code-review） | @ops 但 ops 未绑定此群 | 有 | **Default Agent**（@ 到不存在的 alias 回退） |
| 群聊（绑定 code-review） | @review | 有 | **code-review** |
| 群聊（绑定 code + ops） | @ops | 有（多） | **ops-helper** |
| 群聊（绑定 code + ops） | 无 @ | 有（多） | **静默**（drop） |
| 群聊（无绑定） | — | 无 | **Default Agent** |

**注意**：AgentRouter 返回 nil 时，MultiManager 应 drop 该消息（不路由到任何 agent），这与"群聊未 @ 完全静默"的决策一致。

### 3. Multi-Manager Session Pool

每个 agent profile 拥有独立的 `*channel.Manager`，即独立的 `sync.Map[SessionKey, *Session]`。

```
Channel.MultiManager
├── managers["default"]  → *channel.Manager (Default Agent)
├── managers["code"]     → *channel.Manager (code-review)
└── managers["ops"]      → *channel.Manager (ops-helper)
```

**Session Key** 生成规则不变（`channel.sessionKeyFor(mode, ev)` = `platform:chat_id[:user_id]`），但 manager 按 agent ID 隔离，不同 agent 可以有相同 Session Key 的不同 session。

**关键行为**：
- Session ID 本身已全局唯一（由 `agent.NewSession` 的时间戳 + 随机后缀保证），无需添加 agent 前缀
- Session 文件保持现名 `~/.octo/sessions/<session_id>.jsonl`，仅按 manager 隔离 in-memory 引用
- 首次加载时，所有已有 session 自动归属 Default Agent（session 文件名不带 agent 信息）

### 4. 改造 `buildChannelFactory`

当前 `Server.buildChannelFactory()` 返回无参闭包。需要改为按 profile 参数化：

```go
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
        return all  // Default Agent：返回全部
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
        return RenderManifest(reg)  // Default Agent：全部
    }
    return renderFilteredManifest(reg, profile.ToolSkills)
}
```

### 6. Per-Agent Web 会话隔离

Web 端当前使用全局 `skillReg` 和 `mcpRegistry`。改造后，服务器需要为每个 agent 维护独立的 skill/MCP 状态：

#### 6.1 服务端数据结构

```go
type Server struct {
    // Default Agent 资源：全局唯一，定义所有可用资源
    skillReg       *skills.Registry    // Default Agent：拥有全部 skill 定义（已存在）

    // 以下两个是新增字段
    disabledSkills map[string][]string // agentID → 在该 agent 中禁用的 skill 名称列表
    disabledMCPs   map[string][]string // agentID → 在该 agent 中禁用的 MCP 名称列表

    // 每个 agent 独立的 per-turn 状态（已有，按 session 隔离）
    // turnLocks, sessionAgents 等保持不变，因为不同 manager 的 session key 已是隔离的
}
```

**资源管理权限矩阵**：

| 操作 | Default Agent | Expert Agent |
|------|--------------|--------------|
| 新增 skill 定义 | ✅ | ❌ |
| 删除 skill 定义 | ✅ | ❌ |
| 开关 skill/MCP（本 agent） | ✅ | ✅ |
| 新建会话 | ✅（归 default） | ✅（归该 agent） |
| 查看会话列表 | 仅 default 的 session | 仅该 agent 的 session |

**过滤模型统一为 allowlist**：每个 Expert Agent 的 `Profile.Tools` / `TaskSkills` 就是 allowlist。运行时（`DefaultToolsForProfile` / system prompt 注入）只看 allowlist。不在 `Server` 上额外维护 `disabledSkills` / `disabledMCPs` denylist。

**Tool 分组**：Agent 管理面板中，built-in tool 按组分展示。用户可 allow 整个组，也可在组内 deny 个别工具：

```
┌─────────────────────────────────────────────┐
│  🔍 code-review — Tools                     │
├─────────────────────────────────────────────┤
│  📁 文件操作                          [✅ 全部允许]  │
│     ✅ read_file                             │
│     ✅ write_file                            │
│     ❌ edit_file          ← 组内单独 deny     │
│     ✅ glob                                  │
│  ─────────────────────────                  │
│  📁 搜索                              [❌ 全部禁止]  │
│     ❌ grep                                  │
│     ❌ grep_search                           │
│  ─────────────────────────                  │
│  📁 浏览器                            [✅ 全部允许]  │
│     ✅ browser                               │
└─────────────────────────────────────────────┘
```

**语义**：组的 allow/deny 是快捷操作，最终生成的 `Profile.Tools` 是精确的工具名列表。Group allow + 个别 deny → 该组所有工具除去被 deny 的，写入 Profile.Tools。

#### 6.2 系统级内置 skill 权限

部分内置 skill 涉及平台资源管理（创建 skill、配置 MCP），只允许 Default Agent 使用。Expert Agent 的 skill 面板**看不到**这些 skill，也无法开启：

| 系统级 skill | Default Agent | Expert Agent |
|-------------|--------------|--------------|
| `skill-creator` | ✅ 可见可开关 | ❌ 不可见 |
| `mcp-creator` | ✅ 可见可开关 | ❌ 不可见 |
| `workflow-creator` | ✅ 可见可开关 | ❌ 不可见 |
| `channel-manager` | ✅ 可见可开关 | ❌ 不可见 |
| `cron-task-creator` | ✅ 可见可开关 | ✅ 可见可开关（创建属于自己的 cron） |
| `sub_agent` | ✅ 可见可开关 | ✅ 可见可开关（所有 agent 都用它调度匿名子 agent） |

实现方式：`ManifestForProfile()` 中，对 expert agent profile 额外过滤掉系统级 skill 白名单。这些 skill 的 `visibility` 标记在 frontmatter 中新增 `system: true`，渲染 manifest 时 `if isSystemSkill(skill) && !profile.IsDefault() { skip }`。`cron-task-creator` 和 `sub_agent` 标记为 `system: false`，所有 agent 可见。

#### 6.3 Cron 任务归属

Cron 任务按 agent 归属：**谁创建的归谁**。每个 cron task 记录中新增 `agent_id` 字段，标识创建者。

> **注意**：现有 `Task.Agent string`（语义为 `"general"|"coding"` 预设路由）是死代码，被 `agent_id` 完全替代。不需要保留或重命名。旧 JSON 加载时忽略该字段，`agent_id` 默认 `"default"`。

| 操作 | Default Agent | Expert Agent |
|------|--------------|--------------|
| 创建 cron | ✅ 归 default | ✅ 归该 agent |
| 查看 cron 列表 | 全部（含所有 agent） | 仅自己的 |
| 编辑/删除 cron | 全部 | 仅自己的 |
| **转移 cron 归属** | ✅ 可将自己的 cron 转给其他 agent | ❌ 无转移权限 |

Default Agent 的 cron 管理面板多一个"归属"列和"转移"操作，允许将 default 拥有的 cron 转给任意 expert agent。转移后，cron 的执行 agent 变为目标 agent（使用目标 agent 的 profile 配置、session pool、权限）。

**Cron 转移 + shared session 交互**: 当转移一个 `session_mode: shared` 的 cron 时，必须**强制重置 session**（新建空 session 绑定到目标 agent），因为旧 session 里的 history 是原 agent 上下文的产物，模型、system prompt 都已不匹配。

数据结构变更：

```json
// ~/.octo/tasks/<task-id>.json — 用 agent_id 取代 Agent 字段
{
  "agent_id": "code-review",
  "transferable": true,
  "transferred_at": "2026-07-15T10:00:00Z",
  "transferred_from": "default"
}
```

API 新增：

```
PUT /api/cron/:id/transfer  — body: {"agent_id": "code-review"} — 仅 default 可用
```

#### 6.4 会话列表 API 改造

当前 `GET /api/sessions` 列全部 session（不分 agent）。需要：

```
GET /api/sessions              — 返回 default 的 sessions（向后兼容）
GET /api/agents/:id/sessions   — 返回指定 agent 的 sessions
```

前端按需请求当前选中 agent 的 session 列表。



理由：
- 当前并无跨 agent 记忆冗余的真实投诉
- 全隔（选项 A）需要改 `memory.Dir()` 和 `memorybackend` 接口，约 20 行改动但增加维护复杂度
- 分层合并（选项 B）写记忆时的分层判定依赖 LLM 调用，不准会导致记忆错放
- 共享记忆在多 agent 早期阶段利大于弊（通用事实无需重复教）

后续若出现跨 agent 记忆噪声投诉，再升级到 A（全隔）— 只需把 agent-specific directory 改名就是 shared base，迁移成本低。

#### 6.5 Browser 录制 skill 隔离

Browser 录制的 skill 存储在 `~/.octo/skills/` 下，与用户安装 skill 共享发现/加载路径。录制本身依赖 `browser` 工具（`browser record` 命令），回放依赖 `browser` 工具（`replay`）。

**隔离方式**：在 `ManifestForProfile()` 中，如果 profile 不含 `browser` 工具，过滤掉所有 browser-recorded skill。过滤条件：

```go
// 检测方式：通过 skill source 路径或 frontmatter 标记
func isBrowserSkill(s Skill) bool {
    return strings.Contains(s.Source, "browser/") || s.Source == "browser-record"
}

// ManifestForProfile 过滤
if !allowed["browser"] {
    skills = filterOut(skills, isBrowserSkill)
}
```

**效果**：
- code-review（tools: browser, read_file...）→ 面板可见 browser skill ✅
- doc-writer（tools: read_file, write_file）→ 面板不可见 browser skill ✅
- Default Agent（tools: 全部）→ 面板可见 ✅

**工程量**：约 5 行代码，复用现有 `ManifestForProfile()` 过滤框架。

#### 6.6 Workflow 与 MCP：服务端隔离，运行时校验

Workflow 里调度的匿名子 agent 会继承 caller 的 tool 白名单。但 workflow 编写时依赖的 tool 可能不在所有 agent 的白名单里，导致执行时报错。

**Workflow 前端面板不隔离**：所有 agent 共享同一份 workflow 列表。Workflow 的依赖校验仅在运行时执行 — 当用户触发 workflow 时检查当前 session 对应的 agent profile 是否满足依赖，缺则拒绝并提示在 Agent 管理面板开启所需工具或新建指向兼容 agent 的会话。

**MCP 过滤入口**：选 A。`DefaultToolsForProfile` 统一过滤 — 该函数同时感知 skillReg + MCPRegistry + DefaultRegistry，在一个函数内完成 built-in tool + MCP tool 的 per-profile 过滤。实现简单，单一入口易于维护。

```go
func DefaultToolsForProfile(ctx context.Context, profile *agentprofile.Profile, serverModel string) []agent.ToolDefinition {
    all := DefaultToolsForCtx(ctx, serverModel)
    if profile == nil || len(profile.Tools) == 0 {
        return all  // Default Agent：返回全部
    }
    allowed := make(map[string]bool, len(profile.Tools))
    for _, t := range profile.Tools { allowed[t] = true }
    filtered := make([]agent.ToolDefinition, 0, len(all))
    for _, t := range all {
        if allowed[t.Name] { filtered = append(filtered, t) }
    }
    return filtered
}
```

其中 `DefaultToolsForCtx` 已包含 MCP tool（通过 `ActiveMCPRegistry`），所以 MCP 自然被 profile 白名单过滤。被禁 MCP 的 server 仍保持 registry 注册（只剥工具不拆 server），避免 per-turn 频繁启停。

#### 6.7 Memory 隔离：MVP 不隔离

MVP 阶段 memory backend 不改。所有 agent 共享同一套 `memDir` + `homeMemDir`，语义记忆注入对全部 agent 生效。

### 7. Web UI 布局

#### 7.1 新建会话时选择 Agent

主 "新建会话" 按钮旁加一个小的 "+" 下拉，点击弹出 agent 选择列表：

```
┌─────────────────────────┐
│  🚀 Default Agent       │  ← 默认 agent（始终可见）
│  ─────────────────────  │
│  🔍 code-review         │  ← 已创建的 expert agents
│  🚀 ops-helper          │
│  📝 doc-writer          │
│  ─────────────────────  │
│  [+ 新建 Agent]         │  ← 始终可见
└─────────────────────────┘
```

选择某个 expert agent → 创建归属于该 agent 的新会话。

#### 7.2 新建会话时的 @-agent 入口

`@+` 按钮不在现有的 Composer 输入框里，而是**属于新建会话流程的一部分** — 用于在创建会话时指定目标 agent。

交互位置：新建会话按钮旁的小 "+" 下拉（见 7.1）。点击后弹出 agent 选择列表，选择某个 expert agent → 创建归属于该 agent 的新会话。

**为什么不在输入框里加 @-agent？**
- 会话一旦建立，agent 是不可切换的。想给另一个专家 agent 发消息 → 新建指向该 agent 的会话。
- 想给另一个专家 agent 发消息 → 新建一个指向那个 agent 的会话即可。

#### 7.3 会话一旦建立，Agent 不可切换

会话创建时绑定的 agent 是该会话的永久属性，不随后续消息改变。UI：
- 会话列表每条会话标记所属 agent 的彩色 tag（如 `[🔍 code-review]`）
- 会话内不显示 agent 切换入口
- 想让消息走其他 agent → 新建一个指向其他 agent 的会话（@+ 仅在新建会话流程中出现，不在现有会话的输入框里）

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

`octo` 启动后，session 绑定的 agent 由 flag 决定：

- 未指定：默认 `default`
- 指定：检查 `agentStore.Get(id)` 是否存在。不存在则启动报错 `agent "xxx" not found (available: default, code-review, ops-helper)`

**`-c` 会话列表展示全部**：`octo -c`（无 ID 时）弹出所有 session 的选择列表，不过滤 agent。

TUI 会话的 `source` 字段标记为 `cli`，但 `agent_id` 标记为所选 profile。

#### 8.3 TUI 内查看当前 Agent

TUI 会话建立后 agent 不可切换。提供查看命令：

```
/agent                  — 显示当前会话所属 agent 名称 + 描述
/agent list             — 列出所有可用 agents（用于新建会话时选择）
```

### 9. IM 频道绑定 API

#### 9.1 REST API

```
GET    /api/agents               — 列出所有 profiles
POST   /api/agents               — 创建 profile
PUT    /api/agents/:id           — 更新 profile
DELETE /api/agents/:id           — 删除 profile
POST   /api/agents/:id/bind      — 绑定频道   body: {"platform": "weixin", "chat_id": "xxx"}
DELETE /api/agents/:id/bind      — 解绑频道
POST   /api/agents/:id/reload    — 热加载指定 profile
```

#### 9.2 Agent Profile 校验规则

- `name`：1-32 字符，同一 Store 内唯一
- `id`：创建时由服务端生成（8 位随机小写），创建后 immutable
- `system_prompt`：最大 10000 字符
- `tools`：必须是 `skillReg` + `tools.DefaultRegistry` 中存在名的子集
- `model`：必须在 `~/.octo/config.yml` 的 `models` 列表中
- `mention_as`：每个 alias 必须以 `@` 开头，且全局唯一（同一 alias 不能被两个 profile 声明）

### 10. 数据迁移

#### 10.1 Default Agent Profile（无文件）

Default Agent 没有 JSON 文件。代码中硬编码：

```go
func DefaultProfile() *Profile {
    return &Profile{
        ID:           "default",
        Name:         "Default",
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
- 所有已有 session 归属于 Default Agent（它们都用旧格式存储，不带 agent 前缀）
- `~/.octo/sessions/` 中的文件直接划入 Default Agent 的 session pool
- 这导致 Default Agent 首次加载时 session 列表包含所有历史 session — 符合预期（用户视角无感）

**Cron 迁移**：现有 cron JSON 含 `"agent": "general"`（死代码字段）。加载时忽略旧 `agent` 字段，`agent_id` 默认设为 `"default"`。无需数据迁移，旧值被静默丢弃。

---

## HTTP API 完整清单

### Agent Profile CRUD

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/agents` | 列出所有 profiles（不含 default） |
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

### Cron 归属与转移

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/cron?agent_id=:id` | 按 agent 过滤 cron 列表 |
| PUT | `/api/cron/:id/transfer` | 转移 cron 归属（仅 default） |

### 切换

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/agent-current` | 获取当前 web 端的 active agent（存 cookie 或返回 default） |
| GET | `/api/agents` | 列出所有 profiles（新建会话时选择） |

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
| `web/src/components/agents/AgentList.svelte` | 下拉 agent 列表 |

### 修改文件

| 文件 | 改动 |
|------|------|
| `internal/server/server.go` | `Server` 增加 `agentStore`、`multiMgr`；`initChannels()` 改为 `initMultiManager()` |
| `internal/server/server.go` | `buildChannelFactory()` 改为按 profile 创建 |
| `internal/server/handlers.go` | 新增 agents handlers（CRUD + 资源子路由） |
| `internal/channel/manager.go` | 接受 profile 参数注入 system prompt / model |
| `internal/tools/registry.go` | `DefaultToolsForProfile()` — 按 profile 过滤工具 |
| `internal/skills/skills.go` | `ManifestForProfile()` — 按 profile 过滤 skill manifest；系统级 skill frontmatter 标记 `system: true` 后对 expert agent 隐藏；browser-recorded skill 在 profile 不含 browser 工具时隐藏 |
| `internal/scheduler/` 或 task 存储 | cron task 新增 `agent_id` 字段；`GET /api/cron` 支持 `?agent_id=` 过滤；新增 `PUT /api/cron/:id/transfer` |
| `internal/server/server.go` | `Server.Config`（server.go:56）新增 `agentName` 字段（仅客户端路径 `octo`/`octo-desktop` 设置；`octo serve` 不接受此 flag，由 session 绑定 agent） |
| `cmd/octo/chat.go` | 新增 `--agent` flag |
| `cmd/octo/repl.go` | 新增 `--agent` flag；`/agent` 命令 |
| `cmd/octo-desktop/main.go` | desktop 启动传 `agentName` 给 server |
| `web/src/lib/stores.ts` | 新增 `agentList`, `cronOwnership` stores |
| `web/src/lib/api.ts` | 新增 agents API 调用；cron transfer API |
| `web/src/components/layout/Header.svelte` | 集成 AgentAvatar 下拉 |
| `web/src/views/SkillsView.svelte` | 技能列表根据 active agent 过滤；不在 default 时隐藏新增和系统级 skill |
| `web/src/views/McpView.svelte` | MCP 列表根据 active agent 过滤 |
| `web/src/views/WorkflowsView.svelte` | workflow 面板展示全部 workflow；不做按 agent 过滤 |
| `web/src/views/ChatView.svelte` | 会话列表根据 active agent 拉取 |
| `web/src/views/TasksView.svelte` | cron 列表按 agent 过滤；default 视图显示"归属"列和"转移"操作 |

---

## 测试策略

### 单元测试

1. `store.go`：CRUD、热加载事件、并发安全
2. `router.go`：各种路由场景（绑定冲突、@ 别名、私聊、群聊）
3. `multi_manager.go`：不同 manager 的 session 隔离
4. tools/skills 过滤逻辑

### 集成测试

1. 创建 profile → assign → 重启 → 热加载 → 删除，全流程
2. Default Agent 的 skill/MCP 定义新增后，expert agent 立即可见（在启用列表中）
3. 热加载后新配置生效（无需重启）
4. Default Agent 的 system prompt 不可通过 profile 编辑器修改（只读显示"由 onboard 管理"）
5. IM 频道绑定：发消息到绑定群 → 只有被绑 agent 响应；@ 提及 → 被 @ agent 响应
6. `octo chat --agent code-review` 正常启动
7. `octo serve` 桌面版 default 和新 profile 共存

### 兼容性测试

1. Default Agent profile 缺失（无 agent 文件）→ 所有已有行为不变
2. `~/.octo/agents/` 目录不存在 → fallback 到 Default Agent
3. 已有 session 文件照常归属 Default Agent

---

## 安全

1. **Agent Profile 文件写入限制**：Profile JSON 只能写入 `~/.octo/agents/` 目录，路径校验拒绝 `..` 和绝对路径（与 `session.resolveSessionPath` 同策略）
2. **Tools 白名单**：Expert Agent 不能通过 API 添加 Default Agent 未声明的工具；`PUT /api/agents/:id` 中 `tools` 字段必须在 `tools.DefaultRegistry` 和 `skillReg` 名单位有交集
3. **Mention Alias 全局唯一**：创建/更新时校验 alias 不与其他 profile 冲突（防"身份冒充"）
4. **删除保护**：有频道绑定或有活跃 session 的 profile 删除时返回错误，需先解绑/关闭 session

---

## 高可用

1. **Profile 热加载失败**：文件写入过程中读取到半写状态 → 跳过本次变更，保留旧 profile 并打印警告日志（不崩溃）
2. **Profile 文件损坏（JSON 解析失败）**：跳过该文件，其他 profile 正常加载
3. **Default Agent 不可用时**：Default Agent 是代码内建的，不会"不可用"。agentStore 为空时 fallback 到 Default Agent（向后兼容）
4. **MultiManager 部分 agent 启动失败**：隔离故障 — 单个 Manager 启动失败不影响其他 Manager（与现在单 Manager 启动失败导致 channel 全挂不同，这次是 per-agent 隔离的）

---

## 监控与告警

| 指标 | 说明 |
|------|------|
| `octo.agents.total` | 当前加载的 profile 数量 |
| `octo.agents.route_hit_total` | 按 agent 维度的路由命中计数 |
| `octo.agents.route_fallback_total` | 路由 fallback 到 default 的次数（应该很少） |
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
- [ ] Web 新建会话选 agent → 会话正确归属；skill/MCP 面板不按 agent 过滤
- [ ] 热加载后新配置生效（无需重启）
- [ ] Default Agent 的 system prompt 不可通过 profile 编辑器修改（只读显示"由 onboard 管理"）
- [ ] `octo --agent code-review` 正常启动
- [ ] `octo serve` 桌面版 default 和新 profile 共存

---

## 兼容性

以下逐条说明对现有行为的影响：

| 项目 | 影响 | 原因 |
|------|------|------|
| **已有 session 文件** | 无影响 | 旧 session 文件位于 `~/.octo/sessions/<session_id>.jsonl`，不带 agent 前缀；改造后全部划归 Default Agent 的 session pool |
| **`octo` / `octo chat` 交互** | 无影响 | 未传 `--agent` 时默认使用 Default Agent；Default Agent 的 behavior 与改造前完全一致 |
| **Web 端会话列表** | 无影响 | 未创建 profile 时，`GET /api/sessions` 返回 Default Agent 的所有 session（同改造前） |
| **IM channel 路由** | 无影响 | 未创建 profile 时，所有 channel 消息路由到 Default Agent，行为与改造前一致 |
| **Skills** | 无影响 | Default Agent 拥有全部 skill 定义（默认开启所有 skill）；不存在 skill/MCP 定义丢失的可能 |
| **MCP** | 无影响 | MCP server 的连接管理路径不变（仍走 `tools.ActiveMCPRegistry()`），仅新增 per-agent 禁用开关 |
| **`~/.octo/config.yml`** | 无影响 | Agent profile 是新引入的存储路径 (`~/.octo/agents/`)，不改动现有配置文件 |
| **`onboard` 流程** | 无影响 | onboard 仍操作 `~/.octo/soul.md` 和 `~/.octo/user.md`，不引入新路径 |
| **API 端点** | 向后兼容 | 新增 `/api/agents/*` 路由组；旧端点 `/api/sessions`、`/api/channels/*` 行为不变 |
| **WebSocket 事件** | 无影响 | WS 事件类型不变（still broadcast per-session）；但创建新 session 时需带 `agent_id` 字段（可选，默认 default） |
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

- **`~/.octo/agents/` 目录**：直接删除即可。profile 定义不写入任何其他位置，删除后系统行为恢复为单 agent（Default Agent）
- **已有 session 文件**：不需要迁移或删除；回滚后它们自动归属 Default Agent（读取时默认 agent_id = "default"），行为不变

### 回滚安全性

回滚**无数据丢失风险**：
- Session 文件仅用于读取，改造期间不会被重写
- Profile 文件是纯新增数据，删除不影响既有配置
- 回滚后唯一"丢失"的是用户创建的自定义 profile 定义（但 `~/.octo/agents/` 目录可备份）

---

## 已知限制

- **WeChat 群聊**：WeChat 没有原生 mention，群聊路由只能走频道绑定。私聊绑定同一 expert agent 后，群聊和私聊行为相同（无 @ 区分）。这是 WeChat 平台限制，无法在应用层规避。
- **MCP Server 资源**：被禁 MCP 的 server 仍保持 registry 注册和连接。若 MCC server 数量极大且有连接数上限，可在后续加入 lazy-connect 优化。
- **多 agent 并发 rate limit**：多个 agent 的 cron 同时跑时共用同一个 API key，可能触发 rate limit。留待后续加 per-agent 并发限制或队列。
