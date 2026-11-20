# 团队研发长久记忆服务（team-memory）

一个进程、一个端口，每个项目一个 MCP 端点，多个大模型共享同一份 md 开发记忆。
无账号、无令牌、无数据库；写入必须来自「已读规则」的会话。

## 启动

单二进制：

```bash
DATA_DIR=/srv/team-memory PORT=8080 ./team-memory
```

Docker（数据默认挂在当前目录的 `./data`）：

```bash
docker compose up -d
curl -s http://127.0.0.1:8080/healthz   # {"ok":true,"projects":0,...}
```

容器以 root 进入口脚本，启动时先把 `DATA_DIR`（默认 `/data`）整个目录 `chown` 给容器内的 `memory`
（uid 10001），再用 `su-exec` 降权运行服务。所以宿主机目录无论是 compose 自动新建的 `root:root`
还是已有数据目录，都不需要手工 `chown`。若挂载点是 NFS 且开了 root_squash（chown 无效），入口脚本
会打印 warn 并继续启动，需要你在挂载侧放行 uid 10001 的写权限。

镜像拉不动时：先看 `docker info` 里的 `Registry Mirrors`，或在构建时用 build-arg 指定可用的基础镜像：

```bash
docker build --build-arg GO_IMAGE=registry.example.com/library/golang:1.23-alpine \
             --build-arg RUNTIME_IMAGE=registry.example.com/library/alpine:3.20 \
             -t team-memory:0.1.1 .
```

一个数据目录只允许一个实例：启动会在数据目录写 `.pid`，检测到存活进程直接退出。
因此数据目录必须可写：只读挂载会在启动时报「写 PID 文件 … 失败（数据目录需要可写）」；
目录存在但不可写（如 `0555`）会报「初始化数据目录失败: mkdir /data/projects: permission denied」。
备份 = 打包数据目录：`tar czf memory-$(date +%F).tgz -C ./data .`

## 初始化：把已有知识一次性灌进来

新建项目时不用手工拷文件，`POST /import/<project>` 一次收下整批 md：

```json
{
  "mode": "merge",
  "dry_run": false,
  "rule_md": "# xxx 知识索引\n\n## 文档索引\n\n| 文档 | 适用场景 | 举例 |\n| --- | --- | --- |\n",
  "files": [{"path": "topics/01-后端架构.md", "content": "# 后端架构\n..."}],
  "index": [{"path": "topics/01-后端架构.md", "title": "01-后端架构",
             "scene": "涉及后端路由、模块划分的改动前必读", "examples": ["路由怎么定义？"]}]
}
```

- `mode=merge`（默认）跳过已存在文件，可反复执行；`replace` 覆盖并留 `.history` 快照。
- `rule_md` 只在 `replace` 或当前索引为空时写入，避免覆盖后来人工补的索引。
- 省略 `index` 时按 reindex 口径给未登记主题补 `<!-- auto -->` 行；场景取自首段，建议后续改准确。
- `dry_run=true` 只校验与试算（连 `created`/`index_added` 都会预估），不落盘。
- 单次上限 64MB；路径白名单与 `memory_write` 相同（`RULE.md`、`TASKS.md`、`topics/**.md`、`journal/**.md`）。

目录同步用现成 CLI：它遍历目录、映射为 `topics/<相对路径>`，并把旧索引里指向这些文件的链接自动改写到 `topics/`：

```bash
go run ./cmd/import -url http://127.0.0.1:8080 -project my-project \
  -src "/path/to/documents" \
  -rule "/path/to/knowledge-index.md" -dry-run
```

去掉 `-dry-run` 即真正写入；同样的命令再跑一次只输出 `skipped=N`，所以也能用来增量补新文档。

同一个能力也挂在 MCP 上：工具 `memory_import`（需 `session`，走同一套逻辑），让客户端大模型自己读源文档、分批导入。

## 接入

