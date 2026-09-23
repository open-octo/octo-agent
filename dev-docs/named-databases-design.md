# 具名数据库：定时任务写入、页面读取的 SQLite

## 目标

- 定时任务（或任意会话）采集的数据能落进一个持久的 SQLite 库，HTML 制品和轻应用能直接查询它来展示。
- 写数据的一方和展示数据的一方只靠**库名**对接。定时任务不需要知道哪个页面会读、页面在哪个目录；页面改名、保存成轻应用、删除，都不影响库。
- 会话里的 HTML 制品和保存后的轻应用查同一个库，用同一种写法，保存时不需要搬数据。
- Windows、macOS、Linux 行为一致，不依赖系统里装没装 `sqlite3` 命令行。

## 非目标

- 不做库的管理界面（列表、浏览、删除）。库的删除是用户自己删文件。
- 不做跨调用的事务。一次调用执行一条语句。
- 页面不能创建库，也不能跨库查询。
- 不接受任意路径的 `.db` 文件。工具只认库名，分析用户自己的 `.db` 文件不在这个方案里。
- 不防恶意页面。非公开页面与界面同源，本来就能调 `/api`（见 `dev-docs/same-origin-artifacts-design.md` 的非目标），这里给它的读写权限没有扩大它能做的事。

## 库

库文件在 `~/.octo/databases/<name>.db`，目录取自 `datahome.Path("databases")`，随 `--profile` 走。WAL 模式下旁边会有 `<name>.db-wal`、`<name>.db-shm`。

库名匹配 `^[a-z0-9_-]{1,64}$`，其余一律拒绝。库名直接拼成文件名，这条规则同时排除了路径穿越和大小写不敏感文件系统上的重名。

库只由 `sqlite` 工具创建：工具第一次写一个不存在的库时建出文件。页面请求不存在的库返回 404，写错库名不会悄悄多出一个空库。

## 连接

驱动是 `modernc.org/sqlite`（纯 Go，不需要 CGO）。连接由新包 `internal/sqlitedb` 统一打开，工具和服务端都经过它。每次调用打开一个连接、用完关闭，不做连接池：调用频率远低于打开的开销，而且不会有连接一直占着文件，Windows 上占着的文件删不掉也替换不了。

三种模式对应三种 DSN：

| 模式 | 使用方 | DSN `mode` |
|---|---|---|
| 创建并读写 | `sqlite` 工具 | `rwc` |
| 读写，不创建 | 非公开页面 | `rw` |
| 只读 | 公开轻应用 | `ro` |

每个 DSN 都带 `_pragma=busy_timeout(5000)`；`rwc` 额外带 `_pragma=journal_mode(WAL)`，库在创建时就进入 WAL，之后所有连接沿用。`serve` 进程（定时任务、页面请求）和 CLI/TUI 进程可能同时写同一个库，WAL 加 busy_timeout 让写者排队等待，而不是立刻报 `database is locked`。

### ATTACH 必须关掉

只读连接挡不住 `ATTACH`：`mode=ro` 的连接上 `ATTACH DATABASE '<任意路径>' AS o; CREATE TABLE o.x(a)` 会在那个路径建出文件。`VACUUM INTO '<任意路径>'` 同理。不关掉的话，公开应用的只读和工具"只碰 `~/.octo/databases/`"两条边界都会被打穿，公开应用还能 ATTACH 别的库来读。

驱动没有暴露 authorizer，用的是 `sqlite3_limit`：取得连接（`(*sql.DB).Conn`）后，先调 `sqlite.Limit(conn, lib.SQLITE_LIMIT_ATTACHED, 0)`，再执行语句。限制绑定在单个连接上，每个连接都要设。设为 0 后 `ATTACH` 和 `VACUUM INTO` 都报 `too many attached databases - max 0`。

`load_extension` 在这个构建里返回 `not authorized`，`readfile` / `writefile` 函数不存在，不需要额外处理。

### 一次一条语句

驱动会执行传入字符串里的**所有**语句，只返回最后一条的结果：`Query("select a from t; delete from t")` 返回 0 行，同时删光了表。所以执行前要做单语句检查，检测到第二条语句就拒绝。

`internal/sqlitedb` 里用一个小扫描器判断：跳过单引号字符串、双引号 / 反引号 / 方括号标识符、`--` 行注释和 `/* */` 块注释，找到语句外的第一个 `;` 之后，只允许剩下空白、注释和更多的 `;`。触发器的 `BEGIN … ; … END` 在这个扫描器看来是多条语句，同样被拒绝。

