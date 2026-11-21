# team-memory

给团队研发用的「长久记忆」服务：一个进程、一个端口，每个项目一个 MCP 端点，多个大模型共享同一份 Markdown 开发记忆。无账号、无令牌、无数据库，写入必须来自「已读规则」的会话。

配套一个 VS Code / Trae 扩展，在编辑器里浏览与编辑记忆文件。

- Gitee：<https://gitee.com/tianling/team-memory>
- GitHub：<https://github.com/try520/team-memory>

## 为什么值得

装好之后，AI 每次开工先读一份团队共享的项目记忆，收尾再把结论写回去。直接的好处：

- **不用每次重新交代背景**：项目是干什么的、有哪些约定、上次做到哪，都在 `RULE.md` 和 `TASKS.md` 里。
- **少翻代码**：索引写清「什么场景先读哪篇」，模型按场景读，读一遍，就知道要改哪里，不用频繁翻代码。
- **不重复踩坑**：坑、取舍、接口变更记在 `topics/`，下次直接命中，相同问题，只踩一次。
- **重复操作和规范沉淀成技能**：发版、建表、排查这类固定流程，以及「后端控制层/逻辑层怎么写、前端左右结构、单页结构什么时候用」这类约定，都可以写成 `skills/<name>/SKILL.md`；提审通过后对所有项目可见，团队里每个 AI 都照着执行，不用每次重新讲规则和步骤。
- **多人多模型共用一份**：纯 Markdown，Trae / Claude Code / Qoder / CodeBuddy 看到的是同一份，能直接改，也能用 git 备份。
- **同时干活不撞车**：开工把「涉及文件」登记到 `TASKS.md`，动手前看 `get_rules.tasks`，谁在改哪些文件一目了然；同路径被别人登记会收到 `TASK_CONFLICT` 提醒，覆盖内容必须带 hash，别人中途改过就写不进去。
- **越用越省**：一次 `get_rules` 约 3.7k–5.4k token，读一篇主题约 2.6k；相比新会话重新摸一遍（约 12k–40k），每个会话净省 **4k–28k token，典型约 12k**。

| | 没装 | 装了 |
|---|---|---|
| 新会话开场 | 重新交代背景、翻文件 | 一次 `get_rules` 拿到索引和任务 |
| 找关键代码 | 全库搜索 | 按主题里的「代码位置」直达 |
| 同类任务第二次 | 重新讲一遍操作步骤 | 已有技能，照着执行 |
| 上一个坑 | 可能再踩一次 | 已写在「坑」里 |
| 两个人同时改 | 互相不知道，容易覆盖 | 占用表 + 冲突提醒，覆盖要带 hash |
| 收尾 | 结论留在聊天里，下次就没了 | 写进 journal，下次能查 |

不划算的情况：任务只有一两轮问答；索引或主题本身臃肿（`get_rules` 超过 8k token）。上面的数字来自真实项目 kms 的一次实测（8 篇主题、76 篇 journal、86 个文件），是估算区间，不是 benchmark。

## 如何使用（四步）

按顺序做完四步，一个项目就接好了。以 Trae 为例，其他客户端在每步里给出对应文件。

| 步骤 | 做什么 | 完成标志 |
|---|---|---|
| 1 | 装服务端 | `curl /healthz` 返回 `version` |
| 2 | 配置 MCP | 客户端工具列表出现 6 个 `memory_*` / `skill_*` 工具 |
| 3 | 配置客户端 rule | 新会话第一件事就调 `memory_get_rules` |
| 4 | 装客户端插件 | 侧边栏出现「团队记忆」并能切换项目 |

---

### 第一步：装服务端

前置：Docker 与 Docker Compose v2，一个留给数据的宿主目录。

新建 `docker-compose.yml`：

```yaml
services:
  memory:
    image: registry.cn-hangzhou.aliyuncs.com/mybase-tools/develop-memory:0.1.7
    container_name: team-memory
    restart: unless-stopped
    ports:
      - "8080:8080"
    environment:
      DATA_DIR: /data
      PORT: "8080"
      TZ: Asia/Shanghai
    volumes:
      - ./data:/data
```

