# 团队研发长久记忆服务 · 规划（v5 · 索引地图版）

> 目标：**一个进程、一个端口、每个项目一个端点，多个大模型共享同一份项目开发记忆。**
> 数据是纯 md；内网部署；无账号、无令牌、无审计。
> **v5 范式变化**：从「检索驱动」改为「**索引地图 + 模型自己翻**」——`RULE.md` 是一张「文档 | 适用场景 | 举例」索引表加一段使用规则，模型读索引后按场景挑出最相关的主题文档去读（相关就读、够用就停），**不再靠 search 搜关键词**。

## 相比 v4 砍了什么 / 换掉了什么

| 变化 | 说明 | 代价 / 何时改回 |
|---|---|---|
| 砍掉 `memory_search` | 索引表承担「去哪儿找」；journal 另有按月索引文件 | 只能按主题找，不能按报错原文全文找 → 主题数 >30 或需按错误串定位时加回 |
| 固定 4 文件 → 主题拆分 | `DECISIONS.md`/`GOTCHAS.md` 合并进 `topics/<主题>.md`，一篇一个可命名问题域 | 同一主题的决策与坑混在一篇里 → 用「结论/为什么/坑」三段区分 |
| 砍掉 front matter、字段校验 | 结构靠标题与固定小节 | 无法按元数据统计 → 需要时再加 |
| 工具 4 → **3** | `get_rules` / `read` / `write` | 少一个工具，写入必须由模型提供索引行 |
| 新增索引维护责任 | 新主题必须带索引行，服务端校验并提供 `reindex` | 模型漏写会被拒；维护成本前移到写入时 |
| 保留 | 单进程、端点即项目、rule 门控、并发不丢、最简版本快照、Docker 部署 | — |

---

## 1. 整体形态

```
研发 IDE ──HTTP──> http://<server>:8080/mcp/<project> ──> 一个 Go 进程（单二进制）
                                                          │
                                     /data/memory/projects/<project>/
                                       RULE.md（索引地图）
                                       TASKS.md
                                       topics/<主题>.md
                                       journal/2026-11/<姓名>-<标题>-<hhmmss>.md
                                       journal/2026-11.INDEX.md（一行一次会话）
```

- 项目由 URL 路径决定，工具不带 `project` 参数，物理上无法跨项目。
- 无数据库、无反代、无构建流水线；实现只用 Go 标准库，`CGO_ENABLED=0` 静态编译出单二进制，MCP 协议层自己封装（见 §7.1）。
- 服务只能跑一个实例（启动写 `.pid`，检测到存活进程直接退出）。

## 2. 文件角色与主题拆分

```
projects/<slug>/
├── RULE.md                    # 索引地图（唯一必读入口，60~80 行）
├── TASKS.md                   # 进度：只放进行中/待办，完成即删（≤100 行）
├── topics/
│   ├── 支付回调.md
│   ├── 数据库与迁移.md
│   └── _index/                # 仅在主题 >25 时出现（见 §3.4）
└── journal/
    ├── 2026-11/zhangsan-101502.md
    └── 2026-11.INDEX.md
```

- 一篇主题文档 = 一个**可命名的问题域**（能用一个中文短语命名、能被连续问 3 个问题）。不按目录/模块机械切分，也不按「决策」「坑」切分。
- 主题文档固定五段：`结论` / `为什么` / `坑` / `代码位置` / `相关`（见 §4）。
- 长度：80–200 行最舒服；>250 行必须拆；<30 行且半年没更新，合并进相邻主题。
- **新建主题的判据**（满足任一）：① 该问题域预计会被问 ≥3 次；② 现有主题已 >250 行且其中一段可独立成篇；③ 跨模块的独立流程（如「对账」「退款」）。
- **合并的判据**：① 两篇互相链接 >5 次而场景重叠；② 一篇长期 <30 行；③ 索引里两行「适用场景」几乎同义。
- 主题文件名用中文（与样板一致），服务端按 UTF-8 处理即可，无需 URL 友好。

## 3. RULE.md：索引地图规范

### 3.1 结构

```md
# <项目> 知识索引

<一句话：本索引的作用 + 怎么用>

## 文档索引
| 文档 | 适用场景 | 举例 |
|---|---|---|
| [支付回调](topics/支付回调.md) | … | "…" |

## 使用规则
1. 编码前必读：本索引 + 服务级规则 …
```

- 列定义与样板一致：**文档**（相对链接）、**适用场景**（什么情况下必须读，≤120 字）、**举例**（2~3 个真实中文提问，用引号括起）。
- 场景写「改动前必读」的触发条件（涉及 X、要改 Y），不写文档目录；举例写研发真实会说的话，不写「本文介绍…」。
- 行序：通用/高频在前，专用在后；同时按「问题域」自然聚拢，方便人一眼扫。

### 3.2 使用规则（固定在索引下方，7 条）

