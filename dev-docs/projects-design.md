# 任务与项目：侧栏的两个概念

## 要解决的问题

octo 的工作目录曾是纯粹的**每会话**属性（`agent.Session.WorkingDir`）。围绕同一个代码库开五个会话，就要设置五次工作目录；改仓库位置，要挨个改回来。会话之间没有任何"它们属于同一件事"的表达。

**项目 = 一个工作目录 + 在它上面干活的会话。** 项目内所有会话共享这个目录，改一处，全项目生效；项目也是记忆分层的作用域单位。

用户能创建的东西只有两种：**任务**（一条散落会话）和**项目**。除此之外，侧栏还渲染一类系统自己造的聚合：定时任务的运行簇。曾经存在过第三种用户概念——"普通分组"，一个只有名字、不带目录的组——已经取消：它占着一行看起来像项目的位置，却不携带项目所携带的任何东西（工具的目录、记忆层、总体提示词），让同一个列表用三种行表达两种事物。

## 范围

| 做 | 不做 |
|---|---|
| 项目携带 `working_dir` + `notes` | 项目级默认模型 / 专家 / 权限模式 |
| 项目目录强绑定，实时生效 | CLI / TUI 感知项目 |
| `notes` 注入项目内会话的 system prompt | 嵌套项目、一个会话属于多个项目 |
| Web + 桌面 UI | IM channel / cron 显式绑定项目 |

**每个 cron task 就是一个项目**：调度器为它建一个同名项目，工作目录 `<workspace>/<任务名>`（`cronProjectDir`，按需创建，`TaskID` 记录来源）。这解决了一个实打实的 bug——调度器不像 HTTP 创建路径那样 seed 工作目录，所以运行会话既没有自己的目录、簇也没有，一路落到**服务器的启动目录**：机器上所有定时任务都在 `octo serve` 恰好启动的那个目录里跑，两个任务写同名文件会互相覆盖。一任务一目录同时给了它自己的记忆层（记忆按项目划作用域）。

目录在创建时由任务名派生一次，之后属于项目：**改任务名只改项目名，不动目录**——搬目录要么把任务已经写下的东西留在原地，要么得整体搬走，而改标题的人要的不是这个。任务显式设了 `directory` 时以它为准。

侧栏不给定时任务单独一节：它就是项目，只在项目行上带一个小时钟标记来源。

### 各入口的实际行为

"仅 Web + 桌面"指的是**管理界面**只在 Web/桌面。目录解析本身在服务端，所以覆盖面是这样：

| 入口 | 遵守项目目录 | 给会话单独设 working_dir |
|---|---|---|
| Web / 桌面 | 是 | 无（`PATCH …/working_dir` 一律 409） |
| IM channel | 是 | 无 |
| CLI / TUI | 是（resume 时） | 无 |

**没有任何入口能给会话单独设工作目录，也没有任何入口能创建不带目录的组。** 目录是项目的属性，落地页上选目录这个动作本身就是把新会话归入该目录对应的项目（没有就建）。曾经允许过：散落任务可以把自己指向某个仓库，工具确实在那儿跑，但记忆留在共享层——因为项目记忆按项目归属划作用域，而任务不属于任何项目。于是用户得到一个对着仓库、却对它一无所知的会话。把目录挂到项目上消除了这个割裂。写在这之前的会话，启动时由 `adoptTaskWorkingDirs` 归入其目录对应的项目。唯一排除的是工作区目录——`applyDefaultWorkspaceDir` 给每条没选目录的会话都 seed 了它，收进去等于把整份任务列表塞进一个以工作区命名的项目。排除同时看两个值：当前 `workspace_dir` 解析出的目录，以及内置默认 `~/Octo`（后者无条件排除，因为改过 `workspace_dir`、换过机器、或从备份恢复的会话里仍然写着 `~/Octo`，只比当前配置就正好会把这些扫进项目）。除此之外的目录都算用户选过，一律升级成项目。

**管理界面只在 Web/桌面**——项目的创建和编辑只有一处入口。但目录解析在服务端，所有入口都遵守。

IM 走 `runChannelTurns` → `sessionCwdEnv`，和 Web 共用解析点，一条被拖进项目的 channel 会话下一轮就落在项目目录，冻结身份也随之更新。

CLI/TUI 在 resume 时查一次 `server.ProjectDirForSession`（`cmd/octo/chat.go` 的 `projectRunDir`）：会话属于项目就把**整个 run** 重定位到项目目录。不是只改 `a.CWD` —— 沙箱根、项目 hooks 的信任根、项目记忆目录、env context 全都由同一个 `cwd` 派生，让它们分裂（工具在一个目录跑、hooks 从另一个目录找）比两种选择中的任何一种都糟。

