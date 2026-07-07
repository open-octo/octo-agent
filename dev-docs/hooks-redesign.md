# Hooks 系统重构设计

## 背景与目标

octo 当前有两套互不相干的 hook 机制:

- **shell-out 回合钩子**(`internal/hooks`):env 配置的 `OCTO_HOOK_PRE_TURN` / `OCTO_HOOK_POST_TURN`,给 Hindsight 这类外部检索层用。只在 `cmd/octo/turncore.go:runTurn` 里被调用。
- **agent 内 Go-func 钩子**(`agent.UserInputHook` / `agent.ToolResultHook`):单槽字段,写死服务 memory injector,用户不可配置。

两套机制叠加暴露出 11 个缺陷(见附录「缺陷映射」)。本设计把它们合并为**一个下沉到 agent core 的 hook 引擎**,统一分发,一次性关闭全部缺陷,并把能力面对齐 Claude Code 的 hook 模型(7 事件 + matcher + 阻断)作为 octo 原生特性。

核心判断:agent 循环本身已经具备所有需要的插桩点。只要把 hook 执行从 CLI 层挪进 agent core,并在构造每个 Agent 时挂上一个 Engine(各 Engine 共享进程级 `SeenSet`),三端(CLI/TUI、serve web/WS、IM)与子 agent 就全部自动继承 hook,无需在每个入口各自实现分发逻辑。

### 运行时事实(设计前提)

以下架构事实决定了本设计的多处取舍,先明确:

- **serve 是单进程,同时托管 web + IM 适配器**(channel 在 serve 内运行)。web 和 IM 共享同一个进程、同一份持久 Session。
- **serve 每个回合从磁盘重建 Agent**:`buildAgent(sess)` 构造 Agent,注册进 `sessionAgents` 仅在该回合期间,回合结束即 `delete`。会话之间**没有常驻内存的 Agent**——只有落进 `~/.octo/sessions/` 的持久历史会被下一回合读回。
- **CLI 是长驻 REPL,整个进程一个 Agent**:SessionStart 类"进程边界"事件在 CLI 天然只发生一次。
- **同一会话可在 CLI / web / IM 三端抢占**:共享同一份持久 Session,一端可接管另一端。因此任何跨端一致的状态必须走持久化;任何"每次运行一次"的状态是**进程级**的。

### 验收标准

- 同一份 `hooks.yml` 在 CLI、web、IM、子 agent 回合路径上行为一致。
- 支持 7 个事件点:`SessionStart` / `UserPromptSubmit` / `PreToolUse` / `PostToolUse` / `Stop` / `SubagentStop` / `PreCompact`。
- `PreToolUse` / `UserPromptSubmit` 能**阻断**(退出码 2 → 拒绝 + 把 stderr 回灌给模型)。
- 每个事件可配**多个** hook,带 `matcher` 按工具名过滤。
- `async: true` 的 hook 不阻塞回合;溢出/退出时 payload 落盘,后续启动补投,**不丢留存**。
- 现有 `OCTO_HOOK_*` env 与 Hindsight 脚本零改动继续工作,并自动获得三端覆盖。
- Claude Code 的 hook 脚本(相同事件名 + 退出码协议)手改字段名即可移植。

### 不在本设计范围内

- 不引入 hook 的图形化配置界面(web 面板)——先做数据面。
- 不做 hook 之间的依赖编排 / DAG——每个事件内的 hook 顺序执行,互不感知。
- 不做逐字节的 CC 契约兼容(见「Parity 定位」)。
- 不给 `PreCompact` 否决权(纯副作用)。

---

## Parity 定位:精神对齐,非逐字兼容

对齐 CC 的**事件模型**(7 个事件名与语义)和**退出码协议**(0/2),但 stdin/stdout 的**字段名用 octo 原生风格**(`event` / `additional_context`,而非 CC 的 `hook_event_name` / `hookSpecificOutput.additionalContext`)。

取舍理由:选平台化的真正驱动是让这套能力成为 octo 原生特性,而非做 CC 的二进制平替。逐字兼容会把表面撑大,并被 CC 演进的格式无限绑定。CC 现成脚本手改几个字段名即可移植——事件模型和退出码这两个"心智"是一致的,可移植成本低。

---

## 架构

### 组件

