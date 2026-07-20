# pkg/octoagent 导出层设计

外部 Go module 想把 octo-agent 当 library 用(起因:Sinew Director 想直接 import octo 的 agent
loop,而不是 spawn 子进程或者拷贝一份实现),但 octo 目前所有能力都在 `internal/` 包里——Go 语言规则上
别的 module 根本导不进去。本文档规划怎么开一层 `pkg/octoagent` 导出面,让外部按 `import
"github.com/open-octo/octo-agent/pkg/octoagent"` 拿到 `Agent`、`Run`/`RunStream`、工具编排、
Sender 构造这些能力,同时不破坏现有 CLI/TUI/server/IM 四个入口的行为。

## 背景:为什么现在需要

CLAUDE.md 里 `internal/app` 的定位一直是"唯一构造 provider client 并接入 agent 的地方",四个入口
(CLI/server/IM/cron)都经它复用同一套逻辑,但这套复用**只对 octo 自己的二进制内部有效**——因为承载它
的包全在 `internal/` 下。第一个真正的外部消费者(Sinew Director)出现后,这道边界就要往外挪一层。

## 现状盘点(先立住事实,再设计)

深入看代码后,实际需要新写的代码比最初设想的少很多——大部分该有的解耦早就存在,只是被 `internal/`
挡住了 import 路径。逐项过一遍:

### 1. `agent.Agent` 本身已经是干净的导出类型

`internal/agent/agent.go:118` 的 `Agent` struct——`System`/`Model`/`MaxTokens`/`History`/
`Sender`/`Gate`/`CWD` 全是导出字段,`Run`/`RunStream`/`Turn`/`TurnStream` 全是导出方法。构造用
`agent.New(sender Sender, model string) *Agent`(`internal/agent/agent.go:493`)。**这里不需要写任何
新逻辑,纯粹是 type alias 的事。**

### 2. Sender 构造已经是干净的导出函数,不需要合并去重

最初以为 `internal/server/server.go:1395 senderForEntry` 和 `cmd/octo/chat.go:1011 buildSender`
两份重复实现需要先合并——深入看发现这两个都只是在**外面包了一层 CLI/server 特有的默认值解析**(env
var 优先、读全局 `~/.octo/config.yml` 兜底),内层真正做事的是 `internal/app/sender.go:79`:

```go
type SenderOptions struct {
	Provider        string // vendor ID，如 "anthropic" / "openai" / "kimi" / 自定义 endpoint
	APIKey          string
	BaseURL         string // 空则用 vendor 默认
	Protocol        string // "anthropic" | "openai"，只有 custom vendor 需要
	CacheKey        string
	ThinkingBudget  int
	ReasoningEffort string
	ShowReasoning   bool
}

func NewSender(opts SenderOptions) (agent.Sender, error)
```

`NewSender` 全程不读文件、不依赖全局 config——纯 value in / value out。**Director 不需要
`~/.octo/config.yml`,直接在 `agent.yaml` 解析出的字段上构造一个 `SenderOptions` 传进来即可。**
`senderForEntry`/`buildSender` 那层 CLI/server 专属的默认值解析不用动,也不需要给 Director 复用。

### 3. 工具编排有两套并存的模式,只有一套能给 Director 用

- `app.WireTools`(`internal/app/bootstrap.go:38`)是 CLI 用的模式——靠
  `tools.SetSpawner`/`tools.SetDefaultSubAgentManager`/`tools.SetBrowserRecordingGenerator` 这类
  **进程级全局变量**+`defer cleanup()`,单会话专用。CLI 一个进程只服务一个会话,这样写没问题。
