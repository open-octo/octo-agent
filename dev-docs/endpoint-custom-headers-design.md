# Endpoint 自定义请求头技术方案

## 版本历史

| 版本号 | 日期 | 修订人 | 描述 |
|---|---|---|---|
| 1.0 | 2026-08-17 | Roy Lei | 初稿 |

## 相关文档

- PRD：无（本次是用户直接提出的功能需求，未产出独立 PRD 文档）
- 关联设计：`dev-docs/endpoint-pr5-impl.md`——本次改动是在该文档定义的 Endpoint 二级分组 schema（`internal/config/config.go:94` `Endpoint` struct）上追加一个新字段，不改变其既有结构

## Background（背景）

octo-agent 的 `Endpoint`（`internal/config/config.go:94-122`）目前只能配置 `BaseURL`/`APIKey`/`Protocol` 三类连接参数。部分用户把 octo 接到自建反代网关（reverse proxy gateway）后面，网关除了标准的 `Authorization`/`x-api-key` 鉴权头之外，还要求请求携带额外的固定 HTTP 头（例如租户标识、追踪头）。当前两个 provider client（`internal/provider/anthropic/client.go`、`internal/provider/openai/client.go`）在发请求时只写死了 `Content-Type`/`User-Agent`/鉴权头，没有任何合并自定义头的逻辑，这类网关场景目前无法接入。

## Goals（本次迭代目标）

- 一个 Endpoint 下的所有模型可以共享一组自定义 HTTP 请求头，随每次请求发送给该 endpoint 的 `BaseURL`。
- 自定义头可以覆盖 anthropic/openai client 内置写死的头（`Content-Type`/`User-Agent`/`Authorization`/`x-api-key`/`anthropic-version`），让用户能对接要求非标准鉴权头的网关。
- 通过 Web UI（`EndpointsSection.svelte`）以 JSON 文本编辑自定义头，创建/更新即时生效，不需要重启 `octo serve`。

## Out of Scope

- **按模型（`EndpointModel`）粒度配置自定义头**：本次头信息只挂在 `Endpoint` 级别，与 `BaseURL`/`APIKey`/`Protocol` 同一粒度——反代网关的鉴权/追踪头通常按网关而非按模型区分，且目前没有任何字段是"按模型区分连接参数"的先例（`Vision` 是模型能力标记，不是连接参数），没有必要为此打破现有的"Endpoint=连接层，Model=能力层"分层。
- **头值的环境变量占位符解析**（如 `${MY_TOKEN}`）：只支持静态明文字符串。是否明文落盘的风险与 `APIKey` 字段本身一致（`APIKey` 也允许明文兜底），没必要为这一个新字段单独引入占位符解析机制。
- **HTTP header token 格式的强校验**（合法字符集、CRLF 注入检测等）：只校验 key 非空，格式问题交给 `net/http` 在发请求时报错——这是配置文件手改才会触发的边缘输入错误，不值得为此新增一套 token 校验逻辑。
- **`octo config` CLI 交互式向导录入**：自定义头只能通过手改 `~/.octo/config.yml` 或 Web UI 配置，向导流程不新增录入步骤——这是给新手配置标准 vendor 走的向导,自定义头是反代网关场景下的小众进阶能力,不必给绝大多数用户的向导流程增加一轮几乎总是跳过的追问。
- **敏感值脱敏/不回显**：不像 `APIKey` 那样只返回 `has_api_key` 布尔值,`headers` 在 Web UI 读取端点配置时正常完整回显——换取"打开编辑表单能看到当前完整配置、可以增量修改"的编辑体验,代价是 headers 里若填入敏感 token 会明文出现在前端网络请求/DOM 里(见"兼容性"章节的说明)。

## Naming Glossary

| 术语 | 含义 |
|---|---|
| Endpoint | 用户配置的一组共享 `BaseURL + APIKey + Protocol`（现加 `Headers`）连接参数的渠道，定义于 `internal/config/config.go:94` |
| ModelEntry | `Endpoint` + 其下某个 `EndpointModel` 展开成的扁平投影类型，供 sender 构建代码使用（`internal/config/config.go:41-60`），本次同步加 `Headers` |
| composite id | `"<endpoint_id>::<model>"` 形式的字符串，唯一引用某 endpoint 下的某个 model |
| sender | `agent.Sender` 接口的实现，包装底层 provider client（`anthropic.Client`/`openai.Client`），由 `internal/app/sender.go` 的 `NewSender`/`buildClient` 构造 |