```
internal/hooks/
  engine.go      // Engine:事件分发、matcher、blocking、来源标签
  config.go      // hooks.yml 两级加载 + env 兼容 shim + 迁移
  payload.go     // 各事件的 stdin JSON 信封
  shell.go       // 平台 shell 执行(沿用现有 shellCmd/run)
  builtin.go     // in-process hook 注册(memory injector 迁移落点)
  spill.go       // async 落盘队列 + 启动补投(多进程安全)
```

`hooks.Runner` 升级为 `hooks.Engine`。`Engine` 是唯一的 hook 执行入口,通过新字段 `Agent.Hooks *hooks.Engine` 挂到每个 Agent 上(nil-safe,零值为 no-op)。**每个 Agent 持有自己的 Engine**——因为 memory injector 携带每会话状态(recall/nudge 锁存),in-proc hook 必须按会话隔离;但所有 Engine **共享同一个进程级 `SeenSet`**(`hooks.SharedSeen()`),这正是 SessionStart `resume` 每进程只触发一次的依据。Engine 在各入口的会话构造处组装(CLI 的 `runChat`、server 的 `buildAgent` 与 IM 回合刷新),而非单一 bootstrap 点。

现有 `Agent.UserInputHook` / `Agent.ToolResultHook` 两个单槽字段**删除**,其职责(memory 提醒)改为引擎里注册的内置 in-process hook,与 shell hook 走同一条分发路径。

### Agent core 插桩点

引擎在 agent 循环里的调用点全部在 `internal/agent`,因此天然三端 + 子 agent 共享:

| 事件 | 插桩位置 | 能力 |
|---|---|---|
| `SessionStart` | 首个回合开始前(判定见下) | 注入上下文(落盘) |
| `UserPromptSubmit` | `appendUserInput`(现 `UserInputHook` 处) | 注入上下文 / 阻断 |
| `PreToolUse` | `dispatchTools` 内每个工具 dispatch 前 | 阻断 / 放行 |
| `PostToolUse` | `dispatchTools` 内每个 result 后(现 `applyToolResultHook` 处) | 注入 result 附注 |
| `Stop` | `RunStream` 回合结束(成功**与**失败/中断都触发) | 副作用(留存) |
| `PreCompact` | turn-in / 手动 compaction 前 | 副作用 |
| `SubagentStop` | 子 agent 完成回调(spawner) | 副作用 |

子 agent 也经 `internal/app` 构造并持有同一 `Engine`,其 `Stop` 事件带 `transport: "subagent"`,顶层再收到一次 `SubagentStop`。

---

## SessionStart:三源判定与持久化语义

由于 serve 每回合重建 Agent、且会话三端抢占,SessionStart 的触发不能绑定"in-memory Agent 生命周期"(那在 serve 会退化成每回合触发)。改用两个信号:

- **持久化 `started` 标记**(存 session JSON,全三端共享)。
- **进程级 seen-set**(`map[sessionID]bool`,进程内内存,进程重启即清空)。

判定逻辑(回合开始时):

| source | 条件 | 说明 |
|---|---|---|
| `startup` | 持久 `started` 未设 | 触发并原子写入标记。每会话**一辈子一次**,任意端发首条消息触发。并发首条消息用 check-and-set 保证不 double-fire |
| `resume` | 持久 `started` 已设,但本进程 seen-set 未见过此会话 | 触发并记入 seen-set。语义 = **一个新 OS 进程 attach 到已存在会话** |
| `clear` | 用户执行 `/clear`(ClearHistory) | 历史清空,当作重新开场再触发一次 |

各端净效果:

- **CLI**:长驻 REPL 一个进程一个 Agent → 每次 `octo -c <会话>` 启动 = 新进程 = 一次 `resume`(或全新会话 = `startup`)。
- **serve(web+IM 同进程)**:会话在 serve 里首次被 build → 一次 `resume`;之后 web↔IM 之间抢占接管、后续每条消息都**不再**触发(同进程已见)。仅进程重启重新武装。
- **抢占净效果**:把会话在 web 和 CLI 间来回抢占,每次 CLI 重新拉起重跑一次 `resume`;serve 侧仅进程重启重跑。语义 = "resume 每 OS 进程一次",这是抢占模型下唯一自洽的定义(跨进程共享 seen-set 需分布式协调,得不偿失)。

