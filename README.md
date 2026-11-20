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

## 快速开始（Docker）

```bash
cd src
docker compose up -d
curl -s http://127.0.0.1:8080/healthz
# {"ok":true,"pid":1,"projects":0,"sessions":0,"uptime_s":5,"version":"0.1.2"}
```

`docker-compose.yml` 默认从本目录构建镜像，并把 `./data` 挂到容器的 `/data`。镜像已公开发布、可匿名拉取，也可 `docker compose up -d --build` 本地构建。

预构建镜像在阿里云容器镜像服务（公开，无需登录）：

```yaml
services:
  memory:
    image: registry.cn-hangzhou.aliyuncs.com/mybase-tools/develop-memory:0.1.2
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

**Claude Code**（项目根 `.mcp.json`）：

```json
{"mcpServers":{"team-memory-<project>":{"type":"http","url":"http://<your-server>:8080/mcp/<project>"}}}
```

**Cursor**（`.cursor/mcp.json`）：

```json
{"mcpServers":{"team-memory-<project>":{"url":"http://<your-server>:8080/mcp/<project>"}}}
```

**VS Code / Copilot**（`.vscode/mcp.json`，顶层键是 `servers`）：

```json
{"servers":{"team-memory-<project>":{"type":"http","url":"http://<your-server>:8080/mcp/<project>"}}}
```

只支持 stdio 的客户端：`npx -y mcp-remote http://<your-server>:8080/mcp/<project>` 做代理。

服务端提供 6 个工具：`memory_get_rules`、`memory_read`、`memory_write`、`memory_import`、`skill_list`、`skill_promote`。详见 [src/README.md](src/README.md)。

> 服务端不调用大模型，`initialize.instructions` 也不保证被客户端注入。要让它真的「开场读、结束写」，需要把 [client-rule-prompt.md](docs/design/client-rule-prompt.md) 里的三行写进客户端每次必注入的项目说明（`CLAUDE.md` / `QWEN.md` / `AGENTS.md`）。

## 编辑器扩展

扩展是标准 `.vsix`，VS Code 与 Trae 都可安装。当前插件 `0.1.1`、服务端 `0.1.2`（插件可连 `0.1.1+`）：

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