1. 动手前先读本索引，按「适用场景」选与本次业务最相关的主题读，覆盖本次要碰的代码和数据即停、不够再继续；不必一次把 topics/ 目录读完。
2. 需要源码时，按对应文档「代码位置」里的路径直接去读仓库文件，不要在本服务里找代码。
3. 需要字段/索引/表结构时，按文档里写的表名直接查库（PostgreSQL MCP 等），不要凭记忆。
4. 命中服务级规则第 4 条的任一条件，立即更新对应主题：追加坑/结论用 `append`，改已有段落用 `replace`；
   找不到对应主题才新建，新建必须同时给 `index.scene` 与 `index.examples`，例如
   `{"scene":"涉及退款、部分退款、退款回调的改动前必读","examples":["部分退款怎么退？","退款回调失败会重试几次？"]}`。
5. 新结论与旧结论冲突时：新结论写在前，旧结论保留并标 `（已废弃 2026-11-16，原因：…）`，不要整段删除。
6. 会话收尾写 journal（`journal/YYYY-MM/` 下任意 `.md`，服务端改名为 `<姓名>-<标题>-<hhmmss>.md`，≤300 字）；本次确实无事可记就写
   「本次无需记录：<原因>」——沉默无法区分「判断过」和「忘了」。
7. 索引里的行与 `topics/` 对不上时：缺行用 `mode=reindex` 补（只补不删）；有行无文件先确认，再人工删行。

> 分工：**「什么时候必须写」的唯一权威清单在服务级 `RULES.md` 第 4 条**；本文件只回答「这个项目该写哪篇、索引行怎么填、journal 放哪」，不重复清单。

### 3.3 完整样例（7 行，可直接改成自己的项目）

```md
# my-project 知识索引

本索引是项目记忆的唯一入口：按「适用场景」选与本次业务最相关的主题读，覆盖本次要碰的代码和数据即停，不够再继续；不必一次全读。代码位置在文档里，字段请直接查库。

## 文档索引

| 文档 | 适用场景 | 举例 |
|---|---|---|
| [支付回调](topics/支付回调.md) | 涉及渠道回调、通知重试、重复扣款、幂等键的改动前必读 | "回调为什么会重复扣款？幂等键怎么定？渠道回调乱序怎么办？" |
| [对账与结算](topics/对账与结算.md) | 涉及日终对账、差错处理、结算单生成、金额口径的改动前必读 | "对账跑完差 3 笔怎么办？结算金额和订单金额对不上从哪查？" |
| [退款](topics/退款.md) | 涉及退款申请、部分退款、退款回调、退款失败重试 | "部分退款怎么退？退款回调失败会重试几次？" |
| [渠道接入](topics/渠道接入.md) | 新增/修改支付渠道、签名验签、证书轮换、渠道参数配置 | "新接一个渠道要改哪些地方？证书快过期了怎么换？" |
| [数据库与迁移](topics/数据库与迁移.md) | 改表结构、加索引、写迁移脚本、排查慢查询 | "payment_order 表能加字段吗？迁移脚本放哪、怎么回滚？" |
| [本地开发与联调](topics/本地开发与联调.md) | 本地起服务、联调回调、造测试数据、mock 渠道 | "本地怎么收到回调？怎么造一笔成功的支付？" |
| [线上故障排查](topics/线上故障排查.md) | 线上告警、超时、5xx、渠道侧报错、资金类异常 | "昨晚回调大面积超时是什么原因？渠道返回 502 怎么定位？" |

## 使用规则
1. 动手前先读本索引，按「适用场景」选与本次业务最相关的主题读，够用就停、不够再继续；不要一次读完整文档目录。
2. 需要源码时按文档「代码位置」的路径直接读仓库文件。
3. 需要字段/表结构时按文档里的表名直接查库，不要凭记忆。
4. 命中服务级规则第 4 条任一条件，立即更新对应主题；新建主题必须同时给 index.scene 与 index.examples。
5. 新旧结论冲突：新结论在前，旧结论标「（已废弃 YYYY-MM-DD，原因：…）」。
6. 收尾写 journal；无事可记写「本次无需记录：原因」。
7. 索引行与文件对不上：缺行用 reindex 补，有行无文件先确认再人工删行。
```

### 3.4 规模控制与分层

- 舒适区：**8–15 个主题**；索引 ≤25 行（约 1.5k tokens），一次读完不心疼。
- 超过 25 行 → 分层：`RULE.md` 的表格改为「领域」行，指向 `topics/_index/<领域>.md`（同格式表格）。模型先读项目索引选领域，再读领域索引选相关主题（相关就读、够用就停）。
- 触发与执行：`get_rules` 返回 `warnings: [{type:"index_too_large", rows:28}]`，并提示「按领域拆到 topics/_index/」；拆分由人或模型用 `write` 完成，服务端不自动拆。
- 单行上限：场景 ≤120 字、举例 ≤3 句；超长时 `get_rules` 标 `row_too_long`，不阻断。

## 4. 主题文档模板

