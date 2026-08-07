# web_fetch HTML 清洗（正文提取 + Markdown 转换）设计

> 状态：已拍板（2026-08-07）· 关联：删 Jina 代理 PR（`internal/tools/web_fetch.go`）

## 背景与动机

web_fetch 删掉 Jina 代理后，直接 fetch 返回的是**原始 HTML**：脚本、样式、导航、广告、侧栏全部糊进模型上下文，既费 token 又淹没有效信息。Jina 原有的核心价值——"把页面变成干净可读的文本"——需要在本地方向补回来。

本设计用**零新增第三方依赖**的方式，在 web_fetch 内部做 HTML 清洗：先正文提取，再转 Markdown。

## 目标

- web_fetch 抓到 `text/html` 页面时，默认输出**正文提取 + Markdown 转换**后的干净文本
- 提取失败（JSON 接口、目录页、表格页、短页面）时回退到**全页 Markdown 转换**，至少去噪、保留结构
- 页面标题始终保留在输出开头，模型知道自己读的是什么
- 非 UTF-8 页面（GBK / Big5 / Shift-JIS）正确解码，不出乱码
- 输出里的链接与图片 URL 是绝对 URL，模型可以直接拿去二次 `web_fetch`
- `clean=false` 时返回未经清洗的抓取文本，作为逃生口
- 零新增依赖：`golang.org/x/net`（解析 + 编码嗅探）和它带的 `golang.org/x/text`（转码后端）都已是 indirect 依赖，转 direct 即可

## 非目标

- **JS 渲染**：清洗解决不了客户端渲染页面（返回的是静态骨架），这类页面继续由 `browser` 兜底
- **完美 readability**：不追求 Mozilla Readability 级别的提取精度，启发式够用即可，误判靠 fallback 兜底
- **清洗非 HTML**：`application/json`、`text/plain`、XML 等走原路径，不做转换

## 决策记录

| 决策点 | 结论 |
|--------|------|
| 清洗目标形态 | 先正文提取，再转 Markdown（提取 + 转换结合） |
| 能力放哪一层 | 内置进 web_fetch，默认清洗；`clean=false` 拿原始 |
| 实现方式 | 自研，零新增模块（x/net + x/text 已在依赖树） |
| 提取失败兜底 | 回退到全页 HTML→Markdown 转换，不丢结构 |
| 字符编码归一化 | 独立于 `clean`，始终执行——它是传输层解码，不是清洗 |
| 链接 URL 形态 | 一律绝对化，基准取最终响应 URL（`<base href>` 优先） |
| 表格 | 首版只转规整表格；`colspan`/`rowspan`/嵌套表格降级为按行文本 |

## 管线设计

```
fetchDirect (HTTP GET, 30s)
  → content-type 判断
      ├─ 非文本 → 原路径（binaryContentNotice）
      └─ 文本
          → 字符编码解码为 UTF-8（始终执行，与 clean 无关）
          → 非 text/html → 原样返回
          → text/html + clean=true（默认）
              → HTML→Markdown 清洗
                  ├─ 正文提取成功 → 提取出的正文转 Markdown
                  └─ 提取失败 → 全页转 Markdown
          → 现有大小限制（≤64KB inline / >64KB spill 到临时文件）
```

- `clean=false` → 跳过清洗，返回解码后的原始文本
- 清洗在大小限制**之前**执行：清洗后的 Markdown 通常远小于原始 HTML，spill 阈值逻辑不变，反而更少触发
- 二进制 / 非文本响应：行为不变（`binaryContentNotice`）
- 清洗还顺带修好一个既有缺口：`markdownOutline` 只认 ATX markdown 标题，喂它原始 HTML 时 outline 恒为空，spill 预览等于只给 40 行 `<head>` 骨架。清洗后 spill 预览的 outline 重新可用

## 字符编码

`html.Parse` 假定输入是 UTF-8。GBK / Big5 / Shift-JIS 页面直接喂进去解析出的是乱码，而且**降级链兜不住**：不报错、不触发 fallback，只是安静地输出垃圾。Jina 以前替我们做了这层归一化。

用 `golang.org/x/net/html/charset`。它在 `readBody` 里、大小截断之后接手已读进内存的字节：

```go
r, err := charset.NewReader(bytes.NewReader(body), contentType)
```

它按 `Content-Type` 的 charset 参数 → HTML `<meta charset>` / `<meta http-equiv>` → BOM → 内容嗅探的顺序判定，返回转码后的 UTF-8 reader。判定不出编码时它返回原 reader 而非报错；真出错（未知 label）就按原字节继续，行为退回今天的现状。

这一步在 `clean=false` 时同样执行：拿到乱码字节对调用方没有任何价值，而"原始"指的是未经正文提取和 Markdown 转换，不是未经字符解码。

## 正文提取算法（简化 readability）

基于 DOM 文本密度启发式，核心思路是 readability 的简化版：