## 影响范围

| 服务 / 模块 / 资源 | 改动类型 | 说明 |
|---|---|---|
| `internal/config/config.go` `Endpoint` struct | 新增字段 | 新增 `Headers map[string]string` |
| `internal/config/config.go` `ModelEntry` struct | 新增字段 | 新增 `Headers map[string]string`（投影目标） |
| `internal/config/config.go` `projectToModelEntry` / `EntryByModel` 内联投影 | 逻辑变更 | 两处投影代码都要把 `Headers` 从 `Endpoint` 带到 `ModelEntry` |
| `internal/config/config.go` `Config.Validate()` | 逻辑变更 | 新增对 header key 非空的校验 |
| `internal/app/sender.go` `SenderOptions` / `buildClient` | 新增字段 + 签名变更 | `buildClient` 新增第 5 个 positional 参数 `headers` |
| `internal/provider/anthropic/client.go` `Client` | 新增字段 + 逻辑变更 | 新增 `Headers` 字段，`Send` 内合并进请求 |
| `internal/provider/openai/client.go` `Client` | 新增字段 + 逻辑变更 | 同上 |
| `cmd/octo/chat.go` `buildSender` | 逻辑变更 | 透传 `entry.Headers` |
| `internal/app/vision.go` `NewVisionDescriber` | 逻辑变更 | 透传 `entry.Headers` |
| `internal/server/server.go` `resolveProviderAndModel` / `senderForEntry` | 逻辑变更 | 透传 `entry.Headers` |
| `internal/server/onboard_config_handlers.go` 端点 DTO + CRUD handler | 新增字段 + 逻辑变更 | GET/POST/PATCH 均暴露 `headers` |
| `web/src/lib/api.ts` 端点相关 TS interface | 新增字段 | `EndpointConfig`/`EndpointConfigInput`/`EndpointUpdateInput`/`EndpointMutationResult` |
| `web/src/components/settings/EndpointsSection.svelte` | 新增 UI 字段 | 新增 headers JSON textarea + 表单状态 + 提交逻辑 |

## 详细设计

按改动点分节（逻辑变更类）。

### 改动点 1：配置层 — `Endpoint.Headers` 字段与 `ModelEntry` 投影

`Endpoint` struct（`internal/config/config.go:94-122`）新增字段：

```go
type Endpoint struct {
    ID        string `yaml:"id"`
    Name      string `yaml:"name,omitempty"`
    Provider  string `yaml:"provider"`
    BaseURL   string `yaml:"base_url,omitempty"`
    APIKey    string `yaml:"api_key,omitempty"`
    Protocol  string `yaml:"protocol,omitempty"`
    LiteModel string `yaml:"lite_model,omitempty"`
    // Headers are extra HTTP headers sent with every request to this
    // endpoint's BaseURL. Applied after the client's built-in headers
    // (Content-Type/User-Agent/Authorization/x-api-key/anthropic-version), so
    // a key here overrides the built-in value — lets a reverse-proxy gateway
    // that requires a non-standard auth header take over from the client
    // default. Values are static plaintext; no env-var interpolation.
    Headers map[string]string `yaml:"headers,omitempty"`
    Models  []EndpointModel    `yaml:"models"`
}
```

`ModelEntry`（`internal/config/config.go:41-60`）是 `(Endpoint, EndpointModel)` 的扁平投影，sender 构建代码（`buildSender`、`senderForEntry` 等）都读这个类型而不是直接读 `Endpoint`，因此同步加字段：

```go
type ModelEntry struct {
    Provider string            `yaml:"provider,omitempty"`
    Model    string            `yaml:"model,omitempty"`
    Protocol string            `yaml:"protocol,omitempty"`
    BaseURL  string            `yaml:"base_url,omitempty"`
    APIKey   string            `yaml:"api_key,omitempty"`
    Headers  map[string]string `yaml:"headers,omitempty"`
    Vision   bool              `yaml:"vision"`
}
```