启动并确认：

```bash
docker compose up -d
docker ps                       # STATUS 变成 healthy 即正常
curl -s http://127.0.0.1:8080/healthz
# {"ok":true,"pid":1,"projects":0,"sessions":0,"uptime_s":5,"version":"0.1.7"}
```

几点说明：

- 镜像已公开、可匿名拉取；也可以在本仓库 `src/` 下 `docker compose up -d --build` 本地构建。
- 宿主机端口改 `ports` 左边；容器内固定 `8080`。卷右侧和 `DATA_DIR` 必须都是 `/data`。
- **项目不用提前创建**：客户端第一次请求 `/mcp/<project>` 时，服务端自动建目录、`RULE.md`、`TASKS.md`。
- 一个数据目录只允许一个实例：启动会写 `.pid`，检测到存活进程直接退出。

环境变量：

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `DATA_DIR` | `/data` | 数据根目录，必须可写 |
| `PORT` | `8080` | 监听端口 |
| `TZ` | 系统时区 | 影响 journal 的月份分组 |

---

<a id="mcp-接入"></a>

### 第二步：配置 MCP

每个项目一个 URL：`http://<your-server>:8080/mcp/<project>`，最后一段就是项目 slug（小写字母、数字、中划线）。

建议同时加请求头 `X-Memory-User`，值写你自己的名字（例如 `张三`）。服务端用它作为 TASKS.md 的「推进者」和 journal 文件名，`get_rules` 会回显 `user_name` 与来源。它是自报信息，不是身份认证，不能用于追责；值非法（超 32 字、含竖线/换行）会被忽略并回落到 `s_xxxxxxxx` 会话标签，不阻断连接。

#### Trae：项目根 `.trae/mcp.json`

文件位置：项目根目录 `.trae/mcp.json`（没有就新建）。顶层键是 `mcpServers`。

```json
{
  "mcpServers": {
    "team-memory-<project>": {
      "url": "http://<your-server>:8080/mcp/<project>",
      "headers": {
        "X-Memory-User": "张三"
      }
    }
  }
}
```

字段限制：远程条目**只写 `url`、`headers`，不要写 `type`**。依据本机 Trae CN 安装目录 `resources/app/extensions/configuration-editing/package.json` 的映射 `"**/.trae/mcp.json" → "vscode://schemas/mcp-settings/user"`；该 schema 的 HTTP 分支 `required: ["url"]`，字段只有 `url`、`headers`、`oauth`、`fromGalleryId`、`disabled`，且 `additionalProperties: false`。

注意：`.trae/mcp.json` 和 VS Code 的 `.vscode/mcp.json` 看着像，但不是同一套格式。VS Code 那份对应 schema `vscode://schemas/mcp`，顶层是 `servers` 并写 `type: "http"`；Trae 这份顶层是 `mcpServers`，并且不认 `type`，直接照抄会校验失败。

确认：Trae 的 MCP 设置面板里该服务器显示已连接，工具列表出现 `memory_get_rules` 等 6 个工具。如果看不到工程级配置，检查 Trae 设置里工作区 MCP 开关（本机安装包里对应 `trae.mcp.enableWorkspaceMcp`）是否已开启。

#### Claude Code：项目根 `.mcp.json`

```json
{
  "mcpServers": {
    "team-memory-<project>": {
      "type": "http",
      "url": "http://<your-server>:8080/mcp/<project>",
      "headers": {
        "X-Memory-User": "张三"
      }
    }
  }
}
```

等价的命令行写法：

```bash
claude mcp add --scope project --transport http team-memory-<project> \
  http://<your-server>:8080/mcp/<project> --header "X-Memory-User: 张三"
```

确认：同一目录执行 `claude mcp list`，应看到 `✓ Connected`；会话里 `/mcp` 可查看工具列表。

#### Qoder：项目根 `.mcp.json`

