# Tool Search for MCP 设计

> 用两个桥工具替换全量上传的 MCP 工具 schema,工具名 + 一行描述始终预渲染进 system prompt,schema 按需加载 / 调用,削减每轮请求的 token 开销。

---

## 背景与问题

每个连上的 MCP server 会把它的**每一个**工具的完整 JSON Schema 注入模型可见的 tools 列表,而这份列表每一轮请求都会重发。一两个工具多的 server(几十个工具各带参数 schema)就能占掉几万 token,且与对话内容无关。后果有两个:

1. **成本**:schema 每轮重发,长会话里累计可观;在 anthropic 协议下还会撑大需要缓存的 tools 前缀。
2. **准确率**:工具一多,模型在无关工具间"决策瘫痪",选错工具的概率上升。

octo-agent 的工具装配链路:

```
tools.DefaultTools()  →  agent.Request.Tools  →  provider 序列化  →  wire
```

`tools.DefaultTools()`(`internal/tools/registry.go`)在末尾无条件 `append(mcpToolDefs()...)`;`mcpToolDefs()`(`internal/tools/mcp.go`)为每个 live connection 的每个工具合成完整 `agent.ToolDefinition`。Tool Search 在这条链路上做延迟加载。

## 设计取舍:进程内桥,provider 无关

octo-agent 同时支持 anthropic 协议(真 Anthropic、Kimi `/coding`、DeepSeek `/anthropic`)和 openai 协议(DeepSeek 默认、DashScope、Kimi)。Anthropic 平台原生的 `defer_loading` + `tool_search_tool_regex_20251119` 只在 anthropic 协议下有效,对 openai 协议后端无能为力,而后者是日常与 MSWE 评测的主力。

因此 Tool Search 实现在 **tools 层的进程内桥**:对所有后端透明,provider 序列化零改动,桥工具数量固定(2 个)天然缓存友好。

anthropic 协议下的原生 `defer_loading` 是一个独立、可叠加的后续优化,不在本设计范围。

## v1 → v2:去掉 mcp_search,清单常驻 prompt

