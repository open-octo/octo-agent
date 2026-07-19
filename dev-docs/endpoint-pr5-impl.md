# Endpoint 二级分组 PR5 实施设计

> 本文是 `dev-docs/endpoint-design.md` 的 PR5 实施补充。原文档是 PR1-3 的设计快照（`Models` 权威 + `Endpoints` 内存派生），PR5 把权威反转到 `Endpoints` 并完成写路径切换。读本文前请先读原文档了解背景、术语、复合 id 机制。

## 1. PR5 范围

PR1-3 完成读路径（结构 + 读兼容 + flock + 改名级联 + sender 缓存）。PR4b 完成只读 UI（GET endpoints API + TUI 两级 picker + IM `/model` 分组 + Web 只读卡片）。PR5 是写路径切换 + CLI + 后端 CRUD + 文档：

- `Config.Save()` 从写扁平 `models:` 切到写 `endpoints:` 格式
- 删除 `Config.Models` / `Config.DefaultModel` / `Config.LiteModel` 三个字段（运行时）
- 新增 `UpsertEndpoint` / `UpsertModel` / `SetDefaultComposite` 三个写 API，替换 `SetDefaultEntry` / `SetEntry`
- 新增 `POST/PATCH/DELETE /api/config/endpoints` + `POST/DELETE /api/config/endpoints/{id}/models` + `POST /api/config/endpoints/{id}/default|lite` 共 7 个 CRUD 端点
- 删除老 `POST/PATCH/DELETE /api/config/models*` 5 个端点 + `handleGetConfig` 的 models 字段
- `octo config` 向导升级到 endpoint 概念（单 endpoint 引导，`legacy-<host>-<n>` id）
- `octo config show` 改两级单行输出
- `octo doctor` 改 per-endpoint 检查
- 删除 per-entry `ReasoningEffort` / `ShowReasoning`，只留全局 `Config.ReasoningEffort` / `Config.ShowReasoning`
- endpoint 改名级联接 `invalidateEndpointSenders` 调用点
- 修订 `dev-docs/endpoint-design.md`，commit 进仓库（PR1-3 漏 commit）

**不在 PR5 范围**：

- Web UI 的 endpoint CRUD（PR6 做）—— PR5 只让 Web 卡片节显示"编辑功能将在下个版本提供"提示（i18n key `settings.endpoints.readonly_notice` 已在 PR4b 加）
- IM `/model` 文案 i18n（IM 命令面目前统一英文，单独 PR 处理）
- 老用户回退到 PR4b 的兼容（单向升级，设计 §18.1 明文）

## 2. 数据模型变更

### 2.1 `Config` struct 删字段

`internal/config/config.go` 的 `Config` struct 删除三个字段：

```go
// 删除：
Models       []ModelEntry  // 运行时不再维护，只 fileConfig 中间态用
DefaultModel string
LiteModel    string
```

保留 `Endpoints []Endpoint` / `Default string` / `Lite string` 作为权威字段。`Config.ReasoningEffort` / `Config.ShowReasoning` 全局字段已存在（`config.go:55, 59`），不变。

### 2.2 `ModelEntry` 保留作为投影类型

`ModelEntry` struct 保留，但删除 `ReasoningEffort` / `ShowReasoning` 两个字段：

```go
type ModelEntry struct {
    Provider string
    Model    string
    BaseURL  string
    APIKey   string
    Protocol string
    Vision   bool
    // 删除：ReasoningEffort string
    // 删除：ShowReasoning   *bool
}
```

`EntryByModel` 返回值仍是 `ModelEntry`，内部从 `Endpoint` + `EndpointModel` 投影（Provider/BaseURL/APIKey/Protocol 从 Endpoint，Model/Vision 从 EndpointModel）。调用点（`cachedSenderForEntry` / `buildSender` / `senderForEntry`）签名不动，热路径零回归。

### 2.3 `EntryByModel` bare-model 分支改扫 Endpoints

`config.go:644` 的 bare-model fallback 当前扫 `c.Models`，PR5 后 `c.Models` 没了。改成扫 `c.Endpoints` 找第一个匹配的 model（语义同 `ParseModelFlag` step 2：default endpoint 优先 + 全扫兜底）：

