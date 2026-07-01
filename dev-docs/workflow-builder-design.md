# workflow-builder:把技能引导成一条 saved workflow

## 背景

可组合技能三层(见 `composable-skills-design.md`)修通了管道:录制声明 `outputs`、进 L1 清单可被发现、`skill()` 原语把录制 + SKILL.md 串成一条可 resume/定时/并行的 workflow。但还差交互的一层——**用户怎么造出那条 workflow**。

关键判断:**不需要用户手写 mruby。** B 已把所有技能(名字 / params / outputs)暴露进系统提示,所以模型本就能按一句自然语言描述生成脚本并 `workflow_save`。缺的不是能力,是**可靠性**(模型每次都把接线、干跑、保存做全)和**触发点**(用户知道有这条路、且能唤起它)。

本方案加一个 `workflow-builder` 默认技能,对齐 octo 既有的"创建类技能"套路(`mcp-creator` / `skill-creator` / `web-artifacts-builder`),把授权流程固化成一段对话脚本。**纯 SKILL.md,不加任何核心代码或新工具**——复用已有的 `skill()` 原语、`workflow_save`、`workflow` 工具。

## 方案

### 形态

一个默认技能 `internal/skills/defaults/workflow-builder/SKILL.md`,`go:embed` 进二进制、materialize 到 `~/.octo/skills-default`,和其它默认技能一样进 L1 清单。触发词:「串个流程 / 把这几个技能连起来 / 做个 workflow / 编排一下 / build a workflow」。

### 授权流程(SKILL.md 教模型走的步骤)

1. **盘点可用技能** —— 从系统提示的 `# Available skills`(SKILL.md 技能)和 `# Browser recordings`(录制)两段读候选,把名字、`params`、`outputs` 摆给用户,问要串哪几个、什么顺序。*(依赖 B。)*
2. **确认接线** —— 这是本技能的核心价值:模型**提议** step_i 的 `outputs` 如何喂 step_{i+1} 的 `params`(如 `dl["files"] → merge 的 inputs`),请用户确认或修正,而不是让用户自己在脑子里拼。*(依赖 A 的 `outputs` 声明。)*
3. **生成 Ruby** —— 用 `skill()` 串起来,输入走 `args` 原语参数化(便于复用与定时),把脚本给用户过目。
4. **干跑校验** —— 先用 `workflow` 工具跑一次(必要时用小样例)验证接线真能跑通;失败按报错修再跑。
5. **保存 + 指路** —— `workflow_save`(`name` / `scope`),然后告诉用户三种跑法:会话里点名调、CLI 点名、或用 `schedule` / `cron-task-creator` 配定时。
6. **讲清约束** —— 含浏览器录制的 workflow 需要一个**活的 Chrome**(headless / cron 环境里会明确报错);MD 技能要可靠的结构化交接,就在调用点带 `schema`(见 `composable-skills-design.md` A.4)。

### 为什么"接线"是重点

第 2 步是把"两个技能怎么拼"从**用户脑内的隐性知识**变成**模型提议 + 用户确认的显式步骤**。它之所以能成立,正是因为 A 让每个技能声明了 `outputs`、B 把这些契约暴露在了系统提示里——三层在这一步合拢,builder 只是把它们用起来。

## 不做的

- **不加新工具 / 新核心代码** —— 纯 SKILL.md,复用 `skill()` / `workflow_save` / `workflow`。一个"创建类技能"不该往二进制里塞逻辑。
- **不自动保存** —— 保存是显式一步(用户确认 `name` / `scope`),绝不偷偷写盘。
- **不做可视化拖拽编排** —— web 面板的 "New workflow" 按钮是可选的后续,不在本方案。
- **不校验业务语义** —— 干跑只验证"接线能跑通",不保证流程逻辑对;这是用户的判断。

## 与 save-nudge 的分工(可选后续)

两条引导路径互补:

- **workflow-builder** 管「我想造一条流程」—— 主动、从零编排。
- **save-nudge**(复用现有 nudge injector)管「我刚在会话里手动串过一次,帮我收成 workflow」—— 被动、抓已发生的时刻:当模型一个 turn 内连续成功调了 ≥2 个 `skill` / `run_skill`,同 turn 提示一句"要存成 saved workflow 吗"。

本方案只做 builder;nudge 作为紧跟的小增强,单独评估。

## 验证

- **可发现** —— materialize 后 `skills.Discover` 能列出 `workflow-builder`;`internal/skills/defaults_test.go` 加一条断言它在默认集里(纯指令技能没有单元逻辑可测,保证被打包 + 可发现即可)。
- **端到端(手动)** —— 一次会话说「把 download-excels、merge-excels、excels-to-ppt 串成 monthly-report」,验证模型走完 盘点 → 接线确认 → 生成 → 干跑 → 保存,产物落在 `.octo/workflows/monthly-report.rb`,且 `workflow name=monthly-report` 能跑通。

## 落地

单个 PR:新增 `internal/skills/defaults/workflow-builder/SKILL.md` + 一条 `defaults_test` 断言。属于可组合技能特性的引导层(D),独立于 A/B/C 的管道实现。