代码库里把 `Endpoint`+`EndpointModel` 投影成 `ModelEntry` 的地方**有且仅有两处**，必须同步改，漏掉任何一处都会让该路径下自定义头静默失效（不会报错，只是请求不带头）：

1. `projectToModelEntry`（`internal/config/config.go:524-533`）：

   ```go
   func projectToModelEntry(ep Endpoint, m EndpointModel) ModelEntry {
       return ModelEntry{
           Provider: ep.Provider,
           Model:    m.Model,
           BaseURL:  ep.BaseURL,
           APIKey:   ep.APIKey,
           Protocol: ep.Protocol,
           Headers:  ep.Headers, // NEW
           Vision:   m.Vision,
       }
   }
   ```

   被 `DefaultEntry()`（506-516 行）和 `EntryByModel` 的 bare-model 分支（766、772 行）调用。

2. `EntryByModel` 的 composite-id 分支内联投影（`internal/config/config.go:738-745`，未复用 `projectToModelEntry`，是历史遗留但语义等价的第二份实现）：

   ```go
   return ModelEntry{
       Provider: ep.Provider,
       Model:    m.Model,
       BaseURL:  ep.BaseURL,
       APIKey:   ep.APIKey,
       Protocol: ep.Protocol,
       Headers:  ep.Headers, // NEW
       Vision:   m.Vision,
   }, true
   ```

`Config.Validate()`（`internal/config/config.go:550-655`）在逐 endpoint 校验循环（607-632 行）里新增 header key 非空检查：

```go
for k := range ep.Headers {
    if strings.TrimSpace(k) == "" {
        problems = append(problems, fmt.Sprintf("endpoint %q has a header with an empty name", ep.ID))
    }
}
```

**设计决策**：只校验 key 非空，不做 HTTP header token 格式合法性校验（如是否含非法字符）——这类边缘输入错误交给 `net/http` 在真正发请求时兜底报错，不值得为此新增一套 token 校验逻辑。

**已知接受的边界情况**：Go map 的两个不同字符串 key 若只有大小写差异（例如 `"X-Test"` 和 `"x-test"`），`http.Header.Set` 会把两者规范化成同一个 canonical header 名，写入顺序由 Go map 的随机迭代顺序决定，最终生效值不确定。这是本次不做格式校验（见上）的直接后果，属于用户手工配置出双份大小写不同 key 才会触发的低概率误用场景，不额外处理。

### 改动点 2：Provider 层 — `SenderOptions` → `buildClient` → `Client.Headers` → HTTP 请求头合并

`SenderOptions`（`internal/app/sender.go:32-53`）新增字段：

```go
type SenderOptions struct {
    Provider string
    APIKey   string
    BaseURL  string
    Protocol string
    Headers  map[string]string // NEW
    CacheKey string
    ThinkingBudget int
    ReasoningEffort string
    ShowReasoning bool
}
```

`buildClient`（`internal/app/sender.go:117-169`）签名加第 5 个 positional 参数（不引入 Options struct — 现有风格本就是 positional，且没有证据表明近期还要继续加更多构造参数，为此重构属于超出本次任务范围的额外改动）：

```go
func buildClient(name, apiKey, baseURL, protocol string, headers map[string]string) (provider.Provider, error) {
    // ...（前段不变）
    switch proto {
    case "anthropic":
        client, err := anthropic.New(apiKey)
        if err != nil {
            return nil, err
        }
        if baseURL != "" {
            client.BaseURL = baseURL
        }
        client.Headers = headers // NEW
        return client, nil
    case "openai":
        client, err := openai.New(apiKey)
        if err != nil {
            return nil, err
        }
        if baseURL != "" {
            client.BaseURL = baseURL
        }
        client.Dialect = name
        client.Headers = headers // NEW
        return client, nil
    default:
        return nil, fmt.Errorf("unknown protocol %q for provider %q", proto, name)
    }
}
```

`NewSender`（`internal/app/sender.go:79-102`）第 80 行的调用改为透传：

```go
p, err := buildClient(opts.Provider, opts.APIKey, opts.BaseURL, opts.Protocol, opts.Headers)
```

两个 provider client 各加一个 `Headers` 字段：

`internal/provider/anthropic/client.go:64-74`：