- `Server.prepareToolTurn`(`internal/server/handlers.go:806`)是 Web server 用的模式——这是
  **#1133 修复过的**,把 spawner/sub-agent-manager 从全局变量改成了 `context.Context` 携带
  (`tools.WithSubAgentManager`、`tools.WithWorkingDir`、`tools.WithGoalStore`、
  `tools.WithBackgroundManager`、`tools.WithWorkflowManager`、`tools.WithTaskStore`)。函数内
  注释原话:

  > callers use `tools.DefaultToolsForCtx(ctx, ...)` with the ctx returned above, which sees the
  > ctx-scoped manager just stamped in directly. That removes a data-race-prone per-turn mutation
  > of process-global state on a server that's inherently multi-session.

  这正是 Director 需要的东西——一个进程里并发跑多个 tenant/多个 agent。**这条并发安全路径已经在
  `internal/server` 里跑通并有历史 bug 教训(#1133),pkg/ 要抽的是这一条,不是 `WireTools`。**

  **但 `prepareToolTurn` 不是一个能整体照抄的干净函数**——审查后发现它内部至少还有这些跟"并发安全
  wiring"无关、混在一起的东西,抽取时必须逐条甄别去留(细节见"设计方案 → toolenv"一节):
  - 无条件用本地 YAML 规则文件重建并**覆盖 `a.Gate`**(`a.Gate = app.NewPermissionGate(engine,
    ask)`,`internal/server/handlers.go:896`)——这一行如果原样搬进 toolenv,会直接吞掉调用方自己
    设的 `Agent.Gate`,跟本文档第 4 点"Director 自己实现 `Check`"的前提正面冲突。
  - `config.Load()`(读 `~/.octo/config.yml`,line 822)和 `permission.New(...)`(读
    `~/.octo/permissions.yml`,line 857)两处真实的本地文件 I/O。
  - 四处 `*Server` 方法调用(`s.permissionAskFrom`/`s.rememberedFor`/`s.notifySubAgentExit`/
    `s.deliverModelNote`),其中 `deliverModelNote`(`internal/server/bg_tasks.go:85`)不是 UI
    广播,是往运行中会话的 Inbox 塞消息的业务逻辑,Director 没有对应物。
  - `sessionID` 目前是从 `ctx.Value(ctxKeySessionID{})` 里探测的(未导出 key,line 866/904),不是
    显式参数——按 `ctx` 探测 vs. 显式传参,是两条不同的控制流,不能只是"挪函数"。

### 4. `PermissionGate` 是一个单方法接口,Director 不需要 octo 给它加任何新构造函数

最初规划以为要在 `internal/permission` 里新增一个"不读本地文件、只接程序化 callback"的构造函数——
深入看 `internal/agent/tool.go:48` 的实际定义后,发现这个担心是多余的:

```go
type PermissionGate interface {
	Check(ctx context.Context, name string, input map[string]any) (allowed bool, reason string)
}
```

这就是一个单方法接口,Go 的隐式接口满足意味着**Director 在自己的代码里写一个带 `Check` 方法的类型,
直接赋给 `Agent.Gate`,完全不需要依赖 octo 的 `internal/permission` 包**——那个包(读本地 YAML 规则
文件、`Allow`/`Deny`/`Ask` 三态)是 CLI/server 的实现细节,不是 Director 必须复用的东西。Director
自己的 `Check` 实现直接调 Sinew Spine 的 policy-check API 即可。

`ToolExecutor`(`internal/agent/tool.go:33`)同理,也是单方法接口
(`Execute(ctx, name, input) (ToolResult, error)`)——Director 大概率还是用 octo 自带的
`tools.NewDefaultRegistry()`(见下),但如果需要自定义 dispatch,同样不需要 octo 改任何代码。

### 5. `sub_agent` 过滤不需要 octo 改代码

`tools.DefaultToolsForCtx(ctx, model) []agent.ToolDefinition` 返回的是普通 slice。Director 拿到
后自己按 `Name == "sub_agent"` 过滤掉再传给 `Run`/`RunStream` 即可([[07-orchestrator]] 里
`disallowed_tools: [sub_agent]` 的语义在 Director 自己那一层实现,不需要 octo 提供机制)。

### 6. 一个真实存在、且还没被 #1133 覆盖到的遗留全局状态

`prepareToolTurn` 里仍有三行是进程级全局、每个 turn 都覆盖一次(`internal/server/handlers.go:822-831`):

```go
tools.SetBrowserVision(cfg.ModelVision(a.Model))
tools.SetBrowserRecordingGenerator(app.MakeRecordingGenerator(a.GetSender(), a.Model))
tools.SetBrowserHealer(app.MakeBrowserHealer(a.GetSender(), a.Model))
```

如果 Director 并发跑的两个 agent 都启用 browser 工具且用不同 model,这三行会互相覆盖——**这是一个
真实的并发安全缺口,不是可以靠约定绕开的东西,只是 Sinew 场景目前不用 browser 工具所以不触发**。见
"已知限制"一节。（后续更新：这三行已经改成 ctx-scoped 写法，不再是进程级覆写——见"已知限制"一节里
对应条目。）

### 7. 第四个未 ctx 化的全局状态:workflow discovery cwd

`prepareToolTurn` 末尾还有一处 save-and-restore 式的进程级全局交换
(`tools.SetWorkflowDiscoveryCWD`/`ActiveWorkflowDiscoveryCWD`,`internal/tools/workflow.go`),跟
第 6 点的三个 browser setter 是同一类问题——如果 Director 并发跑的两个 agent working directory 不同
且都用到 workflow 工具,会互相踩。第一版文档漏记了这一条,现在补进"已知限制"。

### 8. MCP client 注册也是同一类全局状态,且目前完全没有导出路径

`tools.SetMCPRegistry(r *mcp.Registry)`(`internal/tools/mcp.go:40`)同样是进程级全局,
`DefaultRegistry.ExecuteStream` 对每次 MCP 工具调用都无条件经它派发。`internal/app/mcp.go` 里
`ConnectMCP`/`SwapMCP`/`ConnectMCPServer`/`ShutdownMCP` 这套 CLI/server 专属的编排逻辑完全没有
导出规划。如果 Director 想用 MCP 工具(大概率会想,Sinew 的 Forge/Spine 就是 MCP server),现在
**没有库安全的路径**——这一条第一版文档完全没提,现在作为已知限制/非目标明确记下来,不装作
不存在。

## 设计方案

新增 `pkg/octoagent`,原则是**只做导出层,不搬迁/重写 `internal/` 里已经跑通的逻辑**——用"类型别名 +
从 `prepareToolTurn` 抽取出的一个新函数"这种最小改动的方式,现有 CLI/server/IM 三个入口继续用它们
现在的路径,不受影响。

```
pkg/octoagent/
├── octoagent.go     // 核心类型别名:Agent/Session/Message/ContentBlock/
│                    // ToolDefinition/ToolExecutor/Sender/PermissionGate/
│                    // EventHandler/AgentEvent/Reply
├── provider/
│   └── provider.go  // alias app.SenderOptions / app.NewSender
├── toolenv/
│   └── toolenv.go   // 包一层从 internal/server 抽出来的并发安全 wiring 函数,
│                    // 以及 DefaultToolsForCtx/NewDefaultRegistry 的转发
├── hooks/
│   └── hooks.go     // alias internal/hooks 的 Engine/Meta/Payload/InProcHook/
│                    // ToolDecision/SeenSet + 构造函数 + Event 常量
└── approval/
    └── approval.go  // 可选的 GateFunc 便捷适配器(见下)
```

### `pkg/octoagent`(核心别名)

```go
package octoagent

import "github.com/open-octo/octo-agent/internal/agent"

type (
	Agent               = agent.Agent
	Session             = agent.Session
	History             = agent.History
	GoalAccountant      = agent.GoalAccountant
	Goal                = agent.Goal
	GoalStatus          = agent.GoalStatus
	Inbox               = agent.Inbox
	InboxItem           = agent.InboxItem
	Message             = agent.Message
	Role                = agent.Role
	ContentBlock        = agent.ContentBlock
	ToolDefinition      = agent.ToolDefinition
	ToolResult          = agent.ToolResult
	ToolExecutor        = agent.ToolExecutor
	Sender              = agent.Sender
	StreamingSender     = agent.StreamingSender
	ToolSender          = agent.ToolSender
	ToolStreamingSender = agent.ToolStreamingSender
	LowEffortSender     = agent.LowEffortSender
	PermissionGate      = agent.PermissionGate
	EventHandler        = agent.EventHandler
	AgentEvent          = agent.AgentEvent
	EventKind           = agent.EventKind
	Reply               = agent.Reply
)

func New(sender Sender, model string) *Agent { return agent.New(sender, model) }

// Message constructors
func NewUserMessage(content string) Message         { return agent.NewUserMessage(content) }
func NewAssistantMessage(content string) Message   { return agent.NewAssistantMessage(content) }
func NewSystemMessage(content string) Message       { return agent.NewSystemMessage(content) }
func NewToolUseMessage(blocks []ContentBlock) Message    { return agent.NewToolUseMessage(blocks) }
func NewToolResultMessage(results []ContentBlock) Message { return agent.NewToolResultMessage(results) }

// ContentBlock constructors
func NewTextBlock(text string) ContentBlock                                       { return agent.NewTextBlock(text) }
func NewToolUseBlock(id, name string, input map[string]any) ContentBlock         { return agent.NewToolUseBlock(id, name, input) }
func NewToolResultBlock(toolUseID, result string, isError bool) ContentBlock     { return agent.NewToolResultBlock(toolUseID, result, isError) }
func NewImageBlock(mimeType string, data []byte) ContentBlock                     { return agent.NewImageBlock(mimeType, data) }
func NewThinkingBlock(thinking, signature string) ContentBlock                   { return agent.NewThinkingBlock(thinking, signature) }

// Role constants
const (
	RoleSystem    = agent.RoleSystem
	RoleUser      = agent.RoleUser
	RoleAssistant = agent.RoleAssistant
)

// Stop reason sentinels returned in Reply.StopReason
const (
	StopReasonMaxTurns    = agent.StopReasonMaxTurns
	StopReasonInterrupted = agent.StopReasonInterrupted
	StopReasonMaxTokens   = agent.StopReasonMaxTokens
	StopReasonStuck       = agent.StopReasonStuck
)

// AgentEvent kinds for RunStream handlers
const (
	EventTextDelta       = agent.EventTextDelta
	EventThinkingDelta   = agent.EventThinkingDelta
	EventToolInputDelta  = agent.EventToolInputDelta
	EventToolStarted     = agent.EventToolStarted
	EventToolProgress    = agent.EventToolProgress
	EventToolDone        = agent.EventToolDone
	EventToolError       = agent.EventToolError
	EventTurnDone        = agent.EventTurnDone
	EventTurnError       = agent.EventTurnError
	EventSteerInjected   = agent.EventSteerInjected
	EventCompactStarted  = agent.EventCompactStarted
	EventCompactProgress = agent.EventCompactProgress
	EventCompactDone     = agent.EventCompactDone
	EventGoalUpdated     = agent.EventGoalUpdated
)

// EventToolOutputCap is the maximum length of the Output field emitted on EventToolDone / EventToolError.
const EventToolOutputCap = agent.EventToolOutputCap

// Goal lifecycle states
const (
	GoalActive        = agent.GoalActive
	GoalPaused        = agent.GoalPaused
	GoalBlocked       = agent.GoalBlocked
	GoalUsageLimited  = agent.GoalUsageLimited
	GoalBudgetLimited = agent.GoalBudgetLimited
	GoalComplete      = agent.GoalComplete
)
```

零逻辑,纯转发。`Agent`/`Session` 等用 `type X = agent.X`(别名,不是新类型)是关键——这样
`octoagent.Agent` 和 `agent.Agent` 是同一个类型,octo 自己的 CLI/server 代码和 Director 的代码可以
互相传值,不需要互相转换。

**补全第一版遗漏的 alias**:外部 module 真正用起来时,只 alias `Message`/`ContentBlock` 是不够的——
构造 `Message` 需要 `Role` 类型和 `RoleUser`/`RoleAssistant` 常量;消费 `RunStream` 的 `AgentEvent` 需要
`EventKind` 类型和所有 `Event*` 常量,以及 `AgentEvent` 中的 `*Goal` 字段需要 `Goal`/`GoalStatus`;对
`Agent.Sender` 做类型断言以启用 streaming 或 tool streaming 需要 `StreamingSender`/`ToolSender`/
`ToolStreamingSender`/`LowEffortSender`;检查 `Reply.StopReason` 需要 `StopReason*` 常量。因此把这些
类型、常量和 `Message`/`ContentBlock` 的构造函数也一并 alias 出去。第一版把这些漏掉了,外部消费者会
立刻写不出完整代码。

**`History`/`GoalAccountant`/`Inbox`/`InboxItem` 补进别名列表**是第一版文档漏掉的一点:
`Agent.History`(`*History`)、`Agent.GoalAcct`、`Agent.Inbox` 都是 `Agent` 自己的导出字段,只做
`type Agent = agent.Agent` 的话,外部代码能读/调这些字段上的方法,但**写不出它们的类型名**(比如
Director 想在自己代码里声明一个 `h *agent.History` 字段或函数签名,就必须 import
`internal/agent`,而它导不进去)。第一次真正用到这一层时大概率就会撞上这个洞,所以把这几个类型也
一并 alias 出去。

`Agent.Hooks`/`Agent.HookMeta` 字段的类型来自 `internal/hooks` 包(`hooks.Engine`/`hooks.Meta`),
不在 `internal/agent` 下——`pkg/octoagent/hooks` alias 了这一层(`Engine`/`Meta`/`Payload`/
`InProcHook`/`ToolDecision`/`SeenSet` + `NewEngine`/`NewSeenSet` + 全部 7 个 Event 常量),外部可以
用纯 Go 代码注册 hook(`RegisterInProc`/`RegisterShell`/`RegisterShellMatched`),不需要
`~/.octo/hooks.yml`。CLI/server 专属的 `EngineFromEnvAndFiles`(读本地文件)不在 alias 范围内。

导出这一层时发现 `internal/hooks.Engine.PreToolUse` 原本只认 shell hook 的退出码/结构化 stdout
协议,`RegisterInProc` 注册的 PreToolUse 回调会被 `Configured()` 认为"已配置"但从不参与
block/allow 判断——这是刻意的设计(注释原话:"PreToolUse is a shell-configured guard"),但会让
"用纯 Go 回调做工具拦截"这个最直接的外部消费场景表现得像"配置了但不生效"。已改成
in-process hook 也能通过返回同样的 `{"decision":"block"/"approve",...}` JSON 字符串参与判断,
求值顺序是 in-process 先、shell 后,block 全局优先于 allow(`internal/hooks/engine.go`
的 `PreToolUse` 方法 + `pretooluse_test.go` 的对应测试)。

这条改动让 `EventPreToolUse` 的 in-process 回调第一次变得真正可达——之前注册了也不会被调用,
所以回调 panic 从来没有实际后果。shell hook 崩溃只会杀掉子进程,不影响宿主 Go 进程;in-process
回调跟 agent 循环跑在同一个 goroutine 里,panic 会直接冒泡打断整个 turn。`PreToolUse`
调用每个 in-process 回调时都包了一层 `recover`(`runInProcHookSafely`),panic 按"非阻塞失败"
处理(notify + 视为无意见,后续回调正常继续跑,不会被跳过)。`Inject`/`Dispatch` 里调用
in-process 回调的地方还没有同样的 recover——那是既有代码,本轮不动,留作后续。

**`Session`/`History` 内嵌 `sync.Mutex`/`sync.RWMutex`(`internal/agent/session.go:77`、
`internal/agent/history.go:14`),不可按值复制**——`type Session = agent.Session` 这个别名本身没
问题,但要在这个类型定义旁边补一行 doc comment 提醒外部调用者"始终用指针,不要按值传递/复制",否则
第一次有人写 `sess := *someSession` 就会撞上 `go vet` 的 copylocks 检查,或者更糟——绕过 vet 检查
后在运行时产生锁的隐式复制。

### Session 持久化:Director 不应该依赖 `Session.Save()`

`Session.Save()` 默认写到 `~/.octo/sessions/`(`sessionsDir()`,`internal/agent/session.go`),除非
调用者把 `Session.Dir` 设成别的路径。这跟本文档给 `NewSender` 立的"不碰本地文件"标准是矛盾的,第一版
没讨论这一点。**结论:Director 不使用 `agent.Session` 的文件持久化**,而是每轮 `Run`/`RunStream`
结束后直接从 `Agent.History` 读回消息,自己序列化进 Sinew Spine 的 PostgreSQL/Redis(这跟 Sinew
07-orchestrator 设计里"Director 自己管 session 持久化"的结论一致)。如果确实要用 `agent.Session`
只是为了复用它的字段结构(而不是它的 `Save`/`Load`),必须显式设置 `Session.Dir` 指向 Director 自己
的存储路径,只是为了复用它的字段结构(而不是它的 `Save`/`Load`),必须显式设置 `Session.Dir` 指向 Director 自己的
存储路径,避免跟本机可能存在的 octo CLI 安装互相污染。

**`Session` 的 alias 场景**:虽然 `Agent` struct 没有 `Session` 字段,但 `Session` 类型实现了
`GoalAccountant` 接口(`Agent.GoalAcct` 的字段类型),并且它的字段结构(`ID`/`Model`/`System`/`WorkingDir`/
`Messages` 等)是 Director 自己持久化时可以直接复用的形状。因此把 `Session` 也 alias 出去,让 Director
可以选择自己创建 `Session` 对象并赋给 `Agent.GoalAcct`,而不是自己重新实现一个 `GoalAccountant`。

### `pkg/octoagent/provider`

```go
package provider

import "github.com/open-octo/octo-agent/internal/app"

type Options = app.SenderOptions

func NewSender(opts Options) (octoagent.Sender, error) { return app.NewSender(opts) }
```

Director 从 `agent.yaml` 解析出 provider/model/base_url/api_key 后,直接构造 `Options` 传进来——
不碰 `~/.octo/config.yml`,不依赖任何本地文件。

### `pkg/octoagent/toolenv`(唯一需要新写逻辑的地方,范围比第一版设想的窄)

审查发现 `prepareToolTurn` 不能整体照抄(见"现状盘点"第 3 点的补充说明)——`Agent.Gate` 的重建/
覆盖、`config.Load()`/`permission.New()` 两处本地文件读取、四个 `*Server` 方法调用,都不能进入
"可复用核心"。把这些排除之后,真正能抽、且值得抽的,只剩下纯 ctx-scoped 状态拼装这一段:

- `tools.NewDefaultRegistry()`
- `tools.WithWorkingDir(ctx, a.CWD)`
- `tools.WithBackgroundManager(ctx, tools.SessionBackgroundManager(sessionID))`
- `tools.WithTaskStore(ctx, tasks.New())`
- `tools.SessionSubAgentManager(sessionID, mkSpawner)` + `tools.WithSubAgentManager(ctx, mgr)`
- `tools.SessionWorkflowManager(sessionID)` + `tools.WithWorkflowManager(ctx, wfMgr)`(可选,见下)

**四处明确不做的事,直接写进函数约定,而不是留给实现者自己猜:**

1. **不碰 `Agent.Gate`**——调用方在调用 `WireForSession` 之前或之后自己设置 `Agent.Gate`
   (配 `approval.GateFunc`),`WireForSession` 绝不重建/覆盖它。
2. **不调 `config.Load()` / `permission.New()`**——不读任何本地文件,这是跟 `provider.NewSender`
   同等级别的约束,必须在实现时显式检查(比如加一条 lint/测试断言这个函数的依赖图里不出现
   `internal/config`/`internal/permission`)。
3. **`sessionID` 永远是显式参数,不再探测 ctx**——`prepareToolTurn` 里"有 session id 走异步
   manager,没有走同步 one-shot"的分支,在抽出来的核心函数里**只保留"有 session id"这一条分支**;
   `prepareToolTurn` 自己保留另一条分支(one-shot 场景不需要，也不会调新函数)。这是一处真实的
   控制流改写,不是简单挪代码。
4. **返回的 `cleanup` 只释放本函数分配的资源,不碰 session-scoped 的 manager 缓存**——
   `SubAgentManager`/`BackgroundManager`/`WorkflowManager` 是按 `sessionID` 缓存的,供该 session
   的多次 turn 复用,因此 `cleanup` 不会销毁它们。`cleanup` 当前用于恢复可能被改动的进程级状态
   (如 workflow-discovery-cwd,如果未来决定处理它的话);如果函数实现中没有任何需要恢复的状态,
   `cleanup` 仍然是 no-op 但保留在签名中以保证向后兼容。调用方应在整个 session 生命周期结束时自行
   管理资源,而不是依赖每次 turn 的 cleanup。

事件回调(sub-agent/workflow 的 `SetOnEvent`/`SetOnExit`/`SetOnDone`)在 `prepareToolTurn` 里绑定的
是 `s.wsHub.broadcast`/`s.deliverModelNote` 这些 `*Server` 专属逻辑,`deliverModelNote`
(`internal/server/bg_tasks.go:85`)还牵扯 `s.sessionAgents`/`enqueueSteer` 这类 Director 没有对应
物的会话状态机——**这部分不抽,交给调用方通过可选回调自己接。**

计划签名(草案,实现时可能微调):

```go
package toolenv

import (
	"context"

	"github.com/open-octo/octo-agent/internal/tools"
	"github.com/open-octo/octo-agent/pkg/octoagent"
)

// Option customizes the tool environment wired by WireForSession.
type Option func(*options)

type options struct {
	subAgentOnEvent func(tools.SubAgentEvent)
	subAgentOnExit  func(tools.SubAgentNotification)
	workflowOnEvent func(tools.WorkflowEvent)
	workflowOnDone  func(tools.WorkflowNotification)
}

// WithSubAgentEvents wires callbacks for sub-agent lifecycle events.
// onEvent fires on each SubAgentEvent; onExit fires when a sub-agent finishes.
func WithSubAgentEvents(
	onEvent func(tools.SubAgentEvent),
	onExit func(tools.SubAgentNotification),
) Option

// WithWorkflowEvents wires callbacks for workflow lifecycle events.
// onEvent fires on each WorkflowEvent; onDone fires when a workflow completes.
func WithWorkflowEvents(
	onEvent func(tools.WorkflowEvent),
	onDone func(tools.WorkflowNotification),
) Option

// WireForSession 为一次 turn 准备并发安全的工具执行环境:一个新的 DefaultRegistry,
// 以及带 SubAgentManager/WorkingDir/BackgroundManager/(可选)WorkflowManager 等
// ctx-scoped 状态的 ctx。sessionID 用于隔离 background 进程和 sub-agent registry
// (同一 sessionID 复用同一份)。
//
// 返回的 cleanup 函数用于释放本函数调用期间可能改动的进程级状态。它不会销毁按 sessionID 缓存的
// manager,因为那些 manager 需要在同一个 session 的多次 turn 之间保持状态。调用方应在整个 session
// 生命周期结束时自行管理资源。
//
// 不做的事(调用方自行负责):
//   - 不读/写 Agent.Gate —— 调用方自己设置策略引擎
//   - 不调用 config.Load() / internal/permission —— 零本地文件依赖
//   - 不处理 browser 工具全局状态(SetBrowserVision 等)与 workflow-discovery-cwd 全局状态——
//     见已知限制;不启用这两类工具就不受影响
//   - 不处理 MCP client 注册(tools.SetMCPRegistry)—— 目前没有库安全路径，见已知限制
func WireForSession(
	ctx context.Context,
	a *octoagent.Agent,
	sessionID string,
	opts ...Option,
) (context.Context, octoagent.ToolExecutor, func())
```

内部实现:在 `internal/app` 新增一个不依赖 `*server.Server` 的包级函数(暂定名
`NewSessionToolEnv`),它已经是"CLI/server/IM 共用逻辑"的归宿。`NewSessionToolEnv` 只包含上面列的那几行
ctx-scoped 拼装 + 可选回调注入;`prepareToolTurn` 保留 `Gate`/`config.Load()`/`permission.New()`/四个
`*Server` 方法调用,改成"调 `NewSessionToolEnv` 拿到 executor+ctx+cleanup,再在外面叠加这些 server 专属
逻辑",而不是反过来把 server 逻辑塞进新函数。`toolenv.WireForSession` 直接 alias `NewSessionToolEnv`。

### `pkg/octoagent/approval`(可选的便捷层，非必需)

`PermissionGate` 是单方法接口，Director 自己写个 struct 实现就够，`approval` 包只是省一点样板：

```go
package approval

import "github.com/open-octo/octo-agent/internal/agent"

// GateFunc adapts a plain function into an agent.PermissionGate.
type GateFunc func(ctx context.Context, name string, input map[string]any) (allowed bool, reason string)

func (f GateFunc) Check(ctx context.Context, name string, input map[string]any) (allowed bool, reason string) {
	return f(ctx, name, input)
}
```

Director 侧用法：`agent.Gate = approval.GateFunc(func(ctx, name, input) (bool, string) {
    return spineClient.CheckPolicy(ctx, tenantID, name, input)
})`。

## 具体改动清单

1. `internal/app`:新增包级函数 `NewSessionToolEnv`,不依赖 `*server.Server`,只做"设计方案 → toolenv"里
   列的那几行 ctx-scoped 拼装(`NewDefaultRegistry`/`WithWorkingDir`/`WithBackgroundManager`/`WithTaskStore`/
   `SessionSubAgentManager`+`WithSubAgentManager`/`SessionWorkflowManager`+`WithWorkflowManager` 可选),对外
   接受回调而不是硬编码 `*Server` 方法。**明确不包含** `Agent.Gate` 重建、`config.Load()`、`permission.New()`。
2. `internal/server/handlers.go`:`prepareToolTurn` 改成调用第 1 步的新函数拿到
   `(ctx, executor)`,自己在外面继续做 `Gate` 重建、`config.Load()`、四个 `*Server` 方法回调注册
   ——**行为不变**,现有测试(`TestPrepareToolTurn_AdvertisesSubAgentAndWorkflow` 等)全部保持通过,
   `sessionID` 探测逻辑(`ctxKeySessionID`)保留在 `prepareToolTurn` 里,继续走 ctx 探测,不改成显式
   参数(那是新函数的调用约定,不是 `prepareToolTurn` 自己的)。
3. 新增 `pkg/octoagent/octoagent.go`:核心类型别名(含 `History`/`GoalAccountant`/`Goal`/`GoalStatus`/
   `Inbox`/`InboxItem`/`Role`/`Message`/`ContentBlock`/`ToolDefinition`/`ToolResult`/`ToolExecutor`/
   `Sender`/`StreamingSender`/`ToolSender`/`ToolStreamingSender`/`LowEffortSender`/`PermissionGate`/
   `EventHandler`/`AgentEvent`/`EventKind`/`Reply`),以及对应的构造函数、常量(`Role*`/`StopReason*`/
   `Event*`/`Goal*`/`EventToolOutputCap`)。加上 `Session`/`History` 不可复制的 doc comment 和 `New`。
   第一版漏掉了 `Role`/`EventKind`/`Goal`/`GoalStatus`/`StreamingSender` 等,外部消费者会写不出完整代码。
4. 新增 `pkg/octoagent/provider/provider.go`:alias `app.SenderOptions` / `app.NewSender`。
5. 新增 `pkg/octoagent/toolenv/toolenv.go`:alias 第 1 步的新函数,签名见上(`sessionID` 显式参数、
   可选回调 `Option`)。
6. 新增 `pkg/octoagent/approval/approval.go`:`GateFunc` 便捷类型。
7. 并发安全集成测试:`pkg/octoagent` 下加一个测试,模拟"一个进程里并发跑 N 个不同 model 的 agent,
   各自过 `toolenv.WireForSession`",断言互不干扰(sub-agent registry、working dir、background
   manager 全部各自隔离),并断言这条路径**不**调用 `config.Load()`/`permission.New()`、**不**修改
   传入 `Agent` 的 `Gate` 字段。这是本次改动的核心风险点。
