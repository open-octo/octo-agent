# 项目（Projects）：给会话分组加上统一的工作目录

## 要解决的问题

octo 的工作目录是纯粹的**每会话**属性（`agent.Session.WorkingDir`）。围绕同一个代码库开五个会话，就要设置五次工作目录；改仓库位置，要挨个改回来。会话之间没有任何"它们属于同一件事"的表达。

侧栏已经有分组（`internal/server/session_groups.go`）——但分组只有一个名字，不携带任何配置。

**项目 = 带工作目录的分组。** 项目内所有会话共享一个工作目录，改一处，全项目生效。

## 范围

| 做 | 不做 |
|---|---|
| 分组携带 `working_dir` + `notes` | 项目级默认模型 / 专家 / 权限模式 |
| 项目目录强绑定，实时生效 | CLI / TUI 感知项目 |
| `notes` 注入项目内会话的 system prompt | 嵌套项目、一个会话属于多个项目 |
| Web + 桌面 UI | IM channel / cron 显式绑定项目 |

cron task 已经在自动建同名分组（`tasks_handlers.go` → `createSessionGroupNamed`）。这些分组保持"普通分组"形态，不受影响。

### 各入口的实际行为

"仅 Web + 桌面"指的是**管理界面**只在 Web/桌面。目录解析本身在服务端，所以覆盖面是这样：

| 入口 | 遵守项目目录 | 改 working_dir 的入口 |
|---|---|---|
| Web / 桌面 | 是 | 有（项目内的会话返回 409） |
| IM channel | 是 | 无 |
| CLI / TUI | 否 | 无 |

IM 走 `runChannelTurns` → `sessionCwdEnv`，和 Web 共用解析点，所以一条被拖进项目的 channel 会话下一轮就落在项目目录，冻结身份也随之更新。

CLI/TUI 是唯一的例外：`cmd/octo/chat.go` 的 `a.CWD` 恒为进程启动目录，整个 `cmd/octo/` 从不读 `Session.WorkingDir`。这是**先于项目就存在**的行为——今天 resume 一条在 Web 里设过工作目录的普通会话也一样不遵守——项目没有加剧它，只是让它更容易被撞见。

反向的静默失效不存在：`SetWorkingDir` 只有 `internal/server` 的三个调用点，CLI/TUI/IM 都没有改目录的入口，所以不会出现"在别处改了目录、Web 里不生效"。CLI/TUI 也不调 `SetComposedSystem`（每进程组一次提示词），因此用 TUI 跑一条项目会话不会把进程目录写进 `ComposedForCWD` 去污染 Web 侧的冻结身份。

## 数据模型

扩展现有的 `sessionGroup`，不新建实体：

```go
type sessionGroup struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	SessionIDs []string `json:"session_ids"`
	Collapsed  bool     `json:"collapsed,omitempty"`

	// WorkingDir 非空 ⇒ 这个分组是一个「项目」，组内所有会话的工具都在
	// 这里运行。空 ⇒ 普通分组，行为与今天完全一致。
	WorkingDir string `json:"working_dir,omitempty"`
	// Notes 是项目级说明，注入组内会话 system prompt 的项目记忆层。
	Notes string `json:"notes,omitempty"`
}
```

**`WorkingDir != ""` 是「项目」的唯一判据。** 存量 `~/.octo/session-groups.json` 无需迁移，反序列化后两个字段为空，就是今天的普通分组。这也让"把分组升级成项目"和"把项目降级回分组"都只是一次字段写入。

## 工作目录的解析优先级

```
项目 WorkingDir  >  Session.WorkingDir  >  服务端默认 workspaceDir
```

项目**优先于**会话自己的值——这是"强绑定"的含义。会话原有的 `WorkingDir` 留在盘上不删，只是被遮蔽；会话移出项目后自动恢复。

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
- **不会漏**。任何路径改了目录（项目、单会话 PATCH、移入移出项目）都自动生效，不用记得在每个写入点补一次清理。
- **顺手修掉存量缺口**，且不是"顺手优化"——它是强绑定正确性的前提。

存量会话的 `ComposedForCWD` 为空，与当前 cwd 不匹配，下一 turn 会重新组装一次。一次性代价，可接受。

### 缓存代价

改项目目录 = 组内所有会话下一 turn 的 prompt 前缀变化 = 各失效一次 prompt cache。改目录是低频操作，可接受。但这也意味着**不要**把频繁变动的东西塞进 `notes`。

### notes 的注入位置

`prompt.ComposePair(base, cwd, envCtx, skills, mcpTools, memory, coauthor, expertMode)` 的 `memory` 参数就是 L1 项目记忆层。把 notes 拼在 `memInjection` 前面即可，不需要改 `ComposePair` 签名。

## 新会话的创建行为

`applyDefaultWorkspaceDir`（`handlers.go:236`）今天给每个新 web 会话 seed 一个 `WorkingDir`。**在项目下创建的会话不 seed** ——留空，让项目解析生效。seed 了反而会在会话移出项目后留下一个莫名其妙的残值。

## API

复用现有的 `PATCH /api/session-groups/{id}`，加两个可选指针字段：

```go
type updateSessionGroupRequest struct {
	Name       *string `json:"name,omitempty"`
	Collapsed  *bool   `json:"collapsed,omitempty"`
	WorkingDir *string `json:"working_dir,omitempty"` // "" 显式降级回普通分组
	Notes      *string `json:"notes,omitempty"`
}
```

目录校验（`expandDir` + `os.Stat` + `IsDir`）今天内联在 `handleUpdateSessionWorkingDir` 里，抽成共享 helper，两个入口的报错文案保持一致。

`GET /api/session-groups` 与会话列表接口顺带返回 `working_dir` / `notes`，前端不额外发请求。

## UI

- **分组头**：项目额外显示一行缩写目录（`~/code/foo`）。行内菜单加「项目设置」，弹出工作目录（复用现有 folder picker / 桌面原生目录对话框）+ 说明文本框。
- **Composer 的 cwd chip**：会话属于项目时显示项目目录，且**置为只读**，点击提示「由项目〈名字〉统一管理」。这一条不能省——否则用户改了没反应，就是一次静默失效。
- **拖入 / 移出项目**：复用现有的 `PUT /api/sessions/{id}/group`。移入后工作目录立刻显示为项目目录，移出后回落到会话自己的值。

## 测试要点

- 解析优先级三种组合：有项目、无项目有会话值、两者都无。
- 移入 / 移出项目后 `sessionCwd` 与 `sessionCwdEnv` 返回一致（防三个解析点分叉）。
- 改项目目录后，下一 turn 的 composed system prompt 里的 `Working directory:` 跟着变。
- 改 `notes` 触发重新组装；不改则复用冻结值（保住 prompt cache）。
- 存量 `session-groups.json`（无新字段）加载后仍是普通分组，会话工作目录不受影响。
- 项目下新建的会话 `WorkingDir` 为空。
