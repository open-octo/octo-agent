# 可组合技能:统一 I/O 契约 + 一份目录 + workflow 管道

## 背景

octo 里有两套"可复用步骤",互不相通:

1. **浏览器录制**(`internal/browser`)—— YAML,存 `~/.octo/browser-skills/*.yaml`,只能经 `browser` 工具的 `run_skill` action 显式点名回放。
2. **普通技能**(`internal/skills`)—— SKILL.md,走 L1/L2 渐进式披露,经 `skill` 工具调用,是模型在会话里能自己想起来用的那种。

当一条真实流水线要横跨两者时——例如「浏览器录制下载 3 个 excel → 技能合并成一张表 → 技能出 PPT」——三个机制缺口叠加,让它跑不顺:

1. **回放无输出交接。** `run_skill` 结束只返回一句 `ran skill "X" (N steps)`(`internal/tools/browser.go:591`)。一个"下载 3 个文件"的录制跑完,agent 拿到的是"跑了几步",拿不到那 3 个文件的路径,下游无从接手。更根本的:回放的动作词表只有 `navigate/wait/click/type/select/upload`,`runStep` 对其它一律 `unknown action`(`internal/browser/skill.go:536`)—— **回放里没有 `download` 步骤**,录制里的"点击导出"只会被当成普通 click,文件即便落盘也无人知晓其路径。
2. **录制不在技能目录里。** 普通技能进 L1 清单,模型会主动想起;浏览器录制只能靠人点名 `run_skill name=`。全仓没有任何地方把这些 YAML 注入清单,所以在普通会话里模型不知道它存在。
3. **两个命名空间。** 两种"技能"是两个物种、两套注册、两种调用方式,天然无法互相衔接。

本方案用一个抽象一次性收掉三者:

> **把"步骤"统一成一个有 I/O 契约的单元;浏览器录制只是 body 为"浏览器回放"的那一种。编排层不新造引擎,直接用已有的 `workflow` 当有类型的管道。**

核心纪律:**统一接口(params in / outputs out),不统一实现(YAML 步骤 vs SKILL.md 各留各的)。** 这是"干净"与"过度统一"的分界线——不去合并两种 body 格式,只对齐它们朝外的那一面。

不做的:
- **不合并 YAML 与 SKILL.md 为单一格式。** 两种 body 的编辑体验、录制/自愈机制完全不同,强并只会两头别扭。只共享目录与契约。
- **不为编排造新引擎。** `workflow` 已有 `pipeline/parallel/args/命名保存/journal 续跑`,是现成的强编排器。本方案只给它补一个"确定性调用技能"的绑定,不引入第二套 DSL。
- **不做输出的富类型系统。** 契约类型只有 `file` / `file[]` / `string`(见下)。够表达文件与标量交接;JSON schema 级的复杂类型等真出现需求再说。

---

## A. 契约:技能声明 `outputs`

这是整个方案唯一真正的地基。没有它,任何交接都只能靠"约定固定目录"这种脆弱手段。

### A.1 浏览器录制:`outputs` + `download` 步骤

`Skill` 结构(`internal/browser/skill.go:17`)加一个与 `Params` 对称的 `Outputs` 字段;`Step` 加一个 `Bind` 字段,把某步的产物绑定到具名 output。

```go
type Skill struct {
    Name        string   `yaml:"name"`
    Description string   `yaml:"description,omitempty"`
    Params      []Param  `yaml:"params,omitempty"`
    Outputs     []Output `yaml:"outputs,omitempty"` // 新增
    Steps       []Step   `yaml:"steps"`
}

// Output 是回放对外暴露的一个具名产物。Type 决定聚合方式:
//   file   —— 单个下载文件的路径(最后一次 bind 到它的 download)
//   file[] —— 多个下载文件路径,按 bind 顺序追加
//   string —— 一次 extract 步骤抓取的文本
type Output struct {
    Name string `yaml:"name"`
    Type string `yaml:"type"` // file | file[] | string
}
```

