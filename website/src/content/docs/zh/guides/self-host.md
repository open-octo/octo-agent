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

下一步：在前面挂一个反向代理做 TLS/域名，然后把同一个运行中的实例
[接入聊天应用](/docs/zh/guides/channels/)。