CLI 只认**项目目录**，不认会话自己的 `WorkingDir`。因为 `applyDefaultWorkspaceDir` 给每条 web 会话都 seed 了默认工作区（`~/Octo`），跟随它会把 `octo -c` 从用户所在的仓库里拽走；项目目录则是用户为一组会话显式设的。

解析只发生一次，和这段代码里其它所有东西一样——REPL 内切换会话不重算，与 CLI 既有的"每进程组一次系统提示词"模型一致。重定位会打印一行说明：静默地在别处跑工具会让人莫名其妙。

CLI 只重定位目录，**不注入项目 notes**——同一会话在 CLI 和 Web 下的 system prompt 因此不同。这是范围裁剪的直接推论（CLI 每进程组一次提示词，且不走服务端的组装点），不是遗漏。

CLI 仍然**没有**修改工作目录的入口。`SetWorkingDir` 只有 `internal/server` 三个调用点，所以不存在"在别处改了目录、Web 里不生效"的反向静默失效；项目设置只能在它被创建的地方改。CLI/TUI 也不调 `SetComposedSystem`（每进程组一次提示词），因此用 TUI 跑一条项目会话不会把目录写进 `ComposedForCWD` 去污染 Web 侧的冻结身份。

## 数据模型

扩展现有的 `sessionGroup`，不新建实体：

```go
type sessionGroup struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	SessionIDs []string `json:"session_ids"`
	Collapsed  bool     `json:"collapsed,omitempty"`

	// WorkingDir 非空 ⇒ 这个组是一个「项目」，组内所有会话的工具都在
	// 这里运行，记忆也按它划作用域。
	WorkingDir string `json:"working_dir,omitempty"`
	// Notes 是项目级说明，注入组内会话 system prompt 的项目记忆层。
	Notes string `json:"notes,omitempty"`
	// TaskID 非空 ⇒ 这个项目是为该 cron 任务创建的。只用于启动时的修复
	// （给写在这之前、还没有目录的簇补上目录），不参与任何 UI 或权限判定。
	TaskID string `json:"task_id,omitempty"`
}
```

**`WorkingDir != ""` 是「项目」的唯一判据。** 没有目录的组是已取消的"普通分组"，启动时由 `dissolvePlainGroups` 解散——只删组，不动会话：不属于任何组的会话就是任务，而那正是这些会话原本被组织成的东西。它们各自 `WorkingDir` 里的真实目录随后由 `adoptTaskWorkingDirs` 接手，所以两个 pass 的顺序是语义的一部分（`reconcileRegistry`）。

定时任务的簇写在它成为项目之前，既没有 `TaskID` 也没有目录，所以解散前先修：从 scheduler 反查回填 `TaskID`（task 的 `SessionGroupID` → 组），再补上 `<workspace>/<任务名>`。没有这一步，每个定时任务的全部运行历史会连同普通分组一起被解散。`TaskID` 也让 scheduler 启动失败时这些项目仍然认得出来。

## 工作目录的解析优先级

```
项目 WorkingDir  >  Session.WorkingDir  >  服务端默认 workspaceDir
```

项目**优先于**会话自己的值——这是"强绑定"的含义。会话原有的 `WorkingDir` 留在盘上不删，只是被遮蔽；项目被删除后自动恢复（删项目是会话离开项目的唯一途径，见下）。

今天有三个解析点各自读 `sess.WorkingDir`：

- `Server.sessionCwdEnv`（`server.go:1732`）——turn 真正用的 cwd + env context
- `Server.sessionCwd`（`server.go:1742`）——会话列表 / 状态展示
- `Server.sessionCwdByID`（`server.go:1754`）——只有 session id 的 WS 推送

三处必须收敛到同一个 helper，否则展示的目录和实际运行的目录会分叉：

```go
// resolveSessionDir 是工作目录的唯一裁决点。
func (s *Server) resolveSessionDir(sessionID string, own string) string {
	if dir := s.projectDirFor(sessionID); dir != "" {
		return dir
	}
	if own != "" {
		return own
	}
	return s.curCwd()
}
```

### 反查索引

`projectDirFor` 需要 sessionID → 项目 的反查。会话列表接口对每个会话调一次 `sessionCwd`（`handlers.go:126`），朴素实现会把 `session-groups.json` 读 N 遍。

