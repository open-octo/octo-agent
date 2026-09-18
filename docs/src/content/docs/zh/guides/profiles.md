---
title: 同时跑多个 octo
description: 用 profile 在同一台机器上开出第二套身份、记忆、会话和凭证。
---

octo 记住的关于你的一切都在 `~/.octo` 下面：它认为你是谁、它学到了什么、每一次会话、你的 key、
你的 skill。一个目录，一个 octo。

profile 给你第二个。`--profile work` 把这整套东西换到 `~/.octo-work`，两边互相看不见。

```bash
octo --profile work
```

功能就这么多。真正值钱的是这条线两边各自装了什么。

## 一个 profile 隔开了什么

每个 profile 各有一份：

- **身份与记忆**——`soul.md`、`user.md`、`octorules.md`、`memories/`
- **会话**——`sessions/`、会话分组、回收站、输入历史
- **凭证与配置**——`config.yml`（provider、模型、端点、API key）、`serve.env`
- **能力**——`skills/`、`workflows/`、`agents/`，以及在旁边物化出来的那几套内置内容
- **对外连接**——`mcp.json` 和它的 OAuth token、`channels.yml` 和 IM 凭证、tunnel 身份
- **治理**——`permissions.yml`、`audit.log`、hooks 及其信任记录
- **运行时状态**——后端的 pid、日志、上传、定时任务、轻应用、浏览器录制

有两样东西是刻意共享的：

- **`~/.octo/bin`**——安装器铺在那儿的辅助二进制，比如 `uv`。那是机器级的工具，不是你的数据；
  多开一个 profile 不该意味着再装一套 Python 工具链。
- **项目本地的 `.octo/`**——仓库自己的 `hooks.yml`、worktree、轻应用属于仓库，你从哪个 profile
  打开它，它就跟到哪里。

另外，全新的 profile 并不是空的。随二进制发布的 skill、workflow 和专家智能体会在首次运行时物化
进去，跟你第一个 profile 当初一样。空掉的是原本属于你的那部分：配置、记忆、会话，以及你自己装的
任何东西。

## 命名规则

字母、数字、`-` 和 `_`，且必须以字母或数字开头。

```bash
octo --profile team-1     # 可以
octo --profile "my work"  # 拒绝
octo --profile _lead      # 拒绝——不能以下划线开头
```

octo 不会校验一个 profile 是否"已存在"，因为创建它的方式就是用它。这也意味着打错一个字母不会
报错，而是悄悄开出第三个空的 octo，所以值得时不时看一眼自己到底有哪些：

```bash
ls -d ~/.octo*
```

## 在命令行里

这是个全局 flag——任意位置、任意子命令、两种写法都行：

```bash
octo --profile work                       # 交互模式
octo --profile=work "帮我梳理一下这个仓库"   # 一次性执行
octo config --profile work                # 配置这个 profile 的 provider 和模型
octo skills list --profile work
```

也可以用环境变量 `OCTO_PROFILE`，配 alias 用它更顺手：

```bash
alias octow='OCTO_PROFILE=work octo'
alias octop='OCTO_PROFILE=home octo'
```

关于这个环境变量有一点要知道：octo 会把它传给自己启动的每一个子进程。agent 用 `terminal` 工具跑
的命令会继承它，包括嵌套调用的 `octo`。多数时候这正是你要的——子代理留在同一套数据里——但也意味着
你在 `work` 会话里跑的脚本读到的是 `work` 的配置，不是默认那套。

TUI 界面不会显示当前在哪个 profile。拿不准的话，`octo serve` 启动时会打印，或者直接用上面那条命令
看目录。

## 管理 profile

profile 在第一次有东西以它的名字运行时就诞生了，所以单单一句 `--profile scratch` 就能造出一个。管理
命令管的是之后的事：看看磁盘上有哪些、提前建好一个目录、以及把某一个删掉。

```bash
$ octo profiles
NAME     SIZE     STATUS                        PATH
default  412.3MB  current, running (pid 96688)  /Users/you/.octo
home     18.0MB   -                             /Users/you/.octo-home
work     96.5MB   running (pid 96701)           /Users/you/.octo-work

$ octo profiles create lab        # 建一个空的 ~/.octo-lab，随后 `octo --profile lab` 就能用
$ octo profiles path work         # /Users/you/.octo-work
$ octo profiles rm home --yes     # 删掉 ~/.octo-home 及其中一切
```

`rm` 不可撤销——这个目录里装着该 profile 的配置和 API key、会话、记忆、技能、IM 凭证和日志，
而且不走回收站。不带 `--yes` 只会打印将要删除的内容。三种目录一律拒删：默认的 `~/.octo`
（它还存着 `~/.octo/bin` 这类机器级的东西）、命令自身所在的 profile、以及后端还在跑的 profile。
后一种先把它停掉：

```bash
octo serve --profile home stop
octo profiles rm home --yes
```

Web 界面在 **设置 → 数据管理 → Profile** 里提供同样三件事：列表会标出你眼前这个后端运行在哪个
目录下、哪些目录的后端正在跑，删除时要你把 profile 名字敲一遍。它不能切换 profile——切换意味着
重启后端，那是桌面端托盘菜单的活（见下文），或者重新起一个 `octo serve --profile`。

## 跑一个后端

