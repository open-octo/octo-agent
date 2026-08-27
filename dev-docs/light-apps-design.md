# Light Apps：对话生成、持久可用的前端小程序

## 背景

octo-agent 已经有一套完整的生成 + 展示循环：Agent 生成 HTML → `show_artifact` → Artifacts panel 渲染。但这个链路是**一次性**的——刷新页面，Artifact 就没了。用户下次想用同一个对账工具、会议模板、随机生成器，得重新描述一遍需求，Agent 重新生成一遍。

同时，Agent 的 token 成本决定了"事事问 Agent"不是最优解。很多场景是**固定流程**——输入确定、规则确定、输出确定——每次走一遍 LLM 又贵又慢。

本方案在现有能力上加一层**持久化**：把"用一次就丢的 Artifact"升级为"保存后随时打开的 Light App"，不引入新工具、不新增后端模块。

## 核心原则

> **不做新工具。** Agent 用已有的 `write_file` / `read_file` / `edit_file` 操作 Light App 文件，`show_artifact` 做预览。Web UI 从文件系统直接读目录列表渲染。全链路零新 tool。

> **够用就好。** 只做纯前端 HTML Light App。不做 Python 进程、不做 TCP 端口分配、不做后端沙箱。octo-agent 的 Artifacts panel（sandboxed iframe）已经是完美的运行容器。

## 设计概览

```
对话中生成 HTML → show_artifact 预览 ──→ 用户确认保存
                                          ↓
                              write_file 写入 ~/.octo/light-apps/<slug>/
                                          ↓
                              用户之后在「轻应用」面板点击打开
                                          ↓
                              Artifacts panel 渲染 index.html
```

生成阶段费 token，使用阶段零 token——和 Swiflow 的理念一致，但用 octo-agent 原生能力承载。

## 存储约定

```
~/.octo/light-apps/
├── csv-reconcile/
│   ├── manifest.json
│   └── index.html
└── meeting-minutes/
    ├── manifest.json
    └── index.html
```

### manifest.json

```json
{
  "slug": "csv-reconcile",
  "name": "CSV 对账工具",
  "description": "上传两份 CSV，按指定列对比差异结果",
  "icon": "📊",
  "created_at": "2026-07-26T10:00:00Z"
}
```

| 字段 | 必填 | 说明 |
|---|---|---|
| `slug` | ✅ | 目录名，字母/数字/连字符，唯一 |
| `name` | ✅ | 展示名称 |
| `description` | ✅ | 一句话描述，在列表卡片中展示 |
| `icon` | ✅ | 单个 emoji，在卡片和标题中展示 |
| `created_at` | ✅ | ISO-8601 时间，Agent 生成时填入 |

### index.html

自包含的 HTML 文件。硬约束（继承自 Artifacts panel）：

- **无外部资源**：不引 CDN、不链外部 CSS/JS/图片。如需图标用 emoji 或内联 SVG
- **CSS 内联**：`<style>` 或行内 style
- **JS 内联**：`<script>` 直接写在 HTML 内
- **文件处理**：`<input type="file">` + `FileReader` API，浏览器端搞定
- **无服务端依赖**：不能发 fetch 到外部 API（Artifacts panel sandboxed iframe 限制）
- **持久化存储可用**：直接用标准 `localStorage`，见下节

### 运行时存储

轻应用跑在 `sandbox="allow-scripts"` 的 srcdoc iframe 里（刻意不给
`allow-same-origin`），origin 是 opaque，浏览器会拒掉所有持久化存储 API —
localStorage / sessionStorage / Cookie / IndexedDB 在里面一律抛 SecurityError。

沙箱不动，改由宿主页面代管：`web/src/lib/laStorage.ts` 在宿主开一个 IndexedDB
（库 `octo-la-storage`、store `kv`，键 `{slug}:{key}` 按应用隔离），并往轻应用文档里注入
一段桥脚本，把 iframe 内的 `window.localStorage` 换成一个同步 shim —— 读走内存
缓存，写同步更新缓存再经 postMessage 落到宿主。文档加载时先 dump 预取该应用的
全部历史数据，`window.__laStorageReady` 在预取落地后 resolve。

对轻应用来说这是**零改动**的：继续写标准 `localStorage`，没有任何特殊接口。
localStorage 原生可用时（非沙箱上下文）桥脚本什么都不做。

与原生 localStorage 的差异，都是刻意的：

- 单键上限 1MB、单应用总量上限 5MB，超限同步抛 `QuotaExceededError`；键名上限
  512 字符，超限同样抛。宿主侧独立复核同一组上限，绕过 shim 也灌不进去。