在 `groupMu` 下缓存整个 `groupFile` 加一份 `map[sessionID]*sessionGroup` 反查表，所有写路径（`saveRegistry`）失效缓存即可。单用户本地工具，进程内缓存足够——跨进程的最后写入者获胜语义本来就是现状（见 `groupMu` 的注释）。

## 与 system prompt 冻结的冲突

这是本设计唯一的硬点。

`Session.ComposedSystem` 在首个 turn 冻结，之后每个 turn 复用同一字符串，为的是保住 provider 的 prompt cache 前缀（`server.go:1284-1298`）。而**工作目录本身就烘焙在里面**——`buildEnvContext(cwd)` 产出的 `- Working directory: /path` 是 composed prompt 的一部分。项目级 `notes` 同理。

所以改项目目录后，工具 cwd 会立刻跟着变（`buildAgent` 每 turn 重设 `a.CWD`），但 system prompt 里还写着老目录。模型会看到两个互相矛盾的事实。

**这个缺口今天已经存在**：`handleUpdateSessionWorkingDir`（`handlers.go:1517`）改单会话工作目录时也没有解冻 prompt。项目功能必须修掉它，否则强绑定语义在模型眼里是假的。

### 方案：把 cwd 和 notes 纳入冻结身份

已有先例——模型切换会强制重新冻结，因为 MCP manifest 依赖模型的上下文窗口（`IsComposedFor` 检查的是模型而不只是"是否为空"）。cwd 和 notes 是完全同构的第三、第四个维度。

```go
// agent 层：冻结时记下它是针对哪个 cwd / 哪份 notes 组装的
ComposedForCWD   string `json:"composed_for_cwd,omitempty"`
ComposedForNotes string `json:"composed_for_notes,omitempty"` // notes 的 sha256 前 12 位

func (s *Session) IsComposedFor(model, cwd, notesHash string) bool {
	return s.ComposedSystem != "" &&
		s.ComposedForModel == model &&
		s.ComposedForCWD == cwd &&
		s.ComposedForNotes == notesHash
}
```

这比"改项目时遍历组内会话逐个 `ClearComposedSystem`"好在三点：

- **不需要处理并发**。遍历清理会和正在跑的 turn 抢写 session 文件；惰性判定发生在下一 turn 的组装点，天然串行。
- **不会漏**。任何路径改了目录（改项目目录、删项目）都自动生效，不用记得在每个写入点补一次清理。
- **顺手修掉存量缺口**，且不是"顺手优化"——它是强绑定正确性的前提。

存量会话的 `ComposedForCWD` 为空，与当前 cwd 不匹配，下一 turn 会重新组装一次。一次性代价，可接受。

### 缓存代价

改项目目录 = 组内所有会话下一 turn 的 prompt 前缀变化 = 各失效一次 prompt cache。改目录是低频操作，可接受。但这也意味着**不要**把频繁变动的东西塞进 `notes`。

### notes 的注入位置

`prompt.ComposePair(base, cwd, envCtx, skills, mcpTools, memory, coauthor, expertMode)` 的 `memory` 参数就是 L1 项目记忆层。把 notes 拼在 `memInjection` 前面即可，不需要改 `ComposePair` 签名。

## 新会话的创建行为

`applyDefaultWorkspaceDir`（`handlers.go:236`）今天给每个新 web 会话 seed 一个 `WorkingDir`。**在项目下创建的会话不 seed** ——留空，让项目解析生效。seed 了反而会在项目被删除后留下一个莫名其妙的残值。

入口是项目头的「+」按钮：`POST /api/sessions` 带 `group_id`，服务端**先入组、再走 seed 逻辑**——顺序是语义的一部分，守卫查的就是"这个会话在不在项目里"。把尚未落盘的会话 ID 先写进 registry 是安全的：registry 只存裸 ID，后续 Save 失败留下的死 ID 按既有设计无害（前端与活会话列表交叉过滤）。未知 `group_id` 直接 404，不产出孤儿会话。

**会话的归属在创建时决定，之后不可改。** `PUT /api/sessions/{id}/group` 一律 409。因为"移动"不是一件事而是四件，且事后无法让它们一致：工具的目录、记忆层、烘焙进 system prompt 的项目 notes、hooks 与沙箱的信任根，全都由项目派生。被移动过的会话会留下一份"前半段在别处跑、读的是另一个项目的 notes"的记录，而保住 prompt cache 的冻结判据分辨不出这种情况（判据是 cwd + notesHash，会话自己的目录恰好等于项目目录时甚至不会重组）。在创建时决定，这四件事对会话的整个生命周期都成立。