8. dev-docs/CHANGELOG:记一笔"octo-agent 首次对外暴露正式 pkg/ API",并明确兼容性承诺(见下)。
9. 端到端最小示例(`examples/octoagent-minimal/main.go` 或类似):构造 Sender + Agent + `toolenv.WireForSession` +
   自定义 `approval.GateFunc`,跑一轮 `RunStream`,验证外部 module 视角下这套 API 真的可用。该示例也
   作为最终验收标准。

## 已知限制（如实记录，不假装解决）

- ~~Browser 工具的三个全局 setter 仍未 ctx 化~~ **已修复**：`SetBrowserVision`（现
  `SetModelVision`）/`SetBrowserRecordingGenerator`/`SetBrowserHealer` 都已经加上
  `WithModelVision`/`WithBrowserRecordingGenerator`/`WithBrowserHealer` 的 ctx 携带路径
  （`internal/tools/vision.go`、`internal/tools/browser.go`），`prepareToolTurn`
  （`internal/server/handlers_prepare_toolturn.go`）改成把这三个 stamp 进 ctx 而不是覆写进程级全局，
  跟第 6 点里"两个 agent 并发用不同 model 会互相覆盖"的竞态一起解决了。`toolenv.WireForSession`／
  `NewSessionToolEnv` 仍然不主动 wire 这三个（调用方要用 browser 工具需要自己
  `ctx = tools.WithModelVision(ctx, ...)` 等），但底层已经是并发安全的 ctx-first-then-global 解析,
  不再是原样覆写的全局变量。
