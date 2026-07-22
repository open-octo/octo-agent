# 会话导出增强：多格式 + 消息选择

## 背景与目标

当前 Web 端会话导出功能非常薄弱：只支持 Markdown 一种格式、全量导出、跳过 tool/thinking 内容。用户无法选择对话片段导出为 PDF/PNG，也无法导出为自包含 HTML 文件。

**目标**：
1. 支持 Markdown、PDF、PNG、JSON、HTML 五种导出格式，全部为本地下载
2. 允许用户选择特定消息子集导出，而非只能全量
3. 交互方式为原地进入"导出模式"，顶部固定栏选格式，不弹框

## 范围

**范围内**：
- Web UI（`ChatView.svelte`）的导出交互重构
- Markdown / PDF / PNG / JSON / HTML 五种格式生成（全部本地下载）
- 消息多选（默认全选、单条勾选/取消）
- HTML 自包含文件生成（DOMPurify 过滤 + 样式内联）

**范围外**：
- 桌面端（nativeShell）导出路径改造（本次只改 Web 路径）
- IM / TUI 端导出功能

## 术语表

| 术语 | 含义 |
|------|------|
| 导出模式 | 用户点击导出按钮后进入的 UI 状态，消息气泡左侧出现 checkbox，顶部出现格式选择栏 |
| DOMPurify | 前端 HTML 净化库，用于防止导出内容中的 XSS |

## 业务流程

### 主流程（全部为下载类导出：MD / PDF / PNG / JSON / HTML）

```
用户在 Web UI 看到会话
  → 点击右上角"导出"按钮（ChatView.svelte:1299）
  → 原地进入"导出模式"：
      · 消息气泡左侧出现 checkbox（默认全选 user + assistant 消息）
      · 顶部固定栏出现格式图标行：MD | PDF | PNG | JSON | HTML
      · 顶部栏显示"已选择 N 条消息"计数
  → 用户调整勾选（取消某些消息 / 重新勾选）
  → 用户点击格式图标（如 PDF）
  → 前端根据勾选的消息子集生成对应格式文件
  → 触发浏览器下载（Blob + a.click()）
  → 退出导出模式
```

## 架构

本次改动**全部集中在前端**（`web/src/views/ChatView.svelte` + `web/src/lib/i18n.ts`）。后端无需改动——现有的 `GET /api/sessions/{id}/messages` 已返回完整 events 数组，前端拿到后只取勾选子集处理。所有格式均在浏览器本地生成并下载，不依赖任何外部服务。

```
┌──────────────────────────────────────────────────────┐
│                    前端 (浏览器)                        │
│  ┌──────────────────────────────────────────────┐    │
│  │ ChatView.svelte                               │    │
│  │  · exportMode 状态                            │    │
│  │  · selectedMessages Set                       │    │
│  │  · 生成 MD / PDF / PNG / JSON / HTML          │    │
│  │  · Blob + a.click() 本地下载                   │    │
│  └──────────────┬───────────────────────────────┘    │
│                 │                                       │
│     GET /api/sessions/:id/messages                    │
└─────────────────┼──────────────────────────────────────┘
                  │
          ┌───────▼──────┐
          │ octo-agent   │
          │ Go 后端      │
          │ (无改动)     │
          └──────────────┘
```

## 详细设计

### 前端状态管理

在 `ChatView.svelte` 新增两个响应式状态（现有 `msgs` 定义于 `ChatView.svelte:74`）：

```typescript
let exportMode = $derived.by(() => $exportModeStore)  // 是否处于导出模式
let selectedMsgIds = $derived($selectedMessagesStore) // 已勾选消息 ID 集
```

实际存储放在新的 `web/src/lib/exportStore.ts`：

```typescript
import { writable, derived } from 'svelte/store'

export const exportModeStore = writable(false)
export const selectedMessagesStore = writable<Set<string>>(new Set())
```

### 进入 / 退出导出模式

**进入**：用户点击 `ChatView.svelte:1299` 的导出按钮 → set exportModeStore = true，同时初始化 selectedMessagesStore 为**全部 user + assistant 消息的 ID**（tool 消息不进入选择范围）。

**退出**（三路径全部支持）：
1. 顶部栏"取消"按钮
2. ESC 键（keydown 监听）
3. 导出过程中按钮 loading 态（不可重复点击）

### 消息 Checkbox

渲染循环 `#each msgs as msg (msg.id)`（`ChatView.svelte:1372`）内，当 `exportMode === true` 且 `msg.type === 'user' || msg.type === 'assistant'` 时，在气泡**左侧垂直居中**出现 checkbox。

