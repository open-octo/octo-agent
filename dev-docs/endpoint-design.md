# Endpoint 二级分组设计文档

## 1. 背景与目标

### 1.1 痛点

octo-agent 当前的模型配置是**扁平的 `models:` 列表**（`internal/config/config.go:96`），每条 `ModelEntry` 自带 `provider / model / base_url / api_key / protocol / vision` 全套字段。该结构有一项硬约束（`internal/config/config.go:36`）：

> "Two entries may not share a model string."

`Validate()`（`internal/config/config.go:403`）对重复 model 报 `duplicate model`。这在中转站（relay/proxy）场景下造成三个叠加痛点：

1. **同 model 不能多渠道共存** —— 同一个 `claude-sonnet-4-6` 无法同时配两个中转站，违反唯一约束
2. **中转站的模型列表没有集中管理** —— 每加一个模型都要重填一遍 `base_url / api_key / protocol`
3. **切换时心智碎** —— 在扁平列表里找"哪个是中转站 A 的 claude"，没有"先渠道后模型"的二级层次

### 1.2 目标

引入 **endpoint（渠道）二级分组**：一个 endpoint 封装共享的 `base_url + api_key + protocol`，下面挂多个 model。中转站用户配一次 endpoint，就能在其下挂任意多个模型；同一个 model 可同时挂在不同 endpoint 下。

### 1.3 范围

**在范围内**：
- `config.yml` schema 升级（向下兼容老扁平 `models:`）
- 三处 UI 改两级（TUI picker / IM `/model` / Web 设置页）
- 新增 `GET /api/config/endpoints` 两级 API（旧 `/api/config/models` 保留并 deprecated）
- endpoint 改名级联、并发写保护、默认回退、`--model` flag 解析、`octo config` / `octo doctor` 升级

**不在范围内**：
- vendor registry（`internal/app/provider.go`）的 `Models` 列表自动同步——仍由人工维护，本次不引入"订阅式模型源"
- audit.log 扩展——audit.log 只记工具调用决策（`internal/audit/audit.go`），endpoint 变更不属此范畴
- docs/blog 之外的用户迁移指南、CHANGELOG——种子用户阶段不需要

---

## 2. 命名术语表

| 术语 | 定义 |
|---|---|
| **endpoint** | 用户配置的一组共享 `base_url + api_key + protocol` 的模型集合。UI 展示名"渠道"。是本次新增的二级实体 |
| **复合 id** | `<endpoint_id>::<model>` 形式的字符串，作为 endpoint 下某 model 的唯一引用。分隔符 `::` |
| **隐式 endpoint** | 老扁平 `models:` 迁移生成的 endpoint，id 形如 `legacy-<host>-<n>`，用户可后续重命名 |
| **vendor** | 厂商目录条目（`app.Registry`），是新建 endpoint 的模板。endpoint 保存后与 vendor 解耦 |
| **BoundEntry** | session 的入口来源锁（`cli/tui/web/api/channel/cron/setup`），与 model 引用无关。本次不改 |
| **ModelConfig** | session/channel 绑定的 model 引用字段，当前存裸 model 字符串，本次升级为复合 id |

---

## 3. 数据模型

### 3.1 新 Config 结构

`internal/config/config.go` 新增 `Endpoint` 类型和 `Config.Endpoints` 字段，保留老 `Models` 字段做读兼容：

```go
// Endpoint 是用户配置的渠道：一组共享连接参数的模型集合。
type Endpoint struct {
    ID       string        `yaml:"id"`               // 唯一标识，复合 id 前缀。正则 ^[a-zA-Z0-9_-]+$，禁含 ::
    Name     string        `yaml:"name,omitempty"`   // 展示名，用户可改
    Provider string        `yaml:"provider"`         // vendor id（anthropic/openai/custom/...）
    BaseURL  string        `yaml:"base_url,omitempty"`// custom 必填，命名 vendor 可空（用 vendor 默认）
    APIKey   string        `yaml:"api_key,omitempty"`// 明文回退，优先 env
    Protocol string        `yaml:"protocol,omitempty"`// "anthropic"|"openai"，仅 custom 需要显式指定
    LiteModel string       `yaml:"lite_model,omitempty"`// 显式 lite model（endpoint 级）
    Models   []EndpointModel `yaml:"models"`          // 该 endpoint 下的模型列表
}

// EndpointModel 是 endpoint 下的一个模型条目。
type EndpointModel struct {
    Model  string `yaml:"model"`        // 模型 id，如 claude-sonnet-4-6
    Vision bool   `yaml:"vision"`       // 是否接受图片输入（model 级能力）
}

type Config struct {
    // Endpoints 是用户配置的渠道列表（新 schema 的权威字段）。
    Endpoints []Endpoint `yaml:"endpoints,omitempty"`
    // Default 是复合 id <endpoint_id>::<model>，指向默认使用的 endpoint+model。
    Default string `yaml:"default,omitempty"`
    // Lite 是复合 id，指向 compaction 用的 lite model。空表示走 ImplicitLiteModel 推断
    Lite string `yaml:"lite,omitempty"`

    // Models 保留老扁平结构，仅用于读兼容（normalize() 阶段转成 Endpoints）。
    // 写路径（Save）永远只写 Endpoints，不写 Models。
    Models []ModelEntry `yaml:"models,omitempty"`  // legacy 读兼容
    // DefaultModel/LiteModel 保留做老字段读兼容，normalize 后映射到 Default/Lite
    DefaultModel string `yaml:"default_model,omitempty"`  // legacy
    LiteModel    string `yaml:"lite_model,omitempty"`     // legacy

    // 其余字段（PermissionMode/Coauthor/AccessKey/Browser/...）不变
    // ... 现有字段保持原样
}
```

