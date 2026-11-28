# WS 重连：去白屏 + 增量历史同步

Issue: open-octo/octo-agent#2745（移动端断线重连整页白屏、4G 下长会话重载约 15 秒）。

## 问题现状

断线重连后，Web UI 对每个已订阅 session 合成 `history_reload`（`web/src/lib/ws.ts:68-79`），
桌面端（`web/src/views/ChatView.svelte:621-626`）与移动端（`web/src/mobile/chatWiring.ts:482-487`）
收到后都先 `clearMsgs(sid)` 再全量重拉历史。两个直接后果：

1. **白屏**：清空发生在 fetch 开始之前，整个拉取+渲染期间用户什么都看不到。
2. **全量重拉**：`GET /api/sessions/:id/messages` 无分页，永远返回全部事件流
   （`web/src/lib/api.ts:200-206`）。几百条消息的会话，下载 + 全量重渲染在 4G 下约 15 秒。

为什么现在必须全量重拉：服务端的 WS replay buffer 只覆盖**正在运行的** turn，turn 结束时
随 live state 一起丢弃（`internal/server/ws_handlers.go:1747`）。断线期间**已完成**的 turn
的事件永久丢失，只能靠重拉持久化历史补洞。

## 目标

- 重连期间不打断用户阅读：旧 transcript 保留在屏幕上直到新数据就绪（去白屏）。
- 常见场景（短暂断线、期间无破坏性重写）零成本同步：只拉取断线期间新增的尾部消息。
- 破坏性重写（edit/rollback/compaction/clear）在断线期间发生时，自动降级为全量重载。

## 方案 A：swap-in-place 去白屏

### 机制

`chatMessages[sid]` 中，历史渲染出的消息永远是一个**前缀**（live 事件只追加在尾部；
`insertConfirmedSteer` 也是插入尾部 streaming assistant 之前，不越过边界）。
流式 delta 的原地 mutation（`appendToLastAssistant`）只作用于 live 后缀里的
streaming 气泡——历史前缀渲染时全部 `streaming: false`，不会被 delta 触碰。

因此用一个 per-session 计数器 `historyPrefixLen`（`web/src/lib/stores.ts`，
Map<sid, number>）标记边界即可，无需给消息加标记字段：

- `history_reload` / `history_rollback` handler：**不再**预先 `clearMsgs`，直接调
  `loadHistory`。
- `loadHistory` 开始时把数组**截断到前缀**：旧 transcript 的 history 部分保留在
  屏幕上，fetch 期间用户可以继续读。旧的 live 尾部被丢弃——这是刻意设计：
  live 尾部是可重建的（连接期间完成的 turn 会随 fetch 的历史重新渲染；仍在运行的
  turn 由 subscribe replay 从头重放），保留它反而会与这两者重复（现有代码正是靠
  全量 clear 避免这种重复）。
- fetch 期间到达的 live 事件（replay 的运行中 turn）落在保留的前缀之后。
- `loadHistory` 的 `.then` 里：`live = msgs.slice(historyPrefixLen)` 捕获它们；
  渲染新历史；`appendLiveAfterHistory` 接回 live 尾；更新
  `historyPrefixLen = 新历史长度`。

### 边界情况

- 服务端 watermark 保证运行中 turn 的消息不进历史响应，因此 replay 与 fetch
  不重叠（运行中 turn 的 user 消息在 watermark 之下，其 replay echo 由
  appendLiveAfterHistory 按 content + created_at 去重）。
- fetch 期间恰好有 turn 完成（live 事件已到 + 历史已含）的竞态与现状相同，
  不在本 PR 扩大。

## 方案 B：重连增量同步

### 服务端：history epoch

Session JSONL 首行 meta record 新增字段 `history_epoch`（int，缺省 0），
加字段的位置仿照同型的 `LastContextTokens`：`Session` 结构体、
`sessionRecord`、`metaRecord()`、`LoadSession` 的 meta case 共四处。

**不**逐调用点手动 +1，而是在 `Session.Save()` 里自动检测：`len(Messages) <
persisted`（破坏性收缩）或 `rewriteIsContent`（SyncFrom 报告的同长度内容重写）
都会在 rewriteAll 前将 epoch +1。这天然覆盖所有重写路径，无遗漏风险：

- handleEditMessage（TruncateTo 后 Save）、handleWSRetry、handleWSRollback
  （截短后 Save，靠 `len < persisted` 触发 rewrite）
