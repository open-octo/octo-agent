---
title: 自托管 octo serve
description: 把 Web 控制台和 IM 桥接跑成一个长期在线的服务。
---

```bash
octo serve                      # 默认绑定 127.0.0.1:8088
octo serve -d                   # 后台运行
octo serve --stop               # 停止后台实例
octo serve -addr :8088          # 暴露到局域网
```

## Profile

下文路径均以默认 profile 为例，其服务数据存放在 `~/.octo`。使用 `octo serve --profile work` 启动时，隔离的 `work` profile 数据根目录为 `~/.octo-work`；机器管理的辅助工具始终共享在 `~/.octo/bin`，不随 profile 隔离。

默认 profile 固定绑 `127.0.0.1:8088`，这是所有客户端内置的端口。命名 profile 不能跟它抢，所以第一次 `octo serve --profile work` 会从 8089 往上取第一个空闲端口，并记到 `~/.octo-work/serve.addr`。之后每次启动都复用同一个地址，手机、Obsidian 插件、VS Code 只需要配一次：

```bash
octo serve --profile work -d
# octo serve daemon started (pid 41288), ready at http://127.0.0.1:8089

octo serve --profile work status
# octo serve daemon: running (pid 41288) at http://127.0.0.1:8089
```

如果记下的端口被别的东西占了，`octo serve` 会直接报错停下，而不是换一个——后端悄悄换地方，等于所有客户端都找不到它。要么把端口腾出来，要么用 `--addr` 主动搬家，搬完会记下新地址：

```bash
octo serve --profile work --addr 127.0.0.1:9100
```

桌面版一次只开一个 profile——数据根目录是进程级的，正因如此 octo 里每一处路径都能自己解析出来，不用一层层传。在托盘的 **配置** 子菜单里选，列出的是磁盘上已有的 root；选完会把选择记到 `~/.octo/desktop-profile` 并重启进去。只有存在一个以上 profile 时才会出现这个子菜单。如果 octo 正在跑任务或正在等你回答，重启前会先问一句，因为这些都会随重启丢掉。

从终端启动仍可以用 `octo-desktop --profile work`，而且不会改变双击图标打开的是哪个 profile。

## 环境变量

完全用环境变量配置 octo（`config.yml` 里什么都不写）需要**两个**变量，不是一个：`OCTO_PROVIDER` 指定用哪家，那家的 key 负责鉴权。光有 key 只说明你**能**连到哪些家，不代表你想用哪家，所以 octo 不替你猜——没有 `OCTO_PROVIDER` 就当作没配置，照常要求你走配置流程。

部分环境变量（`ANTHROPIC_API_KEY`、`OPENAI_API_KEY`、`OCTO_ACCESS_KEY`、`OCTO_LOG_LEVEL`，以及 `TAVILY_API_KEY` 等搜索 key）在运行时控制 octo 的行为。通常由 shell profile export —— 但 **GUI 启动的进程不会继承这些变量**：桌面应用、launchd agent、`.desktop` session 启动时只拿到最小环境，不读 `~/.bashrc` / `~/.zprofile`。

放一份 `~/.octo/serve.env` 即可统一覆盖默认 profile 的所有启动方式。具名 profile 则使用其数据根目录下的对应文件——例如 `octo serve --profile work` 会加载 `~/.octo-work/serve.env`：

```bash
cat > ~/.octo/serve.env << 'EOF'
TAVILY_API_KEY=tvly-xxxxx
OCTO_PROVIDER=anthropic
ANTHROPIC_API_KEY=sk-ant-xxxxx
OCTO_LOG_LEVEL=debug
EOF
chmod 600 ~/.octo/serve.env
```

octo 在启动时（早于任何工具或 channel 读取环境）加载它：

- **简单的 `KEY=VALUE` 行**，一行一个。
- `#` 注释和空行会被跳过。
- 允许 `export ` 前缀（方便直接从 shell rc 文件复制粘贴）。
- Key 会**去空白**；value 内部可以含 `=`（`KEY=val=ue` 能用）。
- **已在进程环境中设置的变量不会被覆盖** ——显式的 `FOO=bar octo serve`、systemd `Environment=`、launchd `SetEnvironmentVariable` 都优先于本文件。这让文件保持为安全的 fallback，不会意外覆盖你显式设的值。

systemd/launchd 打包模板通过 `EnvironmentFile=%h/.octo/serve.env`（`packaging/systemd/octo.service`）把默认 profile 服务指向此文件。要运行具名 profile，unit 必须调用 `octo serve --profile work` 并使用对应的 `EnvironmentFile=%h/.octo-work/serve.env`；现有 unit 不会自行选择 profile。桌面版和 TUI 也会解析所选 profile 对应的文件。