`type` 写 `http`（或 `streamable-http`），其余同上：

```bash
qodercli mcp add -s project -t http team-memory-<project> \
  http://<your-server>:8080/mcp/<project> -H "X-Memory-User: 张三"
```

生成的就是上面那份 `.mcp.json`。确认：`qodercli mcp get team-memory-<project>` 能看到 `Type: http`、URL 与 `X-Memory-User` 头。

#### CodeBuddy：项目根 `.mcp.json`

官方 PROJECT Scope 推荐 `<project root>/.mcp.json`（旧的 `mcp.json` 已废弃），字段同样写 `type: "http"`、`url`、`headers`。项目级配置首次连接需要你在客户端里确认放行。

#### 其他客户端

远程条目统一需要：URL `http://<your-server>:8080/mcp/<project>` + 请求头 `X-Memory-User: <你的名字>`；若客户端要求显式传输类型，写 `http`。只支持 stdio 的客户端可用 `npx -y mcp-remote http://<your-server>:8080/mcp/<project>` 做代理（代理方式无法方便地附加自定义头，名字会回落到会话标签）。

---

### 第三步：配置客户端 rule

服务端不调用大模型，`initialize.instructions` 也不保证被客户端注入。要让模型真的「开场读、结束写」，必须准备一份客户端每次必注入的项目说明。

放哪里：

| 客户端 | 文件 |
|---|---|
| Trae | 项目根 `.trae/rules/project_rules.md`；全局规则是 `user_rules.md` |
| Claude Code | 项目根 `CLAUDE.md` |
| Qwen Code | `QWEN.md` |
| 其他支持 AGENTS.md 的客户端 | `AGENTS.md` |

内容（三行版，直接粘贴）：

```md
## 团队记忆（memory MCP）
- 每次任务开始先调用 memory_get_rules，按索引「适用场景」选与本次业务最相关的主题 memory_read，覆盖要碰的代码和数据即停，不够再继续。
- 命中服务级规则第 4 条任一条件（定方案/踩坑/接口或表结构变更/关键结论/任务状态/纠正旧结论）立即 memory_write。
- 收尾必须写 journal；无事写「本次无需记录：原因」。
```

更完整的版本（TASKS 占用登记、强制检查清单、固定收尾格式、错误处理）：[client-rule-prompt.md](docs/design/client-rule-prompt.md)。

关于 TASKS.md：它是「开工前的计划 + 文件占用表」，固定六列：

```
任务 | 状态 | 下一步 | 涉及文件 | 推进者（自报） | 更新时间
```

要改 1 个及以上文件时，先 `memory_write` 登记一行再动代码；`get_rules` 返回的 `tasks` 就是当前占用表，要改的文件若出现在别人的行里，先沟通或避开；做完删掉自己那行。撞车时响应 `warnings` 会给出 `TASK_CONFLICT`，这是提醒不是锁。

**可选：用 Hook 强制收尾。** 提示词防不住模型忘记，Trae 的 `.trae/hooks.json` 和 Claude Code 的 `.claude/settings.json` 都支持 `Stop` 事件：模型准备结束时跑一个命令，检查本次会话有没有写 journal，没有就 `{"decision":"block"}` 打回去。配置见各自官方文档。

---

### 第四步：添加客户端插件

插件是标准 `.vsix`，VS Code 与 Trae 都能装。当前插件 `0.1.8`，可连服务端 `0.1.1+`（当前服务端 `0.1.7`）。

安装：

1. 扩展面板 → 右上角 `...` → **Install from VSIX...** → 选 `team-memory-0.1.8.vsix`
2. 装完 `Ctrl+Shift+P` → `Developer: Reload Window`

命令行方式（`code` 在 PATH 里时）：

```powershell
code --install-extension .\team-memory-0.1.8.vsix
```

vsix 不在仓库里（已被 `.gitignore` 排除）。自己构建见文末「开发与测试」，或向维护者要 `team-memory-0.1.8.vsix`。