`octo serve` 是 profile 从私事变成公事的地方，因为两个后端不能共用一个端口。

默认 profile 保持 `127.0.0.1:8088`——所有客户端内置的就是这个号。命名 profile 第一次启动时从
8089 往上取第一个空闲端口，然后就记住它：

```bash
$ octo serve --profile work -d
octo serve daemon started (pid 96701), ready at http://127.0.0.1:8089

$ octo serve --profile home -d
octo serve daemon started (pid 96711), ready at http://127.0.0.1:8090
```

这个选择记在该 profile 数据根目录下的 `serve.addr` 里，之后每次启动原样复用。这才是关键：你填进
手机、Obsidian 插件或 VS Code 的那个地址，只有在重启后依然有效才算数。

正因为它是一份承诺而不是一个偏好，记下的端口如果被占了，octo 会直接报错——它不会悄悄挪到下一个，
把所有记着旧号码的客户端晾在那儿：

```
$ octo serve --profile work
octo serve: profile "work" is pinned to 127.0.0.1:8089, but that address is in use (listen tcp 127.0.0.1:8089: bind: address already in use)
  pinned by: /Users/you/.octo-work/serve.addr
  if this profile's own backend is already up: octo serve --profile work status
  to move this profile somewhere else: octo serve --profile work --addr 127.0.0.1:<port>
```

要么把端口腾出来，要么主动给这个 profile 搬家。`--addr` 永远优先，而且会改写记录：

```bash
octo serve --profile work --addr 127.0.0.1:9100
```

守护进程的控制也是按 profile 分开的，`status` 会告诉你这个 profile 在哪儿监听——对自动选出来的
端口来说，这是唯一能查到的地方：

```bash
octo serve --profile work status   # octo serve daemon: running (pid 96701) at http://127.0.0.1:8089
octo serve --profile work stop
```

托管给 service manager 时，unit 必须自己写明 profile，没有任何东西会替你推断：

```ini
ExecStart=/usr/local/bin/octo serve --profile work --no-supervisor
EnvironmentFile=%h/.octo-work/serve.env
```

Web 界面会在侧栏底部版本号旁边显示一个小徽章标出当前 profile，`GET /api/version` 对命名 profile
也会带上 `profile` 字段。两者是同一个理由：同时开着好几个后端时，你眼前这个标签页没有别的线索能
说明它连的是哪套数据。

## 在桌面端

双击图标不会传任何参数，所以桌面端没法像 CLI 那样被告知要开哪个 profile。它改成记住。

打开托盘菜单，在 **Profile** 子菜单里选一个，应用会记下这个选择并重启进去。子菜单列出的是磁盘上已有
的 profile，而且只有存在一个以上时才出现。不开终端就想造出第二个，去 **设置 → 数据管理 → Profile**。

一个应用，一个 profile。切换要重启，而重启会丢掉 octo 手头正在做的事，所以它会先问一句——但只在
确实有东西可丢的时候问。后端空闲时直接切，不弹框。如果 octo 正在处理任务，或者正在等你回答，它会
先说清楚是哪一种再继续。

从终端启动依然可用，而且优先级更高：

```bash
octo-desktop --profile work
```

这一条刻意不会被记住。一次性的启动不该改掉双击图标打开的是哪个。

## 到底拿它做什么

**工作和私人分开。** 最清楚的一个用法，也是那几个身份文件存在的意义。两份不同的 `soul.md`，两套
永不互相污染的记忆。如果 octo 只是个编码 CLI，这没什么意义；但对一个本来就该记住你是谁的东西来说，
把公司的上下文和你自己的搅在一起，恰恰是最该避免的。

**分开的 key 和端点。** 一边是公司的 Anthropic key，一边是你自己的 DeepSeek 或本地 Ollama。不用
再每次跑之前改配置。

**两个 IM 身份。** `channels.yml` 和 IM 凭证在每个 profile 里都只有一份，所以在 profile 之前，
一台机器只能挂一个 bot 身份。现在公司飞书 bot 和你个人的 Telegram 可以同时在线，两个后端，两个端口。

**一严一松两套。** `permissions.yml` 和 `audit.log` 按 profile 分开，所以可以让一个跑 `strict`
并且全量审计——客户环境、生产访问——另一个跑 `auto` 供你自己折腾。

**用完就扔的。** `--profile scratch` 就是一个全新的 octo：没有记忆、没有配置、从头走 onboarding。
适合复现别人的 bug、录 demo、截文档图，全程不碰你真正的环境。用完 `rm -rf ~/.octo-scratch`。
比伪造 `HOME` 干净，因为 `~/.octo/bin` 是共享的，你不用为此重装一遍工具链。

## 需要知道的边界

- **这是组织手段，不是安全边界。** 同一个用户、同一套文件权限。profile 让客户的凭证跟别的东西
  *分开放*，但挡不住任何以你的身份运行的程序。
- **东西不会自己搬过去。** 新 profile 里属于你的部分是空的。想把某个 skill 或 agent 带过去，
  就是一条 `cp`。
- **检索不跨 profile。** 记忆和会话搜索到边界为止。这是你换来隔离所付的代价。
- **桌面端一次只开一个。** 两个 CLI 后端可以并排跑，两个桌面应用不行。
- **没有 `octo profiles list`。** 命令行用 `ls -d ~/.octo*`，桌面端看托盘子菜单。
