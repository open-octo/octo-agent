# Same-Origin Artifacts：制品与轻应用走同源路径

## 目标

- HTML 制品和轻应用在任何访问方式下都能用：本机浏览器、桌面端、ngrok / cloudflared 这类隧道、公网域名反代、IP 直连。
- 只有一条渲染路径。页面从 octo 自己的 origin 下的一个路径加载，不依赖 `*.localhost` 解析，不需要通配域名、证书或额外配置。
- 页面的 CSS 和 JS 不干扰 octo 界面：样式不串、全局变量不串，`localStorage` 不覆盖界面的设置、应用之间不互相覆盖。
- 页面是普通网页：可以调任意公网 API、加载任意外链，同目录的任意文件按相对路径可读。
- 用户可以把单个轻应用标成公开，不登录也能凭链接访问。

## 非目标

- **不防恶意页面。** 页面与 octo 界面同源，它的脚本能读到访问密钥、能调 `/api`，能改宿主 DOM。被 prompt injection 写出来的 HTML 打开后等于已登录的用户。这是有意的取舍：换来的是所有访问方式下都可用、页面能力不打折。
- 不包 IndexedDB、cookie、`sessionStorage`。它们与界面共享，但界面不依赖 `sessionStorage`，IndexedDB 按库名隔开，页面动它们属于上面的恶意范畴。
- Mobile 不提供制品和轻应用，维持现状。
- Markdown 制品预览不变，仍是宿主渲染后放进 srcdoc iframe。

## 路径

| 用途 | URL |
|---|---|
| 轻应用 | `/_apps/<slug>/` |
| 会话 HTML 制品 | `/_artifacts/<token>/` |

两个前缀都在 mux 上注册，与 `/api`、`/ws`、UI 静态文件并列。UI 用 hash 路由（`#/chat/...`），不占用这两个路径。

- `/_apps/<slug>` 与 `/_artifacts/<token>` 不带尾斜杠时 301 到带尾斜杠的形式，保证页面里的相对路径在前缀之下解析。
- `/` 和 `/index.html` 返回入口；其余路径返回根目录下的同名文件。轻应用的根目录是 `~/.octo/light-apps/<slug>/`，制品的根目录是入口 HTML 所在目录。
- 根目录下的任意普通文件都服务，`Content-Type` 按扩展名（`mime.TypeByExtension`），未知扩展名为 `application/octet-stream`。多个 `.html` 可以互相链接。
- 路径逐段解析，`..` 和指向根目录之外的符号链接返回 404。这是 URL 到目录的映射正确性，不是安全边界。
- 只接受 GET / HEAD。
- 响应头：`Cache-Control: no-store`（agent 重写文件后刷新要拿到新内容）、`X-Content-Type-Options: nosniff`。不发 CSP。
- 页面不能用以 `/` 开头的绝对路径引用自己的文件，那会落到前缀之外。提示词要求相对路径。

### 鉴权

两个前缀都经 `requireAuth`，与 `/api` 同一套规则：

- 本机客户端走回环豁免，桌面端、`localhost` 浏览器照常。
- 远程客户端靠访问密钥 cookie。iframe 与界面同源，页面本身和它的子资源请求都会带上 `octo_access_key`（`SameSite=Strict` 对同源请求照发），不需要 token。
- 公开轻应用例外，见下文。

### 制品 grant

`POST /api/sessions/{id}/artifacts/grant` 保留，路径校验不变（html 类型、绝对路径、本会话写过）。变化：

- 不再判断客户端是否本机，任何通过鉴权的客户端都拿到 grant。
- 返回 `{"url": "/_artifacts/<token>/", "expires_at": ...}`，相对 URL，天然落在当前 origin。
- token 仍然只是"这个 URL 对应哪份入口和哪个目录"的映射，24 小时滑动过期，服务重启失效。

### 轻应用

轻应用不需要 grant，URL 由 slug 直接得出。slug 校验沿用 `handleGetLightApp`。

## 注入脚本

服务端读入入口 HTML（上限 64 MB，只因为要整读做注入），在 `<head>` 开始标签之后插入一段 `<script>`；没有 `<head>` 时插在 doctype 之后，没有 doctype 时插在最前面。插在所有页面脚本之前，保证页面第一次访问 `localStorage` 时拿到的已经是包装后的对象。

脚本内容是嵌入二进制的 `internal/server/page_shim.js` 加一行配置 `window.__octoPage={"ns":"<命名空间>","download":<bool>}`（`json.Marshal` 转义 `<>&`）。脚本做两件事。

### localStorage 命名空间

页面与界面共用同一个 `localStorage`。不包的话，页面一句 `localStorage.clear()` 会清掉界面的面板宽度、主题、侧栏分组等设置，两个应用用了同名键会互相覆盖。

脚本用 `Object.defineProperty(window, 'localStorage', ...)` 把 `window.localStorage` 换成一个 Proxy，底层仍是真实的 `localStorage`：