装好后在 `settings.json` 里填服务地址（**不要带 `/mcp`**）：

```json
{
  "teamMemory.serverUrl": "http://<your-server>:8080",
  "teamMemory.autoConnect": true,
  "teamMemory.requestTimeoutMs": 15000
}
```

- 名字不在这里配：插件直接连服务，不读 MCP 客户端那份配置；需要自报姓名时在 MCP 客户端加 `X-Memory-User` 头。
- 项目在侧边栏「切换项目」里选，按工作区记住，不写进 `settings.json`。
- 完整功能（索引警告、reindex、新建主题、写 journal）见 [vscode-extension/README.md](vscode-extension/README.md)。

---

### 装好之后怎么用

第一句可以这样说，同时验证工具连通和索引是否可用：

> 先读索引，然后告诉我这个项目现在有哪些未完成任务。

之后正常对话即可，模型会按第三步的规则自己开场读、收尾写。你可以在三个地方确认它真的在执行：

1. 会话里出现 `memory_get_rules` 调用；
2. 开工时 `TASKS.md` 多了一行，做完那行消失；
3. 收尾后 `journal/YYYY-MM/` 下新增一个 `<姓名>-<标题>-<hhmmss>.md`。

服务端提供 6 个工具：

| 工具 | 用途 |
|---|---|
| `memory_get_rules` | 读服务级规则 + 项目索引，返回 `session`、`tasks`（当前占用表）、`warnings`，写入前必须先调 |
| `memory_read` | 读指定文件，支持历史版本与分页 |
| `memory_write` | 新建/追加/整篇覆盖/补索引；写 journal 时自动改名并进索引 |
| `memory_import` | 批量导入已有知识，先 `dry_run` 预演 |
| `skill_list` | 列出全局与项目技能 |
| `skill_promote` | 技能提审与审批 |

详见 [src/README.md](src/README.md)。

---

## 多个 AI 同时改代码，怎么不撞车

一句话：**开工先登记、动手前看占用、覆盖带 hash**。前两层是提醒，最后一层是硬保护。

### 1. 开工先登记

要改 1 个及以上文件时，先 `memory_write` 往 `TASKS.md` 写一行六列，把「涉及文件」写清楚：

```
任务 | 状态 | 下一步 | 涉及文件 | 推进者（自报） | 更新时间
```

涉及文件写仓库相对路径，多个用逗号或顿号分隔。推进者和更新时间由服务端自动补，不用手填。

### 2. 动手前看占用

`memory_get_rules` 的返回值里已经有 `tasks[]`，每条含 `files` 和 `owner`，不用再单独读 `TASKS.md`。要改的文件如果出现在别人的行里，先说明、避开或沟通，别硬改。

### 3. 撞上会提醒，覆盖会被拦

- 写记忆文件时，若同一路径已被别的会话登记，响应里的 `warnings` 会有 `TASK_CONFLICT`，列出任务、推进者和路径。**只提醒，不阻断**。
- `replace` 必须带上次读到的 `expectedHash`。这期间别人改过同一文件，会返回 `409 HASH_MISMATCH`，逼你重新 `memory_read` 合并后再写，不会静默覆盖。

| 环节 | 触发时机 | 服务端行为 | 是否阻断 |
|---|---|---|---|
| 登记占用 | 开工时 | 写入 TASKS 行 | — |
| 查看占用 | 改文件前 | `get_rules.tasks` 返回当前表 | 否 |
| 冲突提醒 | 写记忆文件且同路径被登记 | `warnings: TASK_CONFLICT` | 否 |
| 覆盖保护 | `replace` 时 hash 不符 | `409 HASH_MISMATCH` | 是 |
| 串行写入 | 任何写入 | 按文件加锁排队 | 排队 |
| 历史快照 | 覆盖前 | `.history` 保留最近 50 份 | 否 |

### 边界要说清

