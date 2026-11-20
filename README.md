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
# {"ok":true,"pid":1,"projects":0,"sessions":0,"uptime_s":5,"version":"0.1.1"}
```

`docker-compose.yml` 默认从本目录构建镜像，并把 `./data` 挂到容器的 `/data`。镜像未发布前直接 `docker compose up -d --build` 即可。

Gitee 不提供容器镜像仓库，预构建镜像计划发布在 GitHub（`ghcr.io/try520/team-memory`），发布后可这样引用：

```yaml
services:
  memory:
    image: ghcr.io/try520/team-memory:0.1.1
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

扩展是标准 `.vsix`，VS Code 与 Trae 都可安装。当前服务端与插件版本均为 `0.1.1`：

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