- **`Agent.Gate` 的 `PermissionGate.Check` 目前是同步阻塞签名**（`(ctx, name, input) (bool,
  string)`，没有单独的"pending/等待中"状态）。调用方如果要做异步审批（等人工审批几十分钟），
  `Check` 内部必须自己阻塞等待（在一个 goroutine 里挂起），而不是让 octo 提供任何挂起/恢复机制——
  这是设计上的既有事实，pkg/ 不改变它。
- **workflow-discovery-cwd 仍未 ctx 化，且不能用同一套方案修**（`tools.SetWorkflowDiscoveryCWD`/
  `ActiveWorkflowDiscoveryCWD`，`internal/tools/workflow.go`）——跟上面三个 browser setter 不同,
  它读取的地方是 `WorkflowTool.Definition()`,而 `agent.ToolDefinition` 接口的 `Definition()`
  方法本身不带 `ctx` 参数,没有 ctx 可读。要修就得改 `Definition()` 的方法签名,这会牵动代码库里
  每一个 `ToolExecutor` 实现,是量级完全不同的改动,这次不做。并发跑的多个 agent 若 working
  directory 不同且都用 workflow 工具,仍会互相踩;`toolenv.WireForSession` 不处理它,调用方不启用
  workflow 工具即可绕开。
- **MCP client 注册目前没有库安全路径**（`tools.SetMCPRegistry`，`internal/tools/mcp.go:40`，以及
  `internal/app/mcp.go` 里 CLI/server 专属的 `ConnectMCP`/`SwapMCP` 编排逻辑）——同样是进程级
  全局，同样完全没有导出规划。Sinew Director 大概率会想用 MCP 工具（Forge/Spine 本身就是 MCP
  server），这是本轮改动**明确排除**的部分，留作后续 `pkg/octoagent/mcp` 的独立工作项，不在这次
  的验收范围内。