### 3.2 YAML 示例

**新结构**（中转站 + 官方）：

```yaml
endpoints:
  - id: relay-a
    name: 中转站A
    provider: custom
    base_url: https://relay-a.example.com
    api_key: sk-relay-a
    protocol: anthropic
    models:
      - model: claude-sonnet-4-6
        vision: true
      - model: gpt-5.4
        vision: true

  - id: official-anthropic
    name: 官方 Anthropic
    provider: anthropic
    models:
      - model: claude-opus-4-8
        vision: true
      - model: claude-sonnet-4-6
        vision: true
      - model: claude-haiku-4-5
        vision: true

  - id: legacy-api.deepseek.com-0
    name: DeepSeek
    provider: deepseek
    models:
      - model: deepseek-v4-flash
        vision: false
      - model: deepseek-v4-pro
        vision: false
    lite_model: deepseek-v4-flash

default: official-anthropic::claude-sonnet-4-6
lite: official-anthropic::claude-haiku-4-5
```

**老结构**（读兼容）：

```yaml
models:
  - provider: anthropic
    model: claude-sonnet-4-6
    base_url: https://api.anthropic.com
    api_key: sk-xxx
    vision: true
default_model: claude-sonnet-4-6
```

`normalize()` 把老结构转成内存 `Endpoints`：该条 entry 包成隐式 endpoint（id `legacy-api.anthropic.com-0`，单 model），`DefaultModel` 映射到 `Default`（`legacy-api.anthropic.com-0::claude-sonnet-4-6`）。

---

## 4. Schema 兼容与迁移

### 4.1 读路径：双 schema 并存

`config.Load()`（`internal/config/config.go:566`）读到 YAML 后，`normalize()` 处理顺序：

1. 若 `Endpoints` 非空 → 直接用新结构（用户已升级）
2. 若 `Endpoints` 为空但 `Models` 非空 → 老扁平迁移：
   - 按 `provider + base_url` 聚合（同 provider+base_url 的多条 entry 聚成一个 endpoint，`api_key`/`protocol` 取第一条）
   - 每个聚合组生成隐式 endpoint，id `legacy-<host>-<n>`（`<host>` 取自 base_url，`<n>` 是同 host 的序号 0 起递增）
   - `DefaultModel`/`LiteModel`（裸 model 字符串）映射到 `Default`/`Lite`（复合 id，指向第一个匹配的 endpoint）
3. 两者都空 → 零配置状态，首次引导

聚合维度用 `provider + base_url`（不用 `+ api_key + protocol`）——同一中转站用户配两套 key 的场景极少，`provider + base_url` 是用户心智边界（"一个中转站一个 endpoint"）。同 base_url 多 key 合并时取第一条 entry 的 key，日志 `slog.Warn("config: multiple api_keys found for same base_url, keeping the first", "base_url", ..., "dropped_keys", N)` 提示。

### 4.2 写路径：统一升级到 endpoints

`Config.Save()`（`internal/config/config.go:653`）永远只写 `Endpoints` / `Default` / `Lite`，不写老 `Models` / `DefaultModel` / `LiteModel`。老用户任何一次 Save（`octo config` / web 设置 / `/model --default`）自动升级文件结构。

### 4.3 PR1 的"加结构不启用写"过渡策略

按 PR 拆分（见 §11），PR1 引入 `Endpoints` 结构和读兼容，但 Save 仍写老扁平 `Models` 格式（内存 endpoints 反向序列化）。这样 PR1 合并后老用户零感，PR4 才切换写路径到 endpoints 格式。

### 4.4 隐式 endpoint id 稳定性

`legacy-<host>-<n>` 在多次 Load→Save 间稳定：同一份老配置每次 normalize 生成的 id 相同（host 和序号确定性）。用户在 web 设置里重命名后变友好 id（如 `relay-a`），改名级联见 §6。

---

## 5. 引用机制与解析

### 5.1 复合 id 格式

`<endpoint_id>::<model>`，分隔符 `::`（双冒号）。选 `::` 而非 `/` 的原因：OpenRouter 模型 id 本身含 `/`（如 `anthropic/claude-sonnet-4-6`，`internal/app/provider.go:112`），用 `/` 做分隔符会三段嵌套歧义。`::` 是 Kubernetes 命名空间分隔符惯例，零冲突。

### 5.2 endpoint id 合法性

正则 `^[a-zA-Z0-9_-]+$`，禁含 `::`、空格、特殊字符。`Validate()` 检查。

### 5.3 默认 endpoint 回退链（ResolveDefault）

`Config.ResolveDefault()` 实现四步回退：

```
1. 解析 cfg.Default 成 (endpointID, model)
2. endpointID 在 cfg.Endpoints 找到？
   - 找到 → 跳 step 3
   - 找不到 → 跳 step 5
3. model 在该 endpoint 的 Models 找到？
   - 找到 → 返回 (该 endpoint, 该 model)  ✓ 完整命中
   - 找不到 → 跳 step 4
4. 该 endpoint 至少 1 个 model？
   - 有 → 返回 (该 endpoint, endpoint.Models[0])  ✓ endpoint 保留、model 兜底
   - 无 → 跳 step 5（空 endpoint 等同不存在）
5. cfg.Endpoints 非空？
   - 非空 → 返回 (Endpoints[0], Endpoints[0].Models[0])  ✓ 第一个 endpoint 兜底
   - 空 → 返回 零值  ✗ 调用方报错引导配置
```