```go
type Client struct {
    APIKey     string
    BaseURL    string
    APIVersion string
    HTTPClient *http.Client
    Retry      retry.Policy
    StreamIdleTimeout time.Duration
    Headers    map[string]string // NEW
}
```

发请求处（`internal/provider/anthropic/client.go:158-165`）在内置头之后、发送请求之前插入合并循环：

```go
httpReq.Header.Set("Content-Type", "application/json")
httpReq.Header.Set("User-Agent", version.UserAgent())
httpReq.Header.Set("x-api-key", c.APIKey)
apiVer := c.APIVersion
if apiVer == "" {
    apiVer = DefaultAPIVersion
}
httpReq.Header.Set("anthropic-version", apiVer)
for k, v := range c.Headers { // NEW — applied last, so it overrides any built-in header above
    httpReq.Header.Set(k, v)
}

resp, err := httpClient.Do(httpReq)
```

`internal/provider/openai/client.go:100-115` 的 `Client` struct 同样加 `Headers map[string]string`；发请求处（`internal/provider/openai/client.go:278-280`）同样插入：

```go
httpReq.Header.Set("Content-Type", "application/json")
httpReq.Header.Set("User-Agent", version.UserAgent())
httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
for k, v := range c.Headers { // NEW — applied last, so it overrides any built-in header above
    httpReq.Header.Set(k, v)
}

resp, err := httpClient.Do(httpReq)
```

**设计决策**：自定义头在内置头 `Set()` 之后写入，允许覆盖（包括 `Authorization`/`x-api-key`），因为反代网关有时要求接管标准鉴权头——`APIKey` 字段本身已经能覆盖标准鉴权场景，这里允许覆盖是为了兜住"网关要求非标准鉴权 scheme"这类特殊情况。

`NewSender` 有且仅有 4 个调用点把 `SenderOptions` 从 `config.ModelEntry` 组装出来，全部需要加 `Headers: entry.Headers`：

1. `cmd/octo/chat.go:1384-1410` `buildSender`（CLI/TUI/sub-agent 共用路径）
2. `internal/app/vision.go` `NewVisionDescriber`（vision-helper 路径，约第 69 行调用处）
3. `internal/server/server.go:1465-1483` `resolveProviderAndModel`（服务器默认 sender）
4. `internal/server/server.go:1667-1684` `senderForEntry`（每 config entry 的 sender）

漏掉任意一个调用点会导致该路径下已配置 headers 的 endpoint 请求不带自定义头，但不会报任何错误——这是本次改动必须全量覆盖、不可遗漏的点。

### 改动点 3：Server API 层 — DTO 与 CRUD handler

`endpointConfigJSON`（`internal/server/onboard_config_handlers.go:268-283`，GET 响应）与 `endpointJSONOut`（767-779 行，create/update 响应）均新增：

```go
Headers map[string]string `json:"headers,omitempty"`
```

`endpointToJSON`（781-796 行）新增 `Headers: ep.Headers,`。

`createEndpointRequest`（742-750 行）与 `updateEndpointRequest`（757-765 行）均新增：

```go
Headers map[string]string `json:"headers,omitempty"`
```

`handleCreateEndpoint`（799-847 行）在构造 `config.Endpoint` 字面量处新增 `Headers: req.Headers,`。

`handleUpdateEndpoint`（853-941 行）的字段 patch 逻辑（901-915 行那段"空字符串=不变"的模式）新增一条，紧跟在 `Protocol` 的 patch 之后：

```go
if req.Headers != nil {
    ep.Headers = req.Headers
}
```

**设计决策**：`BaseURL`/`APIKey`/`Protocol` 是字符串类型，Go 的零值（空字符串）无法区分"请求体里没传这个字段"和"用户想清空它"，所以历史上选择"空字符串=不变，要清空必须把之前的值重新传一遍"这个折中约定（901-908 行注释）。`Headers` 是 map 类型，Go 的 `encoding/json` 天然能区分：请求体完全不带 `headers` key → 反序列化后是 `nil`；请求体显式带 `"headers": {}` → 反序列化后是非 nil 的空 map。因此这里不需要复用字符串字段那种"打折"约定，`req.Headers != nil` 就能精确表达"用户提交了这次编辑"，`{}` 天然等价于"显式清空全部自定义头"。写入是整体替换（不是逐 key merge），与 Web UI 一次性编辑整段 JSON 的交互方式一致。

