---
title: "Loop Engineering 系列（一）：从 Prompt 到 System"
description: "当 AI 不再只是回答一次问题，而是持续地、可回滚地、可观测地运行一个工作流——这就是 Loop Engineering。"
pubDate: 2026-07-03
author: "octo-agent team"
tags: ["loop-engineering", "ai-agent", "octo-agent", "workflow"]
locale: zh
originalSlug: loop-engineering-intro
---

# Loop Engineering 系列（一）：从 Prompt 到 System

> 当 AI 不再只是回答一次问题，而是持续地、可回滚地、可观测地运行一个工作流——这就是 Loop Engineering。本文为 octo-agent 官方技术系列第一篇。

---

## 什么是 octo-agent

octo-agent 是一个**面向工程实践的本地 AI 智能体平台**。它的目标不是做一个更聪明的聊天机器人，而是让 AI 能够稳定、可观测、可回滚地执行重复性工程任务。

与通用 ChatGPT 客户端或单次性的代码生成工具不同，octo-agent 强调：

- **本地优先**：运行在本机，代码和状态都在本地仓库；
- **工作流化**：复杂任务通过 `workflow` 脚本编排，而不是靠一次 prompt 的运气；
- **可重复**：同一个 workflow 可以反复运行，每次行为可控；
- **可观察**：`.octo/` 目录集中保存所有循环状态、日志和运行历史；
- **渐进式**：通过 L1 / L2 / L3 三个阶段，让自动化从「只读」逐步走向「执行」。

它同时提供了 `skill` 系统来沉淀最佳实践、通过 MCP 协议接入外部工具，并通过 `sub_agent` 支持多智能体协作。Loop Engineering 正是建立在 octo 这些能力之上的一种工作模式。

---

## 什么是 Loop Engineering

传统方式：你打字 → AI 回复 → 你检查 → 再打字。每一次任务都需要你主动发起、主动跟进、主动收尾。

Loop Engineering 则是：你设计一个系统，让它自己发现任务、派给子 agent、验证结果、记录状态、按时间表再跑，只在关键节点叫人。

Google 工程师 **Addy Osmani** 的定义最为准确：

> “Loop engineering is replacing yourself as the person who prompts the agent. You design the system that does it instead.”

 Anthropic Claude Code 负责人 **Boris Cherny** 的概括则更接近工程现实：

> “I don’t prompt Claude anymore. I have loops running that prompt Claude and figuring out what to do. My job is to write loops.”

Loop Engineering 不是让 AI 一次性生成答案，而是让 AI 持续地运行一个**有状态、有反馈、可回滚**的工作系统。

---

## 为什么 Loop Engineering 正在成为主流

| 驱动力 | 说明 |
|------|------|
| 工具层成熟 | 现代 coding agent 已经把循环所需的 6 大 primitive 做成一等公民 |
| 从业者证言 | 造工具的人自己说“我不再写 prompt，我写 loop” |
| 范式自然演进 | 从 prompt engineering → context engineering → harness engineering → loop engineering |
| 可复现、可复用 | 一次设计，自动反复跑，适合 CI 失败、issue triage、依赖更新等重复维护任务 |
| 经济性 | 把人类从“反复查看和发起”中解放出来，集中处理真正需要判断的工作 |

Loop Engineering 的兴起，本质上反映了一个趋势：AI 的应用正从**单次交互**走向**持续运转的系统**。

---

## Loop Engineering 的六大构建块

业界基本共识的 6 个 primitive（5 个模块 + 1 个记忆）：

| 模块 | 作用 | 典型实现 |
|------|------|----------|
| **Automations / Scheduling** | 定时触发、发现任务、分类 | cron、hooks、GitHub Actions |
| **Worktrees** | 并行 agent 不互相踩文件 | `git worktree`、隔离目录 |
| **Skills** | 把项目知识固化成文件 | `SKILL.md`、可复用指令 |
| **Plugins / Connectors (MCP)** | 连接外部工具（issue tracker、Slack、API） | MCP servers |
| **Sub-agents** | 制造者 / 检查者分离 | 独立 agent 做 verifier |
| **Memory / State** | 跨对话持久化 | 状态文件、数据库、任务记录 |

octo-agent 把这些 primitive 全部内置，形成了一个统一的 Loop Engineering 平台，而不是零散的工具组合。

---

## octo-agent 如何映射这六大构建块