```go
type Step struct {
    Action   string `yaml:"action"` // 增加 download | extract
    // ...现有字段不变...
    Bind     string `yaml:"bind,omitempty"`  // 新增:把本步产物写入该 output
    JS       string `yaml:"js,omitempty"`    // 新增:extract 步骤的取值表达式
}
```

对应 YAML 形态:

```yaml
name: download-excels
params:
  - name: month
outputs:
  - name: files
    type: file[]
steps:
  - action: navigate
    url: https://report.internal/{{month}}
  - action: download
    selector: 'button:has-text("导出订单")'
    bind: files
  - action: download
    selector: 'button:has-text("导出退款")'
    bind: files
  - action: download
    selector: 'button:has-text("导出结算")'
    bind: files
```

### A.2 回放执行:补 `download`/`extract`,回传 outputs

`runStep`(`internal/browser/skill.go:440`)的 `switch step.Action` 增加两个 case,复用工具层已有能力:

- **`download`** —— 用 `Browser.CaptureDownload`(`internal/tools/browser.go:470` 已在交互式 `download` action 用它)包住对 `selector` 的点击,拿到落地路径,按 `step.Bind` 追加进对应 output 的收集器。下载目录沿用 `downloadDir()` 的配置(`Browser.DownloadDir`,缺省落 temp)。
- **`extract`** —— `page.Eval(step.JS)` 求值,结果字符串写入 `step.Bind` 指向的 `string` output。给"抓一个报表 ID / 状态文本再传给下游"留口子。

`ReplaySkill`(`internal/browser/skill.go:393`)签名从

```go
func ReplaySkill(...) (modified bool, finalPage *Page, err error)
```

改为多返回一个产物表:

```go
func ReplaySkill(...) (modified bool, finalPage *Page, outputs map[string]any, err error)
```

`outputs` 按 `skill.Outputs` 的声明聚合:`file` → `string`,`file[]` → `[]string`,`string` → `string`。未被任何步骤 `bind` 的 output 缺省为空值(空串 / 空数组),不报错——录制可以只声明、暂不绑定。

### A.3 `run_skill` 回传结构化 envelope

`internal/tools/browser.go` 的 `run_skill` case(`internal/tools/browser.go:563`)从只返回一行文字,改为返回结构化结果:

```json
{ "skill": "download-excels", "steps": 5,
  "outputs": { "files": ["/dl/order.xlsx", "/dl/refund.xlsx", "/dl/settle.xlsx"] } }
```

`ToolResult.Text` 放这段 JSON——模型/脚本可解析,这就是产物交接的载体。自愈发生时(`modified`)照旧回写 YAML,并在 envelope 里加 `"self_healed": true`(不再往 JSON 后面拼自然语言,以免破坏可解析性)。

`ToolResult.UI` 富卡片**不做**(可选的后续增强):browser 工具其它 action 也都不出卡片,纯 JSON text 在 tool-result 区块本就可读,组合能力不依赖它。只有当 run_skill 成为"用户会长时间盯着看 outputs"的高频交互面时,再据实加一个列出产物的卡片(有 `uiHead`/`uiTail` helper,`project_tool_ui_payload`)。

### A.4 普通技能:outputs 可选,CC 全兼容

SKILL.md 这侧**不加任何必填字段**——库存的 Claude Code 技能拿过来即插即用。它的结构化输出走 `workflow` 里 `agent(prompt, schema:)` 已有的 StructuredOutput 机制,而 schema **可选、且首选放在调用点**,skill 文件一个字不改:

1. **无 schema** —— 照跑,返回子 agent 的最终文本(`string`)。等价于 `agent()` 不带 schema。
2. **调用点带 schema**(首选) —— `skill(name, params, {schema})` 起子 agent 时强制该 schema,返回校验过的对象。skill 文件保持纯 CC 形态,由 workflow 作者决定自己要的返回形状。
3. **frontmatter 声明 outputs**(可选增强) —— octo 里自写的技能愿意的话可在 frontmatter 加 `outputs` schema 当默认,调用点就不用每次带。它是**额外 key,CC 解析时忽略**,所以带了它的技能仍能原样搬回 Claude Code。

