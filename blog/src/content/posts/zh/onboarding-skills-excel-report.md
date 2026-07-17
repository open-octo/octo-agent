---
title: "Octo 上手系列（二）：Skills 实战——一句话生成一张 Excel 报表"
description: "不用学 openpyxl，不用记函数——把需求说清楚，剩下的交给内置的 office-xlsx 技能。"
pubDate: 2026-07-10
author: "octo-agent team"
tags: ["onboarding", "octo-agent", "skills", "office-xlsx"]
locale: zh
originalSlug: onboarding-skills-excel-report
---

# Octo 上手系列（二）：Skills 实战——一句话生成一张 Excel 报表

> 上一篇装好了 octo，跟它说上了第一句话。这一篇解决一件很多人每月都要做一次的烦心事：整理一张开支明细表。

---

## 先搞清楚：Skill 是什么，为什么不用你去调用

octo 内置了一套 Skills 系统——每个 Skill 就是一份写清楚"什么时候该用、该怎么用"的说明书，装在 `~/.octo/skills-default/` 里。它不会一直占着上下文：session 开始时，octo 只把每个 Skill 的名字和一行描述放进系统提示，等你真正说出一个跟某个 Skill 匹配的需求，它才把那份完整说明书加载进来。

这意味着你**不需要知道 Skill 的存在，也不需要手动调用它**。装完 octo 之后，打开网页界面左侧的"技能"面板，能看到一整张已经装好的列表：

![Octo 技能面板：内置的 office-xlsx、cron-task-creator、mcp-creator 等技能](../_assets/onboarding/skills-panel.png)

列表里那个 `office-xlsx`，就是这一篇的主角——创建、读取、编辑 Excel（`.xlsx`）文件的技能，公式、样式、合并单元格、多表、图表都覆盖了。你完全不需要点它、不需要在对话里提它的名字，只要你的需求里出现"做一个表格""改一下这个 Excel""生成个报表"之类的意图，octo 自己会把它接过去。

---

## 直接说需求

假设你要交一份 7 月的开支明细表。不用想 openpyxl 怎么写，直接说人话：

```text
帮我做一个 202607-开支明细.xlsx，第一个 sheet 叫"明细"，
表头是 日期 / 类别 / 金额 / 备注，先给我填 10 行示例数据，
类别用 餐饮/交通/房租/娱乐/其他 这几种；
第二个 sheet 叫"汇总"，按类别汇总金额，加一个饼图。
```

octo 判断出这是一个 Excel 相关任务后，会自动加载 `office-xlsx` 这份说明书，然后按你的描述去创建文件、写数据、建汇总公式、加图表——这一整套动作背后其实是两个内置脚本在跑（`xlsx_inspect.py` 读结构、`xlsx_edit.py` 做编辑，都基于 openpyxl，通过 `uv run` 临时装依赖，不留痕迹），但这些细节你不需要关心，这正是 Skill 系统存在的意义：**知道怎么做的知识，不需要你随身带着**。

```mermaid
flowchart LR
    A["你：一句话描述需求"] --> B["octo 匹配到 office-xlsx 技能"]
    B --> C["加载技能说明书"]
    C --> D["调用 xlsx_edit.py（openpyxl）"]
    D --> E["生成 .xlsx 文件"]
```

---

## 已经有一份旧表？先让它"看懂"再改

如果你手上已经有一份别人做的、或者你自己上个月做的旧表，直接说"帮我在这份表里加一列同比增长率"，octo 会自己先用 `xlsx_inspect.py` 读一遍这份表的结构——有哪些 sheet、表头长什么样、哪些格是公式、有没有合并单元格——再决定怎么改。这一步是它自己知道要做的，不需要你提醒。

## 想调整样式，照样说人话

不满意默认样式，继续用自然语言提要求就行：

```text
表头加粗、加个浅蓝底色；金额列改成货币格式，两位小数；
整张表按金额从大到小排序。
```

## 一个真实的坑：合计别让它算好写死

如果表里需要一行"合计"，正确的说法是让它**用 Excel 公式**，而不是"帮我算出总数填进去"：

```text
在明细表最后加一行合计，用 SUM 公式算金额列的总和，别自己算好写数字进去。
```

原因很实际：`office-xlsx` 背后的 openpyxl 不会执行公式计算，它只能写公式文本，真正的计算是 Excel/LibreOffice 打开文件时才发生的。如果换成让 Python 算出一个数字直接写进单元格，那个数字就是死的——明细一改，合计不会跟着变，还会不知不觉地对不上。凡是"根据其他格算出来的值"，都该交给公式，这也是它在做财务模型类表格时会主动遵守的一条约定。

---

## 这只是 Skill 系统的一个例子

`office-xlsx` 只是 20 个内置 Skill 里的一个。同样的模式——匹配意图、自动加载、你只管说需求——也适用于让 octo 帮你审一份代码 diff（`code-review`）、整理一篇技术方案（`tech-design`），后面几篇要讲的定时任务（`cron-task-creator`）和接外部工具（`mcp-creator`），本质上也是 Skill。

**系列上一篇**：[Octo 上手系列（一）：装好它，跟它说上第一句话](/blog/posts/onboarding-install-and-first-run/)
**系列下一篇**：[Octo 上手系列（三）：MCP 实战——接上 GitHub，让 octo 帮你理 issue](/blog/posts/onboarding-mcp-github-issues/)