```svelte
{#each msgs as msg (msg.id)}
  <div class="msg-row" class:export-mode>
    {#if exportMode && (msg.type === 'user' || msg.type === 'assistant')}
      <label class="msg-checkbox">
        <input type="checkbox" checked={$selectedMsgIds.has(msg.id)}
               onchange={() => toggleSelect(msg.id)} />
      </label>
    {if}
    <!-- 原有的消息气泡渲染 -->
    {#if msg.type === 'user'}
      ...
```

Checkbox 状态存入 selectedMessagesStore，不干扰原有 chatMessages store。

### 顶部固定栏

进入 exportMode 后，在 `.header-actions` 下方、消息列表上方出现一个固定横栏（`position: sticky; top: 0; z-index: 10`）：

```
┌─────────────────────────────────────────────────────────┐
│ [📄 MD] [📕 PDF] [🖼 PNG] {📋 JSON} [🌐 HTML]  已选择 12 条  │
│                                                [取消] [导出]│
└─────────────────────────────────────────────────────────┘
```

MD / PDF / PNG / JSON / HTML 五个图标按钮**横排**，点击即触发导出（不需要再点确认）。

### 各格式生成逻辑

#### Markdown（已有，改动极小）

现有 `exportTranscript()` (`ChatView.svelte:1097-1167`) 重构为 `exportAsMarkdown(selectedEvents)`。区别：
- 输入从"全部 events"改为"勾选对应的 events 子集"
- 支持包含 tool 内容（由顶部栏开关控制）

#### PNG（`html2canvas`）

1. 克隆当前消息列表 DOM 节点
2. 对克隆节点应用 `html2canvas({ scale: 2 })`
3. 转为 `canvas.toBlob('image/png')`
4. 触发下载

**不做截断**，全量导出为一张长图。

#### PDF（`jspdf` + `html2canvas`）

1. `html2canvas` 截取消息 DOM → `canvas`
2. 用 `jspdf` 将 canvas 图片分页加入 PDF
3. 触发下载

#### JSON

将 events 数组直接 `JSON.stringify(events, null, 2)` 后 Blob 下载。JSON 格式下默认**包含** tool 消息（因为 JSON 常用于备份/分析）。

#### HTML（本地自包含文件下载）

**生成自包含 HTML**：
1. 构建 HTML 文档骨架：`<html><head><style>{inlineCSS}</style></head><body>...`
2. 遍历勾选的消息，渲染为消息气泡 HTML（重新连续编号）
3. 应用 DOMPurify 过滤（防止用户消息中的 `<script>` 被执行）
4. 底部加 header：会话标题、导出时间、`Exported from octo` watermark
5. 样式完全内联（`<style>` 块），不依赖任何外部资源

**下载**：生成的 HTML 字符串直接 `new Blob([html], { type: 'text/html' })` + `a.click()` 本地下载，与其余四种格式走同一条 Blob 下载路径。

### API 设计

本次改动**不需要新增任何前端或后端 API**。复用现有：
- `GET /api/sessions/{id}/messages`（`internal/server/handlers.go:444`）→ 获取完整 events

### HTML 文件模板

```html
<!DOCTYPE html>
<html lang="zh">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{sessionTitle} — Octo Export</title>
<style>
  /* 完全内联样式，复用 ChatView 的设计 token */
  :root { --bg: #1a1a2e; --bubble-user: #2d2d44; --text: #e0e0e0; ... }
  body { font-family: -apple-system, ...; background: var(--bg); color: var(--text); ... }
  .header { ... }  /* 会话标题 + 导出时间 + watermark */
  .msg { ... }     /* 消息气泡 */
  .msg-user { ... }
  .msg-assistant { ... }
  code { ... }     /* 代码块 */
  pre { ... }
</style>
</head>
<body>
<div class="header">
  <h1>{sessionTitle}</h1>
  <div class="meta">导出时间: {exportTime} · Exported from octo</div>
</div>
<div class="conversation">
  <!-- 勾选的消息按重新连续编号渲染 -->
  <div class="msg msg-user"><div class="bubble">{msg1Content}</div></div>
  <div class="msg msg-assistant"><div class="bubble">{msg2Content}</div></div>
  ...
</div>
</body>
</html>
```

### DOMPurify 过滤

用 `dompurify` 库（npm 包 `dompurify`，类型 `@types/dompurify`）在 HTML 生成后过滤：