```go
// Bare-model path: scan Endpoints for the first endpoint whose Models
// contains this model. Prefer the default endpoint when set, mirroring
// ParseModelFlag step 2a; else the first hit.
func (c Config) EntryByModel(model string) (ModelEntry, bool) {
    if model == "" { return ModelEntry{}, false }
    // 复合 id 路径不变（config.go:614-630）
    if endpointID, modelName, ok := splitCompositeID(model); ok { ... }
    // bare-model 路径：扫 Endpoints
    if c.Default != "" {
        if defEp, defM, ok := c.ResolveDefault(); ok && defM.Model == model {
            return projectToModelEntry(defEp, defM), true
        }
    }
    for _, ep := range c.Endpoints {
        for _, m := range ep.Models {
            if m.Model == model {
                return projectToModelEntry(ep, m), true
            }
        }
    }
    return ModelEntry{}, false
}
```

`projectToModelEntry(ep Endpoint, m EndpointModel) ModelEntry` 是新 helper，做投影。

### 2.4 `fileConfig` 保留 `Models` 字段做 Load 中间态

`fileConfig` struct（`config.go:830`）的 `Models []ModelEntry` 字段**保留**——用于读老 `models:` YAML 块。但 `ModelEntry` 删了 `ReasoningEffort`/`ShowReasoning`，所以老文件里的 `reasoning_effort` / `show_reasoning` 字段读不进来。

**直接丢弃老 reasoning 字段，不拷到全局**。`fileConfig` 不加额外的 legacy struct 读 reasoning——老文件里 per-entry 的 `reasoning_effort` / `show_reasoning` 在 Load 时静默丢弃。全局 `cfg.ReasoningEffort` / `cfg.ShowReasoning` 保持空（或用户在顶层设过的值）。代价：老用户的 reasoning 配置丢失，需要重新设全局 reasoning。dev-docs 和 release notes 说明这个行为变化。这简化 Load 实现（无 reasoning 迁移逻辑）。

```go
type fileConfig struct {
    Config `yaml:",inline"`
    // legacy 扁平 models 块（只在 Load 时用，不进运行时 Config）
    Models []ModelEntry `yaml:"models,omitempty"`
    DefaultModel string `yaml:"default_model,omitempty"`
    LiteModel    string `yaml:"lite_model,omitempty"`
    // legacy 顶层字段
    LegacyProvider string `yaml:"provider,omitempty"`
    LegacyModel    string `yaml:"model,omitempty"`
    // ...
}
// ModelEntry 已删 ReasoningEffort/ShowReasoning，老文件里的这两个字段
// 在 Unmarshal 时静默丢弃（yaml.Unmarshal 忽略 struct 里没有的字段）。
```

`normalize()` 时：
1. 老 `models:` 块 → 按 `(provider, base_url)` 聚合成 `c.Endpoints`（复用 PR1 `syncEndpointsFromModels` 逻辑，改成写 `c.Endpoints` 不写 `c.Models`）
2. 老 `default_model` / `lite_model` 映射到 `c.Default` / `c.Lite` 复合 id（复用 PR1 `findEndpointModel`）
3. reasoning 字段不迁移（丢弃）

## 3. 新写 API

替换 `SetDefaultEntry` / `SetEntry` 的三个新 API，在 `internal/config/config.go`：

### 3.1 `UpsertEndpoint`

```go
// UpsertEndpoint inserts or replaces the endpoint with the given ID. If an
// endpoint with ep.ID already exists, it's replaced in place; otherwise ep is
// appended. Default/Lite composite-id references are NOT rewritten (use
// RenameEndpoint for id changes). Returns the index of the upserted endpoint.
func (c *Config) UpsertEndpoint(ep Endpoint) int
```

调用点：`POST /api/config/endpoints`（新建）、`PATCH /api/config/endpoints/{id}`（改 endpoint 连接参数，不含改名）。

### 3.2 `UpsertModel`

```go
// UpsertModel inserts or replaces the model under the given endpoint. If
// endpointID doesn't exist, returns ErrEndpointNotFound. If a model with the
// same Model id already exists under the endpoint, it's replaced; otherwise
// appended. Default/Lite references are updated if they pointed at the old
// model shape (same model id under this endpoint).
func (c *Config) UpsertModel(endpointID string, m EndpointModel) error
```

调用点：`POST /api/config/endpoints/{id}/models`。

### 3.3 `SetDefaultComposite`

```go
// SetDefaultComposite sets cfg.Default to the given composite id. Does NOT
// validate the id resolves — callers should validate first (Validate() catches
// dangling Default). Does NOT invalidate sender cache (default switch doesn't
// rebuild senders, only changes which sender ResolveDefault returns next time).
func (c *Config) SetDefaultComposite(cid string)
```