| Loop 模块 | octo-agent 能力 | 调用方式 |
|---|---|---|
| **Goal / 跑到条件为止** | `/goal <objective>` 或 `create_goal` | 让 agent 持续运行直到满足条件 |
| **Scheduling / Automations** | `cron-task-creator` 技能、`/api/tasks` | 持久化定时任务，session 关了也跑 |
| **In-session 循环** | `loop` 技能 + `schedule_wakeup` | `octo /loop 1h 检查 CI` |
| **Worktree 隔离** | `worktree-isolate` 技能、`workflow` 的 `isolation: "worktree"` | 写代码的 loop 必须开隔离 |
| **Sub-agents** | `sub_agent` 工具、workflow 里的 `agent()` | implementer + verifier 分离 |
| **Skills** | `SKILL.md` + `skill` 工具 | 加载沉淀的最佳实践 |
| **Memory / State** | `MEMORY.md`、`.octo/STATE.md`、task 状态 | 跨对话持久化 |
| **Connectors** | `mcp` 工具 + MCP servers | 接 issue tracker、Slack、staging API |
| **Workflow 编排** | `workflow` 工具 | 多 agent、阶段、并行、流水线 |

octo-agent 的独特之处在于：**这些能力不是各自为政的插件，而是围绕 Loop Engineering 设计的一整套协同机制**。`workflow` 负责编排，`cron-task-creator` 负责调度，`worktree-isolate` 负责隔离，`sub_agent` 负责分工，`skill` 负责沉淀，`MEMORY.md` 和 `.octo/` 负责状态。

---

## L1 / L2 / L3：渐进式自动化的三个档位

Loop Engineering 不是“要么手动、要么全自动”的二元选择。octo-agent 把它拆成三个档位：

| 档位 | 行为 | 适用阶段 | 风险 |
|------|------|----------|------|
| **L1：只读报告** | 发现问题、分类、生成报告，不修改任何外部系统 | 新 loop 上线前 1–3 轮 | 极低 |
| **L2：草案辅助** | 生成修复方案、评论草稿、PR 描述，等待人工确认后发布 | loop 分类质量稳定后 | 低 |
| **L3：自动执行** | 对安全动作自动执行（加 label、发提醒、创建 worktree 和 draft PR），不可逆动作仍留人工 gate | loop 经过验证、安全边界清晰 | 可控 |

这套渐进式模型的价值在于：**让自动化先从“观察”开始，再逐步获得“行动”能力**。这是避免 loop 失控的关键。

---

## 如何在 octo-agent 中实施一个 Loop

### 第一步：写 LOOP.md

任何 loop 上手前先写一份 `LOOP.md`，把目的、触发、发现范围、完成条件、安全红线写死：

```markdown
# Loop: dependency-sweeper

## Purpose
每周自动检查并升级 patch/minor 依赖。

## Trigger
每周一早上 9 点（cron: `0 9 * * 1`）。

## Discovery
运行 `go list -u -m all` / `npm outdated` / `pip list --outdated`。

## Done condition
所有 patch/minor 安全依赖已升级并测试通过；major 依赖已列出供人工处理。

## Safety
- 只升级 patch/minor，major 不自动碰。
- 在独立 worktree 中跑测试，不污染 main。
- 不自动 merge / deploy / 关闭 issue。

## State file
`.octo/dependency-sweeper-state.md`
```

### 第二步：选择触发方式

| 场景 | 推荐方式 | 命令示例 |
|---|---|---|
| 单次、在 session 内反复 | `schedule_wakeup`（`/loop` 技能） | `octo /loop 30m 检查是否还有未合 PR` |
| 长期、跨 session 运行 | `cron-task-creator` | 创建 `cron` 任务，指定 `prompt` 和 `directory` |
| 复杂多 agent 流程 | `workflow` 工具 | `octo workflow daily-triage '{"since": "1d"}'` |

### 第三步：使用内置的 `loop-engineering` skill

```bash
octo /loop-engineering design a daily triage loop for my Go backend
```

这个 skill 会：
1. 梳理 trigger → discovery → state → worktree → implementer → verifier → human gate 的完整链路；
2. 生成 `LOOP.md` 和 `STATE.md` 模板；
3. 提醒你选择 L1 → L2 → L3 的渐进策略。

### 第四步：用 `workflow` 做编排

`workflow` 工具运行在 **IO-free 的 mruby 沙箱**中，脚本本身不能访问文件系统或网络，所有真实 IO 必须通过 `agent()` 委托给子 agent：

```ruby
# ✅ 正确：让子 agent 写文件
agent("Write a report to .octo/STATE.md with this content: ...", { "read_only" => false })

# ❌ 错误：脚本里直接 File.write
File.write(".octo/STATE.md", "...")
```

这种设计强制 loop 的每一步都经过 LLM reasoning，同时也保证了脚本本身的安全性。

---

## 一个典型 loop 的工作流程

```text
定时触发（cron 或 /loop）
  → 发现任务（issue / PR / CI / commit）
  → 分类与优先级排序
  → 写入 STATE.md / 更新状态
  → 对低风险任务：在隔离 worktree 中派 implementer 子 agent 出补丁
  → verifier 子 agent 独立检查补丁 + 测试
  → 安全/白名单内：自动开 PR / 更新 ticket / 加 label
  → 风险/模糊：升级给 human gate
  → 下一轮从 STATE.md 继续
```

