# 浏览器录制功能复杂场景优化

## 背景

在 ke.com（贝壳找房）报告下载等复杂数据看板的录制过程中，发现 `browser` 录制功能在以下场景存在不足：

1. **新 Tab 竞态**：用户点击"查看详情"打开新 Tab，在新 Tab 里点击前录音未就绪 → 事件丢失
2. **缺少等待步骤**：录制只记 click 不记等待，回放时下一步在页面就绪前就点了 → 点空
3. **选择器脆弱**：生成的选择器全是 `div > table > tbody > tr:nth-of-type(4) > td:nth-of-type(1)`，DOM 微调就失效
4. **下载行为丢失**：用户点击触发 Excel 下载，录制只记 click，回放时无法捕获文件
5. **废操作无法精简**：用户选错日期后重新选择，录制里两次选择都在，无法自动剔除试错步骤

## 设计方案

### 1. 新 Tab 录制竞态修复

**问题**：`instrumentPageSession` 是异步的（addBinding → captureScript 注入 → 开始捕获），Tab 打开后用户立刻点击会丢事件。

**方案**：Tab 打开后立即 `Page.stopLoading()` 暂停加载，完成 instrument 后 `Page.navigate` 到当前 URL 重新渲染。用户看到页面时录音已在位，不会丢事件。

```go
func (r *Recorder) instrumentPageSession(ctx context.Context, session string) {
    _, _ = r.page.cli.call(ctx, session, "Page.stopLoading", nil)
    // ... domains + instrumentSession + watchNavigations ...
    _, _ = r.page.cli.call(ctx, session, "Page.navigate", map[string]any{"url": curURL})
}
```

**决策依据**：相比"加 wait 步骤回放时再等"，在源头修复更彻底——不依赖回放时的等待策略。

### 2. 自动插入 wait 事件

**方案**：在 `captureScript` 里嵌入两层检测：

- **网络活动检测**：click 后 150ms 检查 `__octoNet.n > 0`（全局的 fetch/XHR 计数器），有活动则插入 `{type:"wait", wait_kind:"network"}`
- **DOM 变化检测**：`MutationObserver` 监听新增节点，命中"显著元素"（`role="dialog"` / class 含 modal|dialog|calendar|picker / 大面积 overlay）→ 300ms debounce 后插入 `{type:"wait", wait_kind:"element", selector:"..."}`

**显著元素判定规则**（正则匹配 class 关键词）：
```
/(^|\s)(modal|dialog|popup|popover|drawer|overlay|calendar|picker|dropdown|lightbox|tooltip|ant-modal|ant-picker|ant-dropdown|ant-drawer|ant-popover|ant-tooltip)($|\s)/
```

**决策依据**：录制时检测比回放时重试更精准——回放时的 healer 是通过 LLM 猜，录制时检测是确定性的事实。

### 3. 选择器语义化

**方案**：改造 `sel()` 里的路径节点生成逻辑，每个节点优先用"语义锚点"：

| 优先级 | 条件 | 生成 |
|--------|------|------|
| 1 | 节点有 `id` | `#id` |
| 2 | 节点有 `data-testid/data-test/name/aria-label` | `tag[attr="..."]` |
| 3 | 节点有 `role` | `tag[role="..."]:nth-of-type(N)` |
| 4 | 节点有结构 class | `tag.class:nth-of-type(N)` |
| 5 | 退化 | `tag:nth-of-type(N)` |

**结构 class 选择**：prefer 长 class（组件块名优先于状态名）+ BEM（`--` 前缀）+ 组件库前缀（`ant-`, `el-`, `mui-`, `odin-`）。

**效果**：`td.ant-picker-cell[role="gridcell"]:nth-of-type(2)` vs 原来的 `td:nth-of-type(2)`，DOM 行数增减不影响命中。

### 4. 下载行为识别

**方案**：recorder 启动时订阅 `Browser.downloadWillBegin` 事件，维护最近 click 的索引+时间戳，5 秒内发生 download 则把 click 升级为 download 事件并携带 `suggestedFilename`。

```go
func (r *Recorder) watchDownloads(ctx context.Context) {
    _, _ = r.page.cli.call(ctx, "", "Browser.enable", nil)
    _, _ = r.page.cli.call(ctx, "", "Browser.setDownloadBehavior", map[string]any{
        "behavior": "allow", "downloadPath": dlDir, "eventsEnabled": true,
    })
    // 订阅 + 升级最近 click → download
}
```

**`CompileRecording` 处理**：download 事件 → `download` 步骤 + 自动声明 `file[]` 输出绑定。

**决策依据**：CDP `Browser.downloadWillBegin` 是原生事件，比 click 后检查 DOM 或 URL 变化更可靠。`eventsEnabled: true` 是关键，不设置则事件不触发。

### 5. 废操作压缩 + 用户确认

#### 5.1 分层压缩策略

| 层级 | 类型 | 处理方式 |
|------|------|---------|
| Layer 0 | 连续 identical 事件 | 已有 `dedupeConsecutiveEvents` |
| Layer 1 | 确定性废操作（overwrite、A-B-A 回退） | `compressEvents` 自动删，不问 |
| Layer 2 | 不确定操作（有 wait 间隔的改动、导航兜底等） | LLM 标记置信度 + 用户确认 |