每步回退 `slog.Warn("config: default model resolution fell back", "default", cfg.Default, "resolved_to", complexID, "reason", "endpoint_not_found"|"model_not_found"|"empty_endpoint"|"no_endpoints")` 提示。

**设计理由**：保留 endpoint 连接参数（key/base_url）尽量复用——用户配 `default: relay-a::claude-sonnet-4-6`，某天中转站 A 下架了 claude 换成 gemini，`default` 的 model 失效但 endpoint 还在。若直接跳到第一个 endpoint（可能是官方 anthropic，贵），用户被默默切到贵模型。回退链优先保留 endpoint、只回退 model，符合用户意图。

### 5.4 `--model <X>` flag 解析（ParseModelFlag）

`cmd/octo/config.go` 的 `resolveProviderModel` 改造，解析顺序：

```
ParseModelFlag(flagModel, cfg):
  1. flagModel 含 "::"？
     - 是 → 解析 (endpointID, model)，在 cfg.Endpoints 找该 endpoint
       - 找到且 model 存在 → 返回 (该 endpoint, 该 model)  ✓ 精确命中
       - endpoint 找不到 → 报错 "endpoint %q not found (available: ...)"
       - model 找不到 → 报错 "model %q not found in endpoint %q"
     - 否 → 跳 step 2
  2. flagModel 是裸 model：
     a. cfg.Default 能解析出 (defaultEndpoint, defaultModel) 且 defaultModel == flagModel？
        - 是 → 返回 (defaultEndpoint, flagModel)  ✓ default endpoint 优先
     b. 扫所有 endpoint，找 endpoint.Models 含 flagModel 的：
        - 唯一命中 → 返回 (该 endpoint, flagModel)
        - 多个命中 → 返回第一个，slog.Warn "model %q matches multiple endpoints, using %q (use <endpoint>::%q to disambiguate)"
        - 零命中 → 报错 "model %q not found in any endpoint (available: ...)"
```

env `ANTHROPIC_MODEL`/`OPENAI_MODEL`/`*_MODEL` 走相同解析函数。

**设计理由**：老脚本 `octo --model claude-sonnet-4-6` 仍能跑（default 命中或唯一命中），歧义时静默选第一个 + 日志提示，用户看到日志能改用复合 id 精确化。default 优先于全扫——用户配 default 说明"这是我的主 endpoint"，裸 model 优先走它符合直觉。

### 5.5 lite model 推断（ImplicitLiteModel）

`internal/app/provider.go:452` `ImplicitLiteModel` 签名改造：

```go
// 旧: ImplicitLiteModel(provider, model, baseURL string) string
// 新: ImplicitLiteModel(endpoint Endpoint, model string) string
```

推断规则：
1. `endpoint.LiteModel` 非空且 ≠ `model` → 返回 `endpoint.LiteModel`
2. 否则若 endpoint 的 `base_url` 命中官方 vendor（`VendorByBaseURL`，`internal/app/provider.go:467`）且 vendor 有 `LiteModel` → 返回 vendor LiteModel
3. 否则返回空（custom endpoint 未显式配 lite 则无 lite）

**设计理由**：官方 endpoint 零配置自动 lite 是既有体验（老用户迁移后 `官方 Anthropic` endpoint 不标 `lite_model`，主模型 opus 仍自动用 haiku compact）。中转站显式标 lite 是正确心智——中转站模型命名不可控，启发式跨 vendor 推断会影响 compaction 质量，违反"不猜"原则。

---

## 6. endpoint 改名级联

### 6.1 原子级联

endpoint id 用户可改（初始 `legacy-<host>-<n>` → 重命名 `relay-a`）。改名时同一 Save 事务内：

1. 重写 `Config.Default` 的前缀（`oldID::model` → `newID::model`）
2. 重写 `Config.Lite` 的前缀
3. 扫 `~/.octo/channels.yml` 所有 channel 绑定（`channel.Store.ModelConfig`，`internal/channel/manager.go:156`），替换前缀
4. 清旧 endpoint id 的 sender 缓存项（见 §9）

跨文件原子做法：先在内存算出新 `Config` + 新 `channels.yml`，写 `config.yml.tmp` 和 `channels.yml.tmp`，都成功后 rename 两个文件，任一失败删 tmp 不 rename。和 `Config.Save()` 现有"写 .bak 再 rename"模式一致（`internal/config/config.go:661-677`）。

### 6.2 UI 交互

web 设置页改 endpoint id 时弹确认："该 endpoint 被 N 处引用（default、channel 微信、channel 飞书），改名将一并更新这些引用，确认？"——让用户知道会发生什么，但不让他手动做。

---

## 7. 并发写保护

### 7.1 flock 文件锁

`Config.Save()` 加 flock：锁内执行 Load（重读最新）→ Marshal → WriteFile tmp → rename → unlock。

跨进程是真实场景——octo-agent 多入口（TUI 进程 + `octo serve` 进程 + `octo config` 命令）都可能同时写 `config.yml`。flock 是 Unix 跨进程协调的标准手段，Windows 有 `LockFileEx` 对应。