这是一个**递归目标**：定义目的，AI 迭代直到完成或移交。`STATE.md` 和 `processed.json` 让 loop 有记忆，不会因为对话上下文重置而丢失进度。

---

## 四条安全红线

Loop Engineering 的自动化越强，安全边界越重要。octo-agent 推荐以下四条红线：

1. **Maker / Checker 必须分离**：用 `sub_agent` 或 workflow 里的 `agent()` 分别当 implementer 和 verifier。
2. **写文件必须 worktree 隔离**：传 `isolation: "worktree"`，避免污染主分支。
3. **不可逆动作必须 human gate**：merge、deploy、关闭 issue、删除 tag、公共频道发消息，这些 loop 只能建议，不能自动执行。
4. **从 L1 开始**：第一版永远 report-only，别一上来就自动改代码。

---

## Loop Engineering 的应用场景

Loop Engineering 不仅适用于代码开发。只要任务满足四个条件——**输入规律出现、有判断标准、能迭代优化、存在不可逆动作需要 human gate**——它就可以用于任何领域。

| 角色 | 适合做成 loop 的任务 | 运行方式 |
|---|---|---|
| **后端工程师** | 依赖升级、issue triage、CI 失败分类、post-merge 清理 | workflow + cron |
| **内容创作者** | 每天扫描热点 → 选题 → 写初稿 → 人工审 | workflow + scheduler |
| **自由职业者** | 每周整理客户邮件 → 分类 → 草拟回复 | MCP 接邮箱 + agent |
| **客户经理** | 每天读 CRM 更新 → 标出需跟进客户 → 准备话术 | 接 Salesforce/HubSpot |
| **律师 / 法务** | 合同初筛：提取关键条款 → 标风险点 → 人工复核 | 只读 loop，L1 阶段 |
| **个人效率** | 每周整理笔记/账单/待办 → 归类 → 生成下周重点 | 本地文件 + memory |

开发场景有天然优势：代码有版本控制、测试和类型系统，验证标准客观；`git worktree` 和 CI 提供了可回滚的沙箱；issue、PR、CI 都是结构化数据，agent 容易读取。非代码场景则更适合 L1 只读报告和 L2 草稿 + 人工确认，只有极少数低风险任务才适合直接跳到 L3。

---

## 常见风险与应对

| 风险 | 说明 | 应对 |
|------|------|------|
| **Token 成本爆炸** | 子 agent + 长循环 + 高频调度会快速烧钱 | 限制每次处理数量、调低频率、从 L1 开始 |
| **Verification 债务** | 没人看 = 没人知道 loop 出了什么错 | 每次运行写入 STATE.md，定期 review |
| **Comprehension 债务** | loop 写得越快，你离真实代码越远 | 从低风险任务开始，保持 human gate |
| **Cognitive surrender** | 从“用 loop 加速我懂的工作”变成“让 loop 替我想” | 明确 loop 只处理重复性任务，判断留给人 |

Addy Osmani 的警告值得反复咀嚼：

> “Build the loop. But build it like someone who intends to stay the engineer, not just the person who presses go.”

---

## 从哪开始落地

如果你是第一次尝试 Loop Engineering，最容易从**重复维护任务**切入，而不是从重构核心业务逻辑开始。推荐优先级：

1. **Daily Triage**：每天早上扫一遍 issue / PR / CI，省掉“我看看今天有没有啥事”的时间。
2. **Dependency Sweeper**：每周升级 patch/minor 依赖，尤其适合 Go 这种依赖迭代快、API 兼容性通常可控的环境。
3. **Post-Merge Cleanup**：自动清理已合并分支，并提醒关闭关联 issue。破坏性最小，但日常很烦人。

Loop Engineering 最大的价值不是减少写代码的时间，而是减少上下文切换和琐事堆积，让你把精力放在真正需要人类判断的设计、架构和复杂 bug 上。

---

## 系列下一篇

- **Loop Engineering 系列（二）**：[Loop Engineering 实战：用 octo-agent 自动循环打理开源仓库](/blog/posts/loop-engineering-practice/)——记录在 `open-octo/octo-agent` 仓库上落地 issue triage、PR review、auto issue-fix 三个循环的全过程，包含真实流程图、踩坑与代码。

---

## 参考链接

- Addy Osmani: https://addyosmani.com/blog/loop-engineering/
- Cobus Greyling 参考仓库: https://github.com/cobusgreyling/loop-engineering
- The New Stack 报道: https://thenewstack.io/loop-engineering/
- Anthropic harness design: https://www.anthropic.com/engineering/harness-design-long-running-apps
- Anthropic effective harnesses: https://www.anthropic.com/engineering/effective-harnesses-for-long-running-agents
- Loop Engineering guide 站点: https://loopengineering.run/

---

*本文为 octo-agent 官方技术系列第一篇。*