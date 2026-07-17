---
title: "Octo 上手系列（十）：Record & Replay 实战——录一遍你的操作，以后让 octo 自己回放"
description: "把你亲手做一遍的浏览器操作录下来，蒸馏成一段可回放、能自愈的脚本——下次同样的事，octo 自己点完。"
pubDate: 2026-07-18
author: "octo-agent team"
tags: ["onboarding", "octo-agent", "browser"]
locale: zh
originalSlug: onboarding-browser-record-and-replay
---

# Octo 上手系列（十）：Record & Replay 实战——录一遍你的操作，以后让 octo 自己回放

> 上一篇把浏览器接上了，遇到一次性的任务，直接让 octo 边看边点就够了。但同一套操作如果你自己每周都要点一遍，每次都让模型重新"观察-决定-点击"就是在浪费——这一篇讲怎么录一遍，让它以后自己回放，选择器失效了还能自己修。

---

## 录的是你做的操作，不是模型做的操作

这是理解这套机制最关键的一点：调用 `record_start` 之后，octo 会把浏览器**交还给你**，由你亲手完成这几步操作，说一声"做完了"之后，它才调用 `record_stop <名字>` 把过程保存下来。录制期间 octo 不会自己驱动页面——它在旁边看着记，不代劳。

每一步会记录：动作类型（`click`/`type`/`select`/`upload`/`navigate` 这些）、一个锚定在最近的、带稳定 `id` 的祖先元素上的选择器（而不是那种一变页面结构就失效的、按位置算出来的选择器链）、这个元素当时的可见文本，以及所在的 URL。

## 网页界面一键开录

不用记住 `record_start`/`record_stop` 这两个动作名——网页界面的"浏览器"面板里有一个"● 录制"按钮，点一下会开一个新的对话，第一句话就是把这套流程说给 octo 听：先确认浏览器已连接，问你想录什么、起什么名字，调 `record_start` 之后把控制权交给你，等你说完成了再 `record_stop` 保存。

同一个面板上，每条已保存的录制旁边还有：

- **"▶ 回放"**——按名字调 `run_skill`，走的是完整的 agent 路径（后面会讲到的自愈机制也在这条路径里生效），不是绕开模型的服务端直接重放。
- **"✎ 编辑"**——同样是对话式的：读取录制对应的 YAML，把步骤列给你看，你说要改哪一步、加/删/重排哪一步、给哪个参数设默认值，改完写回文件，不会顺手帮你回放一遍。

## 一段录制存下来长什么样

原始录制会经过一次模型处理——把绕路的死胡同去掉、把你当时输入的具体值换成 `{{参数名}}` 占位符、补一段描述——但这一步只能**重新排列或重命名真实录到的步骤**，不能凭空造一个新目标：任何一步如果它的选择器不在原始录制里，就会被直接拒绝，退回用原始步骤。

蒸馏完存成纯 YAML，放在 `~/.octo/browser-skills/<名字>.yaml`——可读、可手改、能用 git 追踪差异。大致长这样：

```yaml
name: submit-expense-report
description: 登录报销系统，填一张标准报销单并提交
params:
  - name: amount
    description: 报销金额
    default: "128.00"
  - name: memo
    description: 报销事由
outputs:
  - name: receipt_url
    type: string
steps:
  - action: navigate
    url: https://expense.example.com/new
  - action: click
    selector: "#category-travel"
    label: 选择"差旅"分类
  - action: type
    selector: "#amount"
    hint: 报销金额
    value: "{{amount}}"
  - action: type
    selector: "#memo"
    hint: 事由说明
    value: "{{memo}}"
  - action: click
    selector: "#submit-btn"
    verify:
      text: 提交成功
  - action: extract
    js: document.querySelector('.receipt-link').href
    bind: receipt_url
```

这份 YAML 里有三个字段值得认识一下。`params` 是回放时可以覆盖的输入：步骤里的 `{{amount}}`/`{{memo}}` 会被替换成调用时传的值，没传就退回 `default`。`hint` 是这个表单字段的无障碍名字（placeholder/name/aria-label/id 或者关联的 `<label>` 文字）——当按位置记下的 `selector` 失效时，回放会先按 hint 重新定位一次，实在不行才交给下面说的自愈。`outputs` 声明了这段录制对外暴露的值：`extract`/`download` 这类步骤可以把结果通过 `bind` 绑定给一个具名的 output，供下游取用。

## 回放怎么保证靠谱

```
browser(action: "run_skill", name: "<录制名>", params: { ... })
```

常见情况下回放是确定性的，不涉及模型调用：每一步先等目标出现再执行，声明了 `verify` 的话还会做一次校验。几个值得知道的健壮细节：

- 点击先按原始录制里的选择器找；如果页面结构有点变化，但一个带着录制时那段可见文字的元素还在，就点那个元素，而不是直接判失败。
- 某一步打开了新标签页，后续步骤会自动跟到新标签页上。
- 输入之后如果字段意外变成空的，会先清空重打一次，再失败才真正判定这一步失败。

回放是"要么整段做完，要么不做"——octo 被明确指示只有当请求和一段录制**从头到尾**完全匹配时才用它，不会只执行一部分、也不会在声明的参数之外自己发挥。录制也只能显式回放，没有关键词触发这条路（早期版本试过又撤了，不够可靠）。

## 选择器彻底失效了呢——自愈

如果某一步的选择器和 `hint` 都找不到东西了——并且只有在配置了专门用于这个用途的模型时——octo 会对页面当前可交互的元素取一份纯文本摘要（选择器 + 可见文本，不截图），连同预期动作、预期的文字标签、失效的旧选择器一起发给模型，让它只回一个修正后的 CSS 选择器。修正会被重试一次，成功的话会**直接写回这段录制的 YAML 文件**——所以一次自愈是持久生效的，不是临时补丁，以后每次回放都不用再修一遍。

## 接进 workflow

一段录制的 `outputs` 可以通过 `skill("browser:<名字>", params)` 直接接进[上一篇系列讲过的 workflow 脚本](/blog/posts/onboarding-workflow-parallel-review/)——比如录一段"登录后台导出账单"，把导出的文件路径当 output 绑出来，下一步再交给另一个 agent 去解析、汇总，整条链路就不用你手动衔接。

---

## 系列到这里

十篇下来，装机、Skills、MCP、Loop、Cron、实战合体、Workflow、Goal、Browser、Record & Replay——覆盖了从"问一次答一次"到"持续自主推进"，也覆盖了从"调用工具"到"真的替你操作一个网页"这两条完全不同的能力线。挑哪一种，取决于你手上的任务长什么样：一次性的问题直接问；要反复盯着用 `/loop`；按时间表触发用 cron；能拆开并行跑用 workflow；说不清楚要几轮的长任务交给 goal；没有 API 只有网页的操作，接上浏览器直接让它点，重复做的事录一遍让它自己回放。

**系列上一篇**：[Octo 上手系列（九）：Browser 实战——把你自己的浏览器接给 octo](/blog/posts/onboarding-browser-setup/)
**系列开头**：[Octo 上手系列（一）：装好它，跟它说上第一句话](/blog/posts/onboarding-install-and-first-run/)