代理环境变量（`HTTPS_PROXY` 等）也走这同一个文件——用法见[选择 Provider · 通过代理访问](/docs/zh/getting-started/choose-a-provider/#通过代理访问)。

## 访问控制

`127.0.0.1` 是回环地址，默认就被信任——不需要 key，这也是默认的绑定方式。一旦绑定得更宽
（`-addr :8088` 或任何非回环地址），每一个非回环客户端发来的 API 和 WebSocket 请求都必须带上访问密钥：

```bash
octo serve -addr :8088 --access-key <key>
```

不传 `--access-key` 时，octo 会依次读取 `OCTO_ACCESS_KEY`、`config.yml`，都没有就自动生成一个并
持久化——启动时会打印一个带 key 的、可以直接打开的 URL（`http://<host>:<port>/?access_key=...`）。

完整的安全边界——防住了什么、明确不管什么——见[安全模型](/docs/zh/reference/security/)。

## 重启

默认情况下 `octo serve` 是**supervisor + worker** 两进程结构：supervisor 启动真正干活的 worker
进程，如果 worker 以退出码 `42` 退出（和 [CLI 参考](/docs/zh/reference/cli/)里"重启请求"是同一套
契约），supervisor 会重新解析一遍二进制路径——这样换了新二进制也能生效——然后重新拉起它。
其他任何退出码都不会触发这个逻辑。

触发重启可以通过 `POST /api/restart`（立即返回 `202`），也可以由模型调用 `restart_server` 工具——
这个工具被显式钉死在 `ask` 权限档位上，不可能被误加进白名单。不管走哪条路，都会先等正在进行的
轮次跑完（或者等满 30 秒超时，以先到者为准）才真正退出进程；在这段排空窗口期新发起的轮次会被拒绝，
提示你过一会儿再试一次——所有传输方式（包括 IM）都是这个提示。

模型没法绕开这套机制走粗暴路线：agent 本身就跑在 server 进程里，所以会杀掉 `octo serve` 或其
supervisor 的 shell 命令（`kill <pid>`、`pkill octo`，包括 `kill $P` 这类要等 shell 展开后才现形的
间接写法）都会被拒绝，并提示模型改用 `restart_server`。这层防护针对的是模型条件反射式的
"杀进程重启"，避免你的会话被它自己掐断；它不是沙箱——真正的隔离见
[沙箱化运行](/docs/zh/guides/sandbox-the-agent/)。

> `restart_server` 依赖 supervisor 重启契约。桌面版以内嵌进程方式运行 server，没有 supervisor，
> 所以不提供这个工具——channel 配置走热加载生效（`POST /api/channels/<platform>/reload`），
> 其他改动则需重启应用。

`--no-supervisor` 会跳过这整套机制，直接跑 worker——把重启完全交给你自己的 init 系统：

## 作为系统服务运行

`octo serve` 是一个长期运行的单进程；通常的做法是交给你的 init 系统托管，而不是在终端里挂着：

```ini
# systemd（Linux）—— ~/.config/systemd/user/octo.service
[Unit]
Description=octo serve

[Service]
EnvironmentFile=%h/.octo/serve.env
ExecStart=/usr/local/bin/octo serve --no-supervisor
Restart=on-failure

[Install]
WantedBy=default.target
```

`--no-supervisor` 让你的 init 系统自己管重启，不再让 octo 自带的自重启 supervisor 重复干这件事。上面的 unit 对应默认 profile；如需 `work`，设为 `EnvironmentFile=%h/.octo-work/serve.env`，并使用 `ExecStart=/usr/local/bin/octo serve --profile work --no-supervisor`。
在 macOS 上，一份带等价 `ProgramArguments` 和 `KeepAlive` 的 `launchd` plist 效果一样——
这正是 `.pkg` 安装器自动注册的东西。

## 日志与排障

前台运行（`octo serve`）会把输出直接打到启动它的那个终端。后台模式（`-d`）没有终端可写，
所以输出——包括 IM 桥接的连接错误，因为桥接和 API 服务是同一个进程——会写到默认 profile 路径
`~/.octo/serve.log`；具名 profile 则使用对应路径，例如 `~/.octo-work/serve.log`：

```bash
octo serve --status   # 守护进程是否在跑，pid 是多少
tail -f ~/.octo/serve.log
# work：octo serve --profile work --status；tail -f ~/.octo-work/serve.log
octo serve --stop
```

守护进程的 pid 记录在默认 profile 路径 `~/.octo/serve.pid`；具名 profile 使用对应路径，例如
`~/.octo-work/serve.pid`。`--status`/`--stop` 直接读这个文件，不会去扫进程表。一个指向已经死掉的
进程的过期 pid，会在下一次 `--status`、`--stop` 或启动时自动清掉。

如果桌面端不是报错而是直接闪退，看默认 profile 路径 `~/.octo/crash.log`（Windows 上是
`%USERPROFILE%\.octo\crash.log`）；具名 profile 使用 `~/.octo-NAME` 下的对应路径。GUI 进程没有终端可以把崩溃信息打出来，所以 app 启动时会把自己的
stderr 指向这个文件：每次启动都会追加一行带版本号和 pid 的标记，后面跟着崩溃时的调用栈（如果崩了的话）。
报告崩溃时请把它一起附上——但贴之前先自己看一眼：MCP server 和它们的子进程也往 stderr 写诊断信息，
所以这个文件里不只有调用栈。

从终端直接跑桌面端二进制时不会做这个重定向，崩溃信息仍然打在终端上，方便开发时直接看到。

下一步：在前面挂一个反向代理做 TLS/域名，然后把同一个运行中的实例
[接入聊天应用](/docs/zh/guides/channels/)。