最初的设计有三个桥工具(`mcp_search` / `mcp_describe` / `mcp_call`),`mcp_search` 用 BM25 对延迟目录做关键词检索。上线后暴露一个问题(#1243):清单一旦延迟,连**工具名字**都从模型可见范围里消失了——用户一提到 Meegle 之类的外部系统,模型必须先调一次 `mcp_search` 确认是否存在对应工具,平白多一轮往返,还经常在第一次回答里就错误地断言"没有这个工具"。

真正需要延迟的只有 schema(占 token 的大头);工具的 name + 一行描述很便宜,没有理由也藏起来。所以 v2 去掉了 `mcp_search` 和它背后的 BM25 检索,把清单直接以纯文本形式**常驻**进 system prompt——模型能一直看见有哪些 MCP 工具、每个大致做什么,不再需要一次工具调用去"发现"。清单不设上限、不截断:名字 + 一行描述的体量远小于触发桥激活所需要的 schema 体量,没有必要为它单独设计分页或检索。

桥因此收缩成两个工具:`mcp_describe`(加载某个工具的完整 schema)和 `mcp_call`(真正调用)。

## 两个桥工具

激活后,`DefaultTools()` 不再吐出 N 个 MCP 工具的完整定义,而是吐出两个桥工具;MCP 工具的完整 schema 退入"延迟目录",仅可经 `mcp_describe` / `mcp_call` 访问。工具的 name + 一行描述则始终以 `# Available MCP tools` 清单的形式出现在 system prompt 里(见下一节)。

| 工具 | 入参 | 行为 |
|---|---|---|
| `mcp_describe` | `name` | 返回该工具的完整 JSON Schema。`name` 从 system prompt 的 `# Available MCP tools` 清单里取,不需要先检索 |
| `mcp_call` | `name`, `arguments` | 真正调用:内部转发到 `executeMCP(name, arguments)`。仅代理 `mcp__` 前缀的工具——内置工具(sub_agent、read_file 等)直接按名调用,不走这里 |

两个桥工具刻意用 `mcp_` 前缀:让模型一眼看出它们是 MCP 专属的加载/调用通道,不会把 `mcp_call` 误当成通用调用器、把内置工具(典型如 `sub_agent`)塞进去。

核心工具(`terminal`、`read_file`、`grep`、`glob`、`web_*`、`sub_agent`、`workflow` 等)**永不延迟**,始终直接出现在列表里。只有 MCP 工具的 schema 进入延迟目录。

## 延迟目录与清单预渲染

- **数据源**:`ActiveMCPRegistry().Connections()` 中每个 `mcp.Tool` 的 `name`、`description`、参数 schema。
- **清单渲染**(`tools.MCPManifestFor(model)`):遍历延迟目录,逐条输出 `- name: 一行描述`,拼成 `# Available MCP tools` 文本块,注入 system prompt(做法与 `skills.RenderManifest` 渲染 `# Available skills` 完全同构)。**不做任何截断或分页**——这份清单只含 name + 一行描述,不含 schema,几十个工具的体量通常是几百 token,远小于触发桥激活的 schema 体量阈值。
- **只在桥激活时渲染**:桥不激活(阈值以下 / off 模式)时,MCP 工具本来就带着完整 schema 全量出现在 tools 数组里,再在 prompt 文本里重复一遍纯属浪费,`MCPManifestFor` 直接返回空字符串。
- **生命周期与 skills manifest 不完全一样,踩过一次坑**:skills manifest 在渲染那一刻数据源(`skills.Discover(cwd)`)已经就绪,渲染即正确。MCP manifest 的数据源(`ActiveMCPRegistry()`)不一定——`octo` CLI 的 headless one-shot 会在渲染前**同步**连完 MCP(见 `cmd/octo/chat.go` 里 `MCPManifestFor` 调用被特意放在 MCP 连接代码块之后,而不是像 skills 那样一开头就渲染),所以渲染时数据已就位;但交互式 TUI 把 MCP 连接推迟到后台 `tea.Cmd`(`mcpBoot`)异步完成,首帧渲染时注定拿到空的清单。第一版实现忽略了这个差异,把 `MCPManifestFor` 和 skills manifest 放在同一处、MCP 连接完成之前调用,导致 TUI(以及一度的 headless)清单永远是空的——复现了 #1243 想修的问题。修复后 `cfg.recomposeMCPManifest`(`cmd/octo/repl.go`)在 `mcpReadyMsg`(`cmd/octo/tuirepl.go`)里跟 `cfg.tools` 的刷新一起重新渲染清单并回写 `a.System`/`a.LeanSystem`。之后跟 skills manifest 一样冻结:MCP server 增删要看到新清单,需要新 session 或重新连接后的下一次 Compose。

## 派发

`DefaultRegistry.Execute`(`internal/tools/registry.go`)在 MCP 路由之前先认桥工具:

```go
switch name {
case "mcp_describe": return execToolDescribe(input)
case "mcp_call":     return execToolCall(ctx, input) // {name, arguments} → executeMCP
}
```

`mcp_call` 落到既有的 `executeMCP`,后者按 `mcp__` 前缀工作,无需改动——延迟目录里的工具名本身就是 `mcp__<server>__<tool>`。

### 权限粒度

权限 gate(`agent.PermissionGate`)在 agent loop 中对每个 tool_use 调用前检查。桥工具下,模型实际发起的 tool_use 名是 `mcp_call`,真实工具名在 `arguments.name` 里。gate 必须**对内层真实工具名**做判定,否则审批粒度退化为"是否允许 mcp_call"这一个粗粒度开关。loop 在见到 `mcp_call` 时解包 `arguments.name` 再交给 gate。同理 hooks / approval prompt 都跑在真实名上。

## 激活:auto 阈值

`tools.DefaultTools()` 本身 model-agnostic,而阈值判断需要 model 的上下文窗口。引入 `tools.DefaultToolsFor(model string)`:

- `DefaultTools()` == `DefaultToolsFor("")`,等价于关闭延迟(全量),保持所有现有调用点向后兼容。
- 知道 model 的调用点(`cmd/octo/chat.go`、`cmd/octo/channel.go`、sub-agent spawner)改调 `DefaultToolsFor(a.Model)`。

判定逻辑(`enabled: auto`):

```
估算 MCP schema tokens ≈ Σ(每个延迟工具 schema 的字节数) / 4
若  estTokens ≥ threshold_pct% × contextWindow(model)  →  启用桥(工具数组吐两个桥工具,system prompt 追加 MCP 清单)
否则                                                    →  直接全量透传(零开销)
```

`contextWindow(model)` 已存在于 `internal/agent/compaction.go`,直接复用。阈值以下不引入任何桥、也不渲染清单,小工具集场景行为与现状一致。

同一个 `toolSearchActive(model, mcpDefs)` 判断同时驱动两处:`DefaultToolsFor` 决定工具数组吐桥还是吐全量定义,`MCPManifestFor` 决定 system prompt 要不要追加清单——两处必须用同一个判断结果,否则会出现"清单里有名字,但工具数组里既没有桥也没有全量 schema"的不一致。

## 配置

挂在 `~/.octo/config.yaml`(`internal/config/config.go` 的 `Config` 下):

```yaml
tools:
  tool_search:
    enabled: auto          # auto(默认) | on | off
    threshold_pct: 10      # auto 模式的激活阈值,占上下文窗口百分比
```

- `off`:现状,MCP 工具全量上传,system prompt 不追加清单。
- `on`:只要有 MCP 工具就强制走桥 + 追加清单,忽略阈值。
- `auto`:按上面的阈值决定。

v1 里的 `search_default_limit` / `max_search_limit` 是 `mcp_search` 检索粒度的配置项,`mcp_search` 去掉后一并删除(Go 端 `ToolSearchConfig`、`~/.octo/config.yaml` schema、web 端 `/api/config/toolsearch` 的请求/响应体、`ToolSearchSettings`/`ToolSearchInfo` TS 类型都同步收窄)。

## 时序

```
模型                      DefaultRegistry              MCP Registry
 │  (system prompt 里已经能看到                          │
 │   "# Available MCP tools" 清单,                       │
 │   直接决定要用哪个工具)                                │
 │                                │                          │
 │  mcp_describe("mcp__gh__…")    │                          │
 ├──────────────────────────────►│  schema lookup           │
 │  full JSON Schema              │◄─────────────────────────┤
 │◄──────────────────────────────┤                          │
 │                                │                          │
 │  mcp_call("mcp__gh__…", {…})   │                          │
 ├──────────────────────────────►│  executeMCP(name, args)  │
 │                                ├─────────────────────────►│
 │  tool result                   │◄─────────────────────────┤
 │◄──────────────────────────────┤  (gate checks real name) │
```

## 兼容性

- **双协议**:桥在 tools 层,anthropic / openai 序列化均透明,无 provider 改动。
- **sub-agent**:spawner 持有 `tools.DefaultTools` 函数值;改传 `DefaultToolsFor(model)` 后子代理同样受益(子代理连同一套 MCP)。lean/子代理 system prompt(`prompt.ComposePair` 的第二个返回值)不携带 MCP 清单,与它已经不携带 skills manifest 的既有取舍一致。
- **`mcp__` 路由**:`mcp_call` 复用既有 `executeMCP`,零改动。
- **MCP 非文本结果**:现有 `formatToolResult` 已将 image/audio 压成占位符,桥不改变这点(独立 gap,不在范围)。

## 已知取舍

- **清单不做相关性排序**:没有 `mcp_search` 之后,清单按 `mcpCatalog()` 的原始顺序(连接顺序)全量列出,不再有 BM25 相关性排序这回事——工具数量在几十量级,顺序列举足够,模型自己扫一遍即可定位。
- **多一次往返仅剩 describe**:`mcp_describe → mcp_call` 比直接调多 1 个 tool turn;比 v1 的三段式(`mcp_search → mcp_describe → mcp_call`)少了一轮,兼顾了准确率(名字常驻,不会误判"不存在")和 token 开销(schema 仍然懒加载)。

## 非目标

- 不实现 anthropic 原生 `defer_loading`(后续可叠加的独立优化)。
- 不延迟核心内置工具,只延迟 MCP(及未来非核心插件)工具的 schema。
- 不改变 MCP 非文本结果的处理。
- 不引入持久化索引或跨会话缓存——清单跟 system prompt 的其余层一样,在 session 开始时渲染一次并冻结。
- 不做清单分页/检索——已论证体量小到不需要。

## 落地切片

1. `internal/tools/tool_search.go`:去掉 `mcp_search` 与 BM25 检索,两个桥工具定义收窄,新增 `MCPManifestFor(model string) string`;`DefaultRegistry` 派发相应收窄(纯 tools 层,可独立测)。
2. `internal/prompt/prompt.go`:`Compose`/`ComposePair` 新增 `mcpTools` 层,插在 skills 层之后、memory 层之前;各调用点(`cmd/octo/chat.go`、`cmd/octo/init.go`、`internal/server/server.go`)传入 `tools.MCPManifestFor(model)`。
3. `internal/config/config.go` + `internal/server/mcp_handlers.go` + web 端类型:去掉 `search_default_limit` / `max_search_limit` 相关字段。
4. 权限 gate 解包 `mcp_call.arguments.name` 后按真实工具名检查(沿用 v1 已有实现,不变)。
5. `cmd/octo/chat.go` + `cmd/octo/repl.go` + `cmd/octo/tuirepl.go`:`MCPManifestFor` 的调用点挪到 MCP 连接代码块之后(headless 同步连接场景下清单才不为空);TUI 场景新增 `replConfig.recomposeMCPManifest` 闭包,在 `mcpReadyMsg` 里跟 `cfg.tools` 一起重新渲染并回写 `a.System`/`a.LeanSystem`(见上一节"生命周期"的踩坑记录)。`internal/server/server.go` 的四个调用点不受影响——server 在任何 session 建立之前就已经连好 MCP 注册表。

## 测试

- 两桥派发:`mcp_describe` 返回完整 schema、`mcp_call` 闭环到 `executeMCP`。
- `MCPManifestFor`:桥激活时返回含全部工具 name+description、不含 schema 的清单;桥不激活时返回空字符串;空目录返回空字符串。
- auto 阈值边界(刚好低于 / 高于 `threshold_pct`),且清单渲染与工具数组的桥/全量选择必须保持一致。
- gate 对内层真实名生效。
- `cmd/octo` 端到端回归(`TestRunChat_Headless_MCPManifestReflectsLiveRegistry`):起一个假 MCP server + 假 OpenAI 兼容 endpoint,强制 Tool Search `on`,断言真正发给模型的请求体里同时有 `# Available MCP tools` 清单和桥工具——这是 headless 路径"清单渲染早于 MCP 连接"这个坑的真实回归用例,只 mock catalog 的单测覆盖不到这类启动时序问题。
- 用 httptest 起假 MCP server 验证 `mcp_call` → `executeMCP` 全链路,遵循"go test 无 live network"约定。