```md
# 支付回调
> 渠道异步通知的接收、验签、幂等与重试。改动前必读。

## 结论（现在就该这么做）
- 幂等键 = `channel + orderNo`，落 `payment_idempotency` 表（唯一索引）。
- 回调先落库再处理，处理失败进重试队列，最多 8 次，退避 2^n 秒。
- 金额一律整数分，禁止 float。

## 为什么
- 2026-03 渠道 A 回调乱序导致重复扣款，见「坑」第 1 条；因此改为先落库 + 唯一索引兜底。
- 放弃「处理成功再落库」：会丢通知，且无法补偿。

## 坑
1. 渠道 A 可能先发 `paid` 后发 `created`；判断状态必须用 `update_time` 比较，不能信到达顺序。（2026-03）
2. 沙箱环境回调不重试，本地联调要手工触发，见「本地开发与联调」。
3. `orderNo` 全渠道唯一，但个别渠道会复用旧 `orderNo`，幂等键里必须带 `channel`。

## 代码位置
- 回调入口：`src/payment/callback.ts`（路由注册在 `src/routes/payment.ts`）
- 幂等：`src/payment/idempotency.ts`
- 重试队列：`src/queue/callback-retry.ts`
- 表结构：`payment_order` / `payment_idempotency`

## 相关
- [对账与结算](对账与结算.md)（金额口径）
- [渠道接入](渠道接入.md)（验签与证书）
```

- 首行标题 = 索引行里的文档名；`>` 定位句 ≤40 字。
- 「代码位置」必须写**可直接打开的路径**（含表名），这是替代 search 的关键。
- 「坑」每条带日期或版本；「为什么」写被否掉的方案，避免后人重提。

## 5. 大模型怎么用（完整流程）

| 步 | 调用 | 为什么 | 跳过的后果 |
|---|---|---|---|
| 1 | `memory_get_rules()` | 拿索引表 + 服务级规则 + 文件清单 + `session` | 写入 428；不知道有哪些主题，只能瞎读 |
| 2 | 在索引里按场景选最相关的主题 | 索引的「适用场景/举例」就是路由表 | 全读一遍，几万 token 进去还没开始干活 |
| 3 | `memory_read("topics/支付回调.md")` | 结论、原因、坑、代码位置都在这一篇 | 只看索引行会误判（索引只有场景，没有结论） |
| 4 | 按「代码位置」直接读源码；按表名直接查库 | 记忆只存指针，不存代码与字段 | 凭记忆猜路径/字段，改错地方 |
| 5 | `memory_write(...)` 更新主题 / 写 journal | 不写回，下一个模型重新踩坑 | 服务退化成一个只读 wiki |

- 索引行的「举例」是给模型判断相似度的，不是给人看的装饰：需求里出现「重复扣款」→ 直接命中「支付回调」。
- 不设固定篇数：相关就读、够了就停。若读到与本次任务无关的主题占比明显偏高，说明索引的「适用场景」切分或描述需要调整，应在写回时调整索引行或拆主题。
- 需要历史（这结论什么时候变的）：`memory_read({path:"topics/支付回调.md", rev:"<ISO>.md"})` 读旧快照，按需即可。

### 5.1 为什么可以不 search

- 「去哪儿找」由索引回答；「具体内容」由 read 回答；「历史」由 journal 月度索引回答（一行一次会话：日期 | 人 | 一句话 | 文件）。
- 模型翻的是**人写的路由表**，比关键词命中更准：报错原文可能不在记忆里，但「这是回调问题」一定在索引里。
- 索引规模 25 行以内时，读完整索引的成本低于做 3 次关键词搜索。

### 5.2 什么情况下仍然需要 search（v5 不加，记下触发条件）

1. 主题数 >30、分层后仍定位不到；
2. 需要在历史 journal 里按**报错原文/日志片段**定位（月度索引只有一句话摘要）；
3. 索引腐烂（很多 `missing_in_index`）导致模型反复读错文档。
出现任一情况再加 `memory_search`（实现不变，只是多一个工具），不要提前加。

### 5.3 什么时候必须写（硬条件清单）

「重要时记录」不是条件，模型无法判断。以下任一条成立就必须立刻 `memory_write`，不等被问、不攒到下次：

| # | 触发条件 | 判断口径（满足即成立） | 写到哪里 |
|---|---|---|---|
| 1 | 定了方案或做了取舍 | 在 ≥2 个可行做法里选了一个，或明确否掉了某个方案，且原因不是「随手」 | 主题「结论」+「为什么」 |
| 2 | 踩坑并解决 | 出现过与预期不符的现象，且已定位根因或给出规避办法 | 主题「坑」 |
| 3 | 接口、表结构、配置项、依赖版本变更 | 字段/签名/默认值/版本号变了，旧调用方或旧部署会受影响 | 主题「结论」+「代码位置」 |
| 4 | 查到影响实现的关键结论 | 外部系统的行为怪癖、限制、版本约束、责任人/入口 | 主题「坑」或「结论」 |
| 5 | 任务状态变化 | 开工、完成、阻塞（卡在哪）、交接（给谁、下一步） | `TASKS.md` |
| 6 | 有人纠正了旧结论 | 记忆里的说法被证明过期或错误 | 主题：新结论在前，旧结论标废弃 |
| 7 | 调试超 30 分钟才定位 | 无论最终是否改代码，定位过程本身可复用 | 主题「坑」 |
| 8 | 会话结束 | 每次会话都要留痕 | `journal/`；无事写「本次无需记录：原因」 |