- 键一律加前缀 `octo.page.<ns>:`。`getItem` / `setItem` / `removeItem` 和属性读写（`localStorage.foo = 'x'`）都经前缀。
- `clear()` 只删自己前缀下的键；`length` 和 `key(i)` 只数自己前缀下的键。
- 同步语义、持久性、配额都是浏览器原生的，刷新、重开、服务重启后数据仍在。

命名空间：

| 页面 | `ns` |
|---|---|
| 轻应用 | slug |
| 会话制品 | `sha256(sessionID + "\n" + entry)` 的前 16 个 hex |

制品的命名空间由会话和入口路径算出，不随 grant token 变化，agent 重写文件、服务重启后制品的存储都保留。

`localStorage` 的存储按 origin 划分，同一个应用在 `http://localhost:8088`、`http://127.0.0.1:8088`（桌面端）和 ngrok 域名下各有一份数据，互不相通。

### 下载桥（仅桌面端）

桌面 webview 没有 download delegate，`<a download>` 是静默空操作。`cfg.Native != nil` 时 `download` 为真，脚本沿用现有拦截方式（document 级 click 监听 + `HTMLAnchorElement.prototype.click` 补丁），把目标 `fetch` 成 Blob，经 `{__laBridge:1, op:'download', ns, name, blob}` 交给宿主，宿主 `deliverLaDownload`（`web/src/lib/laDownload.ts`）走 `/api/native/save-file`。浏览器里不注入这一半。

## iframe

```
<iframe src={url + '?theme=' + theme + '&v=' + rev}
        sandbox="allow-scripts allow-same-origin allow-forms allow-modals allow-downloads allow-pointer-lock"
        allow="fullscreen; clipboard-write">
```

- CSS 和 JS 全局变量的隔离来自 iframe 本身：它是独立的文档。
- `sandbox` 不给 `allow-popups` 和 `allow-top-navigation`，页面误操作不会把 octo 标签页跳走或弹窗。同源页面可以从父文档移除自己的 sandbox 属性，这属于非目标里的恶意范畴。
- `?theme=` 的契约不变（`artifact-design` SKILL 里"theme 从 URL 读"）。`v` 在 agent 重写同一路径时递增，强制重新加载。

## 公开轻应用

`manifest.json` 新增 `public` 字段（bool，缺省 false）。`/_apps/<slug>/` 上的请求在进 `requireAuth` 之前读 manifest：`public` 为真时直接服务，否则按常规鉴权。manifest 每次请求重新读取，关闭公开立即生效。

公开状态只由 UI 设置：

```
PUT /api/light-apps/{slug}/public      （requireAuth）
body: { "public": true }
200: 更新后的 manifest
```

handler 只改 `public` 字段后写回，其余字段原样保留。`internal/prompt/base.md` 不提这个字段。`GET /api/light-apps` 的每个条目增加 `public`。

UI 在轻应用卡片上提供"公开访问"开关，打开时确认一次，说明"任何拿到链接的人都能打开这个应用，包括它目录下的所有文件"；公开的卡片显示链接（`location.origin + '/_apps/<slug>/'`）和复制按钮。本机访问时这个链接是 `localhost`，只有经隧道或域名访问时它才对外有意义，开关旁边说明这一点。

## 存储迁移

轻应用的数据目前可能在两处，都在第一次以新路径打开该应用时迁进命名空间，只写新位置里还不存在的键，迁完记一个标记 `octo.page.migrated.<slug>`（界面自己的 `localStorage` 键，不经前缀）。

1. **`<slug>.apps.localhost` 的 `localStorage`**（v1.16.15 起的独立源）。只有本机客户端（`localAccess`）可能有这份数据。宿主插入一个隐藏 iframe 加载 `http://<slug>.apps.localhost:<port>/__octo_export`，这个页面把自己 origin 的 `localStorage` 整份 `postMessage` 给父窗口，宿主写入后移除 iframe。5 秒没有回音按无数据处理并照样打标记。
2. **宿主 IndexedDB `octo-la-storage`**（独立源之前的 srcdoc shim 时代）。同源之后宿主直接读 `{slug}:{key}` 行写进命名空间，不再经过 iframe。

为此 `*.apps.localhost` 保留一个极小的 handler：只服务 `/__octo_export`，要求 `isLocalPeer`，响应头带 `frame-ancestors http://localhost:* http://127.0.0.1:*`，其余路径 404。桌面端 `Info.plist` 的 `localhost` 子域 ATS 例外随之保留。两者在迁移窗口结束后一起删除。

会话制品不迁移：`<token>.artifacts.localhost` 的 token 随 grant 过期，本来就不保证存储持久。

## 删除的部分

服务端：

- `hostRouter` 的制品源与轻应用源分流、`artifactHostLabel`、`lightAppHostLabel` 的主体（只剩导出页）。
- `internal/server/artifact_gate.go`：CDN 白名单剥离、横幅、`artifactCSP`。
- `tools.ArtifactAssetContentType` 与资产扩展名表。
- grant 的 `isLocalRequest` 判断与 IPv6 字面量 409。
- `lightapp_bridge.js` 的迁移半边；下载半边并入 `page_shim.js`。

