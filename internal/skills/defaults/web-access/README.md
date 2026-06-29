# web-access (octo-native)

octo 的联网总入口 skill：把内置的 `web_search` / `web_fetch` 与原生 `browser` 工具按场景编排，并跨 session 积累站点经验。详见 [SKILL.md](./SKILL.md)。

## 与上游的区别

浏览方法论改编自 [web-access](https://github.com/eze-is/web-access)（一泽 Eze，MIT）。本版本把执行层从 Node CDP proxy（`cdp-proxy.mjs` + `curl localhost:3456`）换成 octo 原生的 `browser` 工具（纯 Go CDP，`CGO_ENABLED=0`），因此：

- **无 Node 依赖** — 不再需要 Node.js 22+、`check-deps.mjs` 或常驻 proxy。
- **浏览器操作走 `browser` 工具** — `observe` / `eval` / `click` / `type` / `scroll` / `screenshot` / `upload` / `download` / `cookies`（含 HttpOnly）/ `record_start`+`run_skill`（录制回放自愈，上游没有的能力）。
- **连接** — 在 `chrome://inspect` / `edge://inspect` 勾选 "Allow remote debugging for this browser instance"，或运行 `octo browser setup`。无配置时 `browser` 会自动连上你已开启远程调试的、已登录的浏览器。

## 站点经验

`references/site-patterns/<domain>.md` 按域名存放操作经验（本地、gitignored、跨 session 复用）。

## 已移除

- 本地浏览器书签/历史检索（上游的 `find-url.mjs`）暂无 Go 等价实现，已移除。若需要"我之前看过的那个页面"这类检索，后续可作为独立 Go 工具补回。

## License

MIT。浏览方法论 © [一泽 Eze](https://github.com/eze-is)（web-access）；octo 适配层随本仓库。