调用点：`POST /api/config/endpoints/{id}/default`、`octo config` 向导最后一步。

### 3.4 老API 删除

`SetDefaultEntry` / `SetEntry` / `DefaultEntry` 全删。调用点迁移：

| 老调用点 | 新调用 |
|---|---|
| `cmd/octo/config.go:598` `full.SetDefaultEntry(outEntry)` | `full.UpsertEndpoint(ep) + full.UpsertModel(ep.ID, m) + full.SetDefaultComposite(cid)` |
| `internal/server/handlers.go:1270, 1421` `cfg.SetEntry + SetDefaultEntry` | 改走新 CRUD handler（见 §4） |
| `internal/server/onboard_config_handlers.go` 5 个老 CRUD handler | 全删，路由也删（见 §4） |

## 4. API 设计

### 4.1 新增 CRUD 端点

`internal/server/onboard_config_handlers.go` 新增 7 个 handler，路由注册在 `internal/server/server.go:830` 附近：

```
POST   /api/config/endpoints                       handleCreateEndpoint
PATCH  /api/config/endpoints/{id}                  handleUpdateEndpoint  (含改名 → 触发级联)
DELETE /api/config/endpoints/{id}                  handleDeleteEndpoint
POST   /api/config/endpoints/{id}/models           handleAddEndpointModel
DELETE /api/config/endpoints/{id}/models/{model}   handleDeleteEndpointModel
POST   /api/config/endpoints/{id}/default          handleSetEndpointDefault
POST   /api/config/endpoints/{id}/lite             handleSetEndpointLite
```

### 4.2 请求/响应 shape

**POST /api/config/endpoints**：
```json
{
  "id": "relay-a",
  "name": "中转站A",
  "provider": "custom",
  "base_url": "https://relay-a.example.com",
  "api_key": "sk-relay-a",
  "protocol": "anthropic",
  "lite_model": "",
  "models": [{"model": "claude-sonnet-4-6", "vision": true}]
}
```
响应：`201 Created` + 完整 endpoint 对象（同 GET shape，含 `has_api_key`）。

**PATCH /api/config/endpoints/{id}**（不含改名）：
```json
{
  "name": "新名称",
  "base_url": "https://new.example.com",
  "api_key": "sk-new",
  "protocol": "openai"
}
```
`id` 字段不在 body 里（URL 里的 `{id}` 是当前 id）。响应：`200 OK` + 更新后的 endpoint。

**PATCH /api/config/endpoints/{id}**（含改名）：
```json
{
  "new_id": "relay-b",
  "name": "..."
}
```
`new_id` 字段存在时触发 `RenameEndpoint`（级联重写 `cfg.Default` / `cfg.Lite` 前缀）+ `invalidateEndpointSenders(oldID)`。响应：`200 OK` + 更新后的 endpoint（新 id）。

**DELETE /api/config/endpoints/{id}**：删 endpoint。若 `cfg.Default` / `cfg.Lite` 指向该 endpoint，清空（走 Repair 兜底重置）。响应：`200 OK` + `{"ok": true}`。

**POST /api/config/endpoints/{id}/models**：
```json
{"model": "gpt-5.4", "vision": true}
```
响应：`200 OK` + 更新后的 endpoint。

**DELETE /api/config/endpoints/{id}/models/{model}**：删 model。若 `cfg.Default` / `cfg.Lite` 指向该 model，走 Repair 兜底。响应：`200 OK` + `{"ok": true}`。

**POST /api/config/endpoints/{id}/default**：body 空。设 `cfg.Default = <id>::<该 endpoint 下第一个 model>`。响应：`200 OK` + `{"default": "<cid>"}`。

**POST /api/config/endpoints/{id}/lite**：body 空。设 `cfg.Lite = <id>::<该 endpoint 下第一个 model>`。响应：`200 OK` + `{"lite": "<cid>"}`。

### 4.3 老 `/api/config/models*` 端点全删

删除 5 个 handler + 路由：

- `POST /api/config/models` (`handleSaveModelConfig`)
- `PATCH /api/config/models/{id}` (`handleUpdateModelConfig`)
- `DELETE /api/config/models/{id}` (`handleDeleteModelConfig`)
- `POST /api/config/models/{id}/default` (`handleSetDefaultModelConfig`)
- `POST /api/config/models/{id}/lite` (`handleSetLiteModelConfig`)

### 4.4 `handleGetConfig` 去掉 models 字段