**三处文案分工（避免重复）**

- **服务级 `RULES.md`**（`get_rules.service_rule`）：唯一权威清单。硬条件完整措辞 + 主题五段骨架都放这里，模型一次 get_rules 就能看到全部「怎么写、何时写」。
- **工具 description**：只讲调用契约——什么时候调、参数怎么填、返回什么。`memory_write` 用一句话指向「服务级规则第 4 条」，并说明返回的 `next` 是下一步，不复制清单。
- **项目 `RULE.md` 使用规则**：只回答「这个项目该写哪篇、索引行怎么填、journal 放哪」；触发条件用一句「以服务级规则第 4 条为准」引用。

**轻量提醒（无身份、无门控）**

- `get_rules` 返回 `last_write`（RFC3339）、`days_since_write`（新项目为 -1）、`record_reminder`（新项目提示「还没有任何结论记录」；≥7 天提示「距上次记录 N 天」）。只统计 `topics/` 与 `journal/`，模板文件不算。
- `write` 成功返回 `next`：新建主题 → 「索引行已插入 RULE.md；收尾再写 journal」；journal → 「确认结论已写进主题文档」；`TASKS.md`/`RULE.md`/主题各有对应提示。
- 已知边界：这些都是**提示**，不是强制。唯一的硬门控仍然只有「未 get_rules 不能写」（428）。模型愿不愿意写，最终取决于它是否遵守 instructions。

### 5.4 实测：模型到底会不会主动写

真实客户端（Qwen Code CLI，MCP Streamable HTTP，`--allowed-mcp-server-names memory`），同一个无记忆提示的任务：
「修一下 calc.go 里的 Average：空切片会返回 NaN，改成返回 0，跑测试」。每个模型重开一个项目：

| 模型 | 客户端下发 `instructions` | 先 `get_rules` | 主动 `write` | 任务完成 |
|---|---|---|---|---|
| deepseek-v4.1-flash | 否 | 否 | 否（明确回复「本次无值得沉淀的决策」） | 是 |
| deepseek-v4-pro | 否 | 否 | 否 | 是 |
| glm-5.3-flash | 否 | 否 | 否 | 是 |
| deepseek-v4.1-flash，instructions 用 `--append-system-prompt` 注入 | 是（模拟） | 否 | 否 | 是 |

- **协议层**：抓包确认客户端的 `/chat/completions` 请求体里没有服务端 `instructions` 的任何一个字。规范允许 server 返回 instructions，但不保证客户端注入。
- **行为层**：即使把同一段 instructions 放进 system prompt，模型仍然不主动 `get_rules`、收尾不写 journal。硬门控只能保证「不读就写不进去」，不能保证「会去读」。
- **结构层**：用显式指令要求「先 `get_rules` 再按规则记录」时，模型能写对：五段齐全、`index.scene/examples` 合格、journal 有「做了什么/结论/未完成」。中途出现过一次 `replace` 用一段「坑」覆盖全文，模型读回后自行恢复——说明 `replace` 仍是最危险的模式。
- **结论**：服务端文案解决「读到之后写得对」，解决不了「会不会来读、会不会来写」。后者只能靠客户端每次都注入的项目说明（`CLAUDE.md` / `AGENTS.md` / `QWEN.md`），本机四轮无例外。可直接粘贴的完整版（含任务结束强制检查与收尾格式）见同目录 [client-rule-prompt.md](client-rule-prompt.md)。
- **补充验证**：把该文件放进项目根 `AGENTS.md` 后，同一任务出现 `get_rules` → 写主题（五段 + index）→ 写 journal → 收尾行「记忆：已更新 …」，基线 5 轮为 0 次调用。代价是轮次 4→11、耗时 70s→198s。

## 6. 索引维护（本版关键）

### 6.1 索引行由谁写

- **新建主题：写入者（模型）必须提供索引行**，`memory_write` 参数带 `index: {scene, examples:[…]}`；缺失 → `409 INDEX_ROW_REQUIRED`，并回传索引格式样例。
- 服务端负责把行**插入 RULE.md 表格末尾**（格式固定，避免模型把表格写坏），并返回 `index_updated: true`。
- 更新已有主题：不强制改索引；`write` 响应提示 `hint: "若适用场景变化，请用 replace 更新 RULE.md 对应行"`。

### 6.2 默认值（模型漏写场景描述时的兜底）

