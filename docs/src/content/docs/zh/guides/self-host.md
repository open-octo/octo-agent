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

`--no-supervisor` 会跳过这整套机制，直接跑 worker——把重启完全交给你自己的 init 系统：

## 作为系统服务运行

`octo serve` 是一个长期运行的单进程；通常的做法是交给你的 init 系统托管，而不是在终端里挂着：

```ini
# systemd（Linux）—— ~/.config/systemd/user/octo.service
[Unit]
Description=octo serve

[Service]
ExecStart=/usr/local/bin/octo serve --no-supervisor
Restart=on-failure

[Install]
WantedBy=default.target
```

`--no-supervisor` 让你的 init 系统自己管重启，不再让 octo 自带的自重启 supervisor 重复干这件事。
在 macOS 上，一份带等价 `ProgramArguments` 和 `KeepAlive` 的 `launchd` plist 效果一样——
这正是 `.pkg` 安装器自动注册的东西。

## 日志与排障

前台运行（`octo serve`）会把输出直接打到启动它的那个终端。后台模式（`-d`）没有终端可写，
所以输出——包括 IM 桥接的连接错误，因为桥接和 API 服务是同一个进程——会写到 `~/.octo/serve.log`：

```bash
octo serve --status   # 守护进程是否在跑，pid 是多少
tail -f ~/.octo/serve.log
octo serve --stop
```

守护进程的 pid 记录在 `~/.octo/serve.pid` 里；`--status`/`--stop` 直接读这个文件，不会去扫进程表。
一个指向已经死掉的进程的过期 pid，会在下一次 `--status` 或启动时自动清掉。

下一步：在前面挂一个反向代理做 TLS/域名，然后把同一个运行中的实例
[接入聊天应用](/docs/zh/guides/channels/)。
