# 团队记忆（Team Memory）VS Code / Trae 插件

在编辑器里浏览、编辑团队研发记忆服务（team-memory）中的项目记忆：切换项目、看 RULE 索引、按主题/journal 浏览、直接改文件保存回服务、查看索引警告并执行 reindex。

> 版本对应：插件 `0.1.1` 可连服务端 `0.1.1+`（当前服务端 `0.1.2`）。两者独立发版，同一小版本号表示该组合已联调；插件可连更高版本服务端。

## 安装

插件是标准 VS Code 扩展（`.vsix`），Trae 基于 VS Code，两个编辑器都能装。

### VS Code

1. 打开扩展面板（`Ctrl+Shift+X`）→ 右上角 `...` → **Install from VSIX...**
2. 选择 `team-memory-0.1.1.vsix`
3. 装完重载窗口（`Ctrl+Shift+P` → `Developer: Reload Window`）

命令行方式（`code` 在 PATH 里时）：

```powershell
code --install-extension .\team-memory-0.1.1.vsix
```

### Trae

1. 打开扩展面板 → 右上角 `...` → **Install from VSIX...**
2. 选择同一个 `team-memory-0.1.1.vsix`
3. 重载窗口

如果 Trae 的安装目录下有命令行工具（`Trae.exe` / `trae.cmd`），也可以：

```powershell
& "C:\path\to\Trae.exe" --install-extension .\team-memory-0.1.1.vsix
```

装好后左侧活动栏会出现「团队记忆」图标。

## 配置

在 `settings.json`（`Ctrl+,` → 右上角打开 JSON）里配置，或直接运行命令 `团队记忆: 配置服务地址`：

```json
{
  "teamMemory.serverUrl": "http://<your-server>:8080",
  "teamMemory.autoConnect": true,
  "teamMemory.requestTimeoutMs": 15000
}
```

| 配置项 | 说明 |
| --- | --- |
| `teamMemory.serverUrl` | 记忆服务地址，默认 `http://127.0.0.1:8080`，**不要带 `/mcp`** |
| `teamMemory.autoConnect` | 启动时自动连接并读取索引 |
| `teamMemory.requestTimeoutMs` | 单次请求超时（毫秒） |

项目**不在设置里配**：在侧边栏标题栏的「切换项目」里选，选择结果按工作区记住（存在工作区状态里，不写 `settings.json`）。旧版本的 `teamMemory.project` 已废弃，留在 `settings.json` 里也不会生效，可以删掉。

## 使用

### 切换项目

侧边栏「项目记忆」标题右侧的文件夹图标（命令 `团队记忆: 切换项目`）就是项目入口：

- 点开是服务上的项目列表（`GET /mcp` 返回的 `projects`），当前项目标「当前」；选中后立即切换并刷新树
- 列表末尾「新建项目…」输入 slug（小写字母、数字、中划线）即可新建并切换；服务端在第一次读取时创建该项目
- 当前项目显示在视图标题旁（`项目记忆  <project>`），底部状态栏同步显示
- 选择结果按工作区记在 `workspaceState`，重开窗口自动恢复；新工作区第一次打开会直接弹项目列表
- 切换后，属于旧项目、仍打开的文档保存时会被拒绝并提示「该文件属于项目 X，当前项目已切到 Y」，从左侧树重新打开即可

标题栏只保留四个高频入口（切换项目、刷新、新建主题、写 journal）；补齐索引（reindex）、索引警告、服务级规则收在标题栏右侧的「更多 …」菜单里。

连接成功后，侧边栏按项目展示：

- **服务健康**：版本、运行时长（来自 `/healthz`）
- **规则与索引**：`RULE.md`，打开即可编辑
- **任务**：`TASKS.md`
- **服务级规则（只读）**：`memory_get_rules` 返回的共用写入规范
- **主题**：`topics/*.md`
- **Journal**：按 `YYYY-MM` 分组，每月第一项是索引
- **索引警告**：未登记索引、索引指向的文件不存在、索引过大、索引行过长；点条目看处理建议

