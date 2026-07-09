---
title: "Octo 上手系列（九）：Browser 实战——把你自己的浏览器接给 octo"
description: "浏览器工具不是又一个 headless 框架——它直接接上你已登录的真实 Chrome，点击、填表、传文件都算一次正儿八经要审批的操作。"
pubDate: 2026-07-17
author: "octo-agent team"
tags: ["onboarding", "octo-agent", "browser"]
locale: zh
originalSlug: onboarding-browser-setup
---

# Octo 上手系列（九）：Browser 实战——把你自己的浏览器接给 octo

> 前八篇覆盖的都是文件、终端、MCP、workflow、goal 这类"有接口可调"的任务。但日常里还有一大类事完全没有接口——登录一个内部后台点几下、在一个只有网页表单的系统里填信息——octo 能做的前提是先给它接上一个真正的浏览器。这一篇就来把这件事办了。

---

## 不是又一个 headless 浏览器框架

octo 的 `browser` 工具通过 Chrome DevTools Protocol（CDP）直接操作一个真实的 Chrome 标签页——不依赖 Puppeteer、Playwright 这类 headless 框架，也不需要额外装任何运行时。这个区别不是实现细节，是能力上的区别：它操作的是**你自己在用的、已经登录好的那个 Chrome**，而不是一个从零开始、什么 cookie 都没有的匿名自动化环境。你在某个内部系统里的登录态，octo 直接就能用。

## 一条命令把它接上

```bash
octo browser setup
```

这个命令会带你打开 `chrome://inspect/#remote-debugging`，勾选"Allow remote debugging for this browser instance"（Chrome 144 之后，默认 profile 上原来那个 `--remote-debugging-port` 命令行参数被禁用了，走 inspect 页面的这个勾选框是目前能用的路径），必要时重启一下浏览器。勾完之后，`octo browser setup` 会在本地 9222 端口反复探测——注意，它验证的不只是"连上了"，还会真正发一次页面级的 CDP 调用确认可用，因为在新版 Chrome 上，浏览器级的连接可能成功，但页面控制那一层还是会失败。探测成功后，端口号会存进你的配置，以后每次开 octo 都直接复用，不用再走一遍这个流程。

如果一时半会没弄好也没关系：命令会停下来等你确认已经打开开关，回车重试，或者随时按 `q` 退出，下次想起来了再跑一遍 `octo browser setup` 就行。

## 连接顺序：三条路径，不会帮你偷偷降级

`browser` 工具按固定顺序尝试拿到一个可操作的页面：

1. **已知的调试端口**（配置里的 `browser.connect_port`）——直接连，这条失败了不会自动退到下一条。这就是 `octo browser setup` 帮你接好的那条路。
2. **接入一个已经在跑、开了远程调试的 Chrome**（`browser.attach_running: true`）——复用你的登录态；但只会接一个原本就开了调试的浏览器，绝不会去劫持一个没开调试的普通 Chrome 窗口。
3. **启动一个全新的临时 profile**——前两条都没配或者都连不上时的兜底选项，没有你的任何登录态。

对应的配置写在 `~/.octo/config.yml` 里：

```yaml
browser:
  attach_running: true   # 复用你已登录的 Chrome，而不是用一个临时 profile
  connect_port: 9222      # 或者通过 --remote-debugging-port 接入
  headless: false          # 默认关闭——交互式工作流需要能看到、能介入
  user_data_dir: ""
  exec_path: ""
  download_dir: ""
```

每一项都有对应的 CLI flag，可以在单次运行时临时覆盖，不用改配置文件。

不管走的是哪条路径，octo 都会打开一个**全新标签页**，而不是复用你已经开着的某个标签页（包括它自己网页界面的那个）——要操作一个已经开着的标签页，得显式调用 `pages`/`select_page` 这两个动作去选。连接会在整个会话里复用，调试器的授权提示只会跳一次，不会每次导航都弹一遍。

## 直接说需求，它自己决定点哪里

你不需要知道 CSS 选择器长什么样，直接描述任务就行：

```text
帮我打开内部工单系统，把 #4821 这张工单的状态改成"已完成"。
```

octo 拿到这句话后，大致是这样一步步来的：`navigate` 打开目标页面 → `observe`（或者更底层的 `ax`，读无障碍树）看清页面上真正存在哪些可交互元素、它们的选择器长什么样 → `click`/`type`/`select` 完成实际操作 → 需要等异步内容加载完时用 `wait`（可以是固定超时，也可以让它按 `network_idle` 等到页面的 fetch/XHR 活动真正停下来，这比死等一个固定时间靠谱）。

几个容易被问到的动作单独说一下：

- **上传文件**用 `upload` 直接传一个绝对路径给文件 `<input>`，而不是让它去点上传按钮——点按钮弹出的是系统原生文件选择框，是没法被自动化驱动的。
- **要读一段 shadow DOM 里的内容，或者 `click`/`type` 够不到的地方**，`eval` 可以直接跑一段 JS，拿到 CSS 选择器穿不透的东西。
- 页面上开了新标签页（比如点了一个"在新窗口打开"的链接），后续动作会自动跟到新标签页上，不用你手动 `select_page`。

## 权限与视觉能力

浏览器动作走的是和其他工具完全一样的权限引擎——在一个已登录账号上点击或者提交表单，会被当成一次真实的、影响很大的操作去审批，而不是被当作只读的一步蒙混过去。截图这个动作只有在当前模型支持视觉能力时才会真的把图片传给模型；纯文本模型拿到的是一句文字提示，而不会收到一个它处理不了、会被直接拒绝的图片内容块。

---

## 下一篇：录一遍，让它自己回放

浏览器接上之后，遇到一次性的任务，直接让 octo 边看边点就够了。但如果同一套操作你自己每周都要点一遍——填一份周报、导一次账单——每次都让模型重新"观察-决定-点击"就有点浪费了。下一篇讲怎么把这套操作**录一遍**，蒸馏成一段可以直接回放、还能自己修选择器的脚本。

**系列上一篇**：[Octo 上手系列（八）：Goal 实战——定一个长期目标，让它自己找空闲时间推进](/blog/posts/onboarding-goal-long-running-migration/)
**系列下一篇**：[Octo 上手系列（十）：Record & Replay 实战——录一遍你的操作，以后让 octo 自己回放](/blog/posts/onboarding-browser-record-and-replay/)