优先级:**调用点 schema > frontmatter outputs > 无(返回文本)**。这不是新机制,就是 `agent(prompt, {schema})` 的那套。

这带来一处与录制的**声明位置不对称**(对调用方透明):YAML 录制的 outputs 只能写在文件里(`download` 步的 `bind` 是引擎结构性绑定,没有"调用点 schema"能替代);MD 技能的 outputs 首选调用点、frontmatter 声明是锦上添花。两个引擎因此在"声明位置"不同,但对 `skill()` 的调用者行为一致、可预期。

### 验证 / 兼容

- 新增 `internal/browser/skill_test.go` 用例:一个含 `download` + `bind: file[]` 的技能,用 `httptest` 造一个触发下载的页面,断言 `ReplaySkill` 回传的 `outputs["files"]` 是两个路径。`extract` 同理断言取回的字符串。
- 无 `outputs` 字段的旧录制:`Outputs` 为 nil,`ReplaySkill` 回传空 map,`run_skill` 的 JSON 里 `outputs: {}`。行为等价于今天,**完全向后兼容**。
- `ReplaySkill` 签名变更是包内改动(调用点只有 `internal/tools/browser.go` 一处),不越 `provider → agent` 依赖边界。

---

## B. 目录:一份清单,不分物种

让浏览器录制和普通技能出现在同一份 L1 披露里,模型(以及写 workflow 的人)在一个地方看到所有可用步骤。

- 扫 `BrowserSkillsDir()`(`internal/tools/browser.go:96`)下的 `*.yaml`,解析出 `name / description / params / outputs`,作为一类条目并入技能清单的构建(`internal/skills` 的 manifest 组装处)。
- 清单里明确标注类型与调用方式,例如:
  `download-excels (browser skill) — 下载指定月份的 3 张报表;params: month;outputs: files(file[]);经 run_skill 调用`。
- 只暴露元信息(名字/描述/参数/产物),不展开 YAML 步骤——步骤是 L2 细节,回放时才读。

这样在普通会话里,用户说"把上个月的报表拉下来并出 PPT",模型能自己在清单里看到这条录制并决定 `run_skill`,而不必被人点名。

### 验证 / 兼容

- 清单条目数在有/无录制两种情况下的断言;录制目录不存在时静默跳过(与今天 `BrowserSkillsDir` 缺省行为一致)。
- 纯增量:不改任何现有技能的披露格式。

---

## C. 管道:`skill()` workflow 原语

`workflow` 是现成的强编排器(`pipeline/parallel/args/log/命名保存/journal 续跑`)。要把 A/B 变成一条可反复跑、可 resume 的流水线,只缺一个能**确定性调用技能并拿回 outputs** 的脚本原语——不经 LLM、零 token(录制回放本就是确定性的),需要判断的步再交给 `agent()`。

### 新增 prelude 原语 `skill()`

与 `agent()` 对仗——都是"跑一个步骤单元":`agent()` 跑一个 LLM 步,`skill()` 跑一个技能步。它**不是模型面的 tool**,只在 workflow 脚本里可调;排序由脚本的确定性控制流决定,派发本身不花 LLM 回合。

```
skill(name, params = {}, opts = {})   →  该技能声明的 outputs 对象(直接返回,不包 envelope)

  按 B 层统一目录解析 name:
    · name 是 YAML 录制  → 跑 ReplaySkill,确定性,0 LLM(失败才 heal)
    · name 是 SKILL.md   → 起 skill 子 agent(opts.schema 可选,见 A.4)
```

**名字全目录唯一**:`skill('x')` 只解析到一个条目;一个 YAML 与一个 MD 重名则报错要求消歧,不猜。逃生口(可选):`skill('browser:download-excels')` / `skill('md:merge-excels')` 显式指定引擎。

