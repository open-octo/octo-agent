# 制品交互：让 agent 看见（并递东西进）用户正在看的制品

## 背景

#2501 给轻应用做了状态镜像（`lightapp_state` / `view_lightapp` / `insert_into_lightapp`）：
app 经 postMessage 桥推出快照，Web UI 转发到 server 的进程内镜像，agent 用三个工具读。
链路本身是通的，但挂错了对象。轻应用版有四条治不好的病：

1. **场景薄**。旗舰场景是"手绘→生成→放回"的共创 loop，能力亮眼但日常频率低，
   更像能力演示而非工作流。
2. **鸡生蛋**。镜像要求 app 作者主动 `pushState`；不为它建的 app 不上报，
   用户问"看我的 app"时大概率什么都看不到。
3. **"必须开着才可见"的心智模型建不起来**。普通用户无法理解"要让 agent 看到，
   得把 app 开在特定位置并保持开着"（2026-09-21 用户原话：正常人无法理解）。
4. **语义错位**。轻应用是全局工具，却被钉进会话上下文的右侧面板，与制品、diff 并列。

交互需求真正长在**会话制品**上：制品本来就是会话上下文，面板是它名正言顺的家；
用户讨论制品时本来就正看着它（"你看我正在看的这页"是零教学成本）；
而且制品的作者是 agent 自己——skill 教一遍，新制品默认带 publish，没有鸡生蛋。

杀手场景是轻应用版从来没有的：**agent 写完一个 HTML 制品，自己回头看渲染结果自检**——
布局有没有崩、图表有没有渲出来、对比度对不对，不再依赖用户用嘴描述。

本文档定稿的改造：三个工具的语义从"按 slug 寻址的全局轻应用"改为
"按会话寻址的制品面板"，轻应用镜像整体退役。

## 目标

1. agent 能读**当前会话**里正打开着的制品：一句话 digest、结构化 summary、可选截图。
2. agent 能把一个文件递进打开着的制品（第一版只收图片，沿用投递票机制）。
3. artifact-design skill 教会 publish 模式，agent 新写的制品默认可交互。
4. 轻应用镜像退役，老轻应用里的 `publish()` 调用退化为 no-op，不报错。

## 非目标

- **不做"agent 主动打开制品"**。制品由对话流和用户自然打开（show_artifact、点击），
  不需要 open_lightapp 式的反向唤起——那个构想随轻应用镜像一起放弃。
- **不做镜像持久化**。沿用内存-only、frame 关闭即删（`lightapp_mirror.go` 的
  磁盘泄漏论证对制品截图同样成立）。
- 不改 artifact_gate 的安全模型：制品仍在自己的源、CSP 白名单不变。
- 不做制品内容的 DOM 级读取（截图 + app 自述足够；DOM 读取是另一个量级的攻击面）。

## 寻址与作用域

镜像键从轻应用的 `slug` 改为 **`(session_id, artifact_path)`**——制品的现有身份就是
这两个（`GET /api/sessions/{id}/artifacts?path=…`，白名单由 transcript 推导）。

三个工具的作用域锁死在**当前会话**：agent 只能看到自己会话的制品镜像，
从工具上下文取 session id，不接受指定别的会话。这消掉了轻应用版"哪个 app、
开没开"的寻址歧义——答案永远是"你这个会话的面板上正开着什么"。

## 协议：桥泛化，不新造

现有桥（`frame_bridge.js`）的 op 分两类：

- **通用交互**：`state`（推快照）、`delivery`（收投递）——制品要用
- **轻应用专属**：storage migrate、download 代理——制品不需要
  （制品没有 localStorage 契约；下载在制品里走常规浏览器路径）

泛化方式：注入配置从 `window.__octoLightApp = {slug}` 变为带种类的
`window.__octoBridge = {kind: "artifact", ns: "<session>\n<path>"}` 与
`{kind: "lightapp", ns: "<slug>", download}`，
桥 JS 按 kind 装配能力。对页面暴露的 API 不变：`window.octo.pushState(...)` /
`window.octo.onDelivery(...)`——artifact-design 文档里的示例代码和轻应用示例可以共用。

**server 注入点**：`serveArtifactOrigin` 交给 `serveArtifactEntry` 时把桥注入入口 HTML
（复用 `lightapp_origin.go` 的 `injectBeforeBody`）。注入前必须过现有的
sessionWrotePath 白名单——天然成立：能走到 serve 的 HTML 本来就是会话写出的。