1. 用 `x/net/html` 解析 HTML 成 DOM 树（`html.Parse`），解析失败 → 直接返回原始文本
2. 删除噪声子树：`script`、`style`、`noscript`、`svg`、`iframe`、`nav`、`footer`、`header`、`aside`、`form`、隐藏节点（`hidden` 属性 / `display:none` 样式）
3. 遍历候选块（`p`、`article`、`div`、`section`、`td` 等），对每个节点打分：
   - 加分：文本长度、段落数
   - 减分：链接文本占比（链接密度高 → 导航/聚合页特征）、class/id 命中噪声词（`nav`、`menu`、`sidebar`、`comment`、`footer`、`ad` 等）
4. 选出得分最高的候选块
5. **向上提升**：从该候选逐级向上，只要父节点的原始正文得分 ≥ 当前节点的 50%，就上提到父节点（见下）
6. 若提取出的文本 < 200 字符，判定**提取失败**，回退全页转换

### 为什么需要向上提升

只取"得分最高的候选"会让引用密集的长文输给它自己的某一章：链接密度惩罚把整篇文章的分压下去，而文章内部某个代码密集、链接稀少的章节几乎不受惩罚，于是赢了——页面被清洗成"第七章"，前六章无声消失。

上提用的是**原始正文得分**（未乘链接密度和 class 权重），因为这个机制存在的意义正是"父节点链接多不构成淘汰理由"。

阈值 50% 是拿真实页面扫出来的拐点，不是拍的：

| 页面 | 原始 | 无上提 | 上提 50% |
|------|------|--------|----------|
| Wikipedia《Go》 | 720 KB | 13 KB（只有 Types 一节） | 101 KB（全文 + infobox） |
| go.dev/blog/go1.22 | 36 KB | 3.3 KB | 3.4 KB（多出作者、日期） |
| MDN Referer | 174 KB | 2.3 KB | 2.3 KB |

再往下放宽到 0.35 / 0.25，六个样本的结果**完全不变**——50% 处父节点得分已经断崖，说明这是结构自然边界而非调参凑出来的数。文章页几乎不受影响（只多出作者/日期这类真元数据），Wikipedia 从残缺变完整。

### 提取阈值

200 字符经真实样本验证保留：达到该量级的提取结果都是真正文，未观察到误判。知乎反爬空壳页（650 B）正确落到兜底路径。

### 两条路径的去噪范围不同（有意）

正文提取路径删掉 `nav` / `footer` / `header` / `aside` / `form`；**全页兜底转换只跳过 `script` / `style` / `noscript` / `svg` / `iframe` / `form`，保留导航与链接**。

这不是笔误。走到全页兜底，通常正因为这是一个链接密集的目录页 / 聚合页 / 索引页——那些链接就是页面的实质内容，删掉等于把页面清空。提取成功的文章页才有条件认定导航是噪声。

### 标题保留

`header` 被当作噪声删除，正文提取又只挑得分最高的那个容器，结果是 `<title>` 和页面主标题大概率都不在输出里。Jina 的输出以 `Title: …` 开头是有实际价值的——模型需要知道自己读的是什么。

约定：清洗结果**始终**以 `# <title>` 开头（title 取 `<head><title>`，为空则取全文首个 `h1`）。若正文转换结果的第一个标题与它文本相同或互为包含，则不重复插入。

## 链接与图片 URL 绝对化

页面里的 `href` / `src` 大量是 `/foo/bar`、`../img.png` 这类相对路径。原样搬进 Markdown，模型拿到后无法再 `web_fetch`，等于给了一堆死链——这也是相对 Jina 输出的一处可感知回退。

一律用 `base.ResolveReference` 转成绝对 URL：

- **基准 URL** 取**最终响应 URL**（`resp.Request.URL`，Go 在跟随重定向后把它更新为最后一跳），不是调用方传入的原始 URL——短链跳转后按原始 URL 解析相对路径会全错
- 页面内如有 `<base href>`，它优先于响应 URL（HTML 规范如此）
- 解析失败、或 scheme 为 `javascript:` / `data:` 的，按原有规则只保留文本

实现上要动一处签名：`readBody` 现在只拿到调用方传入的 `sourceURL`，需要由 `fetchDirect` 把 `resp.Request.URL` 一并透传。spill 文件名与 UI 卡片的 `url` 字段仍用调用方传入的原始 URL，不跟着改——那两处要的是"用户请求了什么"，不是"最后落到哪"。

## HTML→Markdown 转换器

自写 DOM 遍历转换器（`x/net/html` 节点 → markdown 文本），块级语义映射：

| HTML | Markdown |
|------|----------|
| `h1`–`h6` | `#`–`######` |
| `p` | 段落（空行分隔） |
| `ul` / `ol` / `li` | `-` / `1.` 列表 |
| `pre` / `code` | 围栏代码块 / 行内反引号 |
| `blockquote` | `>` 引用 |
| `table` | 规整表格 → GFM 表格；不规整 → 按行文本（见下） |
| `a` | `[text](绝对 href)`（href 为空或 `javascript:` / `data:` 时只留文本） |
| `img` | `![alt](绝对 src)`（保留信息，模型可见图 URL） |
| `strong` / `em` / `br` / `hr` | `**` / `*` / 换行 / `---` |