- **`Session`/`History` 内嵌 `sync.Mutex`/`sync.RWMutex`，不可按值复制**——alias 只解决了"能不能
  导入类型"的问题，不解决"这些类型天生只能用指针传递"这条约束，`pkg/octoagent` 的类型定义旁边需要
  写清楚。
- **错误处理是不透明的**：`Run`/`RunStream` 返回的是 `fmt.Errorf` 包装的普通 error，没有导出的
  sentinel error 类型——外部调用者没法区分"鉴权失败"/"限流"/"超出上下文长度"，这些区分目前只能
  靠解析 provider 内部（`internal/provider/*`，未导出）的错误类型。这次改动**不解决**这个问题，
  当作已知限制记录，而不是假装它不存在。
- **`pkg/octoagent` 首次发布即是公开 API 承诺的开始**：一旦有外部 module 依赖它，`internal/agent`/
  `internal/app`/`internal/tools` 里被 alias 出去的类型/函数签名变更，就会直接破坏外部消费者，
  即便这些包本身仍是 `internal/`。合入前要确认这批签名已经足够稳定。

## 兼容性

- 不修改任何现有 `internal/` 类型的字段/方法签名——`pkg/octoagent` 只做 alias 和新增一个
  `toolenv.WireForSession` 函数，`prepareToolTurn` 重构后行为不变（现有测试兜底）。