底部状态栏显示当前项目；未选项目时显示「未选择项目」，点击直接弹项目列表。连接失败时状态栏变黄，悬浮可看原因。

### 打开与保存

点任意文件会在编辑器里打开；**`Ctrl+S` 直接写回服务**：

- 只有 content 变了才写，模式为 `replace`，自动带上次读到的 hash
- 如果这期间别人改过同一文件，会弹窗二选一：**用我的覆盖** / **重新加载服务器版本**
- 服务不支持删除和重命名（文件系统里会提示）

### 新建主题（必须带索引行）

命令面板 → `团队记忆: 新建主题`，依次填：标题 → 适用场景（什么情况下必须先读）→ 举例（逗号分隔）→ 文件路径（默认 `topics/<标题>.md`）。

服务端会同时把索引行插进 `RULE.md`。绕过这一步直接新建 `topics/x.md` 会被拒绝（`INDEX_ROW_REQUIRED`），这是为了避免出现「模型找不到的文件」。

### 写 journal / 加任务

- `团队记忆: 写 journal`：写入 `journal/<YYYY-MM>/<一句话>-<hhmm>.md`，并自动出现在当月索引里
- `团队记忆: 添加任务`：向 `TASKS.md` 追加一条 `- [ ] ...`

### 索引警告与 reindex

- `团队记忆: 查看索引警告`：列出每条警告和处理建议
- `团队记忆: 补齐索引行（reindex）`：给未登记的主题补默认行（只补不删）

### 命令一览

| 命令 | 说明 |
| --- | --- |
| `团队记忆: 切换项目` | 从服务项目列表里切换，或新建项目（标题栏文件夹图标） |
| `团队记忆: 刷新` | 重新读取当前项目的索引与文件列表 |
| `团队记忆: 连接服务` | 与刷新相同，失败后可重试 |
| `团队记忆: 新建主题` | 新建 `topics/*.md` 并写入索引行 |
| `团队记忆: 写 journal` | 追加当月 journal 并进索引 |
| `团队记忆: 添加任务` | 向 `TASKS.md` 追加待办 |
| `团队记忆: 补齐索引行（reindex）` | 给未登记的主题补默认索引行 |
| `团队记忆: 查看索引警告` | 列出索引警告与处理建议 |
| `团队记忆: 查看服务级规则` | 以只读方式打开服务级写入规范 |
| `团队记忆: 配置服务地址` | 打开本扩展的设置页 |

## 能省多少 token

实测（kms 项目）：一次 `memory_get_rules` 返回约 3.7k–5.4k token，读 1 篇主题约 2.6k，写 1 篇 journal 约 0.2k。对比没有记忆服务时要重新交代背景、翻代码定位、重复踩坑（单会话约 12k–40k），每会话净省 **约 4k–28k token**，典型约 12k。

这是估算而非基准测试，假设与三档区间见仓库根目录 README 的「预估节省的 token」一节。

## 部署服务端

### 前置条件

- Docker（本机验证 29.1.3）和 Docker Compose v2（`docker compose` 子命令，本机验证 v2.40.3）
- 一个留给数据的宿主目录，例如 `./data`

### docker-compose.yml

```yaml
services:
  memory:
    image: registry.cn-hangzhou.aliyuncs.com/mybase-tools/develop-memory:0.1.2
    container_name: team-memory
    restart: unless-stopped
    ports:
      - "28080:8080"
    environment:
      DATA_DIR: /data
      PORT: "8080"
      TZ: Asia/Shanghai
    volumes:
      - ./data:/data
```

镜像自带 healthcheck（每 30 秒请求一次 `/healthz`），compose 里不用重复写。宿主机端口改 `ports` 左边即可，容器内始终是 `8080`；卷右侧和 `DATA_DIR` 必须都是 `/data`。

镜像已公开、可匿名拉取；也可以在本仓库 `src/` 下 `docker compose up -d --build` 本地构建。

