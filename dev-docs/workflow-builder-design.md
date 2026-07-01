# workflow-builder:把技能引导成一条 saved workflow

## 背景

可组合技能三层(见 `composable-skills-design.md`)修通了管道:录制声明 `outputs`、进 L1 清单可被发现、`skill()` 原语把录制 + SKILL.md 串成一条可 resume/定时/并行的 workflow。但还差交互的一层——**用户怎么造出那条 workflow**。

关键判断:**不需要用户手写 mruby。** 模型本就能按一句自然语言描述生成脚本并 `workflow_save`。缺的不是能力,是**可靠性**(模型每次都把接线、干跑、保存做全)和**触发点**(用户知道有这条路、且能唤起它)。

一个必须诚实面对的**非对称**:B 只把**浏览器录制**的 `params`/`outputs` 渲染进了系统提示(`RenderBrowserSkillsManifest`);**SKILL.md 技能在清单里只有 名字 + 描述**(`skills.RenderManifest` 不含 params/outputs——A.4 设想的 frontmatter outputs 目前未实现,`skills.Skill` 连字段都没有)。所以"看清一个技能的输出形状"这件事,录制是清单直给,MD 技能得靠**描述推断 + 调用点 schema**。这直接影响下面第 2 步的措辞,不能含糊。

本方案加一个 `workflow-builder` 默认技能,对齐 octo 既有的"创建类技能"套路(`mcp-creator` / `skill-creator` / `web-artifacts-builder`),把授权流程固化成一段对话脚本。**纯 SKILL.md,不加任何核心代码或新工具**——复用已有的 `skill()` 原语、`workflow_save`、`workflow` 工具。

## 方案

### 形态

一个默认技能 `internal/skills/defaults/workflow-builder/SKILL.md`,`go:embed` 进二进制、materialize 到 `~/.octo/skills-default`,和其它默认技能一样进 L1 清单。触发词:「串个流程 / 把这几个技能连起来 / 做个 workflow / 编排一下 / build a workflow」。

**与 `skill-creator` 划清边界**(两者描述相邻,易误触):workflow-builder = 把**已有的**技能/录制**串成一条可运行的 saved workflow**;skill-creator = **写一个新的 SKILL.md**。两边描述都加上明确的 should-NOT-trigger 提示(builder 不写新技能;skill-creator 不做编排),降低误触/双触。

### 授权流程(SKILL.md 教模型走的步骤)

1. **盘点可用技能** —— 从系统提示的 `# Available skills`(SKILL.md 技能)和 `# Browser recordings`(录制)两段读候选,摆给用户,问要串哪几个、什么顺序。**顺带查重名**:一个名字若同时是录制和 MD 技能,C 的派发会报歧义错——这里就提示用户,并在生成脚本时用 `browser:` / `md:` 前缀消歧。
2. **确认接线**(核心价值,但两类技能强度不同,如实说)——
   - **上游是录制**:清单直给 `outputs`,模型据此提议 `step_i.outputs → step_{i+1}.params` 的映射(如 `dl["files"] → merge 的 inputs`),用户确认/修正。这一档是清单驱动、可靠。
   - **上游是 SKILL.md**:清单没有它的 outputs,模型只能从其**描述 / 正文推断**产出形状,并为该步**提议一个调用点 `schema`**(见 A.4:MD 的结构化输出靠调用点 schema),把推断出的形状交用户确认。这一档是"模型提议、人确认",比录制弱——不要伪装成清单驱动。
3. **生成 Ruby** —— 用 `skill()` 串起来(**产物是 Ruby / mruby,不是 JS**:`skill("name", {"k"=>v}, schema: '…')`、`args["…"]` 读入参),给用户过目。few-shot 例子必须是 Ruby,别继承上游文档里的 JS 写法。
4. **干跑校验(异步,要轮询)** —— `workflow` 工具是**后台执行**:调用返回一个 run id,得 `workflow_status(id)` 轮询到 done 才能判定接线通不通(设个轮询上限,别未完就报成功)。**且干跑会真的执行**:链里含录制,就会在**授权当下**驱动 Chrome——所以要么先要求接上 Chrome(端口 9222),要么只干跑 MD 尾段(用手喂的样例 `files`),录制那步单独用 `run_skill` 验证。
5. **保存 + 指路** —— `workflow_save`(`name` / `scope`)。注意 `scope` 默认 `project` 且**不在 git 仓库里会报"no project root"**——所以先问/判断:仓库内用 `project`,否则用 `user`(`~/.octo/workflows`,跨项目可用)。存好后告诉用户三种跑法:会话点名调、CLI 点名、或用 `schedule` / `cron-task-creator` 配定时。
6. **讲清约束** —— 含录制的 workflow 在 run / cron 时也需要活的 Chrome(headless 环境明确报错);MD 技能要可靠结构化交接就靠调用点 `schema`(第 2 步已提议)。

### 为什么"接线"是重点

第 2 步是把"两个技能怎么拼"从**用户脑内的隐性知识**变成**模型提议 + 用户确认的显式步骤**。对**录制**它是清单驱动的(A 声明 outputs、B 暴露契约,三层合拢);对 **MD 技能**它退化为"模型据描述推断 + 提议调用点 schema、人确认"——仍比让用户空想强,但强度不同,builder 的措辞和 few-shot 要如实区分两者,别对 MD 步骤假装有 outputs 可读。

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

前置:A(#1032)、B(#1038)、C(#1041)均已合入——`skill()` 原语、录制 outputs、清单发现都在,builder 是它们之上的引导层(D)。单个 PR:新增 `internal/skills/defaults/workflow-builder/SKILL.md` + 收紧 `skill-creator` 描述边界 + 一条 `defaults_test` 断言。

已一并订正:`composable-skills-design.md` A.4 原把"MD frontmatter outputs"写成可选增强,但 `skills.Skill` 无该字段、`RenderManifest` 也不渲染——已改为"MD 结构化输出仅靠调用点 schema;frontmatter outputs 未实现"。
