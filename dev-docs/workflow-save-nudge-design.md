# workflow save-nudge:手动串了几个技能后,建议存成 workflow

## 背景

`workflow-builder`(可组合技能 D 层)管「我想造一条流程」——主动编排。但用户常常是**先在会话里手动把几个技能跑通**,事后才意识到能固化。save-nudge 抓这个时机:一个 turn 内手动串了 **≥2 个技能**,就同 turn 提示一句"这条流程可以存成 saved workflow 复用"。它是 builder 的被动补充。

octo 已有同形的机制可仿:`internal/memory/injector.go` 的 `SaveNudge` 是一个 **PostToolUse hook**,在里程碑命令(`gh pr create/merge`)后把一段 `<system-reminder>` 追加到该工具结果上,**每 turn 至多一次**(`UserPromptSubmit` 重新武装),且走 `<system-reminder>` 惯例被 UI 层剥离。workflow 版沿用这套骨架,只换触发信号与文案。

## 方案

### 触发信号(核心决策:什么算"手动串技能")

一个 turn 内调用了 **≥2 个不同技能**(按名字去重),计入两类工具调用:

- `skill` 工具 —— 加载一个 SKILL.md 技能(`input["name"]`);
- `browser` 工具 `action=run_skill` —— 回放一个录制(`input["name"]`)。

两类都算:旗舰流程(下载录制 + 合表 MD + 出 PPT MD)正好横跨录制与 MD。

**排除 / 抑制**:

- 排除 `workflow-builder` 自身(用户已经在造 workflow,别自我提示)。
- 本 turn 若已出现 `workflow` 或 `workflow_save` 调用,则**整个 turn 抑制**——他们已经在做了。

阈值固定 2,不做配置(YAGNI)。只看**单 turn 内**的串联,不跨 turn 累计。

### 机制(仿 memory injector,但独立)

不塞进 `internal/memory`(concern 不同)。新增一个小的、per-session 有状态的 nudger(落在 `internal/tools`,和 skill/workflow/browser 工具同包,能读工具名与输入):

- **状态**:本 turn 见过的技能名集合 `seen`、是否已出现 workflow(_save) 的 `suppressed` 标志、`nudged` latch。
- **PostToolUse hook**:
  - `toolName == "workflow" | "workflow_save"` → 置 `suppressed`。
  - `toolName == "skill"` 或(`toolName == "browser"` 且 `input["action"]=="run_skill"`)→ 取 `input["name"]`,非 `workflow-builder` 就加入 `seen`。
  - 若 `!suppressed && !nudged && len(seen) >= 2` → 置 `nudged`,返回 `<system-reminder>` 文案。
- **UserPromptSubmit hook**:清空 `seen`、`suppressed`、`nudged`(每 turn 重新武装)。
- 串行调用(同 memory injector,来自 agent run loop),latch 无需加锁。

复用 `<system-reminder>` 包裹 → 现有 `StripRemindersForDisplay` 自动把它挡在 UI 之外(模型看得到、用户面板看不到),与 memory nudge 一致。

### 提示文案(要点)

`<system-reminder>` 大意:本 turn 你已运行了多个技能。如果这是一条**可复用**的流程,可以用 `workflow-builder` 技能引导把它串成一个 saved workflow,或直接 `workflow_save` 存下来,以后点名 / 定时复跑。若只是一次性操作,忽略即可。措辞要克制——噪音会训练模型无视提示(memory nudge 的同款告诫)。

### 三端接线(parity)

和 memory injector 一样,在三处 per-session hook engine 上注册,一个 helper 三处共用(`tools.NewWorkflowNudger().RegisterHooks(engine)`):

- CLI —— `cmd/octo/chat.go`(`memory.NewInjector(...).RegisterHooks(hookEngine)` 旁)。
- Web —— `internal/server/server.go`(`injectorFor(sess.ID).RegisterHooks(hookEngine)` 旁)。
- IM —— `internal/server/server.go`(`injectorFor("im:"+…).RegisterHooks(imEngine)` 旁)。

生命周期(两种,状态都是纯 per-turn,结果等价):**CLI** 整个会话一个 engine + 一个 nudger,靠 `UserPromptSubmit` 的 `reset()` 每 turn 重新武装。**Web/IM** 每 turn 重建 engine 并 `NewWorkflowNudger()`,新实例 latch 本就是零值,`reset()` 冗余但无害,per-turn 新鲜性天然隔离、不跨会话泄漏。注意这与 memory injector 不同——injector 经 `injectorFor(key)` 会话粘连(有 recall latch 要跨 turn 保),nudger 无跨 turn 状态,故不必粘连。

## 不做的

- 不加新工具;只是一个 hook + 小状态类型。
- 不自动保存,只提示(保存仍走 workflow-builder / workflow_save 的显式确认)。
- 阈值不可配、不跨 turn 累计。
- 已在用 workflow 的 turn 不提示。
- 不与 memory save-nudge 合并——触发信号与文案都不同,合并只会互相耦合;两者可在同一 turn 各自独立触发(一个盯 `gh pr`,一个盯技能串联),都走 PostToolUse + 每 turn 一次 + `<system-reminder>` 惯例。

## 验证

单测 nudger(纯状态机,无需真工具):

- 1 个技能 → 静默;2 个**不同**技能 → 提示一次;第 3 个 → 不重复。
- 同名技能两次 → 只算一个,不触发。
- `UserPromptSubmit` 重置后 → 再次武装。
- 计入 `browser` 仅当 `action==run_skill`(普通 `browser` navigate/click 不算)。
- 排除 `workflow-builder`;本 turn 出现 `workflow_save` → 全程抑制。

三端各加一处注册(编译期保证);CLI 路径可加一条集成味断言。

## 落地

单个 PR:`internal/tools/workflow_nudge.go`(nudger + `RegisterHooks`)+ 三处 wiring + 单测。前置:workflow-builder(#1045,已合)——文案要指向它。