因此存量里"先建会话、再拖进项目"产生的会话（带着 seeded 的 `~/Octo`，被项目遮蔽）是历史形态，不会再新增。

## API

复用现有的 `PATCH /api/session-groups/{id}`，加两个可选指针字段：

```go
type updateSessionGroupRequest struct {
	Name       *string `json:"name,omitempty"`
	Collapsed  *bool   `json:"collapsed,omitempty"`
	WorkingDir *string `json:"working_dir,omitempty"` // 不可置空（400）——降级的目标已不存在
	Notes      *string `json:"notes,omitempty"`
}
```

目录校验（`expandDir` + `os.Stat` + `IsDir`）今天内联在 `handleUpdateSessionWorkingDir` 里，抽成共享 helper，两个入口的报错文案保持一致。

`GET /api/session-groups` 与会话列表接口顺带返回 `working_dir` / `notes`，前端不额外发请求。

## UI

侧栏分两节，判据全在 `web/src/lib/sidebarSections.ts` 的 `splitSections`：

| 节 | 内容 | 用户可编辑 |
|---|---|---|
| 任务 | 不属于任何项目的会话，平铺 | 会话自身（改名 / 删除 / 置顶 / 归档） |
| 项目 | 每个项目一行，装它的会话；定时任务的项目也在这里 | 是（改名 / 删除 / 排序 / 设置 / 在项目内新建会话） |

没有目录的组前端直接丢弃而不是渲染成第三种行——服务端启动时已经解散它们，在那之前把它的会话渲染两遍比不渲染更糟。

- **新建项目**：先选目录再建，因为项目就是目录。目录决定名字（basename），已有同目录项目则复用而不是造重复。侧栏顶部按钮和会话行的「移动到项目 → 新建项目」走同一条 `resolveProjectForDir`。
- **项目头**：额外显示一行缩写目录（`~/code/foo`）。行内齿轮打开项目设置，工作目录（复用现有 folder picker / 桌面原生目录对话框）+ 说明文本框；目录不可清空。
- **Composer 的 cwd chip**：会话属于项目时显示项目目录，且**置为只读**，点击提示「由项目〈名字〉统一管理」。这一条不能省——否则用户改了没反应，就是一次静默失效。
- **没有"移入 / 移出项目"**。会话行没有这个动作，弹层也一并去掉了。要在某个项目里干活就在那个项目里新建会话（项目头的「+」），或者在落地页选它的目录。删除项目会把它的会话变回任务——这是会话离开项目的唯一途径。

## 多 tab 同步

registry 的每次成功写盘（分组增删改、成员移动、pin、项目目录/notes 编辑）都会全局广播 `session_groups_changed`，钩在唯一写点 `saveRegistry` 上（`notifyGroupsChanged`，Server `initWS` 时装配）——而不是散在每个 handler 里，这样 cron 的编程式写入和将来的新写路径都不会漏。事件**不带 payload**：客户端收到后重新拉 `GET /api/session-groups`，registry 的形状不用在客户端镜像一份。

没有它，一个 tab 里改掉的项目目录在其它 tab 里保持陈旧——sidebar 头和 composer chip（从 groups store 派生）会误报工具实际运行的位置。执行侧永远正确（解析在服务端），这纯粹是显示同步。发起方 tab 自己也会收到广播并重拉一次，幂等无害。

## 测试要点

- 解析优先级三种组合：有项目、无项目有会话值、两者都无。
- 会话在项目里 / 项目被删除后，`sessionCwd` 与 `sessionCwdEnv` 返回一致（防三个解析点分叉）。
- `PUT /api/sessions/{id}/group` 对任何目标（项目、空、不存在）都返回 409，且不改动既有归属。
- 项目遮蔽而不覆写会话盘上的 `WorkingDir`：删项目后回落，重新加载可见原值未被改写。
- 改项目目录后，下一 turn 的 composed system prompt 里的 `Working directory:` 跟着变。
- 改 `notes` 触发重新组装；不改则复用冻结值（保住 prompt cache）。
- 普通分组被解散、其会话变回任务；带目录的会话随后被 `adoptTaskWorkingDirs` 归入项目（顺序）。
- 运行簇在没有 `TaskID` 时靠 scheduler 回填存活；已有 `TaskID` 时不依赖 scheduler。
- 两个 pass 各自幂等（每次启动都跑）。
- 创建不带 `working_dir` 的组返回 400；清空既有项目目录返回 400。
- 运行簇的改名 / 删除 / 塞入会话都返回 409。
- 项目下新建的会话 `WorkingDir` 为空。
