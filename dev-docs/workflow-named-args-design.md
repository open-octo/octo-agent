# Workflow:命名工作流 + args 参数化 + 脚本内 JSON

## 背景

`internal/workflow` 已经能跑 `agent / parallel / pipeline / log / phase / budget_remaining`,`schema` / `isolation: worktree` / 后台运行 / journal 续跑都在。对照 Claude Code 的 workflow,还差三块,本方案一并补齐:

1. **脚本无法参数化** —— 脚本是一段 Ruby 文本,所有输入只能写死进 `script`。没有等价于 CC `args` 全局的入口。
2. **没有命名/可复用工作流** —— 每次都重发整段脚本,无法把一条流水线存成一个可反复调用的单元。
3. **脚本内拿不到结构化数据** —— mruby 的 gembox 是 `stdlib`,**不含 JSON**。`agent(prompt, schema:)` 子 agent 返回的是 JSON 串,脚本里却 `JSON.parse` 不了,只能做字符串处理。这让「找→对抗式验证→去重→综合」这类真正吃结构化结果的流水线写不顺。

三块互相咬合:命名工作流要复用就必须能传 `args`;`args` 和 schema 结果要好用就得有 JSON。所以一起做。

不做的:脚本内联 `workflow(name, args)` 嵌套调用(octo 每次 Run 是独立 wazero VM,内联嵌套要起子 VM、共享预算/并发,复杂度不成比例)。复用通过**工具层** `name` 参数实现,够用。`budget` 富对象(`total/spent`)、per-agent `label/effort/agentType` 不在本期。

---

## A. 脚本内 JSON(重建 mruby.wasm 加 mruby-json)

### 改动

- `internal/workflow/mruby/build_config.rb`:在 cross build 的 gem 列表里加
  ```ruby
  conf.gem core: 'mruby-pack'        # JSON 依赖的字节打包(若 stdlib 未含)
  conf.gem github: 'mattn/mruby-json' # JSON.parse / JSON.generate
  ```
  `mattn/mruby-json` 是纯 C、解析成 mruby `Hash`/`Array`/`String`/数值,与 `MRB_NO_STDIO` 兼容。构建时由 minirake 拉取(`scripts/build-mruby-wasm.sh` 已联网装 wasi-sdk,这步一致)。
- 重生 `mruby.wasm`:`scripts/build-mruby-wasm.sh`,提交新的 `internal/workflow/mruby.wasm`(go:embed 工件,平台无关,单文件)。不进 `make build`/CI 常规链路,仅升级 mruby/改 ABI 时手动重生。
- `prelude.rb`:JSON 现在可用,无需改原语,只更新 `agent()` 文档注释——`schema:` 的返回值仍是 JSON 串,但脚本现在可以 `JSON.parse(agent(..., schema: S))`。

### 验证 / 兼容

- 新增 EH/JSON 烟测(`runtime_test.go`):脚本内 `JSON.parse('{"a":[1,2]}')["a"].size == 2`,守护 wasm 里 JSON 可用且不回归。
- 工件级改动,回滚 = `git revert` 该 `.wasm`。ABI 不变,旧脚本行为不变。

---

## B. args 参数化

### 数据流

`workflow` 工具的 `args`(任意 JSON 值)→ 序列化成 JSON 串 → `WorkflowRunRequest.Args` → `workflow.Options.Args` → host import `env.args` → prelude `args` 方法 → 脚本里是原生 Ruby `Hash`/`Array`/标量。

走 host import(而非把 JSON 拼进源码)避免 Ruby 字符串字面量转义的坑,边界与现有 `agent_take` 一致。因为 A 已经要重建 wasm,加一个 import 是边际成本。

### 改动

**Go 侧 `internal/workflow/runtime.go`**
- `Options` 加 `Args string`(原始 JSON,空 = 无)。
- `backend` 存 `args string`;新增 host 函数
  ```go
  func (b *backend) argsJSON(_ context.Context, mod api.Module, outPtr, outCap uint32) uint32
  ```
  把 `b.args` 写进 guest buffer(与 `agentTake` 同款写法),返回长度。
- `register()` 加 `.NewFunctionBuilder().WithFunc(b.argsJSON).Export("args")`。