跳过节点：`script`、`style`、`noscript`、`svg`、`iframe`、`form`。

**表格的范围控制。** 表格是转换器里最容易写出边界 bug 的部分——`colspan` / `rowspan` / 嵌套表格 / 无 `thead` 的表格 / 用 `table` 做布局的老页面，每一种都要单独处理。首版只转**规整表格**（每行单元格数一致、无 `colspan`/`rowspan`、无嵌套）为 GFM；命中任一不规整特征就降级为**按行文本**（单元格以 ` | ` 连接，不带表头分隔行）。降级不丢内容，只丢对齐。等真实样本证明需要，再补完整实现。

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
6. **规整表格页** → 断言输出 GFM 表格；**带 `colspan` 的表格** → 断言降级按行文本且单元格内容不丢
7. **代码块页** → 断言围栏代码保留
8. **HTML 解析失败样例**（截断标签）→ 断言降级返回原始文本
9. **GBK 页面**（`Content-Type: text/html; charset=gbk` 与「仅 `<meta charset>` 声明」两种）→ 断言中文正确解码；`clean=false` 时同样解码
10. **相对链接页**（`/foo`、`../bar`、含 `<base href>`、经跨主机重定向抵达）→ 断言输出的是绝对 URL，且重定向后基准取最终 URL
11. **清洗后仍 >64KB 的长文** → 断言走 spill 路径，且预览里的 outline 非空（这正是删 Jina 后失效、清洗后恢复的能力）
12. **`clean` 传非 bool 值**（字符串 `"false"`、数字 `0`）→ 断言按默认值 `true` 处理，不报错
13. 现有测试全量回归（`TestWebFetch_*` 补 `clean` 默认值断言）

## 依赖变更

**无新增模块**，两个已有模块从 indirect 转 direct：

| 模块 | 用途 |
|------|------|
| `golang.org/x/net` v0.55.0 | 生产代码：`html`（DOM 解析）、`html/charset`（编码嗅探） |
| `golang.org/x/text` v0.40.0 | 测试代码：`encoding/simplifiedchinese` 构造 GBK 样本 |

`x/text` 本来就是 `html/charset` 的转码后端，只是因为测试**直接** import 它来编码 GBK 断言数据，`go mod tidy` 才把它一并提为 direct。两者早已锁在 go.sum 里，不引入任何新的供应链面。

顺带收尾：`internal/tools/web_search.go` 的 `stripHTML` 注释写着 "pulling one in would balloon dependencies"——`x/net/html` 转 direct 后这句已过期；那里的实体解码只认 6 个实体（`&amp;` / `&lt;` / `&gt;` / `&quot;` / `&#39;` / `&nbsp;`），可以统一到 `html.UnescapeString`。搜索摘要的正则去标签逻辑本身不动。

## 风险与缓解

| 风险 | 缓解 |
|------|------|
| 启发式误判（提取错块/漏提取） | fallback 全页转换兜底 + `clean=false` 逃生口 |
| 性能：5MB 上限 HTML 的 DOM 解析 | x/net/html 单次遍历 O(n)，5MB 内耗时可接受；清洗前先截断到 WebFetchMaxBytes 的既有逻辑不变 |
| 转换器覆盖面不足（少见标签） | 未映射标签降级为保留文本内容，不丢信息；后续按需扩展 |
| 编码嗅探误判（无声明的非 UTF-8 页） | charset 嗅探失败按原字节继续，行为退回今天的现状，不是新增退化 |
| 自研代码量与维护成本 | 812 行（含注释），其中约一半是 HTML→Markdown 转换器。表格只做规整情形，长尾按真实样本驱动扩展 |

## 已知限制

- **`<table>` 布局的老页面**（Hacker News 首页是典型）：整页嵌套在不规整表格里，降级成按行文本后所有条目挤成极少数几行。内容、链接、分数都在，模型能用，但可读性差。真要治需要"表格是布局还是数据"的判别，留到有实际需求再说。
- **客户端渲染页面**：返回静态骨架，清洗只是把骨架变干净。归 `browser` 兜底，与本设计无关。

## 落地计划

1. **docs-only PR**：本设计文档（`dev-docs/web-fetch-cleaner.md`）
2. **实现 PR**：`internal/tools/web_fetch_clean.go`（提取 + 转换器，812 行）+ `web_fetch_clean_test.go`（435 行）+ `web_fetch.go` 管线接入（charset 解码 + 清洗）+ `clean` 参数与描述 + `web-access` / `deep-research` skill 路由说明回改

**发布顺序**：删 Jina 那个 PR 单独落地会让 web_fetch 明显退化——小页面把几十 KB 原始 HTML 灌进上下文，大页面 spill 后 outline 恒空、预览只剩 `<head>` 骨架。两者要么一起进 release，要么在本设计实现前不切版本。