- 只有在 `reindex` 时才自动生成：场景 = 文档标题 + 首段前 60 字；举例 = `"<标题>里有哪些内容？"`，行尾加 `<!-- auto -->`，提示人/模型替换成真实问法。
- 手工/常规写入不猜，缺失即拒绝——错的行比没有行危害更大。

### 6.3 一致性校验与修复

- `get_rules` 每次现算，返回 `warnings`：
  - `missing_in_index`：`topics/` 里有文件但表格没行 → 模型找不到它；
  - `missing_file`：表格有行但文件不存在（改名/删除后没同步）；
  - `index_too_large` / `row_too_long`：规模问题。
- 修复 A（推荐，人工确定性）：`memory_write({path:"RULE.md", mode:"replace", expectedHash, content})` 手工改表。
- 修复 B（批量兜底）：`memory_write({path:"RULE.md", mode:"reindex"})` —— 扫描 `topics/*.md`，只为 `missing_in_index` 的文件追加默认行、保留已有行与顺序，并返回 `{added:[…], kept:n}`；`missing_file` 的行**不自动删**，只报告（防止误删真实存在但临时改名的文档）。
- 每次 `write` 到 `topics/` 之后立刻校验一次索引，把 `missing_in_index` 写进响应，避免问题拖到下一次会话。

### 6.4 journal 索引（服务端自动维护，零模型负担）

- 写 `journal/2026-11/` 下任意 `.md` 时，服务端改名为 `<姓名>-<标题>-<hhmmss>.md` 并向 `journal/2026-11.INDEX.md` 自动追加一行：
  `| 2026-11-16 | zhangsan | 回调重复扣款已定位到幂等键缺失 | zhangsan-101502.md |`
- 摘要取 journal 文件第一个 `#`/`##` 标题或首行前 40 字；`get_rules` 返回 `journal_index` 指向当月与上月两个文件。
- 作用：替代「按关键词搜历史」，模型先看月度索引的一句话，再决定读哪篇 journal。

## 7. 三个工具

| 工具 | 参数 | 返回 |
|---|---|---|
| `memory_get_rules` | `{}` | 索引全文 + 服务级规则 + 文件清单 + `warnings` + `session` + 记录活跃度提醒 |
| `memory_read` | `{path, rev?, offset?}` | 文件内容（>32KB 截断 + `offset` 续读） |
| `memory_write` | `{path, content, mode, expectedHash?, session, index?}` | `{ok, path, hash, lines, index_updated, warnings?, hint?, next}` |

```json
// get_rules 响应（index 直接给 md 文本，模型原样读表格）
{"project":"my-project","rule_hash":"9c1e…","session":"s_7f3a2c1d",
 "service_rule":"# 服务级规则\n1. 先读索引…\n4. 必须记录——满足任意一条就立刻 memory_write…",
 "index":"# my-project 知识索引\n\n| 文档 | 适用场景 | 举例 |\n|---|---|---|\n| [支付回调](topics/支付回调.md) | … | … |",
 "files":[{"path":"topics/支付回调.md","lines":142,"updated":"2026-11-15T18:02:00+08:00"},
          {"path":"journal/2026-11/zhangsan-101502.md","lines":18,"updated":"2026-11-16T10:15:00+08:00"}],
 "journal_index":["journal/2026-11.INDEX.md","journal/2026-10.INDEX.md"],
 "warnings":[{"type":"missing_in_index","path":"topics/退款.md"}],
 "journal_window":{"from":"2026-10-17","to":"2026-11-16","files":63},
 "last_write":"2026-11-16T10:15:00+08:00","days_since_write":0,"record_reminder":""}

// write 成功响应（next 是下一步提示；新建主题与写 journal 的提示不同）
{"ok":true,"path":"topics/退款.md","hash":"a71c…","lines":64,"index_updated":true,
 "next":"索引行已插入 RULE.md；收尾再写 journal（≤300 字）。"}
```

- `files` 最多 200 条，只给路径/行数/更新时间；journal 只列近 30 天，更早的折叠为计数（仍可 read）。
- `read` 粒度：整文件；`rev` 传 `.history/<path>/<ISO>.md` 读旧版；路径不存在 → 404 + `available:[…]`。
- `write` 的 mode：`create`（已存在→409）/ `append` / `replace`（必带 `expectedHash`）/ `reindex`（仅 RULE.md）。
- 路径白名单：`RULE.md`、`TASKS.md`、`topics/**/*.md`、`journal/**/*.md`；其余拒绝并返回允许清单。
- 写入成功后同步更新索引校验结果，返回 `warnings`（若有）。
- `last_write` / `days_since_write` / `record_reminder`：只统计 `topics/` 与 `journal/`；新项目 `days_since_write = -1`；≥7 天返回提醒文案。
- `next`：`journal` → 确认结论已写进主题；新建主题 → 收尾写 journal；`TASKS.md` → 只留进行中/待办；`RULE.md`/主题 → 场景变了同步索引行；`reindex` 补了行 → 把 `<!-- auto -->` 换成真实内容。
- 删除文件：P0 不做（避免误删记忆）；确需删除就 `rm` 后调 `reindex` 看 `missing_file` 提示，再手工删行。