**锁粒度**：只锁 `config.yml` 的 Save。endpoint 改名级联写两个文件时，先锁 `config.yml` 改完 → 释放 → 再锁 `channels.yml` 改 → 释放。两把锁分别持有，不同时持两把，避免死锁。

### 7.2 跨平台实现

复用 `internal/executil` 的 `_windows/_other` 拆分模式。Unix 用 `golang.org/x/sys/unix` 的 `Flock`，Windows 用 `golang.org/x/sys/windows` 的 `LockFileEx`。

### 7.3 已知限制

flock 在 NFS 上不可靠。octo 的 `~/.octo/config.yml` 在 home 目录，绝大多数用户是本地文件系统，NFS home 极罕见。作为已知限制写入 dev-docs。

---

## 8. 持久化字段迁移

### 8.1 字段分类

octo-agent 里 `BoundEntry` 是歧义名，实际两种语义：

| 字段 | 位置 | 语义 | 是否改动 |
|---|---|---|---|
| `Session.BoundEntry` | `internal/agent/session.go:91` | 入口来源锁（`cli/tui/web/api/channel/cron/setup` 枚举） | **不改**——与 model 无关 |
| `Session.LeaseEntry` | `internal/agent/session.go:102` | 跨进程租约锁，同上枚举 | **不改** |
| `Session.Model` | `internal/agent/session.go:32` | 会话当时用的 model 名 | **改**——升级为复合 id |
| `Session.ModelConfig` | `internal/agent/session.go:41` | session 绑定的 model 引用 | **改**——升级为复合 id |
| `channel.Store.ModelConfig` | `internal/channel/manager.go:156` | IM 侧持久化的 model 绑定 | **改**——同上 |
| `channel.Store.Model` | `internal/channel/manager.go:145` | IM 侧记录的 model 名 | **改**——同上 |
| `ModelResolution.BoundEntry` | `internal/channel/manager.go:157` | channel /model 解析结果，值 = 复合 id | **改**——值变成复合 id（字段名不动） |
| `ModelInfo.Model` | `internal/channel/manager.go:145` | /model 列表项的 model id | **改**——展示用复合 id |

### 8.2 读时识别两种格式（不写回）

`Session.Model` / `ModelConfig` / `channel.Store.ModelConfig` 都是 append-only 或 store 文件字段。读到值时：

```go
func resolveLegacyModelRef(model string, cfg config.Config) string {
    if strings.Contains(model, "::") {
        return model  // 已是复合 id
    }
    // 裸 model：找第一个匹配的 endpoint
    for _, ep := range cfg.Endpoints {
        for _, m := range ep.Models {
            if m.Model == model {
                return ep.ID + "::" + m.Model
            }
        }
    }
    // 找不到回退 default
    def := cfg.ResolveDefault()
    slog.Warn("session: legacy model binding fell back to default",
        "legacy_model", model, "resolved_to", defComplexID)
    return defComplexID
}
```

**不写回**的理由：
- session 是 append-only（`internal/agent/session.go:731` 用 `O_APPEND`），`rewriteAll` 是 O(n) 全文件重写，为迁移目的触发代价过高（现有 `rewriteAll` 触发条件是 stale/truncated 前缀，`session.go:716`）
- channel store 写有并发风险（多 session 并发），不碰 store 文件最稳
- 新 session/绑定天然都是复合 id，混格式问题随老 session 自然退役消失

**歧义处理**：同 model 挂多 endpoint 时选第一个匹配，`slog.Info` 提示"该绑定可能不准、建议重新选"。

### 8.3 BoundEntry/LeaseEntry 不动

`Session.BoundEntry`（`session.go:88-91`）是"谁占用这个 session"的入口锁，`LeaseEntry`（`session.go:97-103`）是跨进程租约锁，两者存的是入口来源枚举值（`cli/tui/web/api/channel/cron/setup`），与 model 引用完全无关。本次方案不动它们。

`ModelResolution.BoundEntry`（`manager.go:157`）字段名虽叫 BoundEntry 但存的是 model 引用（`server.go:2138` 赋值 `entry.Model`），值从裸 model 变复合 id，字段名不改（符合"只改该改的"）。

---

## 9. sender 缓存与失效

### 9.1 缓存 key 改复合 id

`internal/server/server.go:1485` 当前 `senderCache[entry.Model]` 用裸 model 做 key，新结构下同 model 挂多 endpoint 会撞 key（`relay-a::claude` 和 `official::claude` 返回错误 endpoint 的 sender）。改为 `senderCache[endpointID+"::"+model]`。

### 9.2 失效按 endpoint 粒度

新增 `invalidateEndpointSenders(endpointID string)`，扫 `senderCache` 删所有前缀 `endpointID+"::"` 的项：

```go
func (s *Server) invalidateEndpointSenders(endpointID string) {
    s.senderCacheMu.Lock()
    defer s.senderCacheMu.Unlock()
    prefix := endpointID + "::"
    for k := range s.senderCache {
        if strings.HasPrefix(k, prefix) {
            delete(s.senderCache, k)
        }
    }
}
```

