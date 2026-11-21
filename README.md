# team-memory

给团队研发用的「长久记忆」服务：一个进程、一个端口，每个项目一个 MCP 端点，多个大模型共享同一份 Markdown 开发记忆。无账号、无令牌、无数据库，写入必须来自「已读规则」的会话。

配套一个 VS Code / Trae 扩展，在编辑器里浏览与编辑记忆文件。

- Gitee：<https://gitee.com/tianling/team-memory>
- GitHub：<https://github.com/try520/team-memory>

## 架构

单二进制 Go 服务（Streamable HTTP + MCP JSON-RPC），数据是纯 Markdown 目录；服务端只负责门控（未读规则不能写）、索引维护与提醒，不调用任何大模型。

```
MCP 客户端（Claude Code / Cursor / VS Code / Trae …）
        │  http://<your-server>:8080/mcp/<project>
        ▼
   team-memory 服务 ── data/projects/<project>/{RULE.md,TASKS.md,topics/,journal/,skills/}
        ▲
        │  同一个 HTTP 接口
   VS Code / Trae 扩展
```

## 预估节省的 token

> 估算，不是 benchmark。基数来自真实项目（kms：8 篇主题、76 篇 journal、86 个文件）的一次实测；token 按「中文 1 字 ≈ 1 token、ASCII 4 字符 ≈ 1 token」折算，故给区间。

| 实测项 | 字符数 | 估算 token |
|---|---|---|
| `memory_get_rules` 一次返回（service_rule + index + 文件列表 + journal 索引） | 12,795 | **3.7k–5.4k**（典型 ~4.6k） |
| 读 1 篇主题文档（8 篇均值） | 6,002 | ~2.6k |
| 写 1 篇 journal（76 篇均值） | 469 | ~0.2k |

客户端加三行规则后，同一任务实测从 4 轮 / 70s 变成 11 轮 / 198s（+7 轮、+128s）。其中约 3 轮是纯记忆开销，按每轮 0.6k–1k token 计，**+2k–3k token**。

没有记忆服务时，新会话要重新交代背景、翻代码定位、重踩已记录的坑。三档假设：

| 档位 | 背景重述 | 定位代码 | 重复踩坑 | 单会话基线成本 |
|---|---|---|---|---|
| 保守 | 2k | 6k（约 3 个文件） | 4k | **12k** |
| 典型 | 3k | 12k（约 5–6 个文件） | 7k | **22k** |
| 乐观 | 4k | 24k（约 10 个文件） | 12k | **40k** |

记忆成本 ≈ get_rules 4.6k + 1 篇主题 2.6k + journal 0.2k + 额外轮次 2.5k，合计 **8k–12k**。扣减后：

| 档位 | 每会话净节省 |
|---|---|
| 保守 | **约 +4k** |
| 典型 | **约 +12k** |
| 乐观 | **约 +28k** |

按 5 人团队、每人每月 60 次会话、其中 40% 属于「需要重新交代背景」的任务（约 120 次/月）：**每月约 0.5M–3.4M token，典型 ~1.4M**。

**不划算的情况**：任务只有一两轮问答；索引或主题本身臃肿（`get_rules` 超过 8k token）；模型在同一会话内连续工作，没有冷启动；索引场景写得不准，读错主题还要纠正。

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
    ├── journal/YYYY-MM/<姓名>-<hhmmss>.md  会话日志
    ├── journal/YYYY-MM.INDEX.md  当月日志索引
    └── .history/<相对路径>/<时间>.md  旧版本快照