- `pkg/octoagent` 一旦发布 tag，按 semver 维护：新增字段/方法可以，删除或改签名要走 major 版本。
- 在 `pkg/octoagent` 存在期间，`internal/agent`/`internal/app`/`internal/tools` 中被 alias 出去的公开
  签名同样受到 semver 约束；非兼容变更不能仅在 `internal/` 内部"悄悄"完成，必须同步体现为 `pkg/octoagent`
  的 major 版本升级，或提供向后兼容的迁移路径。
- CLI/TUI/server/IM 四个入口不受影响，继续走它们现在的路径（`WireTools` 或
  `prepareToolTurn`），不强制迁移到 `pkg/octoagent`。

## 测试计划

1. `pkg/octoagent` 每个别名/转发函数各一个最小烟雾测试（确认类型确实相同、构造函数确实能跑）。
2. `toolenv.WireForSession` 的并发安全集成测试（见上，核心风险点），额外断言两条"不做的事"：
   过程中不出现 `config.Load()`/`internal/permission` 的调用痕迹，且传入的 `Agent.Gate` 在调用前后
   保持不变（防止未来有人不小心把 `prepareToolTurn` 里的 Gate 重建逻辑带回新函数）。
   - 依赖图中不出现 `internal/config`/`internal/permission`:通过静态检查实现,例如在测试中运行
     `go list -deps ./pkg/octoagent/toolenv` 并断言输出不包含 `github.com/open-octo/octo-agent/internal/config`
     和 `github.com/open-octo/octo-agent/internal/permission`。同时可以结合函数签名审查:`NewSessionToolEnv` 的
     实现不 import 这两个包,也不通过任何间接路径调用它们。