- 落盘是异步的，崩溃或提前关闭可能丢最后一次写。同页内读写全同步一致，跨页重启
  的读一致性由启动预取保证。
- sessionStorage 仍然不可用 —— 只存活一次访问的状态放普通变量就够了。

## Agent 交互流程

### 生成 + 保存

```
用户：「我想做一个 CSV 对账工具」
    ↓
Agent: clarify ── 确认列结构、对账规则、输出格式
    ↓
Agent: write_file 写入临时位置，生成完整 index.html
    ↓
Agent: show_artifact ── 在 Artifacts panel 展示，用户预览交互
    ↓
Agent: （预判场景——对账是重复性任务）主动问：
      「效果满意吗？要不要存成轻应用，以后随时打开？」
    ↓
用户：「存，叫 csv-reconcile」
    ↓
Agent: write_file ~/.octo/light-apps/csv-reconcile/manifest.json
Agent: write_file ~/.octo/light-apps/csv-reconcile/index.html
    ↓
Agent: 「已保存！以后在轻应用面板随时打开。」
```

### 更新已有应用

```
用户：「之前那个 CSV 对账工具，加一个导出 Excel 的功能」
    ↓
Agent: read_file ~/.octo/light-apps/csv-reconcile/index.html
    ↓
Agent: edit_file 修改
    ↓
Agent: show_artifact ── 展示更新后的版本
    ↓
Agent: write_file 覆盖原 index.html
```

### 删除

```
用户：「删除 CSV 对账」
    ↓
Agent: rm -rf ~/.octo/light-apps/csv-reconcile/
```

（Agent 可以用 terminal 或确认后用 write_file 覆盖，推荐 terminal `rm -rf`）

## System Prompt 注入

在 Agent 的 system prompt 中新增以下知识段落：

```
## Light Apps

You can turn HTML artifacts into reusable "Light Apps" that users open anytime
without consuming LLM tokens. When you generate an HTML page for the user,
evaluate whether the task is REPEATABLE — if yes, proactively suggest saving.

### Storage
~/.octo/light-apps/<slug>/
  manifest.json  {"slug":"...","name":"...","description":"...","icon":"..."}
  index.html     self-contained HTML (no CDN, no external resources)

### When to suggest
  ✅ Repeated tasks: daily/weekly reports, data reconciliation, format
     conversion, template tools, worksheet generators, checklist tools
  ✅ Well-defined input → output with fixed rules
  ✅ Pure client-side HTML/CSS/JS
  ❌ One-off research or analysis
  ❌ Tasks needing LLM reasoning each time
  ❌ Backend-dependent workflows (use a skill or workflow instead)

### Workflow
1. Generate the HTML, preview with show_artifact
2. Ask the user: "保存为轻应用？"
3. On confirmation: write manifest.json + index.html per the storage spec above
4. Use existing write_file — no special tools needed

### Constraints
- index.html must be fully self-contained (sandboxed iframe environment)
- No CDN, no external images, no cross-origin fetch
- Inline all CSS and JS
- Use FileReader + <input type="file"> for file processing
- Use emoji or inline SVG for icons
- Follow artifact-design skill conventions for layout and colors
```

## 生成质量保障

Agent 生成 Light App HTML 时，已有 `artifact-design` 技能自动生效，确保：

- **窄屏适配**：Artifacts panel 默认宽度 ~720px，不用宽屏布局
- **配色规范**：走 artifact-design 的 `references/charts.md` 分类/顺序/发散色板
- **自包含**：artifact-design skill 已经要求无外链
- **中文友好**：系统默认字体覆盖 CJK

不需要为 Light App 单独写新的 skill——现有技能已经覆盖了 HTML 生成的全部约束。

## Web UI

### 导航入口

在「我的数据」栏目下，位于「助手记忆」和「文件回收站」之间，新增「轻应用」导航项。

### 交互原则：Agentic First UI

面板上的操作按钮不直接修改数据——而是**启动正确的对话**，让 Agent 来执行。这符合 octo-agent「agentic first UI」的设计理念：用户面对的不是工具面板，而是 Agent 的快捷入口。

- **新建** → 打开新会话，Agent 引导用户描述需求并生成 Light App
- **编辑** → 打开新会话，预加载当前 Light App 内容，Agent 根据用户要求修改
- **删除** → 仅需二次确认，不必走 Agent。如果用户要批量清理或条件删除，可以在对话里让 Agent 用 `rm` 处理

### 面板布局