失效时机：
- endpoint 任意字段改（`base_url`/`api_key`/`protocol`/`lite_model`）→ 清该 endpoint 下所有 model 的缓存项（这些字段是 endpoint 级共享，改了所有 model 的 sender 都失效）
- endpoint 下某 model 增/删 → 清该 (endpoint, model) 单项
- endpoint 删除 → 清该 endpoint 下所有缓存项
- endpoint 改名（id 变）→ 清旧 id 下所有缓存项（新 id 还没缓存，下次自然建）

`invalidateSenderCache()`（`server.go:1538`，全量清空）保留作兜底——`octo config --fix` 大改、LoadCached 切换 last good 配置时全量清。

**设计理由**：endpoint 改 `base_url` 是 endpoint 级变更，该 endpoint 下所有 model 的 sender 都失效（共享 base_url）。按 endpoint 粒度失效符合 base_url/key/protocol 是 endpoint 级共享的语义，且缓存命中率高（改一个 endpoint 不影响其他 endpoint 缓存）。

---

## 10. API 设计

### 10.1 新增 `GET /api/config/endpoints`

**路径**：`GET /api/config/endpoints`

**响应**：

```json
{
  "endpoints": [
    {
      "id": "relay-a",
      "name": "中转站A",
      "provider": "custom",
      "base_url": "https://relay-a.example.com",
      "protocol": "anthropic",
      "has_api_key": true,
      "lite_model": "",
      "models": [
        {"model": "claude-sonnet-4-6", "vision": true},
        {"model": "gpt-5.4", "vision": true}
      ]
    }
  ],
  "default": "relay-a::claude-sonnet-4-6",
  "lite": "relay-a::gpt-5.4-mini"
}
```

`has_api_key: true` 而非返回明文 key（安全），前端只展示"已配置/未配置"。

### 10.2 endpoint CRUD

```
POST   /api/config/endpoints                  创建 endpoint
PATCH  /api/config/endpoints/{id}             改 endpoint（含改名 → 触发级联）
DELETE /api/config/endpoints/{id}             删 endpoint
POST   /api/config/endpoints/{id}/models      endpoint 下加 model
DELETE /api/config/endpoints/{id}/models/{model}  删 endpoint 下某 model
POST   /api/config/endpoints/{id}/default     设为 default
POST   /api/config/endpoints/{id}/lite        设为 lite
```

RESTful 资源嵌套，endpoint 是父资源、model 是子资源。改名走 `PATCH /api/config/endpoints/{id}` 带 `new_id` 字段，触发 §6 级联。

### 10.3 旧端点 deprecated

`GET/POST/PATCH/DELETE /api/config/models*`（`internal/server/server.go:830-834`）保留，标记 deprecated：
- 响应加 `X-Deprecated` header + `warning` 字段提示用新端点
- 旧端点继续返回扁平结构（兼容回退）
- 1-2 个 minor 版本后在 CHANGELOG 标注移除

**设计理由**：octo-agent 是开源项目，可能有第三方脚本/插件调旧端点。保留兼容比强删稳妥。新端点名实相符（`/api/config/endpoints` 返回两级结构），旧端点 `/api/config/models` 名实不符（返回 models 但新结构下 models 是子层），deprecation 期让用户迁移。

---

## 11. PR 拆分顺序

按依赖层拆 5 个 PR，PR1 的"加结构不启用写"策略让每 PR 独立可 merge 且不改变运行时行为：

| PR | 内容 | 风险 | 依赖 |
|---|---|---|---|
| **PR1** | config 结构 + 读兼容 + Validate/Repair + ResolveDefault + ParseModelFlag。引入 `Endpoints` 字段，`normalize()` 老扁平 → endpoints 内存，**Save 仍写老扁平格式** | 高 | 无 |
| **PR2** | 读路径适配：`ImplicitLiteModel` 签名改 `(endpoint, model)`；session `Model`/`ModelConfig` 读时兼容；channel store 读时升级 | 中 | PR1 |
| **PR3** | flock 并发保护 + endpoint 改名级联（跨文件 tmp+rename）+ `invalidateEndpointSenders` | 高 | PR1+PR2 |
| **PR4** | 写路径切换（Save 写 endpoints 格式）+ 三处 UI 两级（TUI picker / IM `/model` 分组 / Web 设置页 + 新 API） | 中 | PR1-3 |
| **PR5** | CLI（`octo config` 向导 / `octo config show` / `octo config --fix` / `octo doctor`）+ dev-docs 文档 | 低 | PR1-4 |

**PR1-3 期间老用户配置仍是扁平格式**——这是"零摩擦"的体现，PR4 一次性切换写路径 + UI，此时底层全已就绪。

---

## 12. UI 设计

### 12.1 TUI model picker（两级）

`cmd/octo/tuirepl_view.go:1006` `openModelPicker` 改两级：

- `modelPicker` 结构加 `endpointIdx int` 维度
- 按键：`←→` 切 endpoint，`↑↓` 切 model，`Enter` 确认，`Esc` 取消
- 选中后 `dispatchModel` 接收复合 id（`endpoint_id::model`）
- 渲染：当前 endpoint 高亮，其下 model 列表缩进显示

复用现有 `complItem`（`cmd/octo/tuirepl.go`）和 completion 样式。不引入新依赖。

### 12.2 IM `/model`（分组输出）

`internal/server/server.go:2125` `channelModelOps().Resolve` 改造：

- `/model`（无参）→ 按 endpoint 分组输出文本列表：
  ```
  渠道: relay-a (中转站A)
    • relay-a::claude-sonnet-4-6
    • relay-a::gpt-5.4
  渠道: official-anthropic (官方 Anthropic)
    • official-anthropic::claude-opus-4-8
    • official-anthropic::claude-sonnet-4-6
  ```