前端：

- `probeArtifactOrigin` 与 i18n 文案 `artifacts.local_only`、`lightapps.local_only`。`ArtifactFrame` 的"仅本机可用"分支改为 grant 请求失败时的"预览加载失败"（`originUnavailable`，文案 `artifacts.preview_unavailable`），代码视图不受影响。
- `lightappsAvailable` 与 `hostIsIPv6`；`mountedViews` 不再按可用性过滤。
- `laStorage.ts` 的迁移接收端，改为宿主直接迁移。

## 文档与提示词同步

- `internal/prompt/base.md` 轻应用约束：删掉"its own origin (`<slug>.apps.localhost`)"、CSP 围栏、CDN 白名单列表、"only page-asset types are served / a second `.html` is not"；保留并强调"同目录文件用相对路径，不要以 `/` 开头"；保留"不要调 octo 的 API"作为指导。
- `internal/skills/defaults/artifact-design/SKILL.md`：删掉 CDN 白名单与横幅相关段落，页面可以加载任意外链；`?theme=` 一条不动。
- `SECURITY.md`：删除"Agent-written HTML ... separate origin"一行，改为明确的已接受风险：制品和轻应用与界面同源，agent 写的 HTML 能以用户身份调用 API。
- `dev-docs/serve-auth-design.md`：威胁模型表同步。
- `dev-docs/artifact-origin-design.md`：删除，由本文档取代。
- `dev-docs/light-apps-design.md`、`dev-docs/web-artifacts-panel-design.md`、`dev-docs/web-plugins-design.md`：运行环境一节改为本文档描述的状态并链接本文档；`light-apps-design.md` 的 manifest 字段表加 `public`。
- 用户文档 `docs/src/content/docs/guides/light-apps.mdx` 与 `docs/src/content/docs/zh/guides/light-apps.mdx`：去掉"仅在本机可用"和 CDN 限制；加"远程访问"与"公开访问"两小节。

## 验收

- 本机浏览器、桌面端（macOS WKWebView、Windows WebView2、Linux WebKitGTK）、ngrok：轻应用和 HTML 制品在面板里渲染；`./app.js`、`./style.css`、`./model.glb` 按相对路径加载；页面 `fetch` 任意公网 API 成功。
- `Object.defineProperty(window, 'localStorage', ...)` 在四个引擎（Chrome、Safari / WKWebView、WebView2、WebKitGTK）里都生效，页面里的 `localStorage` 是包装后的对象。这是硬门，任一引擎不生效就要换做法再合。
- 页面执行 `localStorage.clear()` 后，界面的面板宽度、主题等设置仍在；两个应用写同名键互不影响；应用刷新、重开、服务重启后数据仍在。
- 制品的命名空间在 agent 重写文件、服务重启后不变。
- 远程客户端不带密钥 cookie 请求 `/_apps/<slug>/`、`/_artifacts/<token>/` → 401；带 cookie → 200。
- 公开：`public: false` 时远程无 cookie → 401；`PUT .../public` 打开后 → 200；关闭后立即 401。`PUT` 只改 `public`，其余字段保留。
- `..`、根目录外的符号链接 → 404；`/_apps/<slug>` 无尾斜杠 → 301。
- 迁移：在 `<slug>.apps.localhost` 里有数据的应用，以新路径打开后数据可读、已有的新键不被覆盖、标记写入后不再加载导出 iframe；IndexedDB 旧行同样迁入。
- 桌面端 `<a download>` 弹出系统保存对话框；浏览器里原生下载。
- 注入位置：带 doctype 的页面不因注入进入怪异模式（`document.compatMode === 'CSS1Compat'`）。

## 涉及文件

服务端：

- `internal/server/server.go`：`/_apps/`、`/_artifacts/` 路由，公开开关路由，`hostRouter` 收缩为导出页
- `internal/server/artifact_origin.go`：grant 去掉本机判断、返回相对 URL；制品页面 handler
- `internal/server/lightapp_origin.go`：轻应用页面 handler、公开判定、导出页
- `internal/server/page_shim.js`：新增，命名空间包装与桌面下载桥 <!--lint:new-->
- `internal/server/lightapp_bridge.js`、`internal/server/artifact_gate.go`：删除
- `internal/server/lightapps_handlers.go`：manifest `public`、`PUT .../public`
- `internal/tools/artifact.go`：删除资产扩展名表

前端：

- `web/src/components/ArtifactFrame.svelte`、`web/src/components/ArtifactsPanel.svelte`、`web/src/components/MountedApp.svelte`：新 URL、删除仅本机分支
- `web/src/lib/stores.ts`：`lightappURL` 改为 `/_apps/<slug>/`，删除 `lightappsAvailable`
- `web/src/lib/artifacts.ts`：删除探测与不可用状态
- `web/src/lib/laStorage.ts`：宿主侧直接迁移（导出 iframe + IndexedDB）
- `web/src/lib/api.ts`：公开开关请求
- `web/src/views/LightAppsView.svelte`：公开开关与链接

文档：见上一节。