**Guest 侧 `internal/workflow/mruby/runtime.c`**(随 A 一起重建)
- 声明 `import_name("args")` 的 `host_args(char *out, int outcap)`。
- `m_args` 方法(仿 `m_agent_take`,16MiB 缓冲),`mrb_define_method(mrb, k, "__args", m_args, MRB_ARGS_NONE())`。

**`internal/workflow/prelude.rb`**
```ruby
# args returns the workflow's input value (whatever was passed as the tool's
# `args`), parsed from JSON into native Ruby (Hash/Array/scalar). nil when none.
def args
  @__wf_args ||= begin
    s = __args
    s.nil? || s.empty? ? nil : JSON.parse(s)
  end
end
```

**工具侧 `internal/tools/workflow.go`**
- `Definition()` 的 `properties` 加 `args`(`type: object`,描述「传给脚本 `args` 的输入值」)。
- `Execute()`:`argsVal := input["args"]` → `json.Marshal` → `WorkflowRunRequest.Args`。

**`internal/tools/workflow_manager.go`**
- `WorkflowRunRequest` 加 `Args string`;`Start()` 透传到 `workflow.Options{Args: req.Args}`。

### 续跑一致性

`args` 改变会改变脚本控制流,缓存的 agent 结果将错位。把 `args` 纳入 journal 身份:`scriptHash` 改为 `hash(script + "\x00" + args)`(或并列存一个 argsHash)。`resume_from` 时 args 不一致 → 复用现有的「different script」报错路径,提示去掉 `resume_from` 重跑。

---

## C. 命名工作流 + 保存

### 注册表(照抄 `.octo/agents/` 约定)

新文件 `internal/tools/workflow_registry.go`,镜像 `agents.go`:
- `userWorkflowsRoot()` = `~/.octo/workflows`
- `projectWorkflowsRoot()` = `<project-root>/.octo/workflows`
- 扫 `*.rb`,**项目级覆盖用户级**(同名)。
- 元信息用文件开头的注释行(`.rb` 没有干净的 frontmatter):
  ```ruby
  # @description Find bugs, adversarially verify, dedupe, then synthesize a report
  ```
  `name` 默认取文件名(去 `.rb`);`description` 取 `@description` 行,没有则取首行 `#` 注释。脚本主体是**文件全文**(`# @description` 是合法 Ruby 注释,重跑无副作用,无需剥离)。

### 工具表面

**`workflow` 工具加 `name` 参数**
- `name`:运行某个已保存的工作流(从注册表加载脚本)。
- `script` 与 `name` **二选一**:都给/都不给 → 报错。给 `name` 时忽略 `script`,加载注册表脚本,`args` 照常传入。
- `name` 参数的 description 在 `Definition()` 里**动态列出**当前注册表里可用的工作流名 + 描述(扫盘一次,仿 agent preset 的暴露方式),让模型知道有哪些可调。

**新工具 `workflow_save`**
```
workflow_save(name, script, description?, scope?)
  name:        kebab-case 标识,作为 <name>.rb 文件名
  script:      Ruby 脚本主体
  description: 写进 @description 注释行
  scope:       "project"(默认,写 <root>/.octo/workflows/)| "user"(写 ~/.octo/workflows/)
```
- 校验 `name` 合法(kebab,无路径分隔符)。
- 文件已存在则覆盖并在返回里说明(工具不能交互确认);返回写入的绝对路径。
- 落盘内容 = `# @description ...\n\n` + script。

### 注意

- 注册表只读脚本文本,**不缓存**——每次 advertise/run 都扫盘,保证手写文件即时生效(与 agents 一致)。
- 命名工作流仍走完整 journal/后台/kill 链路,与内联 `script` 无差别。

---

## 端到端示例