传输统一是 Streamable HTTP，地址 `http://<server>:8080/mcp/<project>`；不需要额外 header，
`Mcp-Session-Id` 由客户端按协议自动携带（服务端在 initialize 响应头签发）。

**Claude Code**（项目根 `.mcp.json`，或 `claude mcp add --transport http team-memory-my-project <url>`）：

```json
{"mcpServers":{"team-memory-my-project":{"type":"http","url":"http://<your-server>:8080/mcp/my-project"}}}
```

**Cursor**（`.cursor/mcp.json`）：

```json
{"mcpServers":{"team-memory-my-project":{"url":"http://<your-server>:8080/mcp/my-project"}}}
```

**VS Code / Copilot**（`.vscode/mcp.json`，顶层键是 `servers`）：

```json
{"servers":{"team-memory-my-project":{"type":"http","url":"http://<your-server>:8080/mcp/my-project"}}}
```

只支持 stdio 的客户端：`npx -y mcp-remote http://<your-server>:8080/mcp/my-project` 做代理。
首次访问自动创建项目目录、`RULE.md`（含空索引表与使用规则）、`TASKS.md`，零注册。
建议首问：**「先读索引，然后告诉我这个项目现在有哪些未完成任务」**——同时验证索引与工具连通。

### 让模型真的会写：客户端侧还要加三行

实测结论（qwen CLI + deepseek-v4.1-flash / deepseek-v4-pro / glm-5.3-flash，见[方案文档](../docs/design/team-memory-service-plan.md) §5.4）：
服务端 `initialize.instructions` 有的客户端根本不注入上下文；模型也不会因为工具描述就主动
`get_rules`、更不会在任务结束后主动写回。可靠做法是在客户端**每次必注入**的项目说明里写三行
（Claude Code 的 `CLAUDE.md`、Qwen Code 的 `QWEN.md`、通用 `AGENTS.md`）：

```md
## 团队记忆（memory MCP）
- 每次任务开始先调用 `memory_get_rules`，按索引「适用场景」挑 1~2 篇 `memory_read`。
- 命中服务级规则第 4 条任一条件（定方案/踩坑/接口或表结构变更/关键结论/任务状态/纠正旧结论）立即 `memory_write`。
- 收尾必须写 journal；无事写「本次无需记录：原因」。
```

这是客户端配置，不是服务端能力。服务端只负责：门控（未读规则不能写）、索引维护、提醒字段。
完整版（含任务结束强制检查清单、固定收尾格式、错误处理、验收方法）：[client-rule-prompt.md](../docs/design/client-rule-prompt.md)。

## 六个工具

| 工具 | 用途 |
|---|---|
| `memory_get_rules` | 读服务级规则 + 项目索引（RULE.md 全文），返回 `session`、`warnings`、文件清单、当月/上月 journal 索引、`global_skills` 全局技能清单 |
| `memory_read` | 读文件；`rev` 读 `.history` 旧版；超过 32KB 用 `next_offset` 续读；路径含 `skills/<name>/SKILL.md` 与只读的 `@global/skills/<name>/SKILL.md` |
| `memory_write` | `create` / `append` / `replace`（必带 `expectedHash`）/ `reindex`（仅 RULE.md）；写技能文件时校验 frontmatter |
| `memory_import` | 批量导入已有知识：模型先用自己的文件工具读源文档，再分批调它落盘，需 `session`，先 `dry_run` 预演 |
| `skill_list` | 列出全局技能 + 当前项目技能（只读，无需 session），每项含 `scope/name/description/status/source_project/path` |
| `skill_promote` | 技能提审：`submit`（生成全局 pending）→ `approve`（转 approved，各项目可见）/ `reject` / `cancel`，需 `session` |

新建主题必须带 `index: {scene, examples}`，服务端按固定格式把行插进 `RULE.md` 索引表；
写 `journal/YYYY-MM/<label>-<hhmm>.md` 时自动向 `journal/YYYY-MM.INDEX.md` 追加一行。