```typescript
import DOMPurify from 'dompurify'

const cleanHTML = DOMPurify.sanitize(dirtyHTML, {
  ALLOWED_TAGS: ['div', 'span', 'p', 'pre', 'code', 'h1', 'h2', 'h3', 'ul', 'ol', 'li', 'a', 'img', 'strong', 'em', 'blockquote', 'details', 'summary'],
  ALLOWED_ATTR: ['class', 'style', 'href', 'target', 'rel', 'src', 'alt'],
})
```

## i18n 新增 Key

在 `web/src/lib/i18n.ts` 的 EN/ZH 两个对象中新增：

| Key | EN | ZH |
|-----|----|----|
| `chat.export` | Export | 导出 |
| `chat.export_cancel` | Cancel | 取消 |
| `chat.export_selected_count` | {n} selected | 已选择 {n} 条 |
| `chat.export_md` | Markdown | Markdown |
| `chat.export_pdf` | PDF | PDF |
| `chat.export_png` | Image (PNG) | 图片 (PNG) |
| `chat.export_json` | JSON | JSON |
| `chat.export_html` | HTML | HTML |
| `chat.export_include_tools` | Include tool calls | 包含工具调用 |

## 依赖新增

前端 package.json 新增：

```json
{
  "jspdf": "^2.5.2",
  "html2canvas": "^1.4.1",
  "dompurify": "^3.1.6"
},
"@types/dompurify": "^3.0.5"
```

## 测试计划

### 前端单元测试（Vitest）

1. **exportAsMarkdown**：验证勾选 3 条消息 + 跳过 tool 时输出正确的 Markdown 字符串
2. **exportAsJSON**：验证包含 tool 消息时 JSON 结构完整
3. **exportAsHTML**：验证 DOMPurify 正确剔除 `<script>` 标签
4. **toggleSelect**：验证勾选/取消勾选正确更新 Set

### 手动测试 Checklist

| 步骤 | 预期 |
|------|------|
| 点击导出按钮 | 消息气泡左侧出现 checkbox，顶部栏出现格式图标 |
| 取消勾选几条消息 | 顶部栏计数更新 |
| 按 MD 按钮 | 浏览器下载 .md 文件，只包含勾选的消息 |
| 按 JSON 按钮 | 下载的 JSON 包含 tool 消息 |
| 按 PDF 按钮 | 浏览器下载 .pdf，视觉可阅读 |
| 按 PNG 按钮 | 浏览器下载 .png，长会话为单张长图 |
| 按 HTML 按钮 | 浏览器下载 .html 文件，双击本地打开可见对话内容 + watermark |
| 点取消 / 按 ESC | 导出模式关闭，checkbox 消失 |

## 兼容性

| 维度 | 影响 | 处理 |
|------|------|------|
| i18n | 新增 key | 中 EN/ZH 双语完整补齐，旧 key 不受影响 |
| 桌面端 (nativeShell) | 现有 exportTranscript 走 `api.nativeSaveFile` | 本次只动 Web 路径的下载分支，nativeShell 分支暂时统一走浏览器下载（后续可补） |
| localStorage / sessionStorage | 无改动 | — |
| 消息类型 | tool 消息在选择列表外 | checkbox 只在 user/assistant 消息旁渲染 |
| 老浏览器 | html2canvas / jspdf 需 ES2020+ | 现有构建已 target ES2020+，无额外影响 |

## 安全

| 风险 | 处理 |
|------|------|
| XSS via 用户消息 | DOMPurify 过滤 HTML，只允许安全标签和属性 |

## 高可用性

| 场景 | 降级策略 |
|------|---------|
| DOMPurify 未加载 | 不过滤直接渲染（dompurify 是同步依赖） |
| html2canvas 不支持某一 CSS 属性 | 忽略该属性，截图继续 |

## 监控与告警

前端埋点（通过现有 `/api/telemetry` 或无侵入方式）：

| 事件 | 字段 |
|------|------|
| `export_started` | format, msg_count |
| `export_succeeded` | format, msg_count, duration_ms |
| `export_failed` | format, error_kind (`render_failed`) |

## 回滚

前端纯静态资源，回滚到上一个 build 即可。

## 工时估算

| 模块 | 工时 |
|------|------|
| exportMode 状态管理 + checkbox UI | 1d |
| 顶部固定栏 + i18n | 0.5d |
| MD / JSON 导出重构 | 0.5d |
| PDF / PNG 生成 | 1d |
| HTML 生成 + DOMPurify | 1d |
| 测试 | 1d |
| **总计** | **5d** |