- `TASKS.md` 是**自报的协作约定，不是分布式锁**。两个 AI 同时开工，仍可能都登记、都动手。
- 这套机制只保护记忆文件（`RULE.md`、`TASKS.md`、`topics/`、`journal/`、`skills/`）。你仓库里的代码文件不归它管，代码冲突仍然靠 git。
- 实际做法：一个任务尽量只碰少数文件；看到 `TASK_CONFLICT` 先 `git status` / `git diff` 看一眼；收尾立刻删掉自己的 TASKS 行，别让占用表过期。

---

## 架构

单二进制 Go 服务（Streamable HTTP + MCP JSON-RPC），数据是纯 Markdown 目录；服务端只负责门控（未读规则不能写）、索引维护与提醒，不调用任何大模型。

```
MCP 客户端（Trae / Claude Code / Qoder / CodeBuddy …）
        │  http://<your-server>:8080/mcp/<project>
        ▼
   team-memory 服务 ── data/projects/<project>/{RULE.md,TASKS.md,topics/,journal/,skills/}
        ▲
        │  同一个 HTTP 接口
   VS Code / Trae 扩展
```

## 目录结构

```
.
├── src/                 Go 服务（含 Dockerfile、compose、CLI）
├── vscode-extension/    VS Code / Trae 扩展（TypeScript）
├── docs/design/         设计文档与客户端提示词
├── LICENSE
└── README.md
```

- 服务端说明：[src/README.md](src/README.md)
- 插件说明：[vscode-extension/README.md](vscode-extension/README.md)
- 客户端「开场读、结束写」提示词：[client-rule-prompt.md](docs/design/client-rule-prompt.md)

## 数据目录（`data/`）

容器内挂到 `/data`。以下是一个真实实例的现状（`kms` 为示例 slug）：

```
data/
├── .pid                          服务进程号（单实例锁）
├── _service/RULES.md             服务级规则，对所有项目生效
├── skills/                       全局技能根目录，启动时创建，初始为空
│   └── <name>/SKILL.md           提审通过后的全局技能
└── projects/<slug>/
    ├── RULE.md                   项目索引表（模型第一眼读的）
    ├── TASKS.md                  开工前计划 + 文件占用表（六列）
    ├── topics/*.md               主题文档（知识正文）
    ├── journal/YYYY-MM/<姓名>-<标题>-<hhmmss>.md  会话日志
    ├── journal/YYYY-MM.INDEX.md  当月日志索引
    └── .history/<相对路径>/<时间>.md  旧版本快照
```

