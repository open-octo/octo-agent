---
title: 贡献指南
description: 怎么发一个能被合并的 PR。
---

每个 PR 都会有真人 review；机器人也可能会留评论。

## 开始之前

- 读一读 [`.octorules`](https://github.com/open-octo/octo-agent/blob/main/.octorules) 和
  [`CLAUDE.md`](https://github.com/open-octo/octo-agent/blob/main/CLAUDE.md)——里面讲了分层、
  约定和常见的坑。大多数"这个 PR 能不能合"的问题都能在那里找到答案。
- 翻翻 `dev-docs/`——每个功能的设计笔记（沙箱、记忆、skill、子代理……）都在这里。如果你的改动
  涉及这些领域，记得同步更新对应的文档。
- 大的改动先开个 issue 讨论。小修小补可以直接发 PR；新 provider、新工具，或者任何涉及 agent
  循环的改动，先简单讨论一下会更顺利。

## 工作流程

1. 从最新的 `main` fork 或开分支。永远不要直接在 `main` 上提交。
2. 一个 PR 只做一件事。机械式的批量改动（改名、挪文件）可以合在一起，但要自成一个整体。
3. push 之前先跑：
   ```bash
   make test       # go test -race ./...
   make vet
   make fmt-check
   ```
4. push 并开 PR。默认合并方式是 squash-and-merge。
5. commit 消息和 PR 描述用英文写。

## 我们看重什么

- **尽量小的 diff。** 修 bug 不该顺带做一堆无关的清理。重构 PR 不该夹带一个新功能。
- **测试跟代码放在一起。** 新行为要有覆盖；修 bug 要带一个在修复前会失败的回归测试。
- **测试里不跑真实网络请求。** HTTP 测试用 `httptest.NewServer`。真实 API 的冒烟测试用自己的
  key 手动跑，不进 CI。
- **不随便加新的第三方依赖。** 一定要加的话，说明为什么标准库做不到。
- **注释用英文，写"为什么"而不是"是什么"。** 命名本身就该说清楚"是什么"。只有在删掉注释会丢失
  信息时才写——比如一个不明显的约束、对某个已知 bug 的绕过方案、一个值得记录的权衡取舍。

参与贡献即表示你同意你的代码按项目的
[MIT 许可](https://github.com/open-octo/octo-agent/blob/main/LICENSE.txt)发布。