`internal/server/onboard_config_handlers.go:195` `handleGetConfig` 保留（仍被 Web 用来读全局设置），但 `configResponse` struct 删除 `Models` / `DefaultModelIdx` 字段：

```go
type configResponse struct {
    // 删除：Models []modelConfig
    // 删除：DefaultModelIdx int
    FontSize      string  `json:"font_size,omitempty"`
    Language      string  `json:"language,omitempty"`
    ShowReasoning *bool   `json:"show_reasoning,omitempty"`
    Coauthor      *bool   `json:"coauthor,omitempty"`
    WorkspaceDir  string  `json:"workspace_dir,omitempty"`
    ReasoningEffort string `json:"reasoning_effort,omitempty"`  // 新增：全局 reasoning
}
```

前端 `web/src/views/SettingsView.svelte` 读 `cfg.models` 的地方改读 `api.getEndpoints()`（PR4b 已有）。

## 5. endpoint 改名级联

### 5.1 不扫 session 文件

设计文档 §6.1 原说"扫 `~/.octo/channels.yml` 所有 channel 绑定"——**这是技术事实错误**。channel 绑定实际存在 `~/.octo/sessions/*.jsonl` 的 `ModelConfig` 字段（`internal/agent/session.go:41`），不在 `channels.yml`。`channels.yml` 只存 platform credentials（`internal/channel/config.go:20`）。

PR5 的改名级联**只重写 `config.yml`**，不扫 session 文件：

1. `config.Mutate(func(cfg){ cfg.RenameEndpoint(oldID, newID) })` —— PR3 已实现的 `RenameEndpoint`（`config.go:720`）改 `c.Default` / `c.Lite` 复合 id 前缀 + endpoint ID
2. 成功后调 `s.invalidateEndpointSenders(oldID)` —— PR3 已实现（`server.go:1519`），删旧 endpoint 前缀的 sender 缓存
3. **不扫 session 文件**——老 session 里的 stale 复合 id 靠 `EntryByModel` 的 bare-model fallback 兜底（§2.3，PR5 后 fallback 扫 Endpoints 找同名 model）

这符合设计 §8.2 的"session 不写回"原则，且简化实现（无需 `channel.RenameEndpointBindings`）。

### 5.2 跨文件原子性

单文件（`config.yml`），`config.Mutate` 的 flock 已经保证原子性。无跨文件场景。

## 6. `invalidateEndpointSenders` 触发时机

PR3 的 `invalidateEndpointSenders(endpointID)`（`server.go:1519`）接调用点：

| CRUD handler | 触发失效？ | 理由 |
|---|---|---|
| `POST /api/config/endpoints`（新建） | 否 | 新 endpoint 无 sender 缓存 |
| `PATCH /api/config/endpoints/{id}`（改连接参数） | 是 | base_url/api_key/protocol 变，旧 sender 失效 |
| `PATCH /api/config/endpoints/{id}`（改名） | 是 | `invalidateEndpointSenders(oldID)` |
| `PATCH /api/config/endpoints/{id}`（只改 Name） | 是 | 粗粒度策略：宁可多失效，避免 diff 漏字段 |
| `DELETE /api/config/endpoints/{id}` | 是 | 整个 endpoint 的 sender 失效 |
| `POST /api/config/endpoints/{id}/models` | 是 | 粗粒度：model 变也失效（cache key 是复合 id，model 变有孤儿 key） |
| `DELETE /api/config/endpoints/{id}/models/{model}` | 是 | 同上 |
| `POST /api/config/endpoints/{id}/default` | 否 | 只改 `cfg.Default` 指针，不重建 sender |
| `POST /api/config/endpoints/{id}/lite` | 否 | 同上 |

策略：粗粒度 per-endpoint 失效。简单且 sender 重建成本低（毫秒级），endpoint mutation 是低频操作。

## 7. CLI 设计

### 7.1 `octo config`（向导）

`cmd/octo/config.go:299` `runConfigWizard` 改造。单 endpoint 引导流程：

```
1. 选 vendor（anthropic/openai/custom/...）from app.Registry
2. 填 base_url（custom 必填，命名 vendor 可空用默认）
3. 选/填 model（从 vendor.Models 选或手填）
4. 填 api_key
5. 算 endpoint id = legacyEndpointID(hostFromBaseURL(base_url), 0)
   - 若该 id 已存在 → UpsertEndpoint 覆盖（同 host 同 id，语义"更新这个渠道的配置"）
   - 若不存在 → UpsertEndpoint 新建，打印"新建 endpoint X"
6. UpsertModel(endpointID, EndpointModel{Model: model, Vision: vendorVision})
7. 问"设为 default 吗？[Y/n]" → SetDefaultComposite(endpointID::model)
8. Save（写 endpoints 格式）
```