`handleUpdateEndpoint` 末尾已有的 `s.invalidateEndpointSenders(invalidID)`（`internal/server/server.go:1628` 定义，按 `"<endpointID>::"` 前缀清缓存，不区分具体哪个字段变了）无需任何修改就能覆盖 `Headers` 变更——下一轮 turn 的 `senderForEntry` 会用新 `Headers` 重建 client，无需重启 `octo serve`。

### 改动点 4：Web UI 层 — `EndpointsSection.svelte` 编辑表单

`web/src/lib/api.ts` 里四个端点相关 TS interface 均新增 `headers?: Record<string, string>`：`EndpointConfig`（904-917 行）、`EndpointConfigInput`（938-952 行）、`EndpointUpdateInput`（973-980 行）、`EndpointMutationResult`（954-964 行）。

`EndpointsSection.svelte` 表单状态（现有 `fBaseUrl`/`fApiKey` 声明于 33-34 行）新增：

```svelte
let fHeadersText = $state('')  // raw JSON text, parsed on submit
```

表单新增一个 JSON textarea 字段，紧跟在 APIKey 输入框（334-341 行）之后：

```svelte
<label class="field">
  <span class="field-label">{$t('models.headers')}</span>
  <textarea
    class="field-input mono json-area"
    rows={4}
    placeholder={'{"X-Tenant-Id": "abc"}'}
    bind:value={fHeadersText}
    disabled={busy}
  ></textarea>
</label>
```

`openCreate()`（186-197 行）新增 `fHeadersText = ''`；`openEdit(ep)`（199-209 行）新增 `fHeadersText = ep.headers && Object.keys(ep.headers).length > 0 ? JSON.stringify(ep.headers, null, 2) : ''` — headers 字段正常回显（不像 `fApiKey` 那样编辑时永远留空），打开编辑表单时会看到当前完整配置，可以直接增删修改。

`submitForm()`（228-269 行）新增解析与打包逻辑。创建路径（236-247 行内）：

```svelte
const headersText = fHeadersText.trim()
let headers: Record<string, string> | undefined
if (headersText) {
  try {
    headers = JSON.parse(headersText)
  } catch {
    showToast($t('models.headers_invalid_json'), 'error')
    busy = false
    return
  }
}
await api.createEndpoint({
  id,
  name: fName.trim() || undefined,
  provider: fProvider,
  base_url: fBaseUrl.trim() || undefined,
  api_key: fApiKey || undefined,
  protocol: isCustom ? fProtocol : undefined,
  headers,
  models: model ? [{ model, vision: fVision }] : [],
})
```

更新路径（248-266 行内）：

```svelte
const headersText = fHeadersText.trim()
let parsedHeaders: Record<string, string> | undefined
if (headersText) {
  try {
    parsedHeaders = JSON.parse(headersText)
  } catch {
    showToast($t('models.headers_invalid_json'), 'error')
    busy = false
    return
  }
}
const currentCanonical = JSON.stringify(editing.headers ?? {})
const newCanonical = JSON.stringify(parsedHeaders ?? {})
if (newCanonical !== currentCanonical) patch.headers = parsedHeaders ?? {}
```

JSON 解析失败时中断保存并提示，不落盘任何字段（模式与 `McpModal.svelte` 现有的"解析失败→设 `errorMsg`→中断"约定一致，但这是本次针对 headers 新写的独立实现，不复用 `McpModal.svelte` 的代码或组件——`McpModal.svelte` 的 JSON textarea 是整个 `mcpServers` 配置块的批量导入框，`headers` 只是用户粘贴内容里的一个可选字段，组件本身不感知也不单独处理它，与本次要做的"单个 endpoint 的 headers 编辑框"是两个独立实现）。

**设计决策**：headers 是 map 结构，用一个 JSON textarea 编辑整段内容，不新写 key-value 行编辑器组件——目标用户（配置反代网关自定义头）本就能接受 JSON 输入，仓库里也没有现成的 key-value 编辑器可复用，新写一个的工作量与收益不成比例。

## 配置变更

### 配置项

