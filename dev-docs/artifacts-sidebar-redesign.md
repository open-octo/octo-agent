# Artifacts 面板改造：常驻右侧边栏 + Light Apps 入口

## 背景

当前 Artifacts 有两层结构：
- `ArtifactsPanel` — 在 `ChatView` 内部渲染的 420px 侧面板，仅会话内可用
- `ArtifactModal` — 全屏模态弹窗，用于放大查看

Light Apps（PR #1815）目前用 Blob URL + 新标签页打开。如果要把 Light Apps 放进 Artifacts panel 里无缝打开，需要把 panel 从 ChatView 内部**提升到全局布局**，支持两种内容模式。

## 目标

把 Artifacts panel 改造为 App 的常驻右栏：

```
┌──────────────────────────────────────────────────────────────────┐
│  Header                       [☰] [🔔] [📋 toggle]    [⚙]     │
├─────────┬──────────────────────────────────┬─────────────────────┤
│         │                                  │  Artifacts Panel    │
│ Sidebar │  Main content (chat/views)       │  ┌ Light Apps ────┐ │
│         │                                  │  │ 📊 CSV 对账    │ │
│         │                                  │  │ 📝 会议纪要    │ │
│         │                                  │  │ 🗺️ 旅行路线   │ │
│         │                                  │  └────────────────┘ │
│         │                                  │  ┌─────────────────┐│
│         │                                  │  │ 选中的 Light App││
│         │                                  │  │ 或 Session      ││
│         │                                  │  │ Artifact 内容    ││
│         │                                  │  └─────────────────┘│
└─────────┴──────────────────────────────────┴─────────────────────┘
```

### 两种模式

| 模式 | 触发条件 | 面板内容 | 顶部操作 |
|---|---|---|---|
| **session** | 当前在 ChatView，有 session artifacts | 现有 ArtifactsPanel：预览/代码切换 + chip 文件切换 | copy/download/maximize/close |
| **lightapps** | LightAppsView 点「打开」，或非聊天页面手动选择 | Light Apps 列表（chip 切换）+ 渲染选中 HTML | copy/download/close |

### 面板生命周期

- **面板打开/关闭**由 Header 的 toggle 按钮控制
- 在当前会话且 session 有 artifacts 时，toggle 默认显示 session 模式
- 不在会话中打开时，默认显示 lightapps 模式
- LightAppsView 的「打开」按钮**强制将面板切换到 lightapps 模式并定位到对应 app**
- 面板关闭只是隐藏（`panelOpen = false`），不丢失内容状态

## 改动清单

### 1. Store 层（`lib/stores.ts`）

新增：

```ts
// 什么内容驱动面板：null = 关闭, 'session' = assets, 'lightapps' = Light Apps
export const panelContent = writable<'session' | 'lightapps' | null>(null)
// 当前选中的 Light App slug
export const lightappSel = writable<string>('')
// 已加载的 Light Apps 列表（避免 ArtifactsPanel 重复请求）
export const lightapps = writable<LightApp[]>([])
// 已加载的 Light App HTML 缓存 { slug → html }
export const lightappHTML = writable<Record<string, string>>({})
```

保留（已有）：
- `artifacts` / `artifactSel` / `artifactView` / `artifactModalOpen` — 不变
- `artifactsOpen` — 废弃，由 `panelContent` 替代

### 2. `components/ArtifactsPanel.svelte` → 改造

支持双模式渲染：

```
{#if $panelContent === 'lightapps'}
  → 顶部 Light Apps chip 列表（从 $lightapps 读取）
  → 下方 iframe/$preview 渲染选中的 Light App HTML
{:else if $panelContent === 'session'}
  → 现有逻辑：session artifacts
{/if}
```

- 面板宽度保持 420px
- 面板从 `fixed` 绝对定位改为 flex 布局内的右栏

### 3. `components/ArtifactModal.svelte` → 不变

保留全屏放大能力（从 panel 的 maximize 按钮进入），其行为不变。

### 4. `App.svelte` 布局