### 7.1 MCP 协议封装（实现层）

**结论：自己封装协议层，不引官方 Go SDK。** 理由：

1. 服务端只有 5 个方法（`initialize` / `notifications/initialized` / `ping` / `tools/list` / `tools/call`），且一律用 JSON 回包，SDK 的会话管理、SSE 通道、鉴权中间件都用不上；
2. 单二进制、只用标准库，不需要为 MCP 引入第三方依赖与版本升级面；
3. 「端点即项目」「Mcp-Session-Id 绑项目」「rule session 与传输会话分离」是本服务特有的语义，SDK 反而要绕过它的默认模型；
4. 规范符合性用一张 HTTP 行为表 + 表驱动测试自证，比「SDK 应该处理了」更可评审。将来需要服务端推送/进度/鉴权时，可整体换官方 SDK，工具实现不动。

**传输行为（Streamable HTTP，单端点 `POST /mcp/<project>`）**

| 输入 | 响应 |
|---|---|
| `POST` + `Content-Type: application/json` + `Accept` 含 `application/json` | 200，`application/json` 的 JSON-RPC 响应 |
| `POST` 一条通知（无 `id`） | 202，无响应体 |
| `POST` JSON 数组（2025-03-26 批量） | 200，JSON-RPC 响应数组；全是通知则 202 |
| 非 `initialize` 请求缺 `Mcp-Session-Id` | 400 + JSON-RPC `-32600` |
| `Mcp-Session-Id` 未知 / 过期 / 属于别的项目 | 404 + JSON-RPC `-32001`，客户端应重新 `initialize` |
| `Content-Type` 不是 `application/json` | 415 |
| `Accept` 只有 `text/event-stream` | 406（本服务只产出 JSON） |
| 空 body / JSON 解析失败 | 400 + JSON-RPC `-32700` |
| body > 4MB | 413 |
| 不支持的 `MCP-Protocol-Version` 头 | 400 |
| `GET`（要求 SSE 推送流） | 405（规范允许：服务端无主动推送） |
| `DELETE` + 有效 session | 204，会话作废；未知 session → 404 |

- `initialize` 回包带 `Mcp-Session-Id`（`crypto/rand` 32 位 hex）；会话滑动过期 24h，**按项目隔离**，跨项目复用一律 404。
- 协议版本：客户端给的版本在 `{2025-06-18, 2025-03-26, 2024-11-05}` 内就原样回；否则回服务端默认 `2025-06-18`。
- 不做 SSE：`tools/call` 都是毫秒级读写，没有进度/采样/服务端通知需求，JSON 回包对客户端更稳；GET 明确回 405。

**两套 session 不要混**

| | 载体 | 作用 | 失效 |
|---|---|---|---|
| 传输会话 | `Mcp-Session-Id` 头 | 证明这条连接 `initialize` 过 | 24h 空闲、`DELETE`、进程重启 |
| 规则会话 | `get_rules` 返回的 `session` 字段 | 证明「规则确实进了模型上下文」，写入门控 | 12h、`RULE.md` 变化 |

**错误分层：协议错误走 JSON-RPC error，业务错误走 `result.isError`**

| 层 | 例子 | 形态 |
|---|---|---|
| 协议/传输 | 方法不存在 `-32601`、参数结构非法 `-32602`、未初始化 `-32600`、会话失效 `-32001` | HTTP 4xx 或 200 + `error{code,message,data}`，无 `result` |
| 工具执行 | `428 READ_RULES_FIRST`、`409 INDEX_ROW_REQUIRED`、`409 HASH_MISMATCH`、`404 NOT_FOUND`、`400 PATH_NOT_ALLOWED` | HTTP 200 + `result{isError:true, content[0].text=…}`；模型可读错误码自行纠正 |

**工具定义面向模型**：每个工具都有 `title`、说明「何时必须调 / 参数语义 / 返回结构 / 下一步」的 `description`、`inputSchema` 与
`annotations`（`readOnlyHint` 等）；`initialize.instructions` 与 `memory_get_rules` 的描述都写明「写入前必须先 get_rules」。
`tools/call` 的 `structuredContent` 与 `content[0].text` 给同一份 JSON，兼容只读其中一种的客户端。

**连通性冒烟（真实 MCP 客户端）**

```bash
go run ./cmd/smoke -url http://127.0.0.1:8080 -project demo -n 100
```

按真实客户端顺序走：`initialize`（校验 `Mcp-Session-Id`）→ `notifications/initialized` → `tools/list=3` →
`memory_get_rules` → 缺索引新建 10/10 被拒 → 建主题（带索引行）→ `memory_read` 读回 → 100 并发 `append` 无丢失无重复 →
`reindex` 幂等。协议层另有表驱动测试覆盖上表每一行（`internal/mcpserver/http_test.go`）。