| 配置 key | 类型 | 默认值 | 生效方式 | 用途 |
|---|---|---|---|---|
| `endpoints[].headers` | `map[string]string` | 空（未配置） | 动态：`octo serve` 运行中通过 Web UI 更新后，下一轮 turn 即生效（依赖既有的 `invalidateEndpointSenders`）；CLI/TUI 场景下改配置文件需要重启对应命令的进程读取 | 该 endpoint 下所有模型请求时附带的额外 HTTP 头，可覆盖内置的 `Content-Type`/`User-Agent`/`Authorization`/`x-api-key`/`anthropic-version` |

**变更类型**：新增。

**配置依赖关系**：无联动，独立字段。

**回滚策略**：Web UI 场景下把 `headers` 清空（提交空 `{}`）秒级生效；手改配置文件删掉 `headers:` 块需要重启对应进程。

## 接口变更

### 1. `GET /api/config/endpoints` — 响应新增 `headers` 字段

**字段变更**

| 字段 | 类型 | 位置 | 变化 |
|---|---|---|---|
| `headers` | `Record<string, string>` | `endpoints[].headers` | 新增，`omitempty`（未配置时字段缺省） |

**响应示例（Before / After）**

Before：

```json
{
  "endpoints": [
    { "id": "my-relay", "provider": "custom", "base_url": "https://relay.example.com", "protocol": "anthropic", "has_api_key": true, "models": [] }
  ]
}
```

After：

```diff
 {
   "endpoints": [
-    { "id": "my-relay", "provider": "custom", "base_url": "https://relay.example.com", "protocol": "anthropic", "has_api_key": true, "models": [] }
+    { "id": "my-relay", "provider": "custom", "base_url": "https://relay.example.com", "protocol": "anthropic", "has_api_key": true, "headers": {"X-Tenant-Id": "abc"}, "models": [] }
   ]
 }
```

### 2. `POST /api/config/endpoints` — 请求体新增可选 `headers` 字段

**字段变更**

| 字段 | 类型 | 位置 | 变化 |
|---|---|---|---|
| `headers` | `Record<string, string>` | request body | 新增，可选；缺省等价于不设置自定义头 |

**内部逻辑变更**：`handleCreateEndpoint` 把 `req.Headers` 原样赋给新建的 `config.Endpoint.Headers`，无额外逻辑。

### 3. `PATCH /api/config/endpoints/{id}` — 请求体新增可选 `headers` 字段，语义为整体替换

**字段变更**

| 字段 | 类型 | 位置 | 变化 |
|---|---|---|---|
| `headers` | `Record<string, string>` | request body | 新增，可选；字段缺省（`nil`）＝不变，显式传（含 `{}`）＝整体替换当前 headers |

**内部逻辑变更**：见"改动点 3"，`req.Headers != nil` 时整体替换 `ep.Headers`，随后触发既有的 `invalidateEndpointSenders(invalidID)`。

## 外部依赖接口

无新增上游调用——本次改动只影响 octo-agent 自身与用户配置的 LLM 端点之间的请求头，不引入任何新的 HTTP/gRPC/MQ 上游依赖。

## Files Changed / Files NOT Changed

### Files Changed

| 仓库 | 文件 | 改动说明 |
|---|---|---|
| octo-agent | `internal/config/config.go` | `Endpoint`/`ModelEntry` 加 `Headers` 字段；`projectToModelEntry` 与 `EntryByModel` 内联投影都加 `Headers`；`Config.Validate()` 加 header key 非空校验 |
| octo-agent | `internal/app/sender.go` | `SenderOptions` 加 `Headers`；`buildClient` 签名加第 5 个参数并透传给两个 client |
| octo-agent | `internal/provider/anthropic/client.go` | `Client` 加 `Headers` 字段；`Send` 内合并进请求 |
| octo-agent | `internal/provider/openai/client.go` | `Client` 加 `Headers` 字段；`Send` 内合并进请求 |
| octo-agent | `cmd/octo/chat.go` | `buildSender` 组装 `SenderOptions` 时加 `Headers: entry.Headers` |
| octo-agent | `internal/app/vision.go` | `NewVisionDescriber` 组装 `SenderOptions` 时加 `Headers: entry.Headers` |
| octo-agent | `internal/server/server.go` | `resolveProviderAndModel`、`senderForEntry` 组装 `SenderOptions` 时加 `Headers: entry.Headers` |
| octo-agent | `internal/server/onboard_config_handlers.go` | 端点 DTO（`endpointConfigJSON`/`endpointJSONOut`/`createEndpointRequest`/`updateEndpointRequest`）加 `headers` 字段；`endpointToJSON`、`handleCreateEndpoint`、`handleUpdateEndpoint` 读写该字段 |
| octo-agent | `web/src/lib/api.ts` | `EndpointConfig`/`EndpointConfigInput`/`EndpointUpdateInput`/`EndpointMutationResult` 加 `headers?: Record<string, string>` |
| octo-agent | `web/src/components/settings/EndpointsSection.svelte` | 新增 `fHeadersText` 表单状态、headers JSON textarea、`openCreate`/`openEdit`/`submitForm` 对应逻辑 |
| octo-agent | `web/src/lib/i18n` 相关语言文件 | 新增 `models.headers`/`models.headers_invalid_json` 文案（en + zh） |