`legacyEndpointID` + `hostFromBaseURL` 复用 PR1 实现（`config.go:1003, 1015`）。endpoint id 生成规则与 Load 老 `models:` 文件生成的 id 一致，用户在 web 设置里看到的是同一套命名。

### 7.2 `octo config show`

`cmd/octo/config.go:184` `runConfigShow` 改两级单行输出：

```
Config file: /Users/qiao/.octo/config.yml

endpoints:
  relay-a (中转站A, custom, https://relay-a.example.com) — 2 models [default: relay-a::claude-sonnet-4-6]
  official (官方 Anthropic, anthropic, https://api.anthropic.com) — 3 models [lite: official::claude-haiku-4-5]
  legacy-api-deepseek-com-0 (deepseek, deepseek, https://api.deepseek.com) — 2 models

references:
  default = relay-a::claude-sonnet-4-6
  lite = official::claude-haiku-4-5

reasoning:
  effort = medium
  show_reasoning = true
```

每 endpoint 一行：`id (name, provider, base_url) — N models [default/lite 标记]`。无 model 列表（doctor 才有）。`references` 节显示 `cfg.Default` / `cfg.Lite`。`reasoning` 节显示全局设置。

### 7.3 `octo doctor`

`cmd/octo/doctor.go:17` `runDoctor` 改 per-endpoint 检查，照设计 §13.2 样例：

```
octo doctor — checking your setup

config: /Users/qiao/.octo/config.yml
  ✓ config.yml parses
  ✓ 3 endpoints configured

Endpoints:
  ✓ relay-a (中转站A, custom, https://relay-a.example.com)
      2 models: claude-sonnet-4-6, gpt-5.4
      ✓ API key found (endpoint.api_key set)
  ✗ official (官方 Anthropic, anthropic, https://api.anthropic.com)
      3 models: claude-opus-4-8, claude-sonnet-4-6, claude-haiku-4-5
      ✗ API key missing (ANTHROPIC_API_KEY not set, endpoint.api_key empty)
  ✓ legacy-api-deepseek-com-0 (deepseek, deepseek, https://api.deepseek.com)
      2 models: deepseek-v4-flash, deepseek-v4-pro
      ✓ API key found (DEEPSEEK_API_KEY)

References:
  ✓ default = relay-a::claude-sonnet-4-6 (resolves)
  ✓ lite = official::claude-haiku-4-5 (resolves, ≠ default)

1 problem(s) found — `octo config` can add an API key.
```

每个 endpoint 检查：id 合法、有 model、api_key 可达（env 或 `endpoint.APIKey`）。default/lite 引用独立检查可解析 + lite≠default 约束。key 探测沿用现有 `apiKeyReachable` / `apiKeyStatus`（`doctor.go:64-67`）。

## 8. Save 升级策略

`Config.Save()`（`config.go:1407`）当前 marshal 时清空 `Endpoints`/`Default`/`Lite` 只写 `Models`/`DefaultModel`/`LiteModel`。PR5 反转：

```go
func (c Config) saveLocked(path string) error {
    // 直接 marshal Config——Endpoints/Default/Lite 是权威字段，Models/DefaultModel/LiteModel 已删
    data, err := yaml.Marshal(c)
    if err != nil { return err }
    // 写 .bak + rename（现有逻辑不变）
    ...
}
```

**单向升级**：老用户跑 PR5 的 octo 一次，`config.yml` 被升级成 `endpoints:` 格式。回退到 PR4b 的 octo 会看到空模型（PR4b 读 `endpoints:` 块 OK 但 `cfg.Models` 空）。设计 §18.1 明文不承诺回退兼容，dev-docs 和 release notes 说明。

## 9. 文档更新

### 9.1 commit 现有设计文档

PR1-3 期间 `dev-docs/endpoint-design.md` 一直 untracked（从未 commit）。PR5 先把它 commit 进仓库（作为 PR1-3 设计快照）。

### 9.2 修订设计文档

PR5 实施偏离原文档的地方，在 `dev-docs/endpoint-design.md` 正文修订（不是末尾加偏离清单——结构性偏离改正文）：