- `/model <复合id>` → 精确切换
- `/model <裸model>` → 走 ParseModelFlag 规则（default 优先 + 全扫兜底）
- `/model default` → 解绑（回退默认）

`ModelResolution.BoundEntry`（`manager.go:157`）值变成复合 id，`ModelInfo.Model`（`manager.go:145`）展示用复合 id。

IM 输出按 `config.Language`（zh/en）选语言字符串（IM 纯文本载体不走前端 i18n key 体系，octo-agent IM 消息现状即按 Language 切换中英文）。

### 12.3 Web 设置页（两级）

- endpoint 卡片列表：每张卡片显示 endpoint id/name/provider/base_url/api_key 状态/模型列表
- endpoint 卡片内可加/删 model、设 default/lite
- "添加 endpoint"按钮 → 选 vendor 模板（`app.Registry`）→ 填 base_url（custom 必填）/api_key/protocol → 挂模型
- 改 endpoint id 弹确认框（§6.2）
- 模型下拉建议来自该 vendor 的 `VendorModels`（`internal/app/provider.go`），用户可改/加

---

## 13. CLI 设计

### 13.1 `octo config`（向导）

`cmd/octo/config.go:108` `runConfig` 子命令：

- **无参 / `setup` / `init`**：单 endpoint 引导（选 vendor → 填 base_url/custom 必填 → 选/填 model → 填 key），写入/替换默认 endpoint
  - 首次（无 endpoints）→ 完整引导，生成第一个 endpoint（id `ep-1` 或 `legacy-<host>-0`），设为 default
  - 非首次 → 询问"替换默认 endpoint"，覆盖老的默认 endpoint
  - 保留老用户"跑 `octo config` 改默认"的肌肉记忆
- **`show` / `get`**：打印两级 endpoints 列表（每 endpoint 显示 id/name/provider/base_url/models 数/default 标记/lite 标记）
- **`path`**：不变
- **`fix` / `--fix`**：endpoint 级修复（见 §14）

多 endpoint 管理不进 CLI 向导——CLI 嵌套菜单 UX 差，且 octo-agent web 设置页已是 endpoint 管理主战场。纯 SSH/无 web 用户手改 YAML 是合理回退（老结构本来也支持手改）。

### 13.2 `octo doctor`（per-endpoint 检查）

`cmd/octo/doctor.go` 升级：

```
octo doctor — checking your setup

config: /Users/qiao/.octo/config.yml
  ✓ config.yml parses
  ✓ 3 endpoints configured

Endpoints:
  ✓ ep-1 (relay-a, custom, https://relay-a.example.com)
      2 models: claude-sonnet-4-6, gpt-5.4
      ✓ API key found (CUSTOM_API_KEY)
  ✗ ep-2 (官方Anthropic, anthropic, https://api.anthropic.com)
      3 models: claude-opus-4-8, claude-sonnet-4-6, claude-haiku-4-5
      ✗ API key missing (ANTHROPIC_API_KEY not set, endpoint.api_key empty)
  ✓ legacy-deepseek-0 (deepseek, deepseek, https://api.deepseek.com)
      2 models: deepseek-v4-flash, deepseek-v4-pro
      ✓ API key found (DEEPSEEK_API_KEY)

References:
  ✓ default = ep-1::claude-sonnet-4-6 (resolves)
  ✓ lite = ep-1::gpt-5.4 (resolves, ≠ default)

1 problem(s) found — `octo config --fix` can repair config issues.
```

每个 endpoint 检查：id 合法、有 model、api_key 可达（env 或 `endpoint.APIKey`）。default/lite 引用独立检查可解析 + lite≠default 约束。key 探测沿用现有 `apiKeyReachable`/`apiKeyStatus`（`doctor.go:64-67`），串行执行（endpoint 数个位数，毫秒级）。

---

## 14. Validate / Repair 规则

### 14.1 Validate 检查项

`internal/config/config.go:387` `Validate()` 升级：

1. endpoint id 非空、唯一、合法（`^[a-zA-Z0-9_-]+$`，不含 `::`）
2. 每个 endpoint 至少 1 个 model
3. 每个 model 名非空
4. 同 endpoint 内 model 不重复（跨 endpoint 同 model 允许）
5. `Default` 是合法复合 id，能解析到 endpoint + model
6. `Lite` 同上（或空）

### 14.2 Repair 自动修（safe，可逆）

- `Default` dangling → 重置到第一个 endpoint 的第一个 model 的复合 id，日志提示
- `Lite` dangling → 清空
- 空 endpoint（无 model）→ 删除该 endpoint（连同引用级联清理）
- `Lite` 指向的 model == `Default` 指向的 model → 清空 lite

### 14.3 Repair 报 unfixable（需人工）

- **重复 endpoint id** → 不自动改名（怕覆盖用户草稿意图），报"duplicate endpoint id %q — rename one"
- endpoint id 非法（含 `::`/空格）→ 报"endpoint id %q contains illegal chars — rename"
- 无 model 名的 model entry → 报"model in endpoint %q has no name — add one or remove"
- 同 endpoint 内重复 model → 报"duplicate model %q in endpoint %q — remove one"