**relay 端点**：新增 `PUT/DELETE /api/sessions/{id}/artifacts/state?path=…`，
**校验 path 在该会话的制品白名单内**——
镜像键里的 path 不是浏览器说了算，否则任意页面都能往别的会话的镜像里写。

**投递**：票绑定 `(session_id, path)` 而非 slug，WS 广播不变，Web UI 取字节后
postMessage 进对应 iframe。沿用一次性 + TTL。

## Web UI

- `ArtifactsPanel` 的制品 iframe 注册 relay（把现有轻应用 relay 逻辑泛化：
  按桥配置里的 kind 决定转发到哪个端点）。iframe 卸载时发 DELETE，沿用。
- **面板头部加可见性指示**：制品正在向 agent 上报时，面板标题栏显示一个小标记
  （👁，tooltip："agent 可以看到这个制品的内容"）——被动教学，成本几行，
  回答"agent 到底能不能看见"这个用户一定会问的问题。

## 三个工具（改名 + 重语义）

| 旧 | 新 | 语义 |
|---|---|---|
| `lightapp_state` | `artifact_state` | 当前会话哪些制品在推状态：digest、有无截图、多久前更新、是否 stale |
| `view_lightapp` | `view_artifact` | 把指定制品的截图作为 image block 放进对话 |
| `insert_into_lightapp` | `insert_into_artifact` | 递一个文件（第一版仅图片）进打开着的制品 |

三个工具照旧进 `allTools`、过权限闸门。没有镜像时工具照常注册，
返回"让用户在面板里打开那个制品"——沿用"模型看不见的工具等于不存在"的教训。

digest/summary 的归属原则沿用：那是**页面自称**，工具输出里引用而不转述。

## 轻应用镜像退役

- 删 `lightapp_state` / `view_lightapp` / `insert_into_lightapp` 三个工具；
  镜像实现重键为制品版（大部分代码搬运，键和作用域校验是新逻辑）。
- 删 `PUT/DELETE /api/light-apps/{slug}/state`、`GET /api/light-apps/{slug}/delivery/{id}`。
- 轻应用页面注入的桥配置改为 `kind: "lightapp"`，**不再装配 state/delivery**
  （保留 storage/download——那是轻应用的既有契约）。老 app 里的
  `if (window.octo?.pushState)` 守卫使 publish 退化为 no-op，不需要批量改 app。
- 配套删除：`mount: "panel"` 模式（另 PR，用户已决：轻应用打开一律整页
  `app:` 视图，面板只留制品/diff）。

## 配套

- **artifact-design skill**：新增"可交互制品"模式——何时推（变更防抖）、
  推什么（digest 是写给模型的一句话；canvas 制品用 `toBlob` 截图；有选区概念的上报选区）、
  如何用 `onDelivery` 收 agent 递回的图。文档里的示例制品本身演示完整链路。
- **base.md**：Light Apps 段的镜像 loop 描述删除；制品段落说明三工具与
  "写完自检一眼"的推荐动作。
- **product-help**：WEB.md 的制品描述补一句交互能力；无需新文件。

## 安全与隐私

- 截图沿用内存-only：大多数截图几秒内被下一次 push 替换，落盘规则不变
  （仅 view_artifact 真正交给模型时才落盘）。
- 作用域=当前会话 + relay 端点过制品白名单，双重保证一页只能说自己、
  agent 只能看自己的会话。
- stale/evict 阈值沿用（5 分钟报 stale，30 分钟驱逐），制品截图同样适用。

## 验收标准

1. agent 写一个带 publish 的画布制品 → 用户在面板打开 → `artifact_state`
   列出它 → `view_artifact` 把截图放进对话。
2. `insert_into_artifact` 投递图片 → 制品的 `onDelivery` 收到并展示 →
   制品再 push → agent 从 `artifact_state` 看到结果（投递回执沿用"推状态即回执"）。
3. 关闭制品 iframe → 镜像消失，`artifact_state` 不再列出。
4. 老轻应用（画板等）照常工作，`publish()` 静默 no-op。
5. 会话 A 的 agent 读不到会话 B 的制品镜像；白名单外的 path 写不进镜像。