`memory_import` 是给模型用的批量入口：一次可提交多篇原文 + 索引行（`merge` 默认跳过已存在，可反复执行），
适合「这个项目已经有一堆 md 规则，先灌进去」的场景；日常零散结论仍用 `memory_write`。

### 技能（Skill）

技能是可复用的操作手册，数据形态对齐 Agent Skills 的 `SKILL.md`（`---` 包裹的 YAML frontmatter + 正文），
frontmatter 必须有 `name` 与 `description`。项目技能在 `projects/<slug>/skills/<name>/SKILL.md`，
全局技能在 `skills/<name>/SKILL.md`。客户端说自然语言即可，模型按规则映射：

- 「根据之前的操作生成 xx 技能」→ `memory_write` 写 `skills/<name>/SKILL.md`；
- 「修改 / 完善 xx 技能」→ `memory_read` 读全文后 `memory_write` 的 `append` / `replace`；
- 「把 xx 技能提升为全局技能」→ `skill_promote(action=submit)` 提审为 pending，维护者 `approve` 后才进入各项目 `get_rules` 的 `global_skills`。

全局技能对所有项目只读（不能用 `memory_write` 写 `@global/` 路径）；要改全局技能，先在项目里改技能再重新提审。
`submit` → `approve` 为两阶段；服务不内置鉴权与大模型，审批由调用方通过工具完成。

## 协议行为

| 请求 | 响应 |
|---|---|
| POST JSON-RPC（非 initialize）不带 `Mcp-Session-Id` | 400 + JSON-RPC `-32600` |
| `Mcp-Session-Id` 未知 / 过期 / 跨项目 | 404 + `-32001`，客户端重新 initialize |
| 通知（`notifications/initialized` 等） | 202，无 body |
| `Content-Type` 非 `application/json` | 415 |
| `Accept` 只有 `text/event-stream` | 406（本服务只回 JSON） |
| JSON 解析失败 | 400 + `-32700` |
| 不支持的 `MCP-Protocol-Version` | 400 |
| GET（要 SSE 推送） | 405（规范允许） |
| DELETE + 有效 session | 204，会话作废 |

协议错误走 JSON-RPC `error`（-32601 方法不存在、-32602 参数非法、-32001 会话失效），
业务错误走 `result.isError`（428 READ_RULES_FIRST、409 INDEX_ROW_REQUIRED / HASH_MISMATCH、404 NOT_FOUND）。
两套 session：`Mcp-Session-Id` 管传输，`get_rules` 返回的 `session` 管写入门控。

## 三个常见错误

| 错误 | 含义 | 处理 |
|---|---|---|
| `428 READ_RULES_FIRST` / `RULE_CHANGED` | 没读规则，或读完后 `RULE.md` 又变了 | 重新 `memory_get_rules`，把新 `session` 传给 `write` |
| `409 INDEX_ROW_REQUIRED` | 新建主题没给索引行 | 重试时带 `index: {scene:"涉及…的改动前必读", examples:["…？","…？"]}` |
| `409 HASH_MISMATCH` / `EXPECTED_HASH_REQUIRED` | `replace` 没带 hash，或文件已被别人改过 | 重新 `memory_read` 拿最新 `content` 与 `hash`，合并后再 replace |

`get_rules` 的 `warnings` 是索引体检：`missing_in_index`（有文件没登记）、`missing_file`（有行没文件）、
`index_too_large`（>25 行）、`row_too_long`。批量补行用 `memory_write({path:"RULE.md", mode:"reindex"})`，
**只补不删**；删除只能人工改表。

## 验证

```bash
go test ./... -count=1
go run ./cmd/smoke -url http://127.0.0.1:8080 -project demo -n 100
```

冒烟按真实客户端顺序走：initialize（校验 `Mcp-Session-Id`）→ notifications/initialized → tools/list=6 →
get_rules → 缺索引新建 10/10 被拒 → 写主题并读回 → 100 并发 append 无丢失无重复 → reindex 幂等。