3. `internal/server` 现有测试全部保持通过（`prepareToolTurn` 重构的回归兜底），包括
   `TestPrepareToolTurn_AdvertisesSubAgentAndWorkflow`。
4. 一个端到端的最小示例（`examples/octoagent-minimal/main.go` 或类似）：构造一个 Sender + Agent +
   DefaultRegistry + 自定义 `approval.GateFunc`，跑一轮 `RunStream`，验证外部 module 视角下这套
   API 真的可用——这是最终验收标准。

## 非目标

- 不导出 `internal/app.Spawner`/`internal/tools.SubAgentManager` 类型本身(外部还不能直接
  `Start`/`Kill`/`ListRunning` 某个 sub-agent)——`pkg/octoagent/toolenv.WireForSession` 已经把
  `SubAgentManager` wire 进 ctx,`toolenv.DefaultToolsForCtx` 也已导出,模型可以正常调用
  `sub_agent`/`sub_agent_send` 等工具;Director 目前只需要"模型自己调用 sub-agent 工具"这一层,
  不需要在自己代码里直接操纵生命周期,不想要的话用 `disallowed_tools: [sub_agent]` 从工具列表里
  过滤掉即可。
- 不给 `internal/permission.Engine` 加新构造函数——`PermissionGate` 是单方法接口，外部实现即可
  （见"现状盘点"第 4 点，这是本次规划过程中修正掉的一项高估）。
- 不做 browser 工具、workflow-discovery-cwd 的并发安全改造——记在"已知限制"，留给真正需要它们的
  消费者出现时再做。
- **不做 MCP client 注册的导出**（`tools.SetMCPRegistry`/`internal/app/mcp.go`）——这是审查后
  新增的明确排除项，不是"忘了做"，是"这次不做"；Sinew Director 如果要用 MCP 工具，需要单独立项
  设计 `pkg/octoagent/mcp`，不能假设跟着这次改动一起自动可用。
- 不解决 `Run`/`RunStream` 错误的可分类问题（无导出 sentinel error）——记在"已知限制"。
- 不合并 `senderForEntry`/`buildSender` 的重复实现——那层是 CLI/server 专属的默认值解析，
  Director 不需要它，不属于这次改动范围（见"现状盘点"第 2 点，同样是修正掉的一项高估）。
