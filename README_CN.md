# octo-agent

[![Go CI](https://img.shields.io/github/actions/workflow/status/open-octo/octo-agent/go.yml?label=ci&style=flat-square)](https://github.com/open-octo/octo-agent/actions)
[![Website](https://img.shields.io/badge/website-octo--agent.dev-4f46e5?style=flat-square)](https://octo-agent.dev)
[![Go](https://img.shields.io/badge/go-%3E%3D%201.25-00ADD8?style=flat-square)](https://go.dev)
[![License](https://img.shields.io/badge/license-MIT-lightgrey?style=flat-square)](LICENSE.txt)

<p align="center">
  <a href="README.md">English</a> · <a href="README_CN.md">简体中文</a>
</p>

> **开源、单二进制、自托管的 AI Agent。** coding 能力对标 Claude Code，个人助手
> 比 OpenClaw 更轻量 —— 一个 MIT 开源的 Go 二进制，无需 Node / Python / Ruby，
> 接**任意模型**（DeepSeek、Kimi、Anthropic、OpenAI 或任何兼容端点），服务和
> 数据都留在你自己的机器上。

<p align="center">
  <img src="landing/assets/demo-cn.gif" alt="octo 演示" width="760">
</p>

```bash
curl -fsSL https://octo-agent.dev/install.sh | sh     # 单二进制，无需 Node / Ruby / Python 环境
octo config                                            # 选 provider，填 key（DeepSeek / Kimi / 百炼 …）
octo "给 octo config show 加一个 --json 参数并跑测试"   # 一句话 → 完整 agentic 工具循环
```

## 安装

- **Linux / macOS** — `curl -fsSL https://octo-agent.dev/install.sh | sh`
- **Windows** — `irm https://octo-agent.dev/install.ps1 | iex`
- **桌面应用** — 从[最新 release](https://github.com/open-octo/octo-agent/releases/latest)下载安装器：
  `octo-setup.pkg`（macOS）、`octo-setup.exe`（Windows）、`Octo-x86_64.AppImage`（Linux）
- **Go** — `go install github.com/open-octo/octo-agent/cmd/octo@latest`

随时用 `octo upgrade` 升级。各平台细节 —— Gatekeeper / SmartScreen 提示、卸载、
从源码构建 —— 见[安装指南](https://octo-agent.dev/docs/zh/getting-started/install/)。
Windows 安装器经 [SignPath Foundation](https://signpath.org/) 签名，完整的代码
签名政策见 [SECURITY.md](SECURITY.md#code-signing-policy)。

## 快速上手

```bash
octo config                # 一次性设置：选 provider/model，填 API key
octo "介绍一下这个仓库"      # headless 单发：prompt → agentic 工具循环 → 退出
octo                       # 终端交互式 TUI；octo -c 恢复历史 session
octo serve -d              # Web UI + IM 桥接，http://127.0.0.1:8088
```

内置工具（shell、文件读写改、搜索）、MCP 服务、skills 全部默认开启，一条消息
就能真正干活。下一步：
[快速上手](https://octo-agent.dev/docs/zh/getting-started/quickstart/) ·
[选择 provider](https://octo-agent.dev/docs/zh/getting-started/choose-a-provider/) ·
[CLI 参考](https://octo-agent.dev/docs/zh/reference/cli/)。

## 为什么用 octo

octo 不打算在功能上卷赢大厂 agent；它是同一个想法的**开源、可自托管、不绑厂商**
版本 —— 而且是有主张的那种，Rails 风格：约定优于配置，omakase 默认值优于无限选项。

|  | **octo-agent** | Claude Code |
|---|---|---|
| 授权 / 成本 | **MIT 开源，免费，自托管** | 专有，多数场景需 Claude 订阅 |
| 运行时 | **单个自包含 Go 二进制** | 原生安装，绑定 Anthropic 账户 |
| 模型 | **双协议原生 + 任意兼容端点**（DeepSeek/Kimi/百炼/OpenRouter/vLLM） | 以 Anthropic 为主 |
| 部署 / 数据 | **完全自托管，服务与数据都在你手里** | 多数场景由 Anthropic 托管 |
| 技能 | SKILL.md 格式相同，复用 Claude Code 技能 | 原生（skills 的发源地） |

在个人助手这条线上，[OpenClaw](https://github.com/openclaw/openclaw) 是最接近的
同类。octo 覆盖同一块地盘 —— 自托管、MIT、在你已经在用的聊天 App 里找到你 ——
但它是单个静态二进制而不是带依赖树的 Node.js 应用，且自带完整的 coding agent 核心。

## 作者的心里话

上面讲的是 octo「是什么」，这一节讲「为什么」—— 为什么我觉得它值得你花十分钟
装一个，特别是如果你在国内。

**好的 agent 体验不该被环境挡住。** 我自己是 Claude Code 的重度用户，它是目前
最好的 coding agent。但在国内想用上它，要先跨过三道和技术无关的坎：订阅费
（每月 $20 到 $200）、支付方式（需要一张外币卡）、网络。很多人被挡在这三道坎
外面，而不是败在「会不会用 agent」上。octo 做的事说穿了就一件：把同样的工作
方式，带给跨不过这三道坎的人。

**国产模型真的够用了。** DeepSeek、Kimi、Qwen 这两年的进步是实打实的。而模型
只是一半，另一半是 harness —— 工具循环、权限门控、skills、记忆、子代理。把国产
模型接进一个认真做的 harness 里，日常编码任务的完成度和订阅制大厂 agent 已经
没有体感上的鸿沟，但价格便宜一到两个数量级，人民币直接充值。octo 对这些模型是
一等公民支持：Anthropic 和 OpenAI 双协议都是原生实现，不是「兼容模式」凑合。

**已经在用 cc-switch 接国产模型？** 这是个流行的路子，但有两个绕不开的短板：
Claude Code 的提示词缓存是围绕 Anthropic 官方端点调的，第三方模型接进去命中率
并不理想 —— 而缓存命中率直接决定你的 token 账单；web_search、tool_search 这类
依赖 Anthropic 服务端的能力，切到第三方端点后就用不了了。octo 把这两件事都在
自己这一侧做实了：针对几大国产模型逐家做了缓存优化，实测 Kimi、DeepSeek、Qwen
的缓存命中率都能到 95% 以上，实打实地省钱；web_search 和 tool_search 是 octo
内置实现，开箱即用，不挑模型。

**你的数据只经过你自己的机器。** octo 没有云端、没有账号体系，代码里没有任何
遥测埋点 —— 对外的网络请求只有两类：你自己配置的模型 API，和检查更新时访问
GitHub。对在公司内网干活、代码不能离开内网的人，这不是加分项，是先决条件。

**微信、飞书、钉钉里的 agent。** 海外的 agent 产品永远不会认真支持国内的 IM
生态。octo 把 IM 桥接当核心功能做：微信 iLink、飞书、钉钉、企业微信开箱即用。
在工位上布置任务，通勤路上用手机跟进，到家活已经干完了。

**在外面聊得好好的，agent 把自己整没了。** 这是我以前玩 OpenClaw 和 Hermes 时
最破防的场景：人在外面，用微信跟 agent 聊一个功能怎么实现，聊得正顺，它突然
自作主张改了代码、顺手重启服务 —— 然后，就没有然后了。它把自己搞挂了，我只能
干等到晚上回家把它救活。octo 对这个场景做了专门设防：terminal 工具直接拒绝
任何打向 octo 自身进程的 kill / pkill / killall（连 `kill $(pgrep octo)` 这种
拐弯的变体也认得）；重启只有一条路 —— 专门的 restart_server 工具，而它在默认
权限规则里被写死为「必须询问」，网页上弹确认框、IM 里要你明确回复同意才执行；
即便你同意了，重启也是优雅的：先把当前这轮跑完、回复送到你手上，supervisor
再拉起新进程，客户端自动重连。你在外面，永远不会再对着一个突然沉默的聊天窗口
干瞪眼。

**你见过 agent 发疯吗？** 模型越来越强，但本质仍是概率模型。也许你也遇到过
这样的失控现场：它突然认定「环境坏了」「刚才写的全错了」「系统被入侵了」
「这个文件是病毒」—— 然后它真的动手了，`rm` 下去，你一整天的工作成果没了，
再也找不回来。octo 不会让这一幕发生：所有经 octo 发出的删除命令都要过校验，
`rm -rf /`、`rm -rf ~` 这类毁灭性命令被硬编码拒绝 —— 硬到连你自己改权限规则
都放不开；删普通文件，先把目标备份进回收站再执行；write_file / edit_file
覆盖旧文件，同样先存底。回收站默认保留 14 天、上限 10 GiB，在这个范围内，
被误删的文件永远找得回来。

**单二进制在国内的含金量。** 不需要 Node / Python / Ruby 环境，意味着 npm 镜像、
node-gyp 编译失败、依赖版本冲突这些事从根上就不存在。一个二进制文件，拷到任何
机器上就能跑 —— 包括不能上外网的内网机器。

**紧跟前沿，但不照单全收。** 开源平替常见的宿命是慢半拍，octo 不是。今年 6 月
Claude Code 推出 dynamic workflows（模型自己写编排脚本、扇出子代理协同干活），
Codex 推出 record & replay（演示一遍操作、录成可复用的技能），这两个高阶能力
octo 都有，而且结合自己的内置工具做了取舍：dynamic workflow 的官方玩法动辄
几十上百个并行子代理，跑起来壮观，token 账单也壮观 —— octo 把单个 workflow
的并行扇出限制在 8 个，多出来的排队等空位，任务照样全部跑完，账单不会失控；
Codex 的 record & replay 只在 macOS 应用里提供，octo 的浏览器录制回放（带页面
改版后的自愈）对 Windows 和 Linux 一视同仁。

最后一句实话：如果你已经有 Claude 订阅而且用得顺手，请继续用 Claude Code，它
配得上它的价格。octo 适合的是另外一群人 —— 订阅对你太贵、支付和网络够不着、
数据不能出机器，或者单纯想把整套工具攥在自己手里。两边的 SKILL.md 格式互通，
你甚至可以白天用 Claude Code 干重活，日常杂事交给跑着 DeepSeek 的 octo。

## 界面

**稳定版（1.0）。** 规划了八个界面 —— 正好对上章鱼的八条腕 —— 其中七个已上线：

- **CLI** —— 终端里是交互式 TUI，其余场景是 headless 单发
- **Web UI** —— `octo serve`，基于 REST + WebSocket 的本地仪表盘
- **桌面应用** —— 原生窗口 + 系统托盘（macOS / Windows / Linux）
- **IM 桥接** —— 微信 iLink、飞书、钉钉、企微、Discord、Telegram，随 `octo serve` 运行
- **VS Code 插件** —— [`open-octo/octo-vscode`](https://github.com/open-octo/octo-vscode)
- **Obsidian 插件** —— [`open-octo/octo-obsidian`](https://github.com/open-octo/octo-obsidian)
- **Go SDK** —— [`pkg/octoagent`](pkg/octoagent)，把 agent 循环嵌进你自己的程序

第八个界面移动端 App 即将上线。哪些接口可以放心依赖见
[COMPATIBILITY.md](COMPATIBILITY.md)；安全边界见 [SECURITY.md](SECURITY.md)。

## 深入了解

完整文档在 **[octo-agent.dev/docs](https://octo-agent.dev/docs/zh/)**：

- [Skills](https://octo-agent.dev/docs/zh/guides/use-skills/) —— 兼容 Claude Code 的 SKILL.md；软链 `~/.claude/skills` 直接复用现有技能
- [沙箱与回收站](https://octo-agent.dev/docs/zh/guides/sandbox-the-agent/) —— OS 强制隔离（Seatbelt / Landlock），外加文件级回收站，agent 的删除和覆盖都先备份
- [MCP 服务](https://octo-agent.dev/docs/zh/guides/connect-mcp-servers/) —— stdio + HTTP、OAuth，以及面向大工具集的 Tool Search
- [记忆](https://octo-agent.dev/docs/zh/guides/memory/) · [子代理](https://octo-agent.dev/docs/zh/guides/sub-agents/) · [工作流](https://octo-agent.dev/docs/zh/guides/workflows/) —— 持久化与多代理编排
- [浏览器自动化](https://octo-agent.dev/docs/zh/guides/browser-automation/) —— CDP 录制 / 回放 / 自愈
- [IM 渠道](https://octo-agent.dev/docs/zh/guides/channels/) —— 把 octo 接进你的聊天 App
- [配置](https://octo-agent.dev/docs/zh/reference/config-file/) · [权限](https://octo-agent.dev/docs/zh/reference/permissions/) · [工具](https://octo-agent.dev/docs/zh/reference/tools/)
- [架构](https://octo-agent.dev/docs/zh/architecture/system-layers/) —— 分层设计、provider 协议、如何扩展

## 开发

```bash
make build         # ./octo
make test          # go test -race ./...
```

项目约定见 [`CLAUDE.md`](CLAUDE.md) 与 [`.octorules`](.octorules)；PR 流程见
[`CONTRIBUTING.md`](CONTRIBUTING.md)。

## 致谢与前人工作

octo 站在两个项目的肩膀上，这点不遮掩：**[Claude Code](https://code.claude.com)**
—— agent 循环、工具集、SKILL.md 格式和整体 harness 行为塑造了 octo 的内部设计；
**[OpenClacky](https://github.com/clacky-ai/openclacky)** —— octo 的 UI 与交互
设计有很大一部分受它启发。有 bug 或者设计得不好的地方，都算 octo 自己的。

## 贡献者

感谢每一位为 octo 做出贡献的人：

<a href="https://github.com/open-octo/octo-agent/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=open-octo/octo-agent" alt="Contributors" />
</a>

## 许可

MIT。见 [`LICENSE.txt`](LICENSE.txt)。
