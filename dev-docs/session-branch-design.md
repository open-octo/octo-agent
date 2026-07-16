# Session Branch（会话分支）

> 从会话的某条用户消息开一个分支，原会话不变，新建一个会话复制到该消息为止的历史，让用户改写 prompt 做变体对比。

---

## 背景

用户在调试 prompt 时，常常想"这句话换种说法效果会怎样"。之前只能手动复制历史到新会话再改，或者在原会话里 steer——但这会污染原会话。Session Branch 让用户在任意用户消息上点一下就能开一个干净的变体会话，原会话完全不动。

## 功能设计

### 用户流程

1. 在 Web UI 的会话页面，hover 任意一条**用户消息**，操作栏出现 Branch 按钮（git-branch 图标）。
2. 点击 Branch 弹出编辑框，预填该消息的原始内容。
3. 用户改写 prompt（也可以不改），点"运行"。
4. 后端创建一个新 session：复制原 session 的 meta（model、system prompt、working_dir、permission_mode）+ 从开头到所选消息（含）的所有历史消息。如果用户改写了 prompt，最后一条消息的内容会被替换。
5. 前端跳转到新 session，自动发送这条（可能改写过的）用户消息，assistant 立即开始回复。
6. 原 session 完全不受影响。

### Lineage（血统）

新 session 的 `BranchedFrom` 字段记录源 session 的 ID，持久化在 JSONL 的 meta 行里。Web UI 的会话 header 会显示"Branched from <标题>"标签，方便溯源。

## 后端实现

### 数据模型

`Session` 新增一个字段：

```go
type Session struct {
    // ... 现有字段 ...
    // BranchedFrom records the session id this session was branched from.
    // Empty means the session was created normally.
    BranchedFrom string `json:"branched_from,omitempty"`
}
```

`sessionRecord`（JSONL 行）同步加上 `branched_from`，`metaRecord()` 把它写进 meta 行，`LoadSession` 从 meta 行读回来——跨 Save/Load 存活。

### BranchFrom 方法

```go
func BranchFrom(s *Session, count int) *Session
```

- 复制 meta：Model、System、WorkingDir、PermissionMode、ModelConfig
- 复制 `Messages[0:count]`（count 条）
- 设置 `BranchedFrom = s.ID`
- count 越界自动 clamp 到 `[0, len(s.Messages)]`
- 调用方负责 `Save()`

### HTTP API

```
POST /api/sessions/{id}/branch
Body: { "message_index": 42, "prompt_override": "改写后的 prompt（可选）" }
Response: { "session": { ...新 session 的 sessionItem } }
```

- `message_index` 是用户消息在 `Session.Messages` 中的位置（0-based）
- `prompt_override` 非空时，替换分支后最后一条消息（即所选用户消息）的内容
- 越界返回 400，源 session 不存在返回 404

### message_index 对齐

后端在 `history_user_message` 事件里携带 `message_index`（该消息在持久化 `Messages` 数组中的位置）。原因是 replay 时会跳过 tool_result-only 的 bookkeeping 消息，导致前端渲染索引与后端持久化索引不一致。前端 Branch 按钮使用后端发来的 `messageIndex`，而不是 `{#each}` 的数组索引。

## 前端实现

### 消息操作栏

用户消息 hover 显示两个按钮：Branch + Copy。

### 分支编辑弹窗

点击 Branch 弹出 modal：
- 标题"创建分支会话"
- 说明文字
- textarea 预填原 prompt，用户可编辑
- 取消 / 运行按钮

### confirmBranch 流程

1. 调 `api.branchSession(sid, messageIndex, draft)`
2. 把新 session 插入 sidebar 列表，设为 active
3. 关闭 modal
4. 100ms 后通过 `ws.sendMessage` 自动发送变体 prompt（等 store 注册完成）

### Branched-from 标签

会话 header 的标题旁边，如果 `currentSession.branched_from` 非空，显示"Branched from <源 session 标题>"。

## 文件清单

| 文件 | 改动 |
|------|------|
| `internal/agent/session.go` | `BranchedFrom` 字段 + `BranchFrom()` 方法 + metaRecord/LoadSession 同步 |
| `internal/agent/session_persist_test.go` | `TestBranchFrom` + `TestBranchFrom_Clamp` |
| `internal/server/handlers.go` | `handleBranchSession` + `branchSessionRequest` + `toSessionItem` 加字段 |
| `internal/server/ws_handlers.go` | `history_user_message` 事件携带 `message_index` |
| `internal/server/ws_types.go` | `wsEventSessionCreated` 结构体 |
| `internal/server/server.go` | 注册 `POST /api/sessions/{id}/branch` 路由 |
| `internal/server/server_test.go` | `TestHandleBranchSession` |
| `web/src/lib/api.ts` | `branchSession()` |
| `web/src/lib/types.ts` | `Session.branched_from` |
| `web/src/lib/i18n.ts` | `branch.*` 中英文键 |
| `web/src/views/ChatView.svelte` | Branch 按钮 + 编辑弹窗 + confirmBranch + header 标签 |

## 测试

- `TestBranchFrom` / `TestBranchFrom_Clamp`：模型层复制逻辑 + 越界 clamp
- `TestHandleBranchSession`：HTTP 层 404/400/200 + override + 血统 + 源 session 不动
- 现有 `TestHandleGetSessionMessages_*` 仍通过（事件加了 `message_index` 字段）