**输出落点(关键)**:SessionStart 的注入内容折进当轮**第一条用户消息**(`InjectContext` → `appendUserInput` → `History.Append`),随该回合落盘。原因:serve 每回合从磁盘重建 Agent,只有进入持久历史的内容才对下一回合可见——只存在于内存的注入会在回合结束随 Agent 销毁。

因此 SessionStart 内容是**开场那一刻的静态快照,落盘后每轮可见**,直到 compaction 归档。适合"稳定的会话背景"(项目概览、开场记忆预取)。多个进程先后 attach 会各记一段(每 attach 一次),属预期,由 compaction 回收。

**SessionStart vs UserPromptSubmit 分工**:需要每轮刷新的动态内容(当前 git status、针对本条 prompt 现捞的记忆)用 `UserPromptSubmit`——它每回合触发,输出跟当轮用户消息落盘,既新鲜又持久。SessionStart 只负责一次性开场。

---

## 配置

### hooks.yml(两级合并)

对齐 skills 的两级约定(`internal/skills/skills.go`):

- `~/.octo/hooks.yml` —— 用户级,跨项目。
- `<cwd>/.octo/hooks.yml` —— 项目级,可随仓库分发。

合并语义:**追加**——项目级 hook 拼在用户级同事件 hook 之后,都执行。理由:hook 是可累加的副作用,叠加比替换符合直觉;更重要的是覆盖会让项目**静默顶掉全局留存 hook**,与"不丢留存"目标自相矛盾。项目想排除某工具用自己的 `matcher`,而非用覆盖这种大锤。

```yaml
hooks:
  SessionStart:
    - command: "hindsight-warmup"        # 开场一次,stdout 注入并落盘
      timeout: 3s

  UserPromptSubmit:
    - command: "hindsight-retrieve"      # 每轮触发,stdout 注入当轮消息
      timeout: 5s

  PreToolUse:
    - matcher: "terminal|write_file"      # 工具名正则;缺省 = 全匹配
      command: "./scripts/guard.sh"       # 退出码 2 → 拒绝该工具,stderr 作拒绝理由
      timeout: 5s

  PostToolUse:
    - matcher: ".*"
      command: "audit-logger"
      async: true                          # 不阻塞回合

  Stop:
    - command: "hindsight-retain"
      async: true

  SubagentStop:
    - command: "hindsight-retain"
      async: true

  PreCompact:
    - command: "archive-transcript"
      async: true
```

字段:

| 字段 | 默认 | 说明 |
|---|---|---|
| `command` | 必填 | 经平台 shell 执行(`sh -c` / PowerShell),支持管道 |
| `matcher` | `.*`(全匹配) | 仅 `PreToolUse` / `PostToolUse` 生效,正则匹配工具名 |
| `timeout` | `5s`,上限 `30s` | 同现 `OCTO_HOOK_TIMEOUT` 语义 |
| `async` | `false` | `true` = 后台执行不阻塞;仅副作用型事件(`PostToolUse`/`Stop`/`SubagentStop`/`PreCompact`)允许,阻断型事件强制同步 |

### 项目级 hook 的信任(trust-on-first-use)

项目级 `.octo/hooks.yml` 来自仓库,能自动放行工具(见 PreToolUse),存在"clone 恶意仓库静默放行危险工具"的攻击面。防护:

- 检测到**新的或内容变更的**项目级 `hooks.yml` 时,首次加载弹一次确认("此仓库定义了 N 个 hook,其中 M 个可自动放行工具,是否信任?")。
- 批准后记录内容指纹(存磁盘,全三端共享,批准一次全端生效);内容不变不再问。
- 与 always-allow 的"批准一次、记住"心智一致。用户级 `~/.octo/hooks.yml` 是自己写的,不需要此提示。

### env 兼容 shim

加载后把现有 env 合成为等价 hook 条目,再与 `hooks.yml` 合并:

- `OCTO_HOOK_PRE_TURN`  → `UserPromptSubmit` 一条,`timeout = OCTO_HOOK_TIMEOUT`。
- `OCTO_HOOK_POST_TURN` → `Stop` 一条,`async: true`(还原今天 fire-and-forget 的意图,真正后台化)。

现有 Hindsight 用户零改动,且因执行下沉 agent core,自动获得 web/IM 覆盖。