```

| 路径 | 谁写入 / 何时产生 | 模型怎么用 | 手改或删除的后果 |
|---|---|---|---|
| `_service/RULES.md` | 服务首次启动时由 `EnsureServiceLayout` 生成，之后只由人工维护 | `memory_get_rules` 以 `service_rule` 返回全文 | MCP 不可写（不在路径白名单）。手改后会话需重新 `get_rules`；删除会在下次启动重建默认内容，原内容丢失 |
| `skills/` | 启动时创建，初始为空 | 不直接读取，只通过 `get_rules.global_skills` 出现 | 删掉会重启重建，无副作用 |
| `skills/<name>/SKILL.md` | `skill_promote` 的 `approve` 从项目技能复制而来 | `get_rules.global_skills` 列出；`memory_read` 读 `@global/skills/<name>/SKILL.md` | MCP 只读；要改先在项目里改再重新提审。删除即失去该全局技能（项目内副本仍在） |
| `projects/<slug>/RULE.md` | 项目首次被访问时自动生成；新建主题时自动插索引行，`reindex` 补漏 | 每次 `get_rules` 返回全文与 `warnings` | 手改会让旧 session 失效（`RULE_CHANGED`），需重新 `get_rules`；索引写错在下次 `get_rules` 报 `missing_in_index` / `missing_file` / `index_too_large` / `row_too_long` |
| `TASKS.md` | 项目首次访问时生成；表格行由 `memory_write` 写入 | 通过 `memory_read` 读取；`get_rules` 的文件清单里列出 | 六列：任务 / 状态 / 下一步 / 涉及文件 / 推进者（自报） / 更新时间。写入时服务端把旧四列、五列自动升级为六列，并补「推进者」（优先 `userName`，否则会话标签）与「更新时间」；改文件前先读它看占用，任务完成立刻删行。手改后同样要重新 `get_rules` 才能写 |
| `topics/*.md` | `memory_write`（新建必须带索引行）或 `memory_import` | `memory_read` 按索引「适用场景」挑选阅读 | `replace` 前自动快照到 `.history`；直接删除会留下索引行，下次 `get_rules` 报 `missing_file` |
| `journal/YYYY-MM/<姓名>-<hhmmss>.md` | 模型收尾时 `memory_write` 写入，服务端按 `userName`（缺省会话标签）与当前时间重命名 | 不在 `get_rules` 正文里，只在当月索引中出现 | 可直接删除，索引行不会自动清理；不影响主题文档 |
| `journal/YYYY-MM.INDEX.md` | 写 journal 时由服务端自动追加一行（列为「日期 / 会话标签 / 一句话 / 文件」） | `get_rules` 返回当月与上月的索引文件清单，模型按需 `memory_read` | 手改不会立刻报错，但会与日志文件不一致；没有强校验。已有旧文件保留原「人」表头，仅追加新行 |
| `.history/<相对路径>/<时间>.md` | 任何 `replace` 或 `import(mode=replace)` 覆盖前自动生成 | `memory_read` 传 `rev` 读取旧版本 | 每个文件只保留最近 50 份，自动清理；删掉只丢历史，不影响当前内容 |
| `.pid` | 启动时写入进程号（容器内为 `1`），退出时删除 | 模型用不到 | 用作单实例锁。运行中删除可能导致同一数据目录被启动第二个实例，不要删 |

**自动维护 vs 人工编辑**

- 服务端自动维护：`RULE.md` 的索引行、`journal/*.INDEX.md`、journal 文件名、`.history/`、`.pid`、`skills/` 目录本身、`TASKS.md` 的「推进者/更新时间」两列。
- 鼓励人工编辑：`_service/RULES.md`、`topics/*.md` 正文、`TASKS.md` 的「任务/状态/下一步/涉及文件」、项目 `RULE.md` 里索引行的「适用场景」。
- 直接改磁盘文件不会触发写入校验，也不会更新 session 的规则哈希；改动只在**下一次 `get_rules`** 被感知，索引问题也到那时才以 `warnings` 形式报出。

## 快速开始（Docker）

```bash
cd src
docker compose up -d
curl -s http://127.0.0.1:8080/healthz
# {"ok":true,"pid":1,"projects":0,"sessions":0,"uptime_s":5,"version":"0.1.5"}
```

`docker-compose.yml` 默认从本目录构建镜像，并把 `./data` 挂到容器的 `/data`。镜像已公开发布、可匿名拉取，也可 `docker compose up -d --build` 本地构建。

预构建镜像在阿里云容器镜像服务（公开，无需登录）：

```yaml
services:
  memory:
    image: registry.cn-hangzhou.aliyuncs.com/mybase-tools/develop-memory:0.1.5
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

环境变量：

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `DATA_DIR` | `/data` | 数据根目录，必须可写 |
| `PORT` | `8080` | 监听端口 |
| `TZ` | 系统时区 | 影响 journal 的月份分组 |

一个数据目录只允许一个实例：启动会写 `.pid`，检测到存活进程直接退出。

## MCP 接入

每个项目一个 URL：`http://<your-server>:8080/mcp/<project>`。项目在第一次请求时自动创建，零注册。

所有客户端都建议加请求头 `X-Memory-User`，值换成你自己的名字（例如 `张三`）：服务端用它作为 TASKS.md 的「推进者」和 journal 文件名，`get_rules` 会回显 `user_name` 与来源。它是自报信息，不是身份认证，不能用于追责；值非法（超 32 字、含竖线/换行）会被忽略并回落到 `s_xxxxxxxx` 会话标签，不阻断连接。

### VS Code / Copilot：`.vscode/mcp.json`

文件位置：工作区根目录 `.vscode/mcp.json`（没有就新建）。顶层键是 `servers`，远程条目写 `type: "http"`。

```json
{
  "servers": {
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

确认：在 MCP 视图启动该服务器，状态显示已连接，工具列表出现 6 个工具（`memory_get_rules`、`memory_read`、`memory_write`、`memory_import`、`skill_list`、`skill_promote`）；或在 Copilot Chat 的工具体选择器里看到这些工具。

### Trae：`%APPDATA%\Trae CN\User\mcp.json`

文件位置：Windows 国内版 `%APPDATA%\Trae CN\User\mcp.json`，国际版 `%APPDATA%\Trae\User\mcp.json`（本机两个文件均存在）。顶层键是 `mcpServers`。

字段限制：远程条目**只写 `url`、`headers`，不要写 `type`**。本机 Trae CN 的 schema（注册名 `vscode://schemas/mcp-settings/user`，HTTP 分支 `required: ["url"]`、`additionalProperties: false`）没有 `type` 字段，多写会校验失败。

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

确认：设置 > MCP，该服务器显示已连接，工具列表出现上面 6 个工具。

### Claude Code：项目根 `.mcp.json`

文件位置：项目根目录 `.mcp.json`。顶层键是 `mcpServers`，远程条目写 `type: "http"`。

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

等价的命令行写法（`--header` 可以重复，值按需换成你的名字）：

```bash
claude mcp add --scope project --transport http team-memory-<project> \
  http://<your-server>:8080/mcp/<project> --header "X-Memory-User: 张三"
```

确认：在同一目录执行 `claude mcp list`，应看到该服务器 `✓ Connected`；会话里 `/mcp` 可查看工具列表。

### Qoder：项目根 `.mcp.json`

文件位置：项目级 `<项目>/.mcp.json`（顶层 `mcpServers`）；用户级 `%USERPROFILE%\.qoder\settings.json` 的 `mcpServers`。依据 Qoder 官方 MCP 参考，并已用本机 `qodercli mcp add` 实际生成/解析验证。

字段限制：远程条目写 `type: "http"`（或 `streamable-http`）、`url`、`headers`。

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

等价的命令行写法（本机实测生成的就是上面这份 `.mcp.json`）：

```bash
qodercli mcp add -s project -t http team-memory-<project> \
  http://<your-server>:8080/mcp/<project> -H "X-Memory-User: 张三"
```

确认：`qodercli mcp get team-memory-<project>` 能看到 `Type: http`、URL 与 `X-Memory-User` 头；会话内 `/mcp` 查看连接状态与工具列表。本机未启动交互会话，6 个工具的实际注入未实测。

### CodeBuddy：`%USERPROFILE%\.codebuddy\.mcp.json`

文件位置：官方文档为 `%USERPROFILE%\.codebuddy\.mcp.json`（Windows）或 `~/.codebuddy/.mcp.json`。本机已有旧版 `%USERPROFILE%\.codebuddy\mcp.json`；官方文档说明，若旧版文件存在则优先写入已存在的那个。顶层键是 `mcpServers`。

字段限制：HTTP 条目写 `type: "http"`、`url`、`headers`（官方 HTTP 类型字段表）。

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

确认：在 CodeBuddy 的 MCP 设置/面板里查看该服务器已连接、工具列表出现上面 6 个工具。本机只装了 CodeBuddy 扩展，没有可启动的 CLI/IDE，因此未做工具列表实测。

### 其他客户端

远程条目统一需要：URL `http://<your-server>:8080/mcp/<project>` + 请求头 `X-Memory-User: <你的名字>`；若客户端要求显式传输类型，写 `http`。只支持 stdio 的客户端可用 `npx -y mcp-remote http://<your-server>:8080/mcp/<project>` 做代理（代理方式无法方便地附加自定义头，名字会回落到会话标签）。

服务端提供 6 个工具：`memory_get_rules`、`memory_read`、`memory_write`、`memory_import`、`skill_list`、`skill_promote`。详见 [src/README.md](src/README.md)。

开发者姓名来自客户端配置的 `X-Memory-User` 请求头（推荐，一次配置长期生效）；`memory_write` 的可选参数 `userName` 只用于临时改用别的名字。优先级：**工具参数 > 请求头 > 会话标签**。名字用于 TASKS.md 的「推进者」与 journal 文件名/索引标签；工具参数非法返回 400，请求头非法则忽略并回落，不会让整个客户端不可用。它不是身份认证，不能用于追责。

TASKS.md 是**开工前的计划 + 文件占用表**：规划完成后、改代码前先写计划（含涉及文件），改文件前先读它看是否被其他会话登记，任务完成立刻删行。写入时若发现同一路径被别的会话登记，响应 `warnings` 会给出 `TASK_CONFLICT` 提示，但不阻断——这是自报的协作约定，不是锁，真正的兜底是服务端按文件串行 + `expectedHash` 校验。

> 服务端不调用大模型，`initialize.instructions` 也不保证被客户端注入。要让它真的「开场读、结束写」，需要把 [client-rule-prompt.md](docs/design/client-rule-prompt.md) 里的三行写进客户端每次必注入的项目说明（`CLAUDE.md` / `QWEN.md` / `AGENTS.md`）。

## 编辑器扩展

扩展是标准 `.vsix`，VS Code 与 Trae 都可安装。当前插件 `0.1.5`、服务端 `0.1.5`（插件可连 `0.1.1+`）：

```powershell
code --install-extension .\team-memory-<version>.vsix
```

安装后在设置里填服务地址（**不要带 `/mcp`**）：

```json
{
  "teamMemory.serverUrl": "http://<your-server>:8080",
  "teamMemory.autoConnect": true,
  "teamMemory.requestTimeoutMs": 15000
}
```

项目在侧边栏「切换项目」里选择，不写进 `settings.json`。完整功能见 [vscode-extension/README.md](vscode-extension/README.md)。

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

插件（Node.js 18+）：

```bash
cd vscode-extension
npm install
npm run compile
npx vsce package --no-dependencies --allow-missing-repository
```

## 数据与备份

所有内容都在 `DATA_DIR` 下，直接打包即可备份：

```bash
tar czf memory-$(date +%F).tgz -C ./data .
```

`data/`、`bin/`、`node_modules/`、`out/`、`*.vsix` 已在 `.gitignore` 中排除，不要提交。

## 安全边界

服务默认不做鉴权，设计用于内网或本机。暴露到公网前请自行加反向代理与访问控制。

## License

[MIT](LICENSE)。Copyright (c) 2026 田灵。