`skill()` 复用 `agent()` 的整套异步机制:`__skill_start` 是**唯一新增的 host import**,拿回结果走的还是 `agent_wait_any`/`agent_take` 同一条完成队列——所以 `skill()` 在 `parallel`/`pipeline` 里和 `agent()` 一样并发、一样可 resume(同一 journal),无需新的 take/wait 原语。Go 侧 host 函数按名字查统一目录,分派到 `ReplaySkill`(录制)或 skill 子 agent(SKILL.md),把 outputs 序列化成 JSON 回传;prelude `JSON.parse` 成原生 Ruby `Hash`(JSON 在脚本内已可用,见 `workflow-named-args-design.md`)。

**产物恒为合法 JSON,失败则 raise**:录制回outputs、带 schema 的 SKILL.md 结果、无 schema 的自由文本(JSON 编码成字符串)三者都是合法 JSON;一旦某步失败,回传的是错误串而非 JSON,`skill()` 就地 `raise` 中止整条管道,而不是把坏值喂给下游。浏览器录制串行化在同一个 Chrome 会话上(单会话资源,`parallel` 里的多个录制步骤自动排队)。

### 你的三段式,一条管道

工作流脚本是 Ruby(mruby),`skill()` 返回原生 `Hash`:

```ruby
# .octo/workflows/monthly-report — 保存后可命名调用
dl  = skill("download-excels", { "month" => args["month"] })          # 浏览器录制,确定性 → {"files"=>[...]}
tbl = skill("merge-excels", { "inputs" => dl["files"] },              # SKILL.md,调用点带 schema → {"path"=>...}
            schema: '{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}')
ppt = skill("excels-to-ppt", { "table" => tbl["path"] })              # SKILL.md → {"path"=>...}
ppt
```

`dl["files"]` 直接喂下一步——这就是"顺畅"。三种跑法都免费得到:

- **会话里一句话**:"跑 monthly-report,月份 2026-06" → 模型调 `workflow` 命名工作流。
- **CLI / 定时**:点名这个 saved workflow 配 cron(`scheduler` 已有)。
- **单跑某一步**:它在目录里仍是普通技能,`run_skill` / `skill` 照旧。

并行/去重等复杂形态直接吃 workflow 已有原语,例如按月并行下载:

```ruby
all = parallel(args["months"]) { |m| skill("download-excels", { "month" => m }) }
```

### 验证 / 兼容

- `internal/workflow/runtime_test.go`:用假 `SkillFunc` 打通原语端到端——round-trip(参数/名字透传)、`pipeline` 内组合、无 `Skill` 时报错、失败 `raise`。
- `internal/tools/workflow_skill_test.go`:真派发逻辑——`browser:`/`md:` 前缀、未找到、重名歧义、MD 子 agent(有/无 schema 的回传形态)。
- `skill()` 是新增原语,不改 `agent/parallel/pipeline` 语义,旧脚本不受影响。
- 随该原语重生了 `mruby.wasm`(新增 `skill_start` host import),流程同 `workflow-named-args-design.md`。

---

## 为什么这个方案可扩展

扩展点全部退化为"往统一目录里加一个声明了 `outputs` 的技能":

- 新增一个爬数录制 → 加个 YAML,`skill()` 立刻可用,清单里立刻可见。
- 某步想从"浏览器录制"换成"直接调 API / MCP" → workflow 里那一行换掉,上下游契约不变。
- 步骤之间是 Unix 管道式松耦合(载荷只是 `file[]` / `string`),没有任何步骤知道另一步的内部实现。

抽象只有一层——"有 I/O 契约的 step"——且这一层是三段式真实流水线逼出来的,不是预留的。

---

## 落地顺序

核心与外壳分开,按价值递减推进,每步独立可用:

1. **A(契约)** —— `outputs` + 回放 `download`/`extract` 步骤 + `run_skill` 回传 envelope。做完这一步,即便还没有 workflow 绑定,在一次会话里手动串三步也已经能干净交接(模型从上一步的 JSON outputs 里读出文件路径喂下一步)。这是约 80% 的价值。
2. **B(目录)** —— 录制并入 L1 清单,让会话里自动发现。
3. **C(管道)** —— `skill()` workflow 原语,把流水线固化成一个 saved workflow,获得 resume / cron / 并行。