**设计理由**：重复 endpoint id 走保守路径——endpoint id 是复合 id 前缀，自动重命名（如加 `-2` 后缀）会级联改 default/lite/channel 绑定，自动改名 + 自动级联的连锁风险大于让用户手改一次。这与现有 `Repair` 对"重复 model"的处理一致（`internal/config/config.go:751` 现在也是报 unfixable 不自动改）。

---

## 15. i18n

### 15.1 前端 i18n key

`web/src/lib/i18n.ts` 的 `en` / `zh` Record 新增 `settings.endpoints.*` 命名空间：

| key | en | zh |
|---|---|---|
| `settings.endpoints.title` | Endpoints | 渠道 |
| `settings.endpoints.add` | Add endpoint | 添加渠道 |
| `settings.endpoints.id` | ID | ID |
| `settings.endpoints.name` | Display name | 显示名称 |
| `settings.endpoints.provider` | Provider | 厂商 |
| `settings.endpoints.base_url` | Base URL | 接口地址 |
| `settings.endpoints.protocol` | Protocol | 协议 |
| `settings.endpoints.api_key` | API key | API 密钥 |
| `settings.endpoints.api_key.set` | Set | 已设置 |
| `settings.endpoints.api_key.missing` | Missing | 未设置 |
| `settings.endpoints.models` | Models | 模型 |
| `settings.endpoints.models.add` | Add model | 添加模型 |
| `settings.endpoints.models.vision` | Vision | 视觉 |
| `settings.endpoints.default` | Default | 默认 |
| `settings.endpoints.lite` | Lite | 轻量 |
| `settings.endpoints.delete` | Delete endpoint | 删除渠道 |
| `settings.endpoints.rename_confirm` | Rename will update N references, continue? | 重命名将更新 N 处引用，继续吗？ |
| `settings.endpoints.error.duplicate_id` | Duplicate endpoint id %q | 渠道 ID %q 重复 |
| `settings.endpoints.error.invalid_id` | Endpoint id %q contains illegal chars | 渠道 ID %q 含非法字符 |
| `settings.endpoints.error.not_found` | Endpoint %q not found | 渠道 %q 不存在 |
| `settings.endpoints.error.model_not_found` | Model %q not found in any endpoint | 模型 %q 不存在于任何渠道 |
| `settings.endpoints.error.empty` | Endpoint has no models | 渠道下无模型 |

endpoint 的 base_url 区域变体仍复用现有 `settings.models.baseurl.variant.*`（`internal/app/provider.go:170`，语义一致）。

### 15.2 TUI picker i18n

`web/src/lib/i18n.ts` 新增 `tui.model_picker.*`：

| key | en | zh |
|---|---|---|
| `tui.model_picker.switch_endpoint` | Switch endpoint: | 切换渠道： |
| `tui.model_picker.switch_model` | Switch model: | 切换模型： |
| `tui.model_picker.hint` | ←→ endpoint · ↑↓ model · Enter select · Esc dismiss | ←→ 渠道 · ↑↓ 模型 · Enter 确认 · Esc 取消 |

### 15.3 IM 文案

IM 输出是纯文本流（飞书/微信消息），不走前端 i18n key 体系，按 `config.Language`（zh/en）选语言字符串硬编码（octo-agent IM 消息现状即如此）。新增 endpoint 分组标题文本按 Language 切换中英文版本。

---

## 16. 测试计划

### 16.1 分层测试

| 层 | 测试类 | 覆盖点 |
|---|---|---|
| **config 层（~70%）** | 迁移矩阵 | 单 entry → 单隐式 endpoint；同 provider+base_url 多 entry → 聚合；不同 base_url → 多 endpoint；含 LiteModel 的老 entry → endpoint.lite_model 推断；老+新并存双 schema 读 |
| | Validate/Repair 全规则 | 重复 endpoint id（unfixable）；非法 id（unfixable）；空 endpoint（auto-fix 删除）；dangling default/lite（auto-fix）；lite==default（auto-fix 清空） |
| | ResolveDefault 回退链 | 四步回退每步单独测 |
| | ParseModelFlag | 复合 id 精确；裸 model default 优先；全扫兜底；歧义日志 |
| | endpoint 改名级联 | 改名后 default/lite/channel 绑定前缀全更新；跨文件原子（tmp + rename） |
| | flock 并发 | `t.Parallel` 起 10 goroutine 抢写同一 config.yml，验证不丢更新、不 corrupt |
| | vision 迁移 | 老 `ModelEntry.Vision` → `EndpointModel.Vision` 一一对应 |
| **session/channel 层（~15%）** | 读时兼容 | session 文件混格式（老 `Model: claude-sonnet-4-6` + 新 `Model: relay-a::claude-sonnet-4-6`）能正确解析 |
| | channel store 读时升级 | 老裸 model 绑定 → 复合 id（第一个匹配 endpoint）；找不到回退 default |
| **UI 层（~10%，薄）** | TUI picker 两级 | ←→ 切 endpoint、↑↓ 切 model、Enter 确认、Esc 取消（复用现有 `cmd/octo/model_picker_test.go` 结构） |
| | web 设置 API 契约 | `GET /api/config/endpoints` 返回两级结构；`POST/PATCH/DELETE` 增删改 endpoint |
| | IM `/model` 分组输出 | 按 endpoint 分组的文本列表 |
| **端到端（~5%）** | 老用户升级路径 | 老扁平 config → 启动 octo → `/model` 看到两级 picker → web 加新 endpoint → `/model relay-b::gpt-5.4` 切换 → 验证 sender 重建 |
| | 中转站轮换路径 | 配两个中转站 endpoint → `/model` 切换 → 验证 sender 用对应 endpoint 的 key/base_url |