生产环境建议固定 digest（`docker image inspect <image> --format '{{index .RepoDigests 0}}'` 可查），例如 `image: registry.cn-hangzhou.aliyuncs.com/mybase-tools/develop-memory@sha256:<digest>`。

### 数据目录与权限

容器以 root 进入口脚本，启动时对 `DATA_DIR` 执行 `chown -R 10001:10001`，再用 `su-exec` 降权为 uid 10001 运行服务。所以：

- 目录不存在会自动创建；已有数据（包括 `root:root` 的文件）会被递归改属主，**不需要手工 `chown`**。
- 挂载成只读（`:ro`）或目录本身不可写（如 `0555`）：入口脚本只 warn，服务随后明确报错退出（见下方常见错误）。
- NFS 开了 root_squash 时 chown 会失败，入口脚本 warn 后仍以 uid 10001 启动；需要在挂载侧放行 uid 10001 的写权限。

### 首次启动与验证

```bash
docker compose up -d
docker ps                       # STATUS 变成 healthy 即正常

curl -s http://127.0.0.1:28080/healthz
# {"ok":true,"pid":1,"projects":0,"sessions":0,"uptime_s":5,"version":"0.1.1"}

curl -s http://127.0.0.1:28080/mcp
# 返回 service、version、projects、tools 列表
```

项目不是提前登记的：客户端第一次请求 `/mcp/<project>`（或在插件里连接/选项目）时，服务才创建该项目目录和 `RULE.md`、`TASKS.md`，之后 `/mcp` 的 `projects` 里才会出现它。

### 客户端指向

- 插件配置 `teamMemory.serverUrl`：`http://<服务器IP>:28080`，**不要带 `/mcp`**
- MCP 客户端 URL：`http://<服务器IP>:28080/mcp/<project>`（完整配置见下一节）
- 端口按 compose 里 `ports` 的左边改

### 升级与备份

```bash
# 升级：把 compose 里的 tag 改成新版本号后
docker compose up -d            # 本地没有该 tag 会先拉取，也可以先 docker compose pull

# 备份：直接打包数据目录
tar czf memory-$(date +%F).tgz -C ./data .
```

### 常见错误

| 现象 | 原因与处理 |
| --- | --- |
| `mkdir /data/projects: permission denied` | 数据目录对 uid 10001 不可写。用 `0.1.1` 镜像即可自动改属主；若 compose 里写了 `user:`，入口脚本会跳过 chown，需要自己对齐 uid 10001；目录被设成 `0555` 也会报这个 |
| `写 PID 文件 /data/.pid 失败（数据目录需要可写）` | 挂载成了只读（`:ro`）或目录不可写。`.pid` 用于单实例保护，必须能写 |
| 容器起不来、端口冲突 | 改 `ports` 左边换宿主端口；容器内固定 `8080` |
| `healthy` 一直不出现 | `docker logs team-memory` 看服务日志；确认 `/healthz` 从宿主机可访问 |

## 配置 MCP 客户端

服务端是 Streamable HTTP，每个项目一个 URL：`http://<服务器IP>:28080/mcp/<project>`，最后一段就是项目名。项目在第一次请求时自动创建，不需要预先登记。

服务端**不校验任何请求头**，所以 `X-Memory-Client` 只是自定义标识、**不是必需**；下面示例里有的写了、有的没写，两种都能连。连上后工具列表应该有 6 个（`memory_get_rules`、`memory_read`、`memory_write`、`memory_import`、`skill_list`、`skill_promote`）。

示例里的 `<your-server>` 换成你的服务器地址，端口按 compose 里 `ports` 的左边改。

### VS Code：`.vscode/mcp.json`

键是 `servers`，远程条目写 `type: "http"`：

```json
{
  "servers": {
    "team-memory-my-project": {
      "type": "http",
      "url": "http://<your-server>:28080/mcp/my-project",
      "headers": {
        "X-Memory-Client": "vscode"
      }
    },
    "team-memory-my-other-project": {
      "type": "http",
      "url": "http://<your-server>:28080/mcp/my-other-project"
    }
  }
}
```