### 只读连接只接受查询

只读连接拒绝写入，但不拒绝设置进程级状态的 PRAGMA（`soft_heap_limit`、`hard_heap_limit` 对之后的所有连接生效）。只读模式下语句的第一个关键字必须是 `SELECT`、`WITH` 或 `VALUES`，其余返回 `ErrReadOnly`。读表结构用 `SELECT … FROM pragma_table_info('<表>')`。

非公开页面（读写模式）在此之外只多 `INSERT`、`UPDATE`、`DELETE`、`REPLACE`，其余（`CREATE`、`ALTER`、`DROP`、`PRAGMA`、事务语句）返回 `ErrNotAllowed`。表结构和文件设置归 `sqlite` 工具：页面删了表或者用 `PRAGMA journal_mode` 把库切出 WAL，都会让往里写的定时任务出错。

### 上限

公开应用的查询来自任何拿到链接的人，HTTP 服务又没有写超时（为了 WebSocket，`WriteTimeout: 0`），不加上限的话一个匿名请求就能让一个核一直空转（无终止的递归 CTE），或者要出 GB 级的结果（循环里的 `zeroblob`）。每次调用都带 `sqlitedb.Limits`：

| 上限 | 页面接口 | `sqlite` 工具 |
|---|---|---|
| 行数 | 10000 | 200 |
| 结果累计字节 | 16 MB | 1 MB |
| 单个值（`SQLITE_LIMIT_LENGTH`） | 4 MB | SQLite 默认 |
| 超时 | 10 s | 60 s |

- 读到行数或字节上限就停止读取，`truncated` 为真。不再往下数总行数，查询也就随之停止；需要总数用 `count(*)`。
- 超时通过 context 取消，驱动会中断正在执行的语句，返回 `ErrTimeout`。
- 公开应用的查询全局最多 4 个同时进行，超出时返回 503 `database_busy`。

### 执行

语句统一走 `QueryContext`：

- 结果有列（`SELECT`、`PRAGMA`、带 `RETURNING` 的写入）：返回列名和行。
- 结果没有列：在同一个连接上接着执行 `SELECT changes(), last_insert_rowid()`，返回影响行数和最后插入的 rowid。

值的 JSON 编码：整数、浮点、文本原样，`NULL` 为 `null`，BLOB 为标准 base64 字符串。

参数只支持 `?` 占位的位置参数，数组形式传入。

## agent 端：`sqlite` 工具

```json
{
  "db": "prices",
  "sql": "INSERT INTO quote(ts, symbol, price) VALUES (?, ?, ?)",
  "params": ["2026-09-23T10:00:00Z", "AAPL", 231.4]
}
```

| 参数 | 必填 | 说明 |
|---|---|---|
| `db` | ✅ | 库名 |
| `sql` | ✅ | 一条 SQL 语句 |
| `params` | | 位置参数数组 |

返回：有列时是一行列名加若干行数据，以制表符分隔，最多 200 行、16 KB 输出，单个值超过 1000 字节时截短并注明原长度（整行照常输出），有更多行没显示时注明"more not shown"；无列时是 `changes=<n> last_insert_id=<n>`。SQL 错误原样返回给模型。

工具的描述里讲清楚：库在哪、库名规则、一次一条语句、页面怎么查（见下文），让定时任务的 agent 只看工具描述就能写对。查看已有的库用 `glob` 扫 `~/.octo/databases/`，查看表结构用 `SELECT sql FROM sqlite_master`。

注册：

- 加进 `internal/tools/registry.go` 的 `allTools`，在默认工具集里。
- `internal/permission/defaults.yml` 加 `sqlite: - allow: { pattern: "" }`。定时任务、HTTP、IM 都是非交互 transport，隐式 ask 在那里会被判成 deny，不加 allow 这个工具在定时任务里就用不了。工具能碰的只有 `~/.octo/databases/` 下的文件（ATTACH 已关），不需要逐次确认。
- 不进 `readOnlyTools`：它会写，不能和其他工具并行执行。
- 只读子代理（`internal/app/spawner.go` 里过滤掉 `write_file` / `edit_file` 的那个分支）同样过滤掉 `sqlite`。

## 页面端：查询接口

页面用相对路径请求自己前缀下的保留路径：

```js
const res = await fetch('./__octo/db/prices', {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ sql: 'SELECT ts, price FROM quote WHERE symbol = ? ORDER BY ts', params: ['AAPL'] }),
})
const { columns, rows } = await res.json()
```

