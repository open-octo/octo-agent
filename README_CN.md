<p align="center">
  <img src="docs/assets/octo-demo-2.gif" alt="Octo 用三个 sub-agent 并行探索 TUI、IM、Mobile 模块" width="100%">
</p>

<div align="center">

# octo-agent

**开源、单二进制、自托管的 AI Agent。**

coding agent 能力对标 Claude Code；作为个人助手，它比 OpenClaw 更轻量 —— 一个 MIT 开源的 Go 二进制，无需 Node / Python / Ruby，接入**任意模型**（DeepSeek、Kimi、Anthropic、OpenAI 或任何兼容端点），服务和数据都留在你自己的机器上。

[![Go CI](https://img.shields.io/github/actions/workflow/status/open-octo/octo-agent/go.yml?label=ci&style=flat-square)](https://github.com/open-octo/octo-agent/actions)
[![Stars](https://img.shields.io/github/stars/open-octo/octo-agent?style=flat-square)](https://github.com/open-octo/octo-agent/stargazers)
[![Discussions](https://img.shields.io/github/discussions/open-octo/octo-agent?style=flat-square&label=discussions)](https://github.com/open-octo/octo-agent/discussions)
[![Website](https://img.shields.io/badge/website-octo--agent.dev-4f46e5?style=flat-square)](https://octo-agent.dev)
[![Go](https://img.shields.io/badge/go-%3E%3D%201.25-00ADD8?style=flat-square)](https://go.dev)
[![License](https://img.shields.io/badge/license-MIT-lightgrey?style=flat-square)](LICENSE.txt)

[官网](https://octo-agent.dev) · [English](README.md) · [安装指南](https://octo-agent.dev/docs/zh/getting-started/install/) · [文档](https://octo-agent.dev/docs/zh/) · [社区](#社区与交流)

觉得有用的话，给 octo 点个 ⭐ 吧！

</div>

## 为什么用 octo

octo 不是又一个需要"养"的 agent 框架。OpenClaw、Hermes 这类项目往往需要你调环境、写规则、配技能才能让 agent 跑顺；octo 的定位更接近 Codex / WorkBuddy：**下载即用、用户友好**，同时把模型选择权、数据所有权和运行环境牢牢留在你手里。

```bash
curl -fsSL https://octo-agent.dev/install.sh | sh     # 单二进制，无需 Node / Ruby / Python 环境
octo config                                            # 选 provider，填 key（DeepSeek / Kimi / 百炼 …）
octo "给 octo config show 加一个 --json 参数并跑测试"   # 一句话 → 完整 agentic 工具循环
```

octo 围绕这个定位构建：

- **开箱即用**：shell、文件读写改、搜索、MCP、skills、子代理等能力默认全部打开，装完一条消息就能真正干活。
- **模型选择权在你手里**：任何 OpenAI / Anthropic 协议兼容的端点都是原生支持，不绑定任何一家厂商。
- **数据留在你的机器上**：自托管、零遥测，除了你自己配置的模型 API 调用，octo 自身不向外发送任何请求。
- **随处可用**：同一个二进制同时提供 CLI、Web、桌面、IM、编辑器插件、SDK、移动端八种入口。
- **安全默认值**：毁灭性命令硬编码拒绝、删除和覆盖先进回收站，agent 不会把自己改挂，也不会发疯删数据。

如果你能合规稳定地使用 Codex / Claude 订阅，请继续用它们——它们依旧是这个星球上最顶级的 agent harness。否则，octo 值得你认真看一看。

## Highlights

- **单个 ~40 MB 的 Go 二进制**：一条命令下载，拷到任何服务器都能立即运行。没有 Node / Python / Ruby 依赖树，没有 npm 镜像、node-gyp 编译失败、依赖版本冲突的烦恼。
- **缓存不劣化**：针对国产模型逐家做了提示词缓存优化，Kimi、DeepSeek、Qwen 的缓存命中率都能到 **95% 以上**，token 账单可预期。
- **八种界面**：CLI / TUI、Web UI、桌面应用、IM 桥接、VS Code、Obsidian、Go SDK、移动端——很少有其他 agent 项目能同时覆盖这么多入口。
- **零遥测**：不收集 IP、机型、模型选择、使用行为，没有任何遥测埋点。
- **桌面安装包约 100 MB**：相比之下 Codex 桌面版安装包约 **650 MB**。一个薄薄的 agent harness，没必要占用那么大的空间。
- **稳定且安全**：自我保护、优雅重启、回收站兜底（详见[核心特性](#核心特性)）。

单看每一点，或许都有其他产品能覆盖其中一两项。但能同时满足"开箱即用 + 原生多模型 + 八界面 + 单二进制 + 零遥测 + 小体积 + 高稳定性 + 强安全"的，只有 octo。

## 一个二进制，八种界面

```text
octo（单二进制）
  -> CLI / TUI            终端交互与 headless 单发
  -> Web UI               octo serve 本地仪表盘
  -> 桌面应用              macOS / Windows / Linux 原生窗口 + 托盘
  -> IM 桥接               微信 iLink、飞书、钉钉、企业微信、Discord、Telegram
  -> VS Code 插件          open-octo/octo-vscode
  -> Obsidian 插件         open-octo/octo-obsidian
  -> Go SDK               pkg/octoagent，把 agent 循环嵌进你自己的程序
  -> 移动端                iOS + Android 开发者预览
```

**稳定版（1.0）** 已覆盖 CLI、Web UI、桌面端、IM 桥接、[VS Code](https://github.com/open-octo/octo-vscode) / [Obsidian](https://github.com/open-octo/octo-obsidian) 插件、[Go SDK](pkg/octoagent)；第八个界面——移动端 App（iOS + Android）——已实现，目前是**开发者预览**：现在即可从源码自建、配合自托管 relay 使用（见 [`mobile/`](mobile/)），托管 relay 与应用商店版本在路上。

## 核心特性

### 开箱即用，不用"养"

内置工具（shell、文件读写改、搜索）、MCP 服务、skills、子代理全部默认开启。不需要先调环境、写规则、配技能，装完一条消息就能真正开始干活。

### 原生多模型，缓存不劣化

DeepSeek、Kimi、Qwen、Anthropic、OpenAI，或任何兼容 OpenAI / Anthropic 协议的端点，octo 都是原生支持。针对国产模型逐家做了提示词缓存优化，缓存命中率 95% 以上；不会像某些方案把 Claude Code 接在国产模型前面时，因缓存配置不当导致命中率崩塌、token 账单暴涨。

### 稳定且安全：不会把自己改挂，也不会发疯删数据

- **terminal** 工具直接拒绝任何打向 octo 自身进程的 `kill` / `pkill` / `killall`（连 `kill $(pgrep octo)` 这种拐弯变体也认得）。
- **restart_server** 在默认权限规则里被写死为"必须询问"，网页弹确认框、IM 里要你明确回复同意才执行；即便同意，也是优雅热重启——先把当前这轮跑完、回复送到你手上，supervisor 再拉起新进程，客户端自动重连。
- 所有删除命令都过校验，`rm -rf /`、`rm -rf ~` 这类毁灭性命令被**硬编码拒绝**，连自定义权限规则也放不开；普通文件删除和 `write_file` / `edit_file` 覆盖前会先备份到回收站（默认保留 14 天、上限 10 GiB），误删永远找得回来。
- 还可以再加一层 [OS 强制沙箱](https://octo-agent.dev/docs/zh/guides/sandbox-the-agent/)（macOS Seatbelt / Linux Landlock），按需开启。

### Skills：兼容 Claude Code

[SKILL.md 格式](https://octo-agent.dev/docs/zh/guides/use-skills/)与 Claude Code 兼容，软链 `~/.claude/skills` 就能直接复用现有技能。

### MCP 服务

[stdio + HTTP 两种传输、OAuth 鉴权](https://octo-agent.dev/docs/zh/guides/connect-mcp-servers/)，以及面向大工具集的 Tool Search。

### 记忆、子代理与工作流

[跨会话记忆](https://octo-agent.dev/docs/zh/guides/memory/)、[并行子代理](https://octo-agent.dev/docs/zh/guides/sub-agents/)、[多代理工作流编排](https://octo-agent.dev/docs/zh/guides/workflows/)。

### 浏览器自动化

Go 原生 CDP [录制 / 回放 / 自愈](https://octo-agent.dev/docs/zh/guides/browser-automation/)。

### IM 渠道

[把 octo 接进你的聊天 App](https://octo-agent.dev/docs/zh/guides/channels/)，在微信、飞书、Telegram 里随时找它干活。

## 快速上手

### 安装

- **Linux / macOS** — `curl -fsSL https://octo-agent.dev/install.sh | sh`
- **Windows** — `irm https://octo-agent.dev/install.ps1 | iex`
- **国内网络** — 官网或 GitHub 打不开时，走下载镜像（安装与 `octo upgrade` 会自动回退到镜像）：
  `curl -fsSL https://dl.octo-agent.dev/install.sh | sh`（Windows：`irm https://dl.octo-agent.dev/install.ps1 | iex`）
- **桌面应用** — 从[最新 release](https://github.com/open-octo/octo-agent/releases/latest)下载安装器：
  `octo-setup.pkg`（macOS）、`octo-setup.exe`（Windows）、`Octo-x86_64.AppImage`（Linux）
- **Go** — `go install github.com/open-octo/octo-agent/cmd/octo@latest`

随时用 `octo upgrade` 升级。各平台细节 —— Gatekeeper / SmartScreen 提示、卸载、从源码构建 —— 见[安装指南](https://octo-agent.dev/docs/zh/getting-started/install/)。安装器暂未做代码签名，完整说明与哈希校验方式见 [SECURITY.md](SECURITY.md#code-signing-policy)。

### 首次运行

```bash
octo config                # 一次性设置：选 provider/model，填 API key
octo "介绍一下这个仓库"      # headless 单发：prompt → agentic 工具循环 → 退出
octo                       # 终端交互式 TUI；octo -c 恢复历史 session
octo serve -d              # Web UI + IM 桥接，http://127.0.0.1:8088
```

下一步：[快速上手](https://octo-agent.dev/docs/zh/getting-started/quickstart/) · [选择 provider](https://octo-agent.dev/docs/zh/getting-started/choose-a-provider/) · [CLI 参考](https://octo-agent.dev/docs/zh/reference/cli/)。

## 上手路径

1. 一条命令安装：`curl -fsSL https://octo-agent.dev/install.sh | sh`。
2. `octo config` 选 provider、填 API key。
3. `octo "介绍一下这个仓库"` 单发验证一切正常。
4. `octo` 进入终端 TUI，日常交互。
5. `octo serve -d` 打开 Web UI（`http://127.0.0.1:8088`），或直接用桌面应用。
6. 配置 [IM 渠道](https://octo-agent.dev/docs/zh/guides/channels/)，在微信 / 飞书 / Telegram 里继续对话。
7. 按需加 [skills](https://octo-agent.dev/docs/zh/guides/use-skills/)、[MCP 服务](https://octo-agent.dev/docs/zh/guides/connect-mcp-servers/)、[子代理](https://octo-agent.dev/docs/zh/guides/sub-agents/)。

## 架构

```text
┌────────────────────────────────┐
│           八种界面              │
│  CLI/TUI · Web · 桌面 · IM     │
│ VS Code · Obsidian · SDK · 移动 │
└───────────────┬────────────────┘
                │
┌───────────────▼────────────────┐
│           App 装配层            │
│  provider 构造 · 权限门 · 子代理 │
└───────────────┬────────────────┘
                │
┌───────────────▼────────────────┐
│           Agent 内核            │
│  工具循环 · 历史 · 会话持久化    │
└───────────────┬────────────────┘
                │
┌───────────────▼────────────────┐
│         Provider 适配层         │
│  Anthropic / OpenAI 协议及兼容端点│
└───────────────┬────────────────┘
                │
┌───────────────▼────────────────┐
│            工具层               │
│ shell · 文件 · 搜索 · MCP ·     │
│ skills · 浏览器自动化           │
└────────────────────────────────┘
```

分层设计、provider 协议、如何扩展，见[架构文档](https://octo-agent.dev/docs/zh/architecture/system-layers/)。

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
- **微信交流群** —— 扫码添加个人微信，备注 `octo` 拉你进群，聊使用心得、提需求、围观 roadmap：

<p align="left">
  <img src="docs/assets/wechat.jpg" alt="octo-agent 微信二维码" width="200">
</p>

## 开发

```bash
make build         # ./octo
make test          # go test -race ./...
```

项目约定见 [`CLAUDE.md`](CLAUDE.md) 与 [`.octorules`](.octorules)；PR 流程见 [`CONTRIBUTING.md`](CONTRIBUTING.md)。

## 当前状态

octo 已发布 **1.0 稳定版**，CLI、Web UI、桌面端、IM 桥接、编辑器插件、Go SDK 均可放心使用；移动端处于开发者预览。哪些接口可以放心依赖见 [COMPATIBILITY.md](COMPATIBILITY.md)；安全边界见 [SECURITY.md](SECURITY.md)。

## 致谢与前人工作

octo 站在两个项目的肩膀上，这点不遮掩：**[Claude Code](https://code.claude.com)** —— agent 循环、工具集、SKILL.md 格式和整体 harness 行为塑造了 octo 的内部设计；**[OpenClacky](https://github.com/clacky-ai/openclacky)** —— octo 的 UI 与交互设计有很大一部分受它启发。有 bug 或者设计得不好的地方，都算 octo 自己的。

## 贡献者

感谢每一位为 octo 做出贡献的人：

<!-- 这里刻意手写而不是用 contrib.rocks 生成的整图：那张图要过 GitHub 的 camo
     代理，各边缘节点缓存不一致，同一时间不同访客看到的人数可能不一样，新贡献者
     被漏掉多久也没有上限。少写一个人比手动维护更糟。新增贡献者时在这里加一行。 -->
<p>
  <a href="https://github.com/Leihb"><img src="https://avatars.githubusercontent.com/u/28055438?v=4&s=64" width="64" height="64" alt="Leihb" title="Leihb" /></a>
  <a href="https://github.com/eternalweightlessness"><img src="https://avatars.githubusercontent.com/u/210714574?v=4&s=64" width="64" height="64" alt="eternalweightlessness" title="eternalweightlessness" /></a>
  <a href="https://github.com/kunyuanhe-sudo"><img src="https://avatars.githubusercontent.com/u/292632541?v=4&s=64" width="64" height="64" alt="kunyuanhe-sudo" title="kunyuanhe-sudo" /></a>
</p>

完整记录见 [contributors 图表](https://github.com/open-octo/octo-agent/graphs/contributors)。

## 许可

MIT。见 [`LICENSE.txt`](LICENSE.txt)。
