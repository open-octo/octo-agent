# Light App 状态镜像：让 agent 看见插件里的东西

一个挂进界面的 Light App（画板、看板、计算器）对 agent 是完全不透明的：它跑在自己的 origin 上，CSP 把它的出口封死，agent 既不知道它开着，也看不到里面有什么。用户画了张草图想问「这个布局行不行」，只能自己导出图片再上传。

本方案让 Light App **单向地把状态镜像出来**，再给模型两个工具去读。方向是 agent 拉，不是 app 推给对话。

## 目标

- 用户说「看看我画的这个」，模型自己就能取到画布内容，不需要用户先按按钮导出。
- 模型先花很小的代价知道「画布上有什么」，再决定要不要真的看图。
- **Light App 的安全模型一行不动**：它仍然只能单向 push，拿不到会话、API 或凭据。

## 非目标

- **不做反方向。** agent 不能操作 app 的内容（往画布上画、改看板）。那需要 app 订阅会话事件、权限声明、前后台状态处理，是另一个量级，而且现在没有自然用例。
- **不持久化镜像。** 它随 serve 进程消失，匹配「画布是会话草稿」的语义。
- **不做通用 RPC。** 协议只有一个方向、一个动词。

## 为什么必须多一跳

`dsh-image-gen` 的画布和宿主同源，浏览器直接 POST 到一条 route。octo 不行：

`internal/server/artifact_gate.go` 给每个 Light App 响应带 `default-src 'self' data: blob: <CDN 白名单>`，`connect-src` 继承它。Light App 里 `fetch('/api/…')` 既跨 origin 又被 CSP 拦——这是**有意的 egress boundary**（那份注释的原话），也正是 Light App 敢跑任意第三方代码的原因。

所以链路是：

```
Light App (iframe, <slug>.apps.localhost)
      │  postMessage（不是网络请求，CSP 管不着）
      ▼
Web UI 宿主页面 (127.0.0.1:8088)
      │  POST /api/light-apps/{slug}/state（同源，带 access-key cookie）
      ▼
server 内存镜像（per-slug）
      ▲
      │  读
agent 工具：lightapp_state / view_lightapp
```

多出来的这一跳不是负担，它是沙箱的兑现方式：**宿主是唯一持有凭据的一方，app 永远只是在喊话**。

## 协议：`__laBridge` 增加一个 op

现有两个 op（`migrate`、`download`）之外加 `state`，方向仍然只有 app → host：

```js
window.parent.postMessage({
  __laBridge: 1, id: 0, ns: '<slug>', op: 'state',
  summary: { … },        // 任意 JSON，app 自己定义形状
  digest: '画布上有 3 张图、2 段手绘，选中 1 项',   // 一行人话，给模型看
  image: blob,           // 可选：当前选区/视图的截图
}, '*')
```

- `digest` 是**给模型的一句话**。app 比谁都清楚自己里面是什么，让它自己描述，宿主不去猜。
- `summary` 是结构化附加信息，原样转交，宿主不解释。
- `image` 可选，且只在内存里待着——见下。

app 什么时候推由 app 决定（画布变更防抖、选区切换）。宿主不主动拉。

## 镜像

per-slug 的进程内存对象，不落盘：

| 字段 | 说明 |
|---|---|
| `digest` / `summary` | 最近一次 push 的内容 |
| `image` | 截图字节，**只在内存** |
| `signature` | 内容签名，换了内容就换签名 |
| `updatedAt` | 用于「N 秒前更新」和判定陈旧 |

三条从 `dsh-image-gen` 直接借来的经验：

1. **截图只在内存，工具真正消费时才落盘。** 大多数截图几秒内就被下一次 push 替换，或者随 app 关闭作废。octo 的上传目录是内容寻址的，急切落盘等于永久泄漏磁盘。
2. **签名要带身份，不能只看数量。** 选区从「3 个形状」换成「另外 3 个形状」时，数量和类型都没变——只比数量会让工具返回一张过期截图。
3. **没连上时工具照常注册。** 返回的答案告诉模型「让用户打开这个 app」，而不是工具消失或静默失败。一个模型看不见的工具等于不存在（见 `project_subagent_type_discovery`）。

镜像超过一定时间没更新就报 `stale`，让模型知道它看到的可能不是当下。

## 两个工具

**`lightapp_state`** — 便宜的那个。返回哪些 app 正在推状态、各自的 `digest`、有没有截图可看、多久没更新。模型用它判断要不要花代价看图。

**`view_lightapp`** — 贵的那个。把截图作为真正的 image block 放进对话（`agent.NewImageBlock`，`ToolResult.Blocks` 就是为这个存在的），模型于是能像看对话里任何图片一样看用户的手绘。没有截图时，答案是「让用户在 app 里选中要给我看的内容」。

两个工具都进 `allTools`，和其余内置工具一样过 permission gate。

## 画板示例要跟上

`docs/guides/light-apps.mdx` 里的画板示例加上 push：画布变更防抖后推一次 `digest` + `canvas.toBlob()`。这样文档里那个例子本身就演示了完整链路，而不是只演示一个孤立的画板。

`base.md` 也要让 agent 知道这两个工具的存在，以及「用户提到他画的东西时先 `lightapp_state`」。

## 验收标准

1. 画板里画几笔，`lightapp_state` 报出 sketch 正在连接、digest 描述了内容。
2. 选中一部分后 `view_lightapp`，模型能看到那张图并描述出画的是什么。
3. 关掉画板后 `lightapp_state` 报 disconnected/stale，且提示让用户打开，不报错。
4. 未挂载、从未 push 过的 Light App 不出现在 `lightapp_state` 里。
5. 截图在没有工具消费时不落盘（跑完一轮 push 后上传目录无新增）。
6. 远程浏览器下 app 根本不渲染（沿用 mount 的可用性判定），镜像自然为空——工具答「没有 app 在连」。