### Files NOT Changed（显式声明）

| 仓库 | 文件 | 不改原因 |
|---|---|---|
| octo-agent | `cmd/octo/config.go`（`runConfig` 交互式向导） | 面向新手配置标准 vendor 的向导流程不加录入步骤，自定义头只能手改 YAML 或走 Web UI |
| octo-agent | `internal/config/config.go` `EndpointModel` struct | Headers 挂在 Endpoint 级别，不做按模型区分 |
| octo-agent | `web/src/components/overlays/McpModal.svelte` | 该组件是 MCP `mcpServers` 配置块的批量 JSON 导入框，`headers` 只是其中一个可选字段，与本次 Endpoint 的 headers 编辑是完全独立的两套 UI，不共享代码 |
| octo-agent | `internal/mcp/config.go`、`internal/mcp/http.go` | MCP 自己的 `Headers` 字段（远程 HTTP MCP server 连接用）与本次 LLM Endpoint 的 headers 是两个不相关的功能，互不影响 |
| octo-agent | `web/src/components/settings/ModelConfigForm.svelte` | 真正对接 Endpoint CRUD 的组件是 `EndpointsSection.svelte`；`ModelConfigForm.svelte` 不在本次改动路径上 |

## Test Cases

| # | 场景 | 输入 | 期望输出 |
|---|---|---|---|
| 1 | anthropic client 发请求时带自定义头 | `Client{Headers: {"X-Tenant-Id": "abc"}}` 发一次请求，用 `httptest.Server` 捕获 | 捕获到的请求里 `X-Tenant-Id: abc` |
| 2 | openai client 发请求时带自定义头 | 同上，openai client | 同上 |
| 3 | 自定义头覆盖内置头 | `Client{Headers: {"Authorization": "Custom xyz"}}`（openai） | 捕获到的请求 `Authorization` 为 `Custom xyz`，不是 `Bearer <apiKey>` |
| 4 | 未配置 headers 的旧 config.yml 正常加载 | 一份没有 `headers:` key 的 `config.yml` | `Config.Load` 成功，`ep.Headers` 为 `nil`，`Config.Validate()` 无相关报错，发出的请求不带任何多余头 |
| 5 | `Config.Validate()` 拒绝空 header key | `Endpoint{Headers: {"": "x"}}` | `Validate()` 返回包含 "has a header with an empty name" 的 problem |
| 6 | composite-id 路径投影带上 Headers | 通过 `"<endpoint_id>::<model>"` 调 `EntryByModel` | 返回的 `ModelEntry.Headers` 与该 endpoint 配置一致 |
| 7 | bare-model 路径投影带上 Headers | 通过裸 model 字符串调 `EntryByModel`（走 `projectToModelEntry`） | 返回的 `ModelEntry.Headers` 与该 endpoint 配置一致 |
| 8 | vision-helper 路径复用 headers | 配置的 `vision_helper` 指向带 `Headers` 的 endpoint | `NewVisionDescriber` 构造出的 sender 发请求带上该 headers |
| 9 | Web UI 创建端点携带 headers | 表单填 `{"X-Test": "1"}` 并提交 | `POST /api/config/endpoints` 请求体含 `headers`；后续 `GET` 能读到一致的值 |
| 10 | Web UI 更新端点时 headers 留空文本框 | 编辑已有 headers 的端点，不改动 textarea 直接保存其它字段 | `PATCH` 请求体不含 `headers` key（`patch.headers` 未被设置），原 headers 保持不变 |
| 11 | Web UI 更新端点时显式清空 headers | 编辑已有 headers 的端点，把 textarea 清空为 `''` 后保存 | `PATCH` 请求体含 `"headers": {}`，服务端把该端点 headers 清空 |
| 12 | Web UI headers 文本框非法 JSON | 输入 `{invalid`）后点保存 | 前端阻断提交并 toast 报错，不发出网络请求 |
| 13 | 端点更新后立即生效 | 通过 `PATCH` 改某端点 headers，同一进程内紧接着发起一次该端点的 turn | 新 headers 生效，不需要重启 `octo serve`（依赖既有 `invalidateEndpointSenders`） |