### 16.2 跨平台注意事项

按 memory 教训：测试里比较路径禁止硬编码 `/`。涉及 `.octo/uploads` 等路径断言必须用 `filepath.ToSlash` 转换或 `filepath.Join` 构造，保证 Windows（`\`）和 Unix（`/`）都能过。

---

## 17. 前端构建产物策略

严格遵循 PR #1595 教训（memory 记录）：

- `internal/server/webdist/` 已 gitignore（`.gitignore:44`），只保留 `.gitkeep`
- PR4 改前端只提交 `web/src/` 源码（svelte 组件、`web/src/lib/i18n.ts`、API 调用），**不提交 `webdist/*` 产物**
- 本地开发跑 `make web-build`（`Makefile:69`）构建到 `webdist/`，`go run ./cmd/octo serve` 启动看效果
- CI 发版（`.github/workflows/desktop.yml` / `release.yml`）自动跑 `make web-build` 嵌入二进制

---

## 18. 兼容性

### 18.1 老配置文件

老扁平 `models:` 配置文件在 `config.Load()` 时被 `normalize()` 转成内存 `Endpoints`，运行时行为不变。老用户任何一次 Save 后文件升级为新 `endpoints:` 结构。PR1-3 期间 Save 仍写老扁平格式，PR4 切换写路径。

### 18.2 老 session 文件

`Session.Model` / `ModelConfig` 字段读时识别两种格式（裸 model + 复合 id），不写回。老 session 文件保持 append-only 混格式，新 session 天然都是复合 id。

### 18.3 老 channel 绑定

`channel.Store.ModelConfig` 读时惰性升级不写回。老绑定逐渐被用户在 IM 里 `/model <复合id>` 覆盖，新绑定都是复合 id，自然迭代干净。

### 18.4 老脚本

`octo --model claude-sonnet-4-6`（裸 model）走 ParseModelFlag 兼容：default endpoint 优先 + 全扫兜底，老脚本仍能跑。歧义时静默选第一个 + 日志提示，用户可改用复合 id 精确化。

### 18.5 老 API 端点

`GET/POST/PATCH/DELETE /api/config/models*` 保留，标记 deprecated，1-2 个 minor 版本后移除。第三方脚本/插件有迁移窗口。

### 18.6 audit.log

不受影响。`internal/audit/audit.go` 只记工具调用决策（allow/deny/ask），endpoint 变更是用户配置文件修改，不经过 tool gate，不触发 audit.log。配置变更有 `config.yml.bak` 备份（`config.go:661`）+ flock（§7）保护。

---

## 19. 安全

### 19.1 API key 处理

- `Endpoint.APIKey` 存明文（`config.yml` mode 0600，`config.go:653`），是 opt-in 回退，优先 env var（`config.go:48` 注释）
- `GET /api/config/endpoints` 响应只返回 `has_api_key: bool`，不返回明文 key（§10.1）
- flock 保护 `config.yml` 跨进程写，避免并发写导致 key 丢失

### 19.2 endpoint 改名

改名级联是原子事务（§6），不会出现"endpoint 改了但 default 引用没改"的中间态导致 sender 指向错误 endpoint。

### 19.3 权限提升风险

endpoint 变更不涉及权限提升——endpoint 是模型连接配置，不改变 agent 的工具权限（`PermissionMode` 等）。`Session.BoundEntry`（入口锁）与 endpoint 无关，不受影响。

---

## 20. 已知限制

| 限制 | 影响 | 缓解 |
|---|---|---|
| flock 在 NFS 上不可靠 | NFS home 目录用户并发写可能丢更新 | 极罕见场景，dev-docs 注明；建议本地文件系统 |
| vendor registry `Models` 列表滞后于厂商实际模型 | 用户配官方 endpoint 时模型下拉可能缺新模型 | 用户可手填 model id；registry 维护是独立话题，不在本次范围 |
| 同 base_url 多 key 合并丢 key 信息 | 迁移时同 base_url 不同 key 的 entry 被合成一个 endpoint，取第一条 key | 日志 `slog.Warn` 提示 dropped_keys 数；罕见场景 |
| endpoint 改名跨文件原子在极端崩溃窗口可能不一致 | rename 第一个文件后崩溃，第二个文件未 rename | macOS/Linux rename 是原子的，崩溃窗口极小；下次 Load 会发现不一致走 Repair |
| 纯 SSH/无 web 用户管理多 endpoint 要手改 YAML | CLI 向导不支持多 endpoint 管理 | octo-agent web 设置是主推路径；CLI 手改是老结构本就支持的回退 |
| IM `/model` 歧义时静默选第一个 endpoint | 同 model 挂多 endpoint 时裸 model 引用可能不准 | 日志提示；用户可改用复合 id 精确化 |

---

## 21. 决策溯源

本设计的 26 项关键决策通过 grill-me 与用户逐项拍板，决策点 / 选项 / 结论 / 理由的完整记录在对话上下文中。本文档是这些决策的技术展开，不含"待实现阶段确认"项——所有不确定点已在设计阶段闭合（代码可验证事实通过 grep 真实代码确认，真正决策通过 grill-me 与用户拍板）。