#### 5.2 Layer 1 确定性规则

**Overwrite（覆盖写入）**：同 selector 连续的 type/change 事件，保留最后一个。例如 type #q="A" → type #q="AB" → type #q="ABC" → 压缩为 type #q="ABC"。
- 前置条件：两事件间无 navigate/download/wait/click 等 side-effect。

**A-B-A 回退**：click A → （仅 clicks，无 nav/wait/download）* → click A → 删除中间 detour 和 return click，保留最终状态（click A 一次）。
- 前置条件：两 A 之间必须全是 click，任何非 click 事件都会阻断压缩。

**不处理**：Layer 1 不处理跨 wait 的改动（例：click date-20 → wait network → click date-17），因为 wait 意味着用户看到了什么并作出反应——废操作判定需要 LLM 推理，属于 Layer 2。

#### 5.3 用户确认环节

`record_stop` 编译完成后调用 `SummarizeRecording` 生成：
- 录制描述（LLM 输出）
- 每步的操作描述 + 检查环节（根据 step 类型自动推导）
- 最后的确认提示

示例输出：
```
录制描述：深圳贝壳无效店治理报告下载
共 5 步。请确认以下操作步骤：

1. → 导航到 https://odin.ke.com/report/detail?report_id=118240
   检查：URL 包含「odin.ke.com」

2. → 点击「打开日期选择器」
   检查：点击后页面/元素处于预期状态

3. → 等待网络请求完成
   检查：数据加载完毕（网络空闲）

...

请确认以上步骤是否正确、检验环节是否充分，或告诉我哪里需要修改。
```

Agent 展示这段文本 → 用户确认或提修改 → agent 调用工具修改 YAML → 再次确认。

**检查环节推导逻辑**：

| Step 类型 | 检查内容 |
|-----------|---------|
| navigate | URL 包含目标 host（自带） |
| click | 目标元素可交互 / 页面状态变化 |
| type / select | 字段值等于录入值 |
| wait network | 网络空闲（数据加载完） |
| wait element | 元素可见 |
| download | 文件已保存到本地 |

## 文件改动

| 文件 | 改动 |
|------|------|
| `internal/browser/recorder.go` | `RecordedEvent` 字段扩展；`instrumentPageSession` 竞态修复；`watchDownloads` 下载检测；`captureScript` wait 注入 |
| `internal/browser/recording.go` | `compressEvents` + 工具函数；`SummarizeRecording` + `stepSummaryLine`；`CompileRecording` 新增 download/wait 编译分支 |
| `internal/browser/recorder_test.go` | 4 个新增测试：network wait、element wait、download detection、selector semantic |
| `internal/browser/recording_test.go` | 4 个新增测试：compress overwrite、compress A-B-A、compress side-effect、summarize |
| `internal/tools/browser.go` | `record_stop` 返回摘要文本 |

## 测试覆盖

- 全部 browser 包测试通过（含新增 8 个 + 已有全部）
- 关键测试用例：
  - `TestRecorderCapturesNewTab_ClickBeforeInstrument`：验证 stopLoading 修复竞态
  - `TestAutoWaitNetwork` / `TestAutoWaitElement`：验证 wait 自动注入
  - `TestSelectorSemanticAnchor`：验证选择器含 role + class
  - `TestAutoDownloadDetection`：验证 click 升级为 download
  - `TestCompressEventsOverwrite` / `TestCompressEventsABABacktrack` / `TestCompressEventsPreservesSideEffects`：验证 Layer 1 规则
  - `TestSummarizeRecording`：验证用户确认摘要

## 后续可考虑（本次未做）

1. **Layer 2 废操作推理**：LLM distill 时输出"疑似废操作列表 + 置信度（高/中/低）"，agent 根据置信度决定自动删或问用户
2. **check 环节自动生成 Verify**：每步按类型自动带 `Verify.Exists` / `Verify.URL`，回放时自动校验
3. **回放失败的检查项自愈**：回放失败时定位到具体检查项，healer 修复后重放
4. **A-B-A 回退压缩的误删风险**：当前假设"两次相同点击之间全是 click 就是试错"，但纯点击也可能有真实副作用（无网络请求、未命中显著元素规则的客户端状态切换），这类点击会被静默删除。目前无法区分"试错点击"和"有副作用但恰好不触发 wait 的点击"
5. **新 Tab reload 对一次性 URL 的影响**：`instrumentPageSession` 的竞态修复会对新开的 Tab 做一次 `Page.navigate` 重新加载，如果该 Tab 打开的是一次性 URL（如带 `code=` 参数的 OAuth 回调、带一次性 token 的支付跳转），reload 有可能使其失效或触发重复提交。目前仅排除了 `data:`/`blob:`/`chrome-error:` 前缀，未针对一次性 URL 做特殊处理
6. **下载归属的多点击误判**：`upgradeLastClickToDownload` 用"5 秒内最近一次 click"归属下载，如果用户在下载事件真正到达前又点了别的元素，下载会被错误地挂到后一次点击上