放进工作区根目录的 `.vscode/mcp.json`（没有就新建），从 MCP 视图启动服务器。确认：服务器显示已连接、工具列表出现上面 6 个工具。

### Trae：`User/mcp.json`

Trae 的键是 `mcpServers`，远程条目只写 `url`（可选 `headers`），**不要写 `type`**——Trae 的 mcp.json schema 里远程条目只有 `url`、`headers`、`oauth`、`fromGalleryId`、`disabled`，多写字段会校验失败。

文件位置：Windows `%APPDATA%\Trae CN\User\mcp.json`（国际版 `%APPDATA%\Trae\User\mcp.json`）。已有内容时只往 `mcpServers` 里加条目，不要覆盖其他服务器。

```json
{
  "mcpServers": {
    "team-memory-xxxx": {
      "url": "http://<your-server>:28080/mcp/xxxx",
      "headers": {
        "X-Memory-Client": "trae"
      }
    },
    "team-memory-yyyy": {
      "url": "http://<your-server>:28080/mcp/yyyy"
    }
  }
}
```

确认：Trae 的 MCP 设置页里该服务器显示已连接，工具列表出现上面 6 个工具。

### Claude Code：项目根目录 `.mcp.json`

键是 `mcpServers`，远程条目写 `type: "http"`：

```json
{
  "mcpServers": {
    "team-memory-xxxx": {
      "type": "http",
      "url": "http://<your-server>:28080/mcp/xxxx",
      "headers": {
        "X-Memory-Client": "claude-code"
      }
    },
    "team-memory-yyyy": {
      "type": "http",
      "url": "http://<your-server>:28080/mcp/yyyy"
    }
  }
}
```

这份内容就是下面命令实际生成的，也可以直接跑命令：

```bash
claude mcp add --scope project --transport http team-memory-my-project \
  http://<your-server>:28080/mcp/my-project --header "X-Memory-Client: claude-code"
```

确认：在同一目录执行 `claude mcp list`，应输出 `team-memory-my-project: http://<your-server>:28080/mcp/my-project (HTTP) - ✓ Connected`。

### 其他支持 Streamable HTTP 的客户端

只需要一个 URL：`http://<服务器IP>:28080/mcp/<project>`；需要标识来源时再加请求头 `X-Memory-Client: <客户端名>`。没有 `type` 字段概念的客户端只填 URL 即可。

### 元数据端点 `/mcp`（只用来核对，不要配成 MCP 服务器）

`GET /mcp` 返回服务名、版本、已有项目和服务端工具列表：

```bash
curl -s http://<your-server>:28080/mcp
# {"endpoint":"/mcp/<project>","projects":["my-project","my-other-project"],"protocol":"2025-06-18",
#  "service":"team-memory","tools":["memory_get_rules","memory_read","memory_write","memory_import","skill_list","skill_promote"],
#  "version":"0.1.1",...}
```

它不是 MCP 端点：`POST /mcp` 会返回 `400 {"error":"PROJECT_REQUIRED"}`。客户端一律指向 `/mcp/<project>`。

### 多项目

一个项目一个条目，命名建议 `team-memory-<project>`；换项目只改 URL 最后一段，其他字段不用动。上面的示例都配了两个项目。

## 开发

```powershell
cd vscode-extension
npm install
npm run compile        # TypeScript → out/
npx vsce package --no-dependencies --allow-missing-repository
```

调试：用 VS Code 打开 `vscode-extension` 目录，按 `F5` 启动扩展开发宿主。

## 服务端要求

- 服务版本 `0.1.1` 起可用（`0.1.1` 同时修了容器挂载目录权限与分页读取两个问题）
- 如果服务端还是旧的 `0.1.0` 镜像，超过 32KB 的中文文档可能因分页切断 UTF-8 而读不全（出现 `�`）；容器还可能以 `mkdir /data/projects: permission denied` 反复重启。请升级到 `0.1.1`
