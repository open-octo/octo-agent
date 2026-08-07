# web_fetch HTML 清洗（正文提取 + Markdown 转换）设计

> 状态：已拍板（2026-08-07）· 关联：删 Jina 代理 PR（`internal/tools/web_fetch.go`）

## 背景与动机

web_fetch 删掉 Jina 代理后，直接 fetch 返回的是**原始 HTML**：脚本、样式、导航、广告、侧栏全部糊进模型上下文，既费 token 又淹没有效信息。Jina 原有的核心价值——"把页面变成干净可读的文本"——需要在本地方向补回来。

本设计用**零新增第三方依赖**的方式，在 web_fetch 内部做 HTML 清洗：先正文提取，再转 Markdown。

## 目标

- web_fetch 抓到 `text/html` 页面时，默认输出**正文提取 + Markdown 转换**后的干净文本
- 提取失败（JSON 接口、目录页、表格页、短页面）时回退到**全页 Markdown 转换**，至少去噪、保留结构
- `clean=false` 时保持现状行为（返回原始抓取文本），作为逃生口
- 零新增依赖：解析用 `golang.org/x/net/html`（已是 indirect 依赖，转 direct）

## 非目标

- **JS 渲染**：清洗解决不了客户端渲染页面（返回的是静态骨架），这类页面继续由 `browser` 兜底
- **完美 readability**：不追求 Mozilla Readability 级别的提取精度，启发式够用即可，误判靠 fallback 兜底
- **清洗非 HTML**：`application/json`、`text/plain`、XML 等走原路径，不做转换

## 决策记录

| 决策点 | 结论 |
|--------|------|
| 清洗目标形态 | 先正文提取，再转 Markdown（提取 + 转换结合） |
| 能力放哪一层 | 内置进 web_fetch，默认清洗；`clean=false` 拿原始 |
| 实现方式 | 自研，零新依赖（x/net/html 已在依赖树） |
| 提取失败兜底 | 回退到全页 HTML→Markdown 转换，不丢结构 |

## 管线设计

```
fetchDirect (HTTP GET, 30s)
  → content-type 判断
      ├─ 非 text/html → 原路径（binary notice / 原样返回）
      └─ text/html + clean=true（默认）
          → HTML→Markdown 清洗
              ├─ 正文提取成功 → 提取出的正文转 Markdown
              └─ 提取失败 → 全页转 Markdown
          → 现有大小限制（≤64KB inline / >64KB spill 到临时文件）
```

- `clean=false` → 跳过清洗，返回原始文本，完全等同现状
- 清洗在大小限制**之前**执行：清洗后的 Markdown 通常远小于原始 HTML，spill 阈值逻辑不变，反而更少触发
- 二进制 / 非文本响应：行为不变（`binaryContentNotice`）

## 正文提取算法（简化 readability）

基于 DOM 文本密度启发式，核心思路是 readability 的简化版：

1. 用 `x/net/html` 解析 HTML 成 DOM 树（`html.Parse`），解析失败 → 直接返回原始文本
2. 删除噪声子树：`script`、`style`、`noscript`、`svg`、`iframe`、`nav`、`footer`、`header`、`aside`、`form`、隐藏节点（`hidden` 属性 / `display:none` 样式）
3. 遍历候选块（`p`、`article`、`div`、`section`、`td` 等），对每个节点打分：
   - 加分：文本长度、段落数
   - 减分：链接文本占比（链接密度高 → 导航/聚合页特征）、class/id 命中噪声词（`nav`、`menu`、`sidebar`、`comment`、`footer`、`ad` 等）
4. 选出得分最高的候选块作为正文容器
5. 若最高分低于阈值（提取出的文本 < 200 字符），判定**提取失败**，回退全页转换

阈值（200 字符）为起步值，实现后用真实页面样本校准。

## HTML→Markdown 转换器

自写 DOM 遍历转换器（`x/net/html` 节点 → markdown 文本），块级语义映射：

| HTML | Markdown |
|------|----------|
| `h1`–`h6` | `#`–`######` |
| `p` | 段落（空行分隔） |
| `ul` / `ol` / `li` | `-` / `1.` 列表 |
| `pre` / `code` | 围栏代码块 / 行内反引号 |
| `blockquote` | `>` 引用 |
| `table` | GFM 表格 |
| `a` | `[text](href)`（href 为空或 `javascript:` 时只留文本） |
| `img` | `![alt](src)`（保留信息，模型可见图 URL） |
| `strong` / `em` / `br` / `hr` | `**` / `*` / 换行 / `---` |

跳过节点：`script`、`style`、`noscript`、`svg`、`iframe`、`form`（全页转换时同样跳过，去噪但不丢结构）。

## 接口变化

`web_fetch` 新增参数：

```json
"clean": {
  "type": "boolean",
  "description": "Convert HTML responses to clean Markdown (extract the main content when possible). Default true. Set false to get the raw fetched text."
}
```

工具 Description 同步更新，说明默认输出清洗后的 Markdown、`clean=false` 拿原始、JS 渲染页面用 browser。

## 错误处理

- HTML 解析失败 → 返回原始文本（不报错，降级）
- 正文提取失败 → 全页转换（不报错，降级）
- 清洗后为空文本 → 返回原始文本
- 上述降级都不影响 `clean=false` 的显式原始请求

## 测试策略

`internal/tools/web_fetch_clean_test.go`，table-driven，httptest 服务器 + 固定 HTML 样例：

1. **文章页**（含导航/侧栏/页脚噪声）→ 断言输出含标题与正文，不含噪声词
2. **导航/聚合页**（链接密集）→ 断言回退到全页 Markdown（链接保留）
3. **短页面**（正文 < 200 字符）→ 断言回退全页转换
4. **JSON / 非 HTML** → 断言原路径不变
5. **`clean=false`** → 断言返回原始 HTML 文本
6. **表格页** → 断言输出 GFM 表格
7. **代码块页** → 断言围栏代码保留
8. **HTML 解析失败样例**（截断标签）→ 断言降级返回原始文本
9. 现有测试全量回归（`TestWebFetch_*` 补 `clean` 默认值断言）

## 依赖变更

`golang.org/x/net`：indirect → direct（`go mod tidy` 自动处理），无其他新增。

## 风险与缓解

| 风险 | 缓解 |
|------|------|
| 启发式误判（提取错块/漏提取） | fallback 全页转换兜底 + `clean=false` 逃生口 |
| 性能：5MB 上限 HTML 的 DOM 解析 | x/net/html 单次遍历 O(n)，5MB 内耗时可接受；清洗前先截断到 WebFetchMaxBytes 的既有逻辑不变 |
| 转换器覆盖面不足（少见标签） | 未映射标签降级为保留文本内容，不丢信息；后续按需扩展 |

## 落地计划

1. **docs-only PR**：本设计文档（`dev-docs/web-fetch-cleaner.md`）
2. **实现 PR**：`internal/tools/web_fetch_clean.go`（提取 + 转换器，~600-800 行）+ 测试 + `web_fetch.go` 管线接入 + 参数/描述更新