| 章节 | 修订内容 |
|---|---|
| §3.1 Config struct | 删 `Models`/`DefaultModel`/`LiteModel` 字段；`Endpoints`/`Default`/`Lite` 标注"PR5 起权威" |
| §4.1 读路径 | `normalize()` 简化：只处理老 `models:` → `Endpoints` 迁移，不再 `syncEndpointsFromModels` 双向同步 |
| §4.2 写路径 | PR5 已切换，Save 写 `endpoints:` 格式 |
| §4.3 PR1 过渡策略 | 标注"PR1-3 期间过渡，PR5 已切换" |
| §6.1 改名级联 | 修正"扫 channels.yml"为"不扫 session 文件，靠 EntryByModel bare-model fallback 兜底" |
| §8.1 字段分类表 | 删 `Models`/`DefaultModel`/`LiteModel` 行；`Session.Model`/`ModelConfig`/`channel.Store.ModelConfig` 标注"PR5 后复合 id，stale 靠 fallback 兜底" |
| §8.2 读时识别 | `EntryByModel` bare-model fallback 改扫 Endpoints |
| §10.2 CRUD API | 标注"PR5 已实现" |
| §10.3 老 `/api/config/models` | 标注"PR5 已删除" |
| §11 PR 拆分表 | PR4a 标"取消"；PR4b 标"已合并 #1623"；PR5 标"写路径切换 + CLI + CRUD" |
| §13 CLI | 落实 show/doctor 输出格式（§7.2/§7.3） |
| §14 Validate/Repair | endpoint 级 guard 移除（`len(c.Models) == 0` 判断不再需要） |
| §15.1 i18n | PR5 未加新 key（CRUD key 留 PR6）；标注 |
| §18.1 兼容性 | 老文件单向升级；reasoning 从 per-entry 改全局 |
| §18.2/§18.3 老 session/channel | 标注"stale 复合 id 靠 EntryByModel fallback 兜底" |
| §18.4 老脚本 | `--model <bare>` 走 ParseModelFlag step 2，扫 Endpoints |
| §18.5 老 API | 标注"PR5 已删除 `/api/config/models*`" |

### 9.3 新增 §22 PR5 实施记录

末尾加 §22 记录 PR5 的 11 个关键决策（本文 §2-§8 的决策点 + 理由），作为决策溯源补充。

## 10. 测试计划

### 10.1 config 层

| 测试 | 覆盖点 |
|---|---|
| `TestSave_WritesEndpointsFormat` | Save marshal 出 `endpoints:` 块，不写 `models:` |
| `TestLoad_LegacyModelsBlockMigratesToEndpoints` | 老 `models:` 文件 Load 后 `c.Endpoints` 正确，`c.Models` 不存在 |
| `TestLoad_LegacyReasoningDropped` | 老 `models:` 块里 per-entry `reasoning_effort`/`show_reasoning` 字段 Load 后丢弃，全局 `cfg.ReasoningEffort`/`ShowReasoning` 保持空（或顶层值） |
| `TestUpsertEndpoint_InsertAndReplace` | 新 id 追加、已存在 id 替换 |
| `TestUpsertModel_NewAndExisting` | 新 model 追加、已存在 model 替换 |
| `TestUpsertModel_UnknownEndpointReturnsError` | endpoint 不存在报 `ErrEndpointNotFound` |
| `TestSetDefaultComposite` | 设 `cfg.Default`，不验证、不失效 sender |
| `TestEntryByModel_BareModelScansEndpoints` | bare model 走 Endpoints 扫描，default endpoint 优先 |
| `TestEntryByModel_BareModelAmbiguousPicksDefault` | 同 model 多 endpoint，走 default |
| `TestEntryByModel_CompositeIDResolves` | 复合 id 路径不变（PR2 测试保持） |
| `TestRepair_EndpointLevelGuardRemoved` | Validate/Repair 不再要求 `len(Models)==0` |

### 10.2 server 层