路由：

| 页面 | 路由 | 鉴权 | 权限 |
|---|---|---|---|
| 会话制品 | `POST /_artifacts/{token}/__octo/db/{name}` | `s.api`（`requireAuth`），token 必须是有效 grant | 读写 |
| 非公开轻应用 | `POST /_apps/{slug}/__octo/db/{name}` | `requireAuth` | 读写 |
| 公开轻应用 | 同上 | 不鉴权 | 只读，且库必须在 manifest `databases` 里 |

轻应用的分流沿用 `handleLightAppPage` 的做法：鉴权前先读 manifest，`public` 为真就走只读分支，否则套 `requireAuth`。公开应用**一律只读**，即使访问者是已登录的本人，这样页面代码不需要按访问者分支。

与现有 `GET /_apps/{slug}/{path...}`、`GET /_artifacts/{token}/{path...}` 方法不同，不冲突。对 `__octo/db/...` 发 GET 落到文件服务，按普通文件查找返回 404。

请求体 `{"sql": "...", "params": [...]}`，上限 1 MB。响应：

```json
{ "columns": ["ts", "price"], "rows": [["2026-09-23T10:00:00Z", 231.4]], "truncated": false }
{ "changes": 1, "last_insert_id": 42 }
```

行数和字节上限见上文，超出时 `truncated` 为 `true`。响应头与页面文件一致（`setPageHeaders`：`no-store`、`nosniff`）。

错误：

| 情况 | 状态 | `error` |
|---|---|---|
| 库名不合法 | 400 | `invalid_database_name` |
| 多条语句、SQL 语法或执行错误 | 400 | SQL 错误信息 |
| 超时 | 400 | `query_timeout` |
| 公开应用读取未声明的库 | 403 | `database_not_declared` |
| 公开应用写入或执行非查询语句 | 403 | `database is read-only here` |
| 非公开页面执行行级读写以外的语句 | 403 | `ErrNotAllowed` 的信息 |
| 库不存在 | 404 | `database_not_found` |
| busy_timeout 后仍被锁；公开查询并发已满 | 503 | `database_busy` |

### manifest `databases`

轻应用 `manifest.json` 新增可选字段 `databases`（字符串数组），列出页面会查的库。它只在应用公开时起作用：公开应用只能读列出的库。非公开时不检查。

这个字段由 agent 写，和 `public` 由 UI 写不同。提示词要求页面用到哪些库就写哪些，这样用户以后在 UI 上打开公开开关时不需要回头补。

因为列表是 agent 写的，用户打开公开开关时的确认框会列出这些库名（`lightapps.public_on_db`），用户知道点下去会把哪些库开放出去。

解码是宽松的：数组照常；单个字符串当作只有一项的列表；其他形状解码为空，不报错。严格解码失败会让整个应用从列表里消失；解码为空对公开应用意味着一个库都读不了。

会话制品没有 manifest，也不会公开，不涉及这个字段。

## 提示词与文档

- `internal/prompt/base.md` 轻应用一节：
  - 新增"Where a page keeps its data"小节，先讲 `localStorage` 和具名库怎么选：只服务于这个页面在这台浏览器里的数据（视图状态、草稿、公开应用里访客自己的状态）用 `localStorage`；会被别人读到的数据（定时任务或 agent 写的、agent 以后要读的、要跨设备的、量大要查询的、用户自己录入丢了会心疼的记录）用具名库，即使页面是唯一的写入方。`localStorage` 的代价写明：按 origin 分开存（桌面端、`localhost` 浏览器、隧道各一份），agent 读不到，容量小，清站点数据就没了。
  - 同一小节讲具名库的用法：页面用 `fetch('./__octo/db/<name>', …)` 查，制品和轻应用写法相同；库和表由 agent 建页面时用 `sqlite` 工具建好；页面用到的库写进 manifest `databases`。
  - 页面写入只做行级操作（`INSERT` / `UPDATE` / `DELETE` / `REPLACE`），`CREATE` / `ALTER` / `DROP` / `PRAGMA` 会被拒绝；表结构由 `sqlite` 工具负责。
  - "Do not call octo's own API from the page" 保留，并注明 `./__octo/db/` 是页面自己的数据接口，不在此列。
  - "When to suggest" 的 ❌ "Backend-dependent workflows" 保留：这里的"后端"指需要 LLM 或服务端逻辑，定时采集加页面展示是 ✅ 场景，补一条。
  - 对话里读写库一律用 `sqlite` 工具，不经 terminal 调 `sqlite3` 或脚本（Windows 没有 `sqlite3`；工具带锁等待和单语句防护）。
  - 每次运行不需要判断的确定性采集任务，建议写成操作系统定时任务（cron / launchd / 任务计划程序）执行的脚本，而不是 octo 定时任务：后者每次都是一轮 LLM，且只在 octo 运行时触发。脚本用语言自带的 SQLite 库写库，约定是：库先用 `sqlite` 工具建好（WAL）；打开时带锁等待；解释器和文件用绝对路径；每次运行写进 `runs` 表供页面显示；注册定时任务前先说明要跑什么、多久一次。