| 路径 | 谁写入 / 何时产生 | 模型怎么用 | 手改或删除的后果 |
|---|---|---|---|
| `_service/RULES.md` | 服务首次启动时生成，之后只由人工维护 | `memory_get_rules` 以 `service_rule` 返回全文 | MCP 不可写（不在路径白名单）。手改后会话需重新 `get_rules`；删除会在下次启动重建默认内容，原内容丢失 |
| `skills/` | 启动时创建，初始为空 | 不直接读取，只通过 `get_rules.global_skills` 出现 | 删掉会重启重建，无副作用 |
| `skills/<name>/SKILL.md` | 项目技能由 `memory_write` 写入；`skill_promote(action=promote)` 或 `approve` 后移动到 `skills/`（与 `projects/` 平级），项目内副本删除 | `get_rules.global_skills` 列出；`memory_read` 读 `@global/skills/<name>/SKILL.md` | MCP 只读；要改先在项目里建一份再提升。删除即失去该全局技能 |
| `projects/<slug>/RULE.md` | 项目首次被访问时自动生成；新建主题时自动插索引行，`reindex` 补漏 | 每次 `get_rules` 返回全文与 `warnings` | 手改会让旧 session 失效（`RULE_CHANGED`），需重新 `get_rules`；索引写错在下次 `get_rules` 报 `missing_in_index` / `missing_file` / `index_too_large` / `row_too_long` |
| `TASKS.md` | 项目首次访问时生成；表格行由 `memory_write` 写入 | `get_rules.tasks` 直接返回解析后的行；需要原文时 `memory_read` | 六列：任务 / 状态 / 下一步 / 涉及文件 / 推进者（自报） / 更新时间。写入时服务端把旧四列、五列自动升级为六列，并补「推进者」与「更新时间」；任务完成要删行。手改后同样要重新 `get_rules` 才能写 |
| `topics/*.md` | `memory_write`（新建必须带索引行）或 `memory_import` | `memory_read` 按索引「适用场景」挑选阅读 | `replace` 前自动快照到 `.history`；直接删除会留下索引行，下次 `get_rules` 报 `missing_file` |
| `journal/YYYY-MM/<姓名>-<标题>-<hhmmss>.md` | 模型收尾时 `memory_write` 写入，服务端按 `userName`（缺省会话标签）与当前时间重命名，并保留你给的文件名做标题 | 不在 `get_rules` 正文里，只在当月索引中出现 | 可直接删除，索引行不会自动清理；不影响主题文档 |
| `journal/YYYY-MM.INDEX.md` | 写 journal 时由服务端自动追加一行（列为「日期 / 会话标签 / 一句话 / 文件」） | `get_rules` 返回当月与上月的索引文件清单，模型按需 `memory_read` | 手改不会立刻报错，但会与日志文件不一致；没有强校验。已有旧文件保留原「人」表头，仅追加新行 |
| `.history/<相对路径>/<时间>.md` | 任何 `replace` 或 `import(mode=replace)` 覆盖前自动生成 | `memory_read` 传 `rev` 读取旧版本 | 每个文件只保留最近 50 份，自动清理；删掉只丢历史，不影响当前内容 |
| `.pid` | 启动时写入进程号（容器内为 `1`），退出时删除 | 模型用不到 | 用作单实例锁。运行中删除可能导致同一数据目录被启动第二个实例，不要删 |

**自动维护 vs 人工编辑**

- 服务端自动维护：`RULE.md` 的索引行、`journal/*.INDEX.md`、journal 文件名、`.history/`、`.pid`、`skills/` 目录本身、`TASKS.md` 的「推进者/更新时间」两列。
- 鼓励人工编辑：`_service/RULES.md`、`topics/*.md` 正文、`TASKS.md` 的「任务/状态/下一步/涉及文件」、项目 `RULE.md` 里索引行的「适用场景」。
- 直接改磁盘文件不会触发写入校验，也不会更新 session 的规则哈希；改动只在**下一次 `get_rules`** 被感知，索引问题也到那时才以 `warnings` 形式报出。

## 数据与备份

所有内容都在 `DATA_DIR` 下，直接打包即可备份：

```bash
tar czf memory-$(date +%F).tgz -C ./data .
```

`data/`、`bin/`、`node_modules/`、`out/`、`*.vsix` 已在 `.gitignore` 中排除，不要提交。

## 开发与测试

```bash
git clone https://gitee.com/tianling/team-memory.git
cd team-memory
```

服务端（Go 1.23+）：

```bash
cd src
go test ./... -count=1
go build -o bin/team-memory ./cmd/memorysvc
```

冒烟（按真实客户端顺序走握手、读写与并发）：

```bash
go run ./cmd/smoke -url http://127.0.0.1:8080 -project demo -n 100
```

插件（Node.js 18+），产物即第四步用的 vsix：

```powershell
cd vscode-extension
npm install
npm run compile
npx vsce package --no-dependencies --allow-missing-repository `
  --baseContentUrl "https://github.com/try520/team-memory/blob/main/vscode-extension" `
  --baseImagesUrl "https://github.com/try520/team-memory/raw/main/vscode-extension"
```

`--baseContentUrl` 不能省：仓库主地址在 Gitee，vsce 检测不到相对链接基准会直接报错。

## 安全边界

服务默认不做鉴权，设计用于内网或本机。暴露到公网前请自行加反向代理与访问控制。`X-Memory-User` 是自报姓名，不是身份认证。

## License

[MIT](LICENSE)。Copyright (c) 2026 田灵。