**迁移注意(行为变更)**:`Stop` 现在**成功与失败/中断都触发**(旧的 post-turn 只在成功时触发),这是缺陷 #9 的修复。因此经 `OCTO_HOOK_POST_TURN` 接入的留存脚本现在也会在失败/被中断的回合上被调用,此时 `assistant_reply` 可能为空且 payload 带非空 `error` 字段。**留存脚本应检查 `error` 字段**,失败回合按需跳过索引,避免写入残缺记录。payload 是向后兼容的超集(新增 `error` / `tools_used`),只解析 `user_input`/`assistant_reply` 的旧脚本不会解析报错,仅是多了失败回合的调用。

---

## Hook 协议

### stdin 信封

每次调用向脚本 stdin 写一个 JSON,含公共信封 + 事件特有字段:

```jsonc
{
  "event": "PreToolUse",
  "session_id": "sess_...",
  "cwd": "/abs/path",
  "transcript_path": "~/.octo/sessions/sess_....json",
  "model": "claude-opus-4-8",
  "transport": "cli|web|im|subagent",

  // 事件特有:
  "source": "startup|resume|clear",       // SessionStart
  "user_input": "...",                     // UserPromptSubmit / Stop
  "tool_name": "terminal",                 // Pre/PostToolUse
  "tool_input": { "...": "..." },          // Pre/PostToolUse
  "tool_result": "...",                    // PostToolUse
  "assistant_reply": "...",                // Stop:最终文本
  "tools_used": ["terminal", "write_file"],// Stop:本回合调用过的工具名
  "error": "..."                           // Stop:回合失败/中断时的信息,成功时缺省
}
```

公共信封补齐了今天只有 `user_input` 的窘境:检索层可按 `session_id` 分桶,按 `transport` 区分来源。

### 退出码与输出

对齐 CC 退出码语义:

| 退出码 | 行为 |
|---|---|
| `0` | 成功。stdout 若解析为 `{"additional_context": "..."}` 用该字段,否则用 stdout 原文;在支持注入的事件(`SessionStart`/`UserPromptSubmit`/`PostToolUse`)注入,其余忽略 |
| `2` | **阻断**。`PreToolUse`:拒绝该工具,stderr 作拒绝理由回灌模型(等价一次 tool_result error);`UserPromptSubmit`:中止本回合,stderr 作反馈注入。副作用型事件退出码 2 无阻断语义,降级为非阻断错误 |
| 其余非零 | 非阻断错误,记 notice,事件继续(保持今天"忽略并继续"的行为,向后兼容) |

进阶(PR2 可选增强):stdout 为结构化 JSON `{"decision": "block"|"approve", "reason": "..."}` 时按字段精确控制,覆盖退出码的粗粒度语义。首版只做退出码。

### 与 permission Gate 的关系

`PreToolUse` hook 与交互式 permission Gate **互补,不替代**。`dispatchTools` 内顺序:

1. 先跑 `PreToolUse` hook。`approve` → 跳过 Gate 直接放行;`block` → 拒绝,不进 Gate;`0` 且无 decision → 继续。
2. 未被 hook 决定的工具照常走 Gate(用户交互批准 / always-allow 规则)。

即:hook 做**程序化策略**(CI 环境自动放行、自动拦截危险命令),Gate 做**人工兜底**。项目级 hook 的自动放行受 trust-on-first-use 约束。

---

## async 生命周期:溢出落盘(多进程安全)

今天的 post-turn 号称 fire-and-forget,实际同步阻塞下一个 prompt。新设计:

- `async: false`(阻断型事件默认):同步执行,受 `timeout` 约束。
- `async: true`:提交到 `Engine` 持有的 bounded worker pool(并发上限,默认 4),不阻塞回合返回。

不丢留存 + 不重引入阻塞,靠**溢出落盘**:

- 队列满 / 进程退出 drain 超时时,把未送出的 payload 落到 `~/.octo/hooks-pending/`(一文件一 payload)。
- 每个进程启动时扫描该目录补投。
- **多进程安全**:serve 与 CLI 可能同时对同一会话跑留存 hook、同时补投。认领用**原子 rename**(`.pending` → `.claimed.<pid>`),抢到的进程才投递,避免两个 drainer 重复投递同一条留存。投递成功删除文件,失败改回 `.pending`。

这满足"记忆型用户不丢留存"的刚需,又不把刚修的阻塞变着法儿请回来。