| 测试 | 覆盖点 |
|---|---|
| `TestCreateEndpoint` | POST 创建，201 + has_api_key |
| `TestUpdateEndpoint_ConnectionParams` | PATCH 改 base_url/api_key，触发 invalidateEndpointSenders |
| `TestUpdateEndpoint_RenameTriggersCascade` | PATCH 带 new_id，Default/Lite 前缀重写 + 旧 sender 失效 |
| `TestUpdateEndpoint_RenameCollision` | new_id 撞现有 endpoint，409 |
| `TestDeleteEndpoint` | DELETE，Default/Lite 指向它时清空 |
| `TestAddEndpointModel` | POST model，vision flag |
| `TestDeleteEndpointModel` | DELETE model，Default/Lite 指向时走 Repair |
| `TestSetEndpointDefault` | POST default，设 cfg.Default |
| `TestSetEndpointLite` | POST lite，设 cfg.Lite |
| `TestHandleGetConfig_NoModelsField` | GET /api/config 响应不含 models/default_model_idx |
| `TestOldModelsRoutes_Gone` | POST /api/config/models 返回 404 |
| `TestInvalidateEndpointSenders_OnCRUD` | 各 CRUD handler 触发/不触发失效（§6 表） |

### 10.3 CLI 层

| 测试 | 覆盖点 |
|---|---|
| `TestConfigWizard_CreatesFirstEndpoint` | 首次跑向导，生成 `legacy-<host>-0`，设 default |
| `TestConfigWizard_OverwritesSameHost` | 同 host 已存在，覆盖 |
| `TestConfigWizard_NewHostCreatesNew` | 不同 host，新建，提示 |
| `TestConfigWizard_AskSetDefault` | 最后一步问"设为 default"，Y/n 都正确 |
| `TestConfigShow_TwoLevelOutput` | show 输出格式（§7.2） |
| `TestDoctor_PerEndpointCheck` | doctor 输出格式（§7.3），含 key missing 场景 |

### 10.4 跨平台

按 memory 教训：测试里比较路径禁止硬编码 `/`，用 `filepath.ToSlash` 或 `filepath.Join`。PR5 新测试不涉及路径比较，但审计迁移的老测试（如 `onboard_config_handlers_test.go` 里的 `cfg.Models[0]` 断言改成 Endpoints 后）要保证 Windows 兼容。

## 11. 兼容性

### 11.1 老配置文件

老扁平 `models:` 配置文件在 `config.Load()` 时被 `normalize()` 转成 `c.Endpoints`。老用户任何一次 Save 后文件升级为新 `endpoints:` 结构。**单向升级，不可回退到 PR4b**（PR4b 读 `endpoints:` 块 OK 但 `cfg.Models` 空，web 设置页 AI Models 节会空）。

### 11.2 老 session 文件

`Session.Model` / `ModelConfig` 字段读时识别两种格式（裸 model + 复合 id），不写回。老 session 里的 stale 复合 id（endpoint 改名后）靠 `EntryByModel` 的 bare-model fallback 兜底（扫 Endpoints 找同名 model）。

### 11.3 老 channel 绑定

同 §11.2，channel store 是 `agent.Session`，`ModelConfig` 字段读时惰性升级不写回。

### 11.4 老脚本

`octo --model claude-sonnet-4-6`（裸 model）走 `ParseModelFlag` step 2（default 优先 + 扫 Endpoints 兜底），老脚本仍能跑。

### 11.5 老 API 端点

`GET/POST/PATCH/DELETE /api/config/models*` PR5 全删。第三方脚本调旧端点会 404。octo-agent 种子用户阶段，deprecation period 价值不大，直接删。

### 11.6 reasoning 字段迁移

老 `ModelEntry.ReasoningEffort` / `ShowReasoning` 在 Load 时**直接丢弃**，不拷到全局。用户要重新设全局 reasoning（`octo config` 或 web 设置页）。dev-docs 和 release notes 说明这个行为变化。代价：老用户的 per-entry reasoning 配置丢失；但多数用户只用 default entry 的 reasoning，且 lite model 不走 reasoning，影响小。

## 12. 安全

- `Endpoint.APIKey` 存明文（`config.yml` mode 0600），是 opt-in 回退，优先 env var
- CRUD API 响应只返回 `has_api_key: bool`，不返回明文 key（PR4b 已实现）
- POST /api/config/endpoints 的 `api_key` 字段是写入字段（请求里有、响应里无）
- flock 保护 `config.yml` 跨进程写（PR3 已实现）
- endpoint 改名级联不涉及权限提升（§19.3 不变）

## 13. 已知限制