- mid-turn compaction（SyncFrom 折叠后的 History 变短，persistTurnProgress 的
  Save 触发 rewrite）
- /clear（Messages = nil）
- 首轮失败回滚（popLast 后 SyncFrom 变短）

只追加的 turn（含 interrupt：只追加 interrupt note，不删消息）不触发。
epoch 的正确性依赖同一 session 的破坏性写入被现有 turn/binding 机制串行化；
它不是并发写令牌。如果未来允许多个持有旧 Session 副本的写入者并发重写，
计数器可能从不同旧值计算出同一个新值，届时应改为每次重写生成唯一版本标识。

### 服务端：messages 端点增量参数

`GET /api/sessions/:id/messages` 新增查询参数：

- `after=<int>`：客户端已消费的持久化消息条数（exclusive count，语义同
  `message_index+1`）。
- `epoch=<int>`：客户端缓存的 epoch。

响应新增字段：

- `epoch`（int）：服务端当前 epoch。全量响应也携带。
- `history_len`（int）：本次服务端实际 serve 的消息条数（含 watermark 截断）。
  客户端以此更新自己的 `after` 游标，不依赖事件里的 message_index 推算
  （tool_result-only 消息不产生事件，但仍占 index）。
- `incremental`（bool）：本次是否为增量响应。

判定逻辑：

- 未带 `after`/`epoch`（首次加载）→ 全量，行为不变。
- `epoch` 不匹配，或 `after > history_len`（断线期间历史被截短）→ 全量，
  `incremental: false`，客户端走 A 的整体替换路径。
- 匹配且 `after <= history_len` → 只对 `msgs[after:]` 生成事件流，
  `incremental: true`，事件保持绝对 `message_index`。
  `after == history_len`（断线期间无事发生）→ 空 events，客户端零渲染开销。

### 客户端

- 新增共享模块（`web/src/lib/stores.ts`）：Map<sid, {after: number, epoch: number}>。
  全量/增量每次成功后更新。
  只在内存中（页面刷新即丢失，此时本来就要全量加载，无缓存一致性负担）。
- `api.getSessionMessages(id, pos?)`：有缓存位置时带 `after`/`epoch` 参数。
- `loadHistory` 分两路：
  - 全量响应（或 `incremental: false`）→ A 的整体替换路径（clear + 重渲染）。
  - 增量响应 → 保留已渲染前缀，只对尾部事件逐个 `handleHistoryEvent`
    （append 模式），不重渲染旧内容；`finishToolsById` 收尾增量里的 tool 卡片。
- 挂载（session 切换）路径始终全量：mount effect 已经 `clearMsgs`，增量追加到
  空列表会缺前缀。
- WS 层（`web/src/lib/ws.ts`）不变：重连仍合成 `history_reload`，由 loadHistory
  自行决定全量或增量。服务端兼容旧客户端（不带参数即全量），客户端兼容旧服务端
  （无 `epoch` 字段时按全量处理，`epoch` 缺省视为匹配 0）。

### 降级安全性核对

| 断线期间发生 | epoch/after 表现 | 结果 |
|---|---|---|
| 无事发生 | epoch 相同，after == history_len | 空增量，零开销 |
| 正常 turn 完成 | epoch 相同，after < history_len | 尾部增量 |
| interrupt | 历史只增长（前缀不变） | 尾部增量 |
| edit / retry rollback | epoch +1 | 全量降级 |
| mid-turn compaction | epoch +1（index 整体平移） | 全量降级 |
| /clear | epoch +1 | 全量降级 |
| 服务端重启 | epoch 持久化，仍一致；replay buffer 丢失属正常语义 | 尾部增量或空增量 |

rollback 后历史重新增长超过客户端 after 的场景（先截短再长跑）：epoch 已 +1，
仍走全量，不会错拼。

## 测试计划

服务端（`internal/server`）：
- epoch 在 edit/rollback/compaction/clear 各路径 +1 并持久化。
- messages 端点：增量参数匹配/不匹配/越界三路的响应形状；watermark 截断与
  `after` 的组合；`history_len` 正确性。

前端（`web/src/lib`）：
- swap-in-place：fetch 期间的 live 事件在 swap 后保留，历史不重复。
- 增量 append：tail 事件正确接在历史后；全量降级时整体替换。
- ws.test.ts 现有重连语义（history_reload 仍派发）不回归。