## 8. 「先读 rule」的最轻实现

只做一件事：**写入必须带 `get_rules` 返回的 `session`，且该 session 记录的 `rule_hash` 等于当前 `RULE.md` 的 hash。**

```js
sessions.set(sessionId, { project, ruleHash, at: Date.now() })  // 内存表，12h 惰性过期

if (!sessions.has(session)) return 428 { error:"READ_RULES_FIRST",
  hint:"先调 memory_get_rules()，把返回的 session 传给 write。", preview: 服务级 4 条 }
if (sessions.get(session).ruleHash !== currentRuleHash()) return 428 {
  error:"RULE_CHANGED", hint:"索引已更新，请重新 memory_get_rules()。" }
```

`rule_hash = sha256(服务级 RULES.md + 项目 RULE.md)`，每次 get_rules 现算；重启丢 session 属正常（模型重读一遍即可）。不防伪造，也不打算防——它保证的是「规则确实进了上下文」。

## 9. 并发不丢内容 + 最简版本快照

- **单实例**：`/data/memory/.pid`，检测到存活进程则拒绝启动（最重要的一条）。
- **按文件串行**：进程内 `Map<path, Promise>` 互斥；append 是单次 `fs.appendFile`，replace 是「快照 → tmp → fsync → rename」。
- **原子替换**：任何时刻读到的都是完整文件；不同文件完全并行。
- 同文件并发 replace 且未带 `expectedHash` → 409 要求重读；带了的按后写者胜（RULE 要求 replace 必带）。
- **快照**：replace 前把旧内容拷到 `.history/<path>/<ISO>.md`，每文件保留最近 50 份；`read(rev)` 取回，或人直接 `cp`。不提供 history/restore/diff 工具。

## 10. 接入配置

传输统一是 **Streamable HTTP**，地址固定 `http://<server>:8080/mcp/<project>`，不需要额外 header；
`Mcp-Session-Id` 由客户端按协议自动携带（服务端在 initialize 响应头签发），`X-Memory-Client` 不再需要。

**Claude Code**（项目根 `.mcp.json`）：

```json
{"mcpServers":{"team-memory-my-project":{"type":"http","url":"http://<your-server>:8080/mcp/my-project"}}}
```

也可用 CLI：`claude mcp add --transport http team-memory-my-project http://<your-server>:8080/mcp/my-project`

**Cursor**（`.cursor/mcp.json`）：

```json
{"mcpServers":{"team-memory-my-project":{"url":"http://<your-server>:8080/mcp/my-project"}}}
```

**VS Code / Copilot**（`.vscode/mcp.json`，顶层键是 `servers`）：

```json
{"servers":{"team-memory-my-project":{"type":"http","url":"http://<your-server>:8080/mcp/my-project"}}}
```

只支持 stdio 的客户端：`npx -y mcp-remote http://<your-server>:8080/mcp/my-project` 做 stdio ↔ HTTP 代理。

- 多项目写多个条目；项目由 URL 决定，工具不接受 `project` 参数。
- 首次接入：`get_rules` 自动创建项目目录、`RULE.md`（含空索引）与 `TASKS.md`，零注册。
- 建议首问：「先读索引，然后告诉我这个项目现在有哪些未完成任务」——同时验证索引与工具连通。

## 11. 部署与运行

```dockerfile
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/team-memory ./cmd/memorysvc

FROM alpine:3.20
RUN adduser -D -u 10001 memory && mkdir -p /data && chown memory:memory /data
COPY --from=build /out/team-memory /usr/local/bin/team-memory
USER memory
ENV DATA_DIR=/data PORT=8080
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/team-memory"]
```

```yaml
services:
  memory:
    build: .
    image: team-memory:0.1.1
    ports: ["8080:8080"]
    volumes: ["/srv/team-memory:/data"]
    environment: { DATA_DIR: /data, PORT: 8080 }
    restart: unless-stopped
```

```bash
docker compose up -d --build
curl -s http://localhost:8080/healthz          # {"ok":true,"projects":0,...}
go run ./cmd/smoke -url http://localhost:8080 -project demo   # 真实 MCP 客户端连通性冒烟
# 备份（每天 03:00，保留 7 份）
0 3 * * * cd /srv/team-memory && tar czf /srv/backup/memory-$(date +\%F).tgz . && find /srv/backup -name 'memory-*.tgz' -mtime +7 -delete
```

## 12. P0 任务清单（按顺序）