| 限制 | 影响 | 缓解 |
|---|---|---|
| 单向升级不可回退 PR4b | 用户回退看到空模型 | dev-docs + release notes 说明；YAML 手改 5 分钟可恢复 |
| 老 session stale 复合 id 靠 fallback 兜底 | 改名后老 session 下次 turn 可能走错 endpoint（bare-model fallback 找到同名 model 但不是原 endpoint） | 极罕见场景（endpoint 改名 + 老 session 复用）；用户可 `/model <新复合id>` 显式重绑 |
| reasoning 降级到全局且老配置丢弃 | 老 per-entry reasoning 设置丢失，需重新设全局 | dev-docs 说明；多数用户只用 default entry 的 reasoning，影响小 |
| Web CRUD 留 PR6 | PR5 后 web 不能编辑 endpoint | CLI `octo config` 能完整管理；web 卡片节有"编辑将在下个版本"提示 |
| 老 `/api/config/models` 端点删除 | 第三方脚本调旧端点 404 | 种子用户阶段，第三方脚本概率极低 |

## 14. Release order

单仓库（octo-agent），单 PR（PR5）。无多 repo 协调。

PR5 合并后，PR6 做 Web UI CRUD（endpoint 卡片可编辑、加 model 表单、改名确认弹窗、default/lite 切换按钮），并加 §15.1 剩余的 i18n key（`add`/`delete`/`rename_confirm`/`error.*`）。

## 15. Rollback

- **代码回滚**：revert PR5 commit。`config.yml` 若已被 PR5 升级成 `endpoints:` 格式，revert 后 PR4b 的 octo 读 `endpoints:` 块 OK 但 `cfg.Models` 空——用户需手动把 `endpoints:` 块改回 `models:` 块（YAML 手改）或从 `config.yml.bak` 恢复（PR5 Save 前的备份是老格式）
- **数据回滚**：`config.yml.bak` 是上次成功 Save 的备份。PR5 首次 Save 前的 `.bak` 是老 `models:` 格式，可恢复
- **配置回滚**：无独立配置项

## 16. 决策溯源（PR5）

PR5 的 11 个关键决策通过 grill-me 拍板：

1. **`c.Models` 字段**：彻底删（运行时）。理由：最干净，长期维护成本最低；`fileConfig.Models` 保留作 Load 中间态
2. **新写 API**：`UpsertEndpoint` + `UpsertModel` + `SetDefaultComposite` 三个，抛弃 `SetDefaultEntry`/`SetEntry`。理由：endpoint 是一等公民，model 是子资源，新 API 贴合两级结构和 RESTful CRUD 路径
3. **向导 endpoint ID**：`legacy-<host>-<n>`（复用 PR1 隐式 id 规则）。理由：和 Load 老 `models:` 生成的 id 一致，用户在 web 看到同一套命名
4. **向导非首次替换语义**：按 host 找同 id 覆盖，找不到新建并提示，最后显式问"设为 default 吗"。理由：透明，老 endpoint 不丢，default 切换用户有控制权
5. **改名级联**：只重写 `config.yml`，不扫 session 文件。理由：符合 §8.2 session 不写回原则；老 session stale 复合 id 靠 EntryByModel fallback 兜底
6. **sender 失效**：粗粒度 per-endpoint，任何 endpoint mutation 都失效该 endpoint 全部 sender；`SetDefaultComposite` 不触发。理由：简单，sender 重建成本低，endpoint mutation 低频
7. **show/doctor 输出**：show 单行快速浏览、doctor 照设计 §13.2 样例完整。理由：show 是快速看一眼，doctor 是诊断，职责区分
8. **Save 升级**：单向，不管回退。理由：设计 §18.1 明文；octo-agent 种子用户阶段回退需求极低
9. **`ModelEntry` 类型**：保留作为投影类型。理由：sender 相关热路径签名不动，降低回归风险
10. **reasoning 归属**：删 per-entry，全局 `Config.ReasoningEffort`/`ShowReasoning`，老文件 per-entry reasoning 直接丢弃不迁移。理由：简化 Load 实现（无迁移逻辑）；lite model 不走 reasoning，主力 model 通常独占 default endpoint，per-entry reasoning 实际使用极少
11. **Web CRUD 范围**：PR5 只做后端 + CLI + 文档，Web UI 留 PR6。理由：PR5 已很大（删 Models + CLI + reasoning + CRUD + 文档），Web CRUD 全做规模爆炸

技术事实纠正（原设计文档 R3 违规）：

- §6.1 "扫 `~/.octo/channels.yml` 所有 channel 绑定" —— **错误**。channel 绑定在 `~/.octo/sessions/*.jsonl` 的 `ModelConfig` 字段（`internal/agent/session.go:41`），不在 `channels.yml`。PR5 修正为"不扫 session 文件"