```
┌─ 轻应用 ────────────────────────────────────────────┐
│                                      [+ 新建轻应用] │
│                                                     │
│  ┌──────────────┐  ┌──────────────┐  ┌───────────┐  │
│  │     📊       │  │     📝       │  │    🗺️     │  │
│  │  CSV 对账工具│  │  会议纪要模板│  │  旅行路线  │  │
│  │  上传两份    │  │  结构化记录  │  │  行程规划  │  │
│  │  CSV 对比   │  │  要点和待办  │  │  可视化    │  │
│  │             │  │             │  │           │  │
│  │ [打开] [编辑]│  │ [打开] [编辑]│  │ [打开] [编辑]│
│  │        [删除]│  │        [删除]│  │        [删除]│
│  └──────────────┘  └──────────────┘  └───────────┘  │
│                                                     │
│  ── 还没有轻应用 ──                                  │
│  点击「新建轻应用」让 Agent 帮你生成一个吧！          │
│                                                     │
└──────────────────────────────────────────────────────┘
```

### 卡片元素

每个卡片展示：
- **icon**（emoji，大号显示在卡片顶部）
- **name**（应用名称，粗体）
- **description**（一行灰色小字）
- **操作按钮**（三个，底部分布）

### 操作按钮行为

| 按钮 | 行为 |
|---|---|
| **打开** | 在 Artifacts panel 渲染 `index.html` |
| **编辑** | 打开新会话，自动填入上下文：「请帮我修改轻应用「{name}」，当前内容：{index.html 摘要}」 |
| **删除** | 弹出二次确认：「确定删除轻应用「{name}」吗？」，确认后删除对应目录 |

### 顶部新建按钮

「+ 新建轻应用」按钮打开新会话，自动填入引导性 prompt：「我想创建一个新的轻应用，用来……」。Agent 按已有流程 clarify → 生成 → 预览 → 保存。

### 数据来源

前端可以直接通过 octo-agent 的文件读取能力访问 `~/.octo/light-apps/`，或者通过一个轻量 API 端点：

```
GET /api/light-apps           →  {"apps": [{slug, name, description, icon, created_at}]}
GET /api/light-apps/{slug}    →  {"manifest": {...}, "html": "<index.html content>"}
```

推荐走 API 而不是让前端直接读文件系统——路径解析、安全边界更清晰。实现只是读目录 + 解析 manifest.json + 读 index.html，几十行 Go。

## 和现有功能的关系

| 现有能力 | 关系 | 说明 |
|---|---|---|
| **Skills** | 互补 | Skill 是给 Agent 看的指令模板；Light App 是给用户用的工具 |
| **Workflows** | 互补 | Workflow 编排 Agent 多步骤执行；Light App 不经过 Agent |
| **Artifacts** | 升级 | Artifacts 是临时展示；Light App 是持久化的 Artifact |
| **Cron tasks** | 无关 | Cron 定时触发 Agent；Light App 不适合做定时任务 |
| **show_artifact** | 复用 | 预览和打开都用同一个 panel，不需要新工具 |

## 不做的事

- **不做 Python/Node 运行时 Light App**。保持零外部依赖，只用浏览器沙箱。如果未来真有需求（如需要 pandas 处理大 CSV），再评估是否引入 `pyodide`（WASM Python）而不是起系统进程。
- **不做 Light App 间的数据共享**。每个 App 独立，不引入跨 App 的消息机制。
- **不做 Light App 的版本管理**。覆盖即更新，不保留历史版本。用户要回滚可以自己在对话里让 Agent 重新生成。
- **不做「从 Light App 回调 Agent」**。纯前端闭环。宿主与轻应用之间只有存储桥这一条 postMessage 通道，协议就四个操作（dump/set/remove/clear），不扩成通用 RPC。用户想用 AI 能力时回到对话。

## 实现分阶段

| 阶段 | 内容 | 预估 |
|---|---|---|
| **P0 - 核心** | System prompt 注入 Light Apps 知识 | 加一段 prompt，10 分钟 |
| **P0 - 核心** | Agent 生成 HTML 时主动建议保存 | prompt 驱动，不写代码 |
| **P1 - UI** | 前端「轻应用」列表页 | 一个 Vue 页面，~150 行 |
| **P1 - API** | `GET /api/light-apps` + `GET /api/light-apps/{slug}` | 两个 handler，~50 行 Go |
| **P1 - UI** | Artifacts panel 打开 Light App | 复用现有能力 |
| **P2 - 优化** | 列表页搜索、排序、空状态引导 | 锦上添花 |

P0 不写一行代码，配好 prompt 就能让 Agent 开始生成和保存 Light App。
P1 加前端入口和 API 端点，用户才能真正「打开」已保存的应用。
