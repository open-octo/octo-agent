# octo-agent

[![Go CI](https://img.shields.io/github/actions/workflow/status/open-octo/octo-agent/go.yml?label=ci&style=flat-square)](https://github.com/open-octo/octo-agent/actions)
[![Stars](https://img.shields.io/github/stars/open-octo/octo-agent?style=flat-square)](https://github.com/open-octo/octo-agent/stargazers)
[![Discussions](https://img.shields.io/github/discussions/open-octo/octo-agent?style=flat-square&label=discussions)](https://github.com/open-octo/octo-agent/discussions)
[![Website](https://img.shields.io/badge/website-octo--agent.dev-4f46e5?style=flat-square)](https://octo-agent.dev)
[![Go](https://img.shields.io/badge/go-%3E%3D%201.25-00ADD8?style=flat-square)](https://go.dev)
[![License](https://img.shields.io/badge/license-MIT-lightgrey?style=flat-square)](LICENSE.txt)

<p align="center">
  <a href="README.md">简体中文</a> · <a href="README_EN.md">English</a>
</p>

<p align="center">觉得有用的话，给 octo 点个 ⭐ 吧！</p>

> **开源、单二进制、自托管的 AI Agent。** coding agent 能力对标 Claude Code；作为
> 个人助手，它比 OpenClaw 更轻量 —— 一个 MIT 开源的 Go 二进制，无需 Node / Python /
> Ruby，接入**任意模型**（DeepSeek、Kimi、Anthropic、OpenAI 或任何兼容端点），服务
> 和数据都留在你自己的机器上。

```bash
curl -fsSL https://octo-agent.dev/install.sh | sh     # 单二进制，无需 Node / Ruby / Python 环境
octo config                                            # 选 provider，填 key（DeepSeek / Kimi / 百炼 …）
octo "给 octo config show 加一个 --json 参数并跑测试"   # 一句话 → 完整 agentic 工具循环
```

<p align="center">
  <img src="docs/assets/octo-demo-2.gif" alt="Octo 用三个 sub-agent 并行探索 TUI、IM、Mobile 模块" width="100%">
</p>

## 安装

- **Linux / macOS** — `curl -fsSL https://octo-agent.dev/install.sh | sh`
- **Windows** — `irm https://octo-agent.dev/install.ps1 | iex`
- **国内网络** — 官网或 GitHub 打不开时，走下载镜像（安装与 `octo upgrade` 会自动回退到镜像）：
  `curl -fsSL https://dl.octo-agent.dev/install.sh | sh`（Windows：`irm https://dl.octo-agent.dev/install.ps1 | iex`）
- **桌面应用** — 从[最新 release](https://github.com/open-octo/octo-agent/releases/latest)下载安装器：
  `octo-setup.pkg`（macOS）、`octo-setup.exe`（Windows）、`Octo-x86_64.AppImage`（Linux）
- **Go** — `go install github.com/open-octo/octo-agent/cmd/octo@latest`

随时用 `octo upgrade` 升级。各平台细节 —— Gatekeeper / SmartScreen 提示、卸载、
从源码构建 —— 见[安装指南](https://octo-agent.dev/docs/zh/getting-started/install/)。
安装器暂未做代码签名，完整说明与哈希校验方式见
[SECURITY.md](SECURITY.md#code-signing-policy)。

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

octo 不是又一个需要“养”的 agent 框架。它的定位更接近 Codex / WorkBuddy：
**下载即用、用户友好**，同时把模型选择权、数据所有权和运行环境牢牢留在你手里。

### 1. 开箱即用，不用“养”

OpenClaw、Hermes 这类项目往往需要你调环境、写规则、配技能才能让 agent 跑顺。
octo 默认就把 shell、文件读写改、搜索、MCP、skills、子代理等能力全部打开，
装完一条消息就能真正开始干活。

### 2. 原生支持所有主流模型，缓存不劣化

DeepSeek、Kimi、Qwen、Anthropic、OpenAI，或任何兼容 OpenAI / Anthropic 协议的
端点，octo 都是原生支持。针对国产模型逐家做了提示词缓存优化，Kimi、DeepSeek、
Qwen 的缓存命中率都能到 **95% 以上**；不会像某些方案把 Claude Code 接在国产模型
前面时，因缓存配置不当导致命中率崩塌、token 账单暴涨。

### 3. 八种界面，随处可用

- **CLI / TUI** —— 终端交互与 headless 单发
- **Web UI** —— `octo serve` 本地仪表盘
- **桌面应用** —— macOS / Windows / Linux 原生窗口 + 托盘
- **IM 桥接** —— 微信 iLink、飞书、钉钉、企业微信、Discord、Telegram
- **VS Code 插件** —— [`open-octo/octo-vscode`](https://github.com/open-octo/octo-vscode)
- **Obsidian 插件** —— [`open-octo/octo-obsidian`](https://github.com/open-octo/octo-obsidian)
- **Go SDK** —— [`pkg/octoagent`](pkg/octoagent)，把 agent 循环嵌进你自己的程序
- **移动端** —— iOS + Android 开发者预览（[`mobile/`](mobile/)）

很少有其他 agent 项目能同时覆盖这么多入口。

### 4. 极简内核：单个 ~40 MB 的 Go 二进制

一条命令下载，拷到任何服务器都能立即运行。没有 Node / Python / Ruby 依赖树，
没有 npm 镜像、node-gyp 编译失败、依赖版本冲突的烦恼。

### 5. 零遥测

除了你自己配置的模型 API 调用，octo 自身不会向外发送任何请求。不收集 IP、
机型、模型选择、使用行为——没有任何遥测埋点。

### 6. 桌面安装包也只有 100 MB 左右

基于“单二进制 + 零遥测”这两点，octo 的桌面安装包约为 **100 MB**。相比之下，
Codex 桌面版和 WorkBuddy 动辄 **1 GB 上下**。一个薄薄的 agent harness，没必要占用
那么大的空间。

### 7. 稳定且安全：不会把自己改挂，也不会发疯删数据

- **terminal** 工具直接拒绝任何打向 octo 自身进程的 `kill` / `pkill` / `killall`
  （连 `kill $(pgrep octo)` 这种拐弯变体也认得）。
- **restart_server** 在默认权限规则里被写死为“必须询问”，网页弹确认框、IM 里
  要你明确回复同意才执行；即便同意，也是优雅热重启——先把当前这轮跑完、回复送
  到你手上，supervisor 再拉起新进程，客户端自动重连。
- 所有删除命令都过校验，`rm -rf /`、`rm -rf ~` 这类毁灭性命令被**硬编码拒绝**，
  连自定义权限规则也放不开；普通文件删除和 `write_file` / `edit_file` 覆盖前
  会先备份到回收站（默认保留 14 天、上限 10 GiB），误删永远找得回来。

### 8. 同时做到以上所有的，只有 octo

单看每一点，或许都有其他产品能覆盖其中一两项。但能同时满足“开箱即用 + 原生多模型 + 八界面 + 单二进制 + 零遥测 + 小体积 + 高稳定性 + 强安全”的，只有 octo。

### 9. 最后的建议

如果你能合法稳定地使用 Codex / Claude 订阅，请继续用它们——它们依旧是这个星球上
最顶级的 agent harness。否则，octo 值得你认真看一看。

## 界面

**稳定版（1.0）** 已覆盖 CLI、Web UI、桌面端、IM 桥接、VS Code / Obsidian 插件、Go SDK；
第八个界面——移动端 App（iOS + Android）——已实现，目前是**开发者预览**：现在即可从源码
自建、配合自托管 relay 使用（见 [`mobile/`](mobile/)），托管 relay 与应用商店版本在路上。

哪些接口可以放心依赖见 [COMPATIBILITY.md](COMPATIBILITY.md)；安全边界见
[SECURITY.md](SECURITY.md)。

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

## 社区与交流

- **Bug / 功能建议** —— [GitHub Issues](https://github.com/open-octo/octo-agent/issues)
- **使用问题 / 讨论** —— [GitHub Discussions](https://github.com/open-octo/octo-agent/discussions)，公开可沉淀，后来的同学能搜到答案
- **微信交流群** —— 扫码进群，聊使用心得、提需求、围观 roadmap。群二维码 7 天
  有效、到期会更新；扫不进的话到 Discussions 喊一声：

<p align="left">
  <img src="docs/assets/wechat-group-qr.jpg" alt="octo-agent 微信交流群二维码" width="200">
</p>

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