`.octo/workflows/bug-hunt.rb`:
```ruby
# @description 多 lens 找 bug → 三票对抗式验证 → 去重 → 综合报告

require 'json'
BUG_SCHEMA = '{"type":"object","properties":{"bugs":{"type":"array",...}}}'
VERDICT    = '{"type":"object","properties":{"refuted":{"type":"boolean"}}}'

target = args["target"]            # ← 由 workflow(name:"bug-hunt", args:{target:"internal/agent"})

# 找(barrier)
phase "find"
raw = parallel(%w[correctness security perf]) { |lens|
  JSON.parse(agent("从 #{lens} 角度审计 #{target} 找 bug", schema: BUG_SCHEMA))["bugs"]
}.flatten

# 去重(纯 Ruby)
require 'set'
seen = Set.new
fresh = raw.reject { |b| !seen.add?("#{b['file']}:#{b['line']}") }

# 对抗式验证(每个 finding 内嵌 parallel 三票)
phase "verify"
confirmed = parallel(fresh) { |b|
  votes = parallel([1,2,3]) { |_|
    JSON.parse(agent("尝试反驳:#{b['desc']}。拿不准判 refuted。", schema: VERDICT))["refuted"]
  }
  votes.count { |r| !r } >= 2 ? b : nil
}.compact

# 综合
phase "synth"
agent("把这些确认的 bug 写成报告:#{JSON.generate(confirmed)}")
```
调用:`workflow(name: "bug-hunt", args: {target: "internal/agent"})`。

---

## 文件改动清单

| 文件 | 改动 |
|---|---|
| `internal/workflow/mruby/build_config.rb` | 加 `mruby-json`(+ 必要的 `mruby-pack`) |
| `internal/workflow/mruby/runtime.c` | 声明 `env.args` import + `m_args` 方法 + `__args` 绑定 |
| `internal/workflow/mruby.wasm` | 重生并提交(go:embed 工件) |
| `internal/workflow/runtime.go` | `Options.Args`、`backend.args`、`argsJSON` host 函数、`register()` 加 export、`scriptHash` 纳入 args |
| `internal/workflow/prelude.rb` | `def args`;更新 `agent()` schema 注释提示可 `JSON.parse` |
| `internal/tools/workflow.go` | `args` + `name` 参数;`name`/`script` 二选一;`name` 动态列举;序列化 args |
| `internal/tools/workflow_manager.go` | `WorkflowRunRequest.Args` 透传 |
| `internal/tools/workflow_registry.go` | **新增** 注册表(镜像 `agents.go`) |
| `internal/tools/workflow_save.go` | **新增** `workflow_save` 工具 |
| `internal/app/*`(WireTools) | 注册 `workflow_save` 工具(仅 Spawner 在场时,与 `workflow` 一致) |

---

## 测试计划

- `runtime_test.go`:`args` 投递(传 JSON → 脚本 `args["k"]` 读到)、`args` 为空时 `args` 返回 `nil`、脚本内 `JSON.parse`/`JSON.generate` 烟测。
- `journal_test.go`:同脚本不同 `args` 的 `resume_from` 报「不一致」。
- `workflow_registry_test.go`:用户级/项目级优先级、`@description` 解析、按 `name` 加载、未知 `name` 报错。
- `workflow_save_test.go`:写盘路径、scope 切换、name 校验、覆盖行为。
- `workflow_test.go`:`name` 与 `script` 同给/同缺报错;`name` 跑通注册表脚本。

---

## 分期(三个独立可合 PR)

1. **PR1 — JSON**:`build_config.rb` + 重生 `mruby.wasm` + prelude 注释 + JSON 烟测。自包含,先落地解锁结构化脚本。
2. **PR2 — args**:host import `env.args` + `runtime.c`(随 PR1 的 wasm 重生一起,或 PR2 再重生一次)+ `Options.Args` + 工具 `args` 参数 + 续跑身份。
3. **PR3 — 命名工作流**:注册表 + 工具 `name` 参数 + `workflow_save` 工具 + WireTools 注册。

> 实际实现:把 `env.args` import(PR2 的 C 部分)与 PR1 的 `mruby-json` 一起放进**同一次** wasm 重生,所以全程只重生一次。PR2/PR3 之后均为纯 Go/prelude/工具侧改动,不再动 `runtime.c`。

---

## 兼容性与回滚

- `args` / `name` 均为可选参数,不传 = 旧行为,既有脚本与调用不受影响。
- `mruby.wasm` 回滚 = `git revert` 二进制。新增 `env.args` import 对旧脚本无影响(不调用即不触发)。
- `CGO_ENABLED=0` / 单 Linux runner 6 目标交叉编译链路不变(wazero 纯 Go,wasm 是静态工件)。