把 `ArtifactsPanel` 从 ChatView 内部提升到 `.content` 层级：

```svelte
<div class="content">
  <Sidebar />
  <main class="main">...</main>
  {#if $panelContent}
    <ArtifactsPanel />
  {/if}
</div>
```

`.content` 的 CSS 从 `display: flex` 加 `min-width: 0`。

### 5. `views/ChatView.svelte` → 精简

- 移除 `import ArtifactsPanel`
- 移除 `{#if $artifactsOpen}<ArtifactsPanel />{/if}`
- toggle 按钮从 ChatView header 移到全局 Header（见下）

### 6. `components/layout/Header.svelte` → 新增 toggle

Header 右侧（settings 旁边）新增 Artifacts toggle 按钮：

```svelte
<button class="hdr-btn" title={$t('artifacts.toggle')} onclick={togglePanel}>
  <iconify-icon icon="ant-design:file-text-outlined" width="14"></iconify-icon>
</button>
```

`togglePanel()` 逻辑：
- 如果 `panelContent === null`：打开面板
  - 当前在 ChatView 且 session 有 artifacts → 设为 `'session'`
  - 否则 → 设为 `'lightapps'`
- 否则关闭：`panelContent.set(null)`

### 7. `views/LightAppsView.svelte` → 改「打开」

当前 `handleOpen` 用 Blob URL 新标签页：

```ts
const blob = new Blob([detail.html], { type: 'text/html;charset=utf-8' })
window.open(URL.createObjectURL(blob), '_blank')
```

改为：

```ts
// 加载 Light App 数据到全局 stores
lightapps.set(apps)  // 或异步加载
lightappHTML.update(m => ({ ...m, [slug]: detail.html }))
lightappSel.set(slug)
panelContent.set('lightapps')
```

### 8. i18n 新增

```ts
// EN
"artifacts.toggle": "Artifacts",
"artifacts.light_apps": "Light Apps",

// ZH
"artifacts.toggle": "预览面板",
"artifacts.light_apps": "轻应用",
```

### 9. `lib/artifacts.ts` → 微调

`observeArtifact` 首次写入时打开面板的逻辑：
- 从 `artifactsOpen.set(true)` 改为 `panelContent.set('session')`

### 10. `CommandPalette.svelte` → 补充入口

`cmdk` 搜索增加 `artifacts` 项：
```ts
{ id: 'artifacts', icon: 'ant-design:file-text-outlined', label: () => $t('artifacts.toggle'), shortcut: '', run: () => panelContent.set(panelContent.get() ? null : 'session') },
```

## 不涉及

- `ArtifactModal` 不改（全屏放大仍然从 panel 的 maximize 进入）
- 移动端（`web/src/mobile/ArtifactViewer.svelte`）不改——移动端布局不同，另案处理
- `FileRecallView` 不改
- Go 后端不改

## 改动量

| 文件 | 改动类型 | 量 |
|---|---|---|
| `lib/stores.ts` | 新增 4 个 store | ~20 行 |
| `components/ArtifactsPanel.svelte` | 双模式渲染 | ~50 行 |
| `App.svelte` | 布局重构 + import | ~15 行 |
| `views/ChatView.svelte` | 移除 ArtifactsPanel + toggle | ~-20 行 |
| `components/layout/Header.svelte` | 新增 toggle 按钮 | ~20 行 |
| `views/LightAppsView.svelte` | 改 handleOpen | ~10 行 |
| `lib/i18n.ts` | 新增 key | ~6 行 |
| `lib/artifacts.ts` | panelContent 替换 artifactsOpen | ~3 行 |
| `CommandPalette.svelte` | 新增入口 | ~3 行 |
| **总计** | | **~130 行净增** |

## 兼容性

- 回退兼容：`artifactsOpen` store 保留，`ChatView` 里的 toggle 按钮删除但引用该 store 的 `hdr-btn` 也一并移走，不影响其他地方
- 桌面端 / `octo serve` 无差异，布局一致
- 移动端不参与本次改造