## 兼容性

- **数据兼容**：老 `config.yml` 没有 `headers:` key 时，`Endpoint.Headers`/`ModelEntry.Headers` 均为 Go 零值 `nil`。`Config.Validate()` 里新增的校验循环对 `nil` map 直接跳过（`range nil` 是合法 no-op）；provider client 里 `for k, v := range c.Headers` 对 `nil` map 同样是 no-op，请求行为与今天完全一致，无需任何数据迁移或回填。
- **旧调用方**：`NewSender` 的 4 个调用点（`cmd/octo/chat.go`、`internal/app/vision.go`、`internal/server/server.go` 两处）在本次改动前都不知道 `Headers` 这个概念。这不是"新旧调用方共存期的兼容问题"，而是本次改动必须一次性全量覆盖的 4 个点——已在"改动点 2"和 Files Changed 表中逐一列出，任何遗漏都会导致该路径下已配置 headers 的 endpoint 静默不生效（不报错，只是请求不带头），是本设计明确要求在同一个 PR 内关闭的风险，不允许分批上线。
- **Global / CN**：不适用。octo-agent 是本地/自托管运行的单机 CLI 工具（`octo serve` 单进程），没有多区域部署或双站点数据隔离的概念。
- **灰度期共存**：不适用。`Headers` 是全新字段，`omitempty` 保证旧配置文件、旧请求体解析行为不变，没有新旧逻辑并行的过渡窗口——功能要么配置了（生效），要么没配置（等同于今天的行为），不存在中间态。
- **API 兼容**：`GET`/`POST`/`PATCH` 三个端点接口的 `headers` 字段均为新增可选字段（Go 侧 `omitempty`，TS 侧 `?:`）。未升级的旧前端请求体不带 `headers` key 时，Go 侧对应字段解析为 `nil`，`handleUpdateEndpoint` 的 `req.Headers != nil` 判断走"不变"分支——完全向后兼容，不影响任何现有 client。

### 发布顺序

单仓库改动（octo-agent 的 Go 后端与 Svelte 前端同仓库同 PR），无需跨仓库协调发布顺序。前端改动落地后需要按仓库既有纪律重新构建 `webdist`（`go:embed` 的静态资源目录，`.gitignore` 排除，PR 只提交 `.svelte` 源码，由 CI/发布流程负责重建），这是仓库既有的常规发布步骤，非本设计引入的新规则。

## 监控 & 告警

无新增。octo-agent 是本地/自托管运行的 CLI 工具，没有集中式 metrics/dashboard 基础设施；某个 endpoint 因自定义头配置错误导致请求失败，会体现为该 endpoint 下 turn 的现有错误提示（HTTP 状态码/provider 报错），用户据此自行排查，不需要为此新增专门的监控项。

## 灰度 & 回滚

### 灰度策略

纯新增能力，默认不生效（`Headers` 为空 map/`nil`），不影响任何已有 endpoint 的现有行为，可以直接随下一个版本发布，不需要 feature flag 或百分比灰度。

### 回滚方案

- **代码回滚**：直接 revert 相关 PR。已经在 `config.yml` 里写入 `headers:` 的用户，字段会保留在磁盘上（`Config.Save` 不会主动删除未知字段之外的数据），但回滚后的代码不再读取/发送这些头，效果等同于该功能被禁用，不会丢失用户此前的配置内容；下次重新升级到含本功能的版本会自动恢复生效。
- **数据回滚**：无数据库/MQ 变更，不适用。