- [ ] 1. 初始化仓库：Go 1.23 + 标准库（`CGO_ENABLED=0`），`go build ./cmd/memorysvc` 产出单二进制
- [ ] 2. 配置与启动自检：`DATA_DIR`/`PORT`，创建 `_service/RULES.md` 与目录骨架
- [ ] 3. 单实例保护：写 `.pid`，检测存活进程则退出
- [ ] 4. 路径安全：白名单（RULE/TASKS/topics/journal）+ 拒绝 `..`/绝对路径/非 `.md`
- [ ] 5. 项目自举：`ensureProject(slug)` 生成 `RULE.md`（含表头与使用规则）、`TASKS.md`
- [ ] 6. 文件锁 + 原子写（tmp→fsync→rename）+ append
- [ ] 7. 历史快照：replace 前写入 `.history/`，每文件留 50 份
- [ ] 8. `memory_get_rules`：拼服务级 + 索引全文、算 `rule_hash`、签发 session、附 `files`/`warnings`/`journal_index`
- [ ] 9. 索引解析与校验：解析表格行（文档/场景/举例），比对 `topics/*.md`，产出 `missing_in_index` / `missing_file` / `index_too_large` / `row_too_long`
- [ ] 10. `memory_read`：当前文件 + `rev` 读快照 + 32KB 截断与 `offset`
- [ ] 11. `memory_write`：四种 mode + `expectedHash` CAS + session/rule_hash 校验
- [ ] 12. 索引行插入：新主题缺 `index` → 409；有 → 在表格末尾插入固定格式行
- [ ] 13. `mode:"reindex"`：只为缺失文件补默认行（带 `<!-- auto -->`），不删行
- [ ] 14. journal 索引：写 journal 时自动追加一行到 `journal/YYYY-MM.INDEX.md`
- [ ] 15. 错误响应统一 `{error, hint, …}`；409 带 `hash` 与索引行样例，428 带规则预览
- [ ] 16. `/healthz` + `/mcp`（列项目）
- [ ] 17. Dockerfile + compose + 数据卷；重启后数据完整
- [ ] 18. 冒烟脚本：两个客户端并发写同一主题 100 次，校验无丢失无交错；新主题缺索引 100% 被拒；reindex 修复 dangling
- [ ] 19. 一页接入说明：URL 怎么填、首问验证、三个常见错误
- [ ] 20. MCP 协议封装加固：`initialize` / `notifications/initialized` / `ping` / `tools/list` / `tools/call`；
      `Mcp-Session-Id` 签发、复用、24h 过期、`DELETE` 终止、跨项目 404；`415` / `406` / `400` / `404` / `413` 语义；
      协议错误（JSON-RPC `error`）与工具错误（`result.isError`）分层；三客户端接入样例；
      真实 MCP 客户端冒烟 `cmd/smoke`（initialize → get_rules → 写一条 → 读回）

## 13. 验收口径

1. **零门槛**：新机器只拿到 URL，5 分钟内配置成功并写入一条 journal（3 人各一次）。
2. **索引可用**：新项目 `get_rules` 返回可直接读的索引（含表头与使用规则）；`index` 文本里的链接可被 read 打开。
3. **索引强制**：新建主题不带 `index` → 100% 返回 `INDEX_ROW_REQUIRED`；带了的行格式正确且出现在 RULE.md 末尾。
4. **索引自愈**：手工往 `topics/` 放一篇未登记文档 → `get_rules` 报 `missing_in_index`；`reindex` 后该文档有行，已有行与顺序不变。
5. **模型能自己翻**：抽 10 个真实需求（含「回调为什么会重复扣款」「新接渠道改哪」），模型 ≤2 次 read 命中正确主题，人工判分 ≥8/10。
6. **并发**：两客户端并发 append 同一主题 100 次 → 无丢失无交错；并发 replace 未带 hash → 409；带了 hash 的后写者胜。
7. **门控**：未带 session 的写 100% 428；改 `RULE.md` 后旧 session 立即失效。
8. **版本**：写坏的 `RULE.md`/主题可用 `.history/` 取回并写回，内容字节一致。
9. **规模**：索引到 26 行时 `get_rules` 返回 `index_too_large`；按提示分层后每层 ≤25 行且模型仍能在 2 次 read 内命中。
10. **部署**：`docker compose down && up` 后数据与索引完整；`healthz` 正常。
11. **协议**：真实 MCP 客户端（`cmd/smoke`）走完 initialize → notifications/initialized → tools/list → get_rules → 写 → 读回；
    非 initialize 缺 `Mcp-Session-Id` → 400，未知 session → 404，`DELETE` 后同 session → 404，`GET` → 405，错误 Content-Type → 415。

## 14. 局限与未来

- **无全文检索**：只能按主题找；报错原文、日志片段、很久以前的临时讨论找不到 → 触发条件见 §5.2，届时加回 `memory_search`。
- **索引质量决定一切**：场景写得含糊，模型就会读错文档；这也是为什么新主题必须由写入者提供索引行，而不是自动生成。
- **journal 只做流水**：结构化结论必须写进主题文档，否则下个模型只能靠翻月度索引猜。
- **规模上限**：单项目 ≤300 个 md、单主题 ≤250 行；超过后靠分层索引 + 按日期折叠 journal。
- **将来加语义检索的切入点**：只改工具层——`get_rules` 之后增加「按问题推荐相关主题」的可选调用，或给 `memory_read` 加 `related` 提示；文件格式、索引表、部署都不用动。