- `sqlite` 工具描述：同样写明对话里用工具、octo 之外运行的脚本用语言自带的 SQLite 库并带锁等待。
- `dev-docs/light-apps-design.md`：manifest 字段表加 `databases`，链接本文档。
- `docs/src/content/docs/guides/light-apps.mdx`、`docs/src/content/docs/zh/guides/light-apps.mdx`：加"展示定时任务采集的数据"一节；"不适合"里的"需要后端 API / 数据库"改为"每次使用都要跑服务端逻辑"。
- `docs/src/content/docs/guides/cron-tasks.md` 及中文版：提一句采集结果可以写进具名库给轻应用展示。

## 依赖

新增 `modernc.org/sqlite` v1.59.0。它的 go.mod 是 `go 1.25.0`，与本仓库和 CI 一致；`golang.org/x/sys v0.47.0` 与现有版本相同。另一个纯 Go 选项 `github.com/ncruces/go-sqlite3` v0.35.5 要求 `go 1.26.0`，会迫使整个仓库升级 Go 版本，不选。

体积：darwin/arm64 上 `go build ./cmd/octo` 从 46,992,658 字节到 53,323,586 字节，+6.3 MB（+13.5%）。

`cmd/octo-desktop/go.mod` 通过 `replace` 引用根模块，在该目录跑 `go mod tidy` 同步新依赖。

## 验收

- 工具：写一个不存在的库时创建文件并进入 WAL；非法库名被拒；`ATTACH`、`VACUUM INTO` 被拒且目标文件不存在；两条语句的输入被拒且第一条没有执行；`SELECT` 超过 200 行时截断并注明总行数；`INSERT` 返回 `changes` 和 `last_insert_id`。
- 并发：两个 `*sql.DB`（模拟两个进程）同时写同一个库，不出现 `database is locked`。
- 接口，制品：有效 grant 下 `POST ./__octo/db/<name>` 可读可写；无效 token 404；库不存在 404 且没有建出文件。
- 接口，非公开轻应用：远程无 cookie 401；带 cookie 可读可写；DDL 和 `PRAGMA` 403，库仍在 WAL。
- 接口，公开轻应用：无 cookie 读已声明的库 200；读未声明的库 403；任何写入 403；`ATTACH`、`PRAGMA` 403；无终止的递归 CTE 在行数上限处返回；100 MB 的 `zeroblob` 被拒。
- 跨站：本机请求带外站 `Origin` 不能改非公开应用的库。
- 上限：超时在限定时间内返回 `ErrTimeout`；字节上限按累计字节截断。
- 页面实测：会话里做一个读具名库的制品，在制品栏里显示数据；保存成轻应用后不改任何代码照常显示；定时任务往库里追加数据后刷新页面能看到新数据。
- Windows CI 通过（WAL 文件锁、路径拼接）。

## 涉及文件

- `internal/sqlitedb/sqlitedb.go`：库名校验、路径、连接、ATTACH 限制、单语句检查、执行与结果编码 <!--lint:new-->
- `internal/tools/sqlite.go`：`sqlite` 工具 <!--lint:new-->
- `internal/tools/registry.go`：`allTools` 注册
- `internal/permission/defaults.yml`：`sqlite` allow
- `internal/app/spawner.go`：只读子代理过滤
- `internal/server/db_pages.go`：两个页面前缀下的查询接口 <!--lint:new-->
- `internal/server/server.go`：路由注册
- `internal/server/lightapps_handlers.go`：`lightAppManifest` 加 `Databases`（宽松解码）
- `web/src/views/LightAppsView.svelte`、`web/src/lib/api.ts`、`web/src/lib/i18n.ts`：公开确认框列出库名
- `internal/prompt/base.md`、`dev-docs/light-apps-design.md`、上文列出的用户文档
- `go.mod`、`go.sum`、`cmd/octo-desktop/go.mod`、`cmd/octo-desktop/go.sum`