---

## 迁移与向后兼容

- `OCTO_HOOK_PRE_TURN/POST_TURN/TIMEOUT` 继续有效(shim 合成),Hindsight 脚本零改动。
- `Agent.UserInputHook` / `Agent.ToolResultHook` 字段删除;memory injector 的 `Reminder` / `SaveNudge` 改为 `builtin.go` 里注册的内置 hook(`UserPromptSubmit` / `PostToolUse`),经同一引擎分发。`cmd/octo/chat.go` 与 `internal/server/server.go` 里那 4 处 `a.UserInputHook = ... / a.ToolResultHook = ...` 接线删除,统一在 app bootstrap 注册。
- `cmd/octo/turncore.go` 里 `cfg.hooks.Pre/Post` 两段调用删除,回合钩子改由 agent core 触发。`replConfig.hooks` 字段移除。

---

## 可观测性

- 每个触发的 hook 记一条结构化 notice:事件名、命中的 hook(command 摘要)、verdict(injected / blocked / passthrough / error)、耗时。async hook 记提交与投递两态。
- `octo hooks list`:打印两级合并后生效的 hook 配置(来源标注 user/project、是否已信任)。
- 阻断(`PreToolUse` deny)在 UI 明确标注是 hook 拒绝而非 Gate 拒绝,附 stderr 理由,便于调试。

---

## 分阶段落地

每个 PR 独立可合、独立可测(`httptest` + 无实网)。

**PR1 — 引擎下沉 + 统一分发(闭环 #1 #7 #8 #9 #10 #11 + #3 的 UserPromptSubmit/Stop/PostToolUse/SessionStart)**
- 新 `hooks.Engine` + `Agent.Hooks` 字段 + agent core 插桩(UserPromptSubmit/PostToolUse/Stop/SubagentStop/SessionStart)。
- SessionStart 三源判定(持久 started 标记 + 进程级 seen-set)+ 输出落盘。
- 富信封 payload;Stop 带 `tools_used` / `error`,成功与失败都触发。
- memory injector 迁为内置 hook;删除两个单槽字段与 4 处接线。
- env shim;app bootstrap 构造挂载引擎;删除 turncore 的 Pre/Post 调用。三端与子 agent 均生效。

**PR2 — 工具级阻断(闭环 #2 + #3 的 PreToolUse)**
- `PreToolUse` 插桩进 `dispatchTools`,退出码 2 阻断协议 + 与 Gate 的顺序编排。
- 结构化 JSON decision 作为可选增强。

**PR3 — 多 hook + matcher + 项目级配置 + async 落盘(闭环 #4 #5 #6)**
- `hooks.yml` 两级加载与追加合并;matcher 正则;每事件 hook 数组。
- 项目级 trust-on-first-use 指纹。
- async worker pool + 溢出落盘 + 多进程原子认领 + 启动补投。

**PR4 — 剩余事件 + 可观测性(闭环 #3 全量)**
- `PreCompact` 插桩(纯副作用)。
- `octo hooks list` + 结构化 notice。

---

## 附录:缺陷映射

| # | 缺陷 | 由哪部分修复 |
|---|---|---|
| 1 | shell 钩子只在 CLI/TUI 生效 | 执行下沉 agent core + app 挂载(PR1) |
| 2 | pre 无法阻断 | 退出码 2 协议(PR2) |
| 3 | 生命周期点太少、两套割裂 | 7 事件统一引擎(PR1/2/4) |
| 4 | 每种钩子只能配一条、无 matcher | hooks.yml 数组 + matcher(PR3) |
| 5 | 只有 env、无项目级文件 | hooks.yml 两级 + trust-on-first-use(PR3) |
| 6 | post 同步阻塞下一 prompt | async worker pool + 溢出落盘(PR3) |
| 7 | 钩子上下文太薄 | 富 stdin 信封(PR1) |
| 8 | assistant_reply 只有最终文本 | Stop payload 加 tools_used(PR1) |
| 9 | post 仅成功触发 | Stop 成功/失败/中断都触发 + error 字段(PR1) |
| 10 | in-agent 钩子单槽 | 内置 hook 注册表统一分发(PR1) |
| 11 | 子 agent/workflow 不跑钩子 | 引擎随 Agent 下沉 + SubagentStop(PR1) |
