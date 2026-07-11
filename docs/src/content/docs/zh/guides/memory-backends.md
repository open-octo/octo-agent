---
title: 接入记忆后端
description: 可选的语义召回能力，来自 hindsight、mem0 或 agentmemory——和 MEMORY.md 是两回事。
---

octo 可以选择性地接入一个自托管的外部记忆服务，让它给你的对话建索引，供 agent 之后搜索。这跟
[`MEMORY.md`](/docs/zh/guides/memory/) 是两件事：`MEMORY.md` 是 agent 整理好的常驻指引
（偏好、规则、项目决定），每次会话都会被冻结进系统提示里。而记忆后端是对原始对话文本做的
自由格式语义召回——提取和索引都是后端自己在做，octo 不会碰或者复制 `MEMORY.md` 那一层来
支撑它。

支持三种后端，最多选一个：

- [hindsight](https://github.com/vectorize-io/hindsight)——自托管，默认不需要鉴权；如果不想自己
  跑容器，也有一个托管的 [Hindsight Cloud](https://docs.hindsight.vectorize.io/) 选项（见下文）。
- [mem0](https://github.com/mem0ai/mem0)——自托管（用的是 mem0ai/mem0 仓库里的 `server/`），
  默认开启鉴权；也有一个托管的 [mem0 Platform](https://docs.mem0.ai/platform/quickstart)（云端）
  选项（见下文）。
- [agentmemory](https://github.com/rohitg00/agentmemory)——自托管的 Node/TypeScript 服务，默认
  不需要鉴权，也是最省事的一个：自带本地 embedding，上手不需要任何外部 LLM 或 API key。

hindsight 和 mem0 需要一个 LLM（用于事实提取）和一个 embedding 模型（用于搜索）——可以用你
自己的 OpenAI 兼容端点（DashScope/百炼、DeepSeek 等），hindsight 还额外支持完全本地的搭法。
agentmemory 则开箱即用、全程本地跑（本地 embedding，LLM 可选）。下面的步骤是每一种都经过实测、
可以直接照抄的快速上手——是对它们自己文档的补充，不是替代。

## 本地跑一个后端

三选一。都假设 Docker 已经装好并且在运行。

### hindsight

三者里最好上手：不用配数据库，默认也不需要鉴权。

```bash
docker run -d --name hindsight \
  -p 8888:8888 -p 9999:9999 \
  -v hindsight-data:/home/hindsight/.pg0 \
  -v hindsight-hf-cache:/home/hindsight/.cache \
  -e HINDSIGHT_API_LLM_PROVIDER=openai \
  -e HINDSIGHT_API_LLM_MODEL=<your-model> \
  -e HINDSIGHT_API_LLM_BASE_URL=<your-openai-compatible-base-url> \
  -e HINDSIGHT_API_LLM_API_KEY=<your-api-key> \
  -e HINDSIGHT_API_EMBEDDINGS_LOCAL_MODEL=BAAI/bge-small-en-v1.5 \
  ghcr.io/vectorize-io/hindsight:latest
```

`HINDSIGHT_API_LLM_*` 可以指向任意 OpenAI 兼容端点（DashScope 的
`https://dashscope.aliyuncs.com/compatible-mode/v1`、DeepSeek、真正的 OpenAI 等等）——
hindsight 只用它来整理归纳保留下来的内容，不用来做 embedding（embedding 是通过
`HINDSIGHT_API_EMBEDDINGS_LOCAL_MODEL` 在本地跑的，不需要 key）。第一次启动要
**大约 1-2 分钟**，因为它要下载 embedding/reranker 模型并且拉起一个内嵌的 Postgres——
API 一时半会没反应不用慌。

确认它起来了：

```bash
curl http://localhost:8888/v1/default/banks
# {"banks":[]}  —— 空的没关系，第一次写入的时候会自动建一个 bank
```

除非你在容器上显式设置了 `HINDSIGHT_API_TENANT_API_KEY`，否则不需要任何鉴权。

#### Hindsight Cloud（不用跑 Docker）

Vectorize 还提供一个托管版本——[Hindsight Cloud](https://docs.hindsight.vectorize.io/)——给不想
自己跑容器的人用。它用的是同一套 REST API，端点是 `https://api.hindsight.vectorize.io`，路径
结构（`/v1/default/banks/...`）也跟自托管容器一模一样，所以把 octo 指过去只是改配置，不用改代码：

```yaml
memory_backend:
  type: hindsight
  base_url: https://api.hindsight.vectorize.io
  api_key: "<你的 Hindsight Cloud API key>"
  namespace: octo-agent
```

跟自托管默认情况不同，Hindsight Cloud 是强制要求 API key 的——去它的控制台生成一个填在这里。
octo 会把它当作 `Authorization: Bearer <api_key>` 发出去，正好是云端 API 要求的格式。

### mem0

需要 Postgres（带 pgvector）——官方的 `server/` 那套栈用 Docker Compose 把它一起打包了。

```bash
git clone https://github.com/mem0ai/mem0
cd mem0/server
cp .env.example .env
```

编辑 `.env`：

```bash
OPENAI_API_KEY=<your-api-key>
POSTGRES_PASSWORD=<pick-anything>
AUTH_DISABLED=true   # 仅限本地开发用——真要上鉴权见下面的"鉴权"一节
MEM0_DEFAULT_LLM_MODEL=<your-model>
MEM0_DEFAULT_EMBEDDER_MODEL=<your-embedding-model>
```

然后：

```bash
make bootstrap
```

**如果你用的是非 OpenAI、但 OpenAI 兼容的 provider**（DashScope、DeepSeek……），光在 `.env`
里写模型名是不够的——mem0 的 OpenAI 客户端默认指向 `api.openai.com`。要通过 `/configure`
接口把它指到你自己 provider 的 base URL，**要在存任何东西之前做**：

```bash
curl -X POST http://localhost:8888/configure \
  -H "Content-Type: application/json" \
  -d '{
    "llm": {"provider": "openai", "config": {"model": "<your-model>", "openai_base_url": "<your-base-url>"}},
    "embedder": {"provider": "openai", "config": {"model": "<your-embedding-model>", "embedding_dims": <dims>, "openai_base_url": "<your-base-url>"}}
  }'
```

（用 `AUTH_DISABLED=true` 的话不需要带鉴权 header。）`embedding_dims` 这个字段很关键——
如果漏填这个然后碰到维度报错，看下面的[排查](#排查)。

#### 鉴权

`AUTH_DISABLED=true` 拿来本地试用没问题，但等于跳过了真正的访问控制。要长期用的话，
去掉这个变量，照常跑 `make bootstrap`，它首次启动时会打印一个管理员邮箱/密码/API key——
把这个生成出来的 API key 填进 octo 配置的 `api_key` 里，别留空。

#### mem0 Cloud（不用 Postgres/Docker）

mem0 也有一个托管版本——[mem0 Platform](https://docs.mem0.ai/platform/quickstart)——给不想
自己搭 `server/` 那套栈的人用。跟 Hindsight Cloud 不一样，这**不是**配个 URL 就能切换的——
Platform API 用的端点路径和鉴权 header 都跟自托管 server 不一样，所以 octo 需要显式设置
`mode: cloud` 才能对上：

```yaml
memory_backend:
  type: mem0
  mode: cloud
  api_key: "<你的 mem0 Platform API key>"
  namespace: octo-agent
```

`base_url` 可以不填——`mode: cloud` 且没设 `base_url` 时会自动用
`https://api.mem0.ai`。`api_key` 是必填的（Platform 没有免鉴权模式）；octo 会把它当作
`Authorization: Token <api_key>` 发出去，正好是 Platform API 要求的格式。

### agentmemory

三者里最轻的一个：不用 Docker，不用数据库，也不用 API key。它内置了 SQLite 存储，embedding 模型
也是本地跑的（通过 `@xenova/transformers` 用 `all-MiniLM-L6-v2`），所以开箱即用。

```bash
npm install -g @agentmemory/agentmemory
agentmemory
```

（或者不装直接跑：`npx @agentmemory/agentmemory`。）REST API 默认监听 `127.0.0.1:3111`，另有一个
实时查看器跑在 `3113`。

:::caution[必须从一个固定、可写的目录启动]
agentmemory 的存储引擎把 state 存到 **相对当前工作目录（CWD）的 `./data/`**。如果每次从不同目录
启动它——或者作为服务运行、工作目录默认成了 `/`（launchd、systemd）——它就写不进去：`state::set`
会一直卡到 180 秒超时，**数据在重启后全部丢失**（每次启动都是空索引，上次运行以来存的东西悄无声息地
没了）。务必从一个稳定、可写的目录启动它（比如 `~/.agentmemory`），在进程管理器里显式指定这个目录
（launchd plist 的 `WorkingDirectory`、systemd unit 的 `WorkingDirectory=`）。验证是否生效：存一条
东西后重启 server，同样的 query 应该还能查到，且 `~/.agentmemory/data/state_store.db` 应当存在。
:::

确认它起来了：

```bash
curl http://localhost:3111/agentmemory/health
# {"status":"healthy",...}
```

默认不需要鉴权。如果要加固，启动时设置 `AGENTMEMORY_SECRET`，并把同样的值填进 octo 的 `api_key`
——octo 会以 `Authorization: Bearer <api_key>` 发过去。

外部 LLM 是可选的（用来自动做观测摘要）——想用的话在 `~/.agentmemory/.env` 里设置
`OPENAI_API_KEY`、`ANTHROPIC_API_KEY` 或 `GEMINI_API_KEY`。octo 只用两个端点：每轮对话通过
`/agentmemory/remember` 存，召回走 `/agentmemory/search`（narrative 格式，会返回完整的原文）
——两者都不要求配置 LLM。

## 工作原理

- **存储是自动的。** 每一轮结束后，octo 都会在后台把这一轮的内容发给后端——没有
  `memory_store` 工具，也不需要 agent 自己决定要不要存。这跟这些后端本来的设计用法是一致的：
  你喂给它们原始文本，提取和去重它们自己搞定。整个过程是发出去就不管了——存储失败不会有任何
  提示，也不会拖慢当前这一轮。
- **召回默认是一个工具。** 当 agent 怀疑某个之前的会话或对话里提到过相关内容时，会调用
  `memory_recall`。这个调用**会**阻塞在网络往返上，出错也会显式暴露出来，因为这是一个明确、
  可见的动作，不是背后悄悄发生的副作用。要不要调用是模型自己的判断——碰到一个不太像"接着
  之前的话题"的孤立事实类问题时可能会漏查（如果不想依赖这个判断，见下面的 `auto_recall`）。

## 配置方法

在 `~/.octo/config.yml` 里加一个 `memory_backend` 区块：

```yaml
memory_backend:
  type: hindsight        # hindsight | mem0 | agentmemory
  mode: ""               # 只对 mem0 有意义："cloud" 或 ""（自托管，默认）
  base_url: http://localhost:8888
  api_key: ""            # 可选——具体看下面每个后端的说明
  namespace: my-project  # 限定存储/召回的记忆范围；不填默认是 "default"
  auto_recall: false     # 可选——见下面「自动召回」
```

- **`type`** 选择用哪个后端。不填（或者整段都不写）就是彻底关掉这个功能——不会向模型
  暴露任何工具，也不会往外发任何东西。
- **`mode`** 只对 `type: mem0` 有意义：设成 `cloud` 就会走托管的 mem0 Platform，而不是自托管
  server（见上文「mem0 Cloud」）——两者端点路径和鉴权 header 都不一样，不会根据 `base_url`
  自动判断。hindsight 和 agentmemory 不看这个字段。
- **`base_url`** 是后端的 REST 端点——也就是你把它的 server 跑在哪（照上面的方式搭的话，
  hindsight/mem0 是 `http://localhost:8888`，agentmemory 是 `http://localhost:3111`）。`mem0` 配合
  `mode: cloud` 时可以不填，会自动用 `https://api.mem0.ai`。
- **`api_key`** 是可选的，具体看后端：
  - 自托管 hindsight 默认不需要鉴权；只有在 server 上开了 `HINDSIGHT_API_TENANT_API_KEY` 时才
    需要填一个 API key。Hindsight Cloud 是例外——它始终要求填控制台生成的 API key。
  - 自托管 mem0 默认要求鉴权——把 server 那个兼容 `X-API-Key` 的 key 填在这里，或者本地开发时
    直接用 `AUTH_DISABLED=true` 跑 server，把这里留空。mem0 Cloud（`mode: cloud`）始终要求填
    控制台生成的 API key。
  - agentmemory 默认不需要鉴权；留空即可，除非你启动 server 时设了 `AGENTMEMORY_SECRET`，
    那就把同样的值填在这里（会以 `Authorization: Bearer` 发过去）。
- **`namespace`** 限定存储/召回的范围——对应 hindsight 的 `bank_id`、mem0 的 `user_id`，
  或者 agentmemory 的 `project`。每个项目用一个稳定的值（或者干脆留默认，共用一个桶）。
- **`auto_recall`** ——见下文。默认 `false`。

改完这里之后要重启 `octo`（或者 `octo serve`）——这个配置和其他所有配置文件设置一样，
只在会话开始时读一次。

验证接线是否正确：启动 `octo`，随便聊几句，然后问一个需要回想起刚才内容的问题
（换一个新会话，或者等 `octo` 重启之后再问）——应该能看到它调用了 `memory_recall`，
并且把之前说过的事实找回来了。

### 自动召回

把 `auto_recall` 设成 `true`，会在**每一轮**都自动用用户这句话调用一次 `Recall`，把结果直接
塞进这一轮的上下文——不用等模型自己判断要不要调 `memory_recall`。工具本身还是照常可用，
留着给模型做更深或换个角度的搜索；注入的内容里会带一句提示，告诉模型不用为了同一个问题
再调一次。

代价是每一轮都多一次有界的延迟（这次调用是同步的，最多等 3 秒，出错或超时就静默跳过），
换来的是不用再依赖模型"要不要查"的判断——如果你发现它对着一个明明在后端里存着的问题
答"不知道"，而不是先试一下 `memory_recall`，这个开关能解决问题。不想让每一轮都多一次
网络往返、只想靠工具自己判断的话，就保持关闭。

## 排查

- **mem0：`psycopg.errors.DataException: expected 1536 dimensions, not N`**——mem0 的
  Postgres 向量列的维度是在第一次存东西的时候就定死的（默认 1536，对应 OpenAI 默认的
  embedding 模型）。如果你的 embedder 输出的维度不一样，必须在**第一次**调用 `/memories`
  **之前**就通过 `/configure` 把 `embedding_dims` 设对。如果已经用错误的维度存过东西了，
  没法原地修——只能清空重来：`docker compose down -v && docker compose up -d`，然后立刻
  再调一次 `/configure`，在存任何东西之前完成。
- **mem0：`provider_auth_failed` / 来自 `api.openai.com` 的 401**——你的 LLM/embedder
  配置还是指向真正的 OpenAI。通过 `/configure` 设 base URL（光改 `.env` 不够）。
- **agentmemory：刚存完召回却是空的**——确认 server 确实起着
  （`curl http://localhost:3111/agentmemory/health`），并且 octo 的 `namespace` 在重启前后
  保持一致；它对应 agentmemory 的 `project`，search 的返回范围就是靠它限定的。
- **agentmemory：重启后数据全没了，或日志刷 `Invocation timeout after 180000ms: state::set` /
  `index persistence: failed`**——server 是从一个它写不进去的工作目录启动的（state 存储是相对 CWD
  的 `./data/`，默认 CWD 为 `/` 的服务写不进根目录）。从一个固定、可写的目录重启它，并在进程管理器里
  设 `WorkingDirectory`——见上面 agentmemory 安装小节的警示框。`~/.agentmemory/data/state_store.db`
  出现（且能扛过一次重启）就说明修好了。
- **agentmemory：受限网络下启动卡住 / 本地 embedding 模型下不下来**——它首次运行会从 HuggingFace 拉
  `all-MiniLM-L6-v2`。如果你部署的环境里 `huggingface.co` 被墙或被限速，启动前用 `HF_ENDPOINT`
  环境变量指向镜像（如 `HF_ENDPOINT=https://hf-mirror.com`）。
- **hindsight：`docker run` 之后马上连不上**——再等一两分钟，它还在下载/加载 embedding 和
  reranker 模型。`docker logs hindsight` 能看到进度。等它打印出启动横幅之后，后续重启就会
  快很多（模型缓存在 `hindsight-hf-cache` 这个 volume 里）。
- **用的是 Colima 而不是 Docker Desktop，绑定挂载的 volume 在容器里是空的**——Colima 只会
  把特定的宿主机路径共享进它的虚拟机（默认是你的主目录和 `/tmp/colima`）。把仓库克隆到你
  主目录下面，而不是 Colima 配置的挂载范围之外的路径（比如别放在随便一个 `/tmp/...` 或者
  `/private/tmp/...` 下面），不然这些项目用的 `.:/app` 这类绑定挂载会悄悄挂载成空目录。
- **octo 从来不调用 `memory_recall`，或者后端根本收不到任何东西**——确认
  `memory_backend.type` 确实设了（这个区块空着或者没写，功能就是关闭的，不会报错），
  并且 `base_url` 跟你实际暴露出来的端口对得上。
