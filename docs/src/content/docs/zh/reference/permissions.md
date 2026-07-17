---
title: 权限系统
description: 门控每一次工具调用的 allow/deny/ask 规则引擎。
---

不管是 CLI、Web UI 还是 IM 渠道，每一次工具调用在真正执行前都会经过同一套权限引擎。这一页是规则
文件的参考；这套引擎在整体流程里所处的位置见[Agent 循环](/docs/zh/concepts/agent-loop/)。

## `~/.octo/permissions.yml`

顶层的 key 是工具名；每个工具下面是一份有顺序的规则列表。每条规则要么是 `allow:`、要么是 `deny:`、
要么是 `ask:`，每个匹配子句要么是 `pattern`（子串匹配，给 `terminal` 用）、要么是 `hostname`
（glob 列表，给 `web_fetch` 用）、要么是 `path`（glob 列表，支持 `$CWD` 展开，给文件类工具用）。
匹配结果按**档位而不是声明顺序**判定——**`deny` 永远压 `ask`，`ask` 永远压 `allow`**——命中的
理由取该档位里第一条匹配的规则。所有档位都没匹配到才落到隐式的 `ask`。

```yaml
terminal:
  - deny:  { pattern: "rm -rf /" }
  - ask:   { pattern: "sudo " }
  - allow: { pattern: "git status" }
  # 其他情况 => 隐式 ask

web_fetch:
  - deny:  { hostname: ["10.*", "192.168.*", "127.*", "localhost", "*.local"] }
  - allow: { hostname: ["github.com", "*.github.com"] }

write_file:
  - deny: { path: ["**/.ssh/**", "/etc/**", "**/.env"] }
  # 这里没有给 $CWD/** 配 allow —— 见下面的说明
```

在 `$CWD` 里面**不等于**对 `write_file`/`edit_file` 免检——只有 `read_file` 才把整个文件系统
（除了那几条针对凭据路径的 deny）当作可以无需询问直接读取的安全区。写或改任何路径，包括 cwd 内的，
只要没有规则命中都会落到隐式的 `ask`，所以下面的 `--permission-mode` 才是真正决定它是弹确认、
自动放行、还是拒绝的东西。

你写在文件里的某个工具的 key 会**整体替换**内置默认的那份规则列表，而不是往上面追加——默认规则里
还想保留的部分，需要自己抄一份过来。有一个例外不受替换影响：一小批硬编码的灾难级 deny 规则
（`rm -rf /usr`、往设备上 `dd`、`mkfs`、`shutdown` 这类）会追加在你的规则之后，并借 deny 档位
的优先级生效——你在文件里写 `allow` 也压不过它们。

### 匹配细节

- `terminal` 的 `pattern` 以 `^` 开头时是**命令位置锚定**：只在"命令词能出现的位置"匹配——行首、
  链式操作符（`;`、`&&`、`|`）之后、或 `sudo`、`env`、`VAR=…` 这类透明前缀之后。用它来避免子串
  误伤：裸写 `deny: {pattern: "format"}` 会连 `docker ps --format json` 一起挡掉，`"shutdown"`
  会挡掉 `git commit -m "fix shutdown handling"`——而 `^format` / `^shutdown` 只匹配作为命令执行
  的那个词，不碰参数和引号里的文本。
- `terminal` 的 `pattern` 如果以 `/` 或 `~` 结尾，会做边界锚定：`deny: {pattern: "rm -rf /"}`
  会挡住清空根目录，但不会挡住 `rm -rf /Users/me/project`。
- `terminal` 的 **allow** 规则比 `deny`/`ask` 严格：命令必须（去掉首尾空白后）以这个 pattern
  开头，并且**不能**包含任何 shell 连接用的特殊字符（`; | & $ ( ) < > `` ` `` 换行）——所以
  `ls && rm -rf /` 蹭不过一条 `allow: "ls"` 的规则。
- `hostname` 的 glob 里，一个 `*` 只匹配一段 DNS 标签——`*.dev` 能匹配 `foo.dev`，匹配不了
  `foo.bar.dev`。
- `path` 的 glob 里，`**` 可以匹配任意多段路径；`$CWD` 会展开成引擎构造时的工作目录。
- `permissions.yml` 文件不存在不算错误——直接用内置默认规则。

## `--permission-mode`

三个取值，优先级和其他配置一样（flag > `config.yml` > 内置默认）：

| 模式 | 对 `ask` 这个结论做什么 |
|---|---|
| `interactive`（主 CLI 的默认值） | 原样传下去——由调用方弹出确认 |
| `auto` | 直接判成 `allow`，不弹确认 |
| `strict` | 直接判成 `deny`——评测、IM 桥接，以及其他无人值守场景用这个姿态 |

mode 只会去改**隐式或显式的 `ask`** 这一种结论——规则匹配出来的明确 `allow` 或 `deny` 永远不会被
mode 覆盖。

:::note
`octo init` 会把自己的 `--permission-mode` 默认设成 `strict`，跟主 CLI 的 `interactive` 默认值
无关——它是一次性的分析运行，不是一个交互式会话。它在 `strict` 模式下依然能不弹确认地写出
`.octorules`，但不是因为 strict 模式允许 cwd 写入：`octo init` 把自己的工作目录显式传成了一个
写入白名单根目录（跟 memory 目录用的是同一套机制），跟 `permissions.yml` 无关，也不受 mode 影响。
:::

:::note
cron 定时任务的会话是另一个"需要在无人应答的情况下写文件"的场景。新建的任务会话默认走 `auto`，
而不是普通 web/CLI/IM 会话的全局 `interactive` 默认值——如果 `config.yml` 显式配置了
`permission_mode`（`interactive`/`strict`/`auto` 任意一个），仍然照常尊重；只有"什么都没配置"
这一种情况的兜底值不一样。
:::

## 记住一次选择

在交互式确认里回答"总是允许"，会允许这个确切的 `(工具, 输入)` 组合，但只在**这个会话生命周期内**
有效——它从来不会被写进 `permissions.yml`；长期生效的策略始终需要手动改文件才算数。这个功能在三种
传输方式上表现一致（TUI/Web 弹窗里的"总是允许"选项，或者在 IM 里直接回复"总是允许"）。

`write_file`/`edit_file` 是"确切 `(工具, 输入)`"这条规则的例外：它们的输入里还带着新内容，每次
调用都不一样，如果按完整输入记，缓存永远不会命中第二次。这两个工具只按路径记——批准一次编辑后，
本次会话内对**这个文件**的后续编辑都不会再问，换一个文件还是会问一次。

一条 `deny` 规则永远压得过"记住"的缓存——规则先扫一遍，只有规则的结论不是 `deny` 时才会去看这份
记住的缓存。所以即使用户之前说了"总是允许"，之后收紧 `permissions.yml` 也会在下一次调用时立刻生效。
反过来，切到 `strict` 模式**不会**追溯撤销之前已经记住的允许——mode 只管未来还没被回答的确认。
这份记住的缓存会在对应会话结束时被清掉：Web UI 里删除会话时，或者 IM 里执行
`/bind`/`/unbind`/`/new`/`/clear` 时（见[Slash 命令](/docs/zh/reference/slash-commands/)）。

下一步：[`PreToolUse` hook](/docs/zh/guides/hooks/) 可以在这些规则之上再加更严的门控——但永远
不能放宽，因为规则给出的明确 `deny` 是最终结论。
