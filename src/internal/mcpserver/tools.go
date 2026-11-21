package mcpserver

// toolDefs 返回所有工具的定义（记忆 4 个 + 技能 2 个）。工具不接受 project 参数：项目由端点路径决定。
// 描述面向模型：写清「什么时候调、参数什么语义、返回什么结构、下一步做什么」。
func toolDefs() []any {
	return []any{
		map[string]any{
			"name":  "memory_get_rules",
			"title": "读取项目规则与索引",
			"description": "在任何读取或写入之前先调用。返回服务级规则、项目 RULE.md 索引全文、文件清单、索引体检 warnings、" +
				"当月/上月 journal 索引、距上次记录的天数（last_write / days_since_write / record_reminder），以及写入所需的 session。" +
				"用返回索引里的「适用场景/举例」决定先读哪些最相关的主题文档，覆盖本次要碰的代码和数据即停，不够再继续；不必一次读完整 topics/ 目录。" +
				"服务端会回显 user_name 与 user_name_source（param / header / session）：姓名通常来自客户端配置的 X-Memory-User 请求头，" +
				"通常无需在工具参数里重复传；仅临时改用别的名字时才传 userName。" +
				"返回结构：{project, rule_hash, session, user_name, user_name_source, service_rule, index, files[], journal_index[], warnings[], journal_window, last_write, days_since_write, record_reminder}。",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"userName": map[string]any{"type": "string",
						"description": "可选。仅临时改用别的名字时传；正常由客户端 X-Memory-User 头统一提供，不要每次重复传"},
				},
				"additionalProperties": false,
			},
			"annotations": map[string]any{"readOnlyHint": true, "openWorldHint": false},
		},
		map[string]any{
			"name":  "memory_read",
			"title": "读取记忆文件",
			"description": "读取项目内一个 md 文件的全文：RULE.md、TASKS.md、topics/**/*.md、journal/**/*.md。" +
				"按相关度读索引选中的主题，够用就停；index 里只有场景，结论/原因/坑/代码位置都在主题文档里。" +
				"传 rev 可读该文件被覆盖前的历史快照；文件超过 32KB 时用 next_offset 作为 offset 续读。" +
				"返回结构：{path, rev?, hash, lines, bytes, truncated, next_offset?, content}。" +
				"路径不存在时返回 isError=true、error=NOT_FOUND，并附 available 文件列表。",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":   map[string]any{"type": "string", "description": "项目内相对路径，例如 topics/支付回调.md"},
					"rev":    map[string]any{"type": "string", "description": "历史快照文件名，例如 2026-11-16T10-03-11.000.md；不传读当前版本"},
					"offset": map[string]any{"type": "integer", "minimum": 0, "description": "字节偏移，用于大文件续读，默认 0"},
				},
				"required":             []string{"path"},
				"additionalProperties": false,
			},
			"annotations": map[string]any{"readOnlyHint": true, "openWorldHint": false},
		},
		map[string]any{
			"name":  "memory_write",
			"title": "写入项目记忆",
			"description": "写入项目记忆，必须带 memory_get_rules 返回的 session（未读规则会返回 428 READ_RULES_FIRST）。" +
				"何时写：命中服务级规则第 4 条任一条件（定了方案或取舍、踩坑并解决、接口/表/配置/依赖变更、关键结论、任务状态变化、纠正旧结论）就立即写，不要等被问、不要攒着；每次会话收尾都写 journal，无事写「本次无需记录：原因」。" +
				"mode=create 新建文件（已存在返回 409）；append 追加内容（journal、结论、坑，不改旧内容）；" +
				"replace 整篇覆盖，必须带 expectedHash（memory_read 返回的 hash），覆盖前自动留历史快照，hash 不符返回 409 HASH_MISMATCH；" +
				"reindex 只作用于 RULE.md，为 topics/ 下没登记的主题补默认行（行尾 <!-- auto -->），不删任何已有行。" +
				"新建 topics/ 下的主题必须同时给 index.scene（什么场景必须读，≤120 字）与 index.examples（2~3 个真实提问），" +
				"否则返回 409 INDEX_ROW_REQUIRED；服务端会把索引行按固定格式插入 RULE.md。写 journal/YYYY-MM/ 下的 .md 时，" +
				"服务端会自动改名为 <姓名>-<hhmmss>.md（无名则用会话标签）并向当月 journal 索引追加一行。返回结构：{ok, path, hash, lines, index_updated, warnings?, hint?, next}；" +
				"TASKS.md 是开工前计划 + 文件占用表（六列：任务/状态/下一步/涉及文件/推进者/更新时间）：规划完成后先写计划再改代码，" +
				"改文件前先读 TASKS.md 看有没有别的会话登记同一文件，做完立刻删行；服务端只在 warnings 提示冲突、不阻断，" +
				"这是自报约定而非锁，兜底是按文件串行 + expectedHash。" +
				"userName 可选：调用方自报的开发者姓名，用于 TASKS.md 的「推进者」与 journal 文件名/索引「会话标签」。" +
				"正常由客户端配置的 X-Memory-User 请求头统一提供，通常不需要每次传；仅本次要临时改用别的名字时才传。" +
				"优先级：参数 > 请求头 > 会话标签。它不是身份认证，不能用于追责；不知道不要编。参数非法返回 400，请求头非法会被忽略并回落。" +
				"next 是下一步该做什么的提示（例如「索引行已插入，收尾再写 journal」），按它继续，不要写完就走。",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{"type": "string", "description": "项目内相对路径：RULE.md、TASKS.md、topics/**.md、journal/YYYY-MM/<任意名>.md（服务端会改名为 <姓名>-<hhmmss>.md）"},
					"content": map[string]any{"type": "string",
						"description": "写入内容（md）。create/replace 是完整正文，append 是要追加的片段；reindex 时忽略"},
					"mode": map[string]any{"type": "string", "enum": []string{"create", "append", "replace", "reindex"},
						"description": "写入模式：create 新建 / append 追加 / replace 覆盖（需 expectedHash）/ reindex 补索引（仅 RULE.md）"},
					"expectedHash": map[string]any{"type": "string",
						"description": "replace 必填：memory_read 返回的 hash，用于并发冲突检测"},
					"session": map[string]any{"type": "string",
						"description": "memory_get_rules 返回的 session；规则变更后旧 session 失效，需重新获取"},
					"userName": map[string]any{"type": "string",
						"description": "可选。仅临时改用别的名字时传；正常由客户端 X-Memory-User 头统一提供。≤32 字，不能含竖线/换行；优先级：参数 > 请求头 > 会话标签。不是身份认证，不用于追责"},
					"index": map[string]any{
						"type":        "object",
						"description": "新建主题时必填：索引行的「适用场景」与「举例」",
						"properties": map[string]any{
							"scene":    map[string]any{"type": "string", "description": "什么情况下必须读这篇（触发条件），≤120 字"},
							"examples": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "2~3 个研发真实会问的中文问题"},
						},
						"required":             []string{"scene", "examples"},
						"additionalProperties": false,
					},
				},
				"required":             []string{"path", "mode", "session"},
				"additionalProperties": false,
			},
			"annotations": map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false, "openWorldHint": false},
		},
		map[string]any{
			"name":  "memory_import",
			"title": "批量导入已有知识",
			"description": "把一个已有项目的历史知识批量导入团队记忆，用于首次接入或迁移；日常零散结论仍用 memory_write。" +
				"用法：先用你自己的文件读取工具读源文档，再分批调用本工具（每批 ≤10 篇或 ≤200KB，避免参数过大），" +
				"files 里放原文，path 写成 topics/<文件名>.md；源项目若已有索引表（如「文档|适用场景|举例」三列表），" +
				"把每行的场景与举例放进 index，质量远好于服务端自动生成。" +
				"先带 dry_run=true 预演，看 created / index_added / warnings，再正式导入。" +
				"mode=merge（默认）跳过已存在文件、可反复执行；replace 覆盖并留 .history 快照。" +
				"rule_md 传整篇 RULE.md，只在 replace 或当前索引为空时写入，不会覆盖后来人工补的索引。" +
				"省略 index 时按 reindex 口径给未登记主题补 <!-- auto --> 行，之后应改成真实触发条件。" +
				"必须带 memory_get_rules 返回的 session（未读规则返回 428 READ_RULES_FIRST）。" +
				"返回结构：{ok, project, dry_run, created, replaced, skipped, rule_written, index_added, files[], warnings?, session?, hint}；" +
				"session 仅在 rule_md 被写入时返回，之后继续写用它，否则沿用原 session。",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session": map[string]any{"type": "string",
						"description": "memory_get_rules 返回的 session"},
					"mode": map[string]any{"type": "string", "enum": []string{"merge", "replace"},
						"description": "merge（默认，跳过已存在文件）| replace（覆盖并留历史快照）"},
					"dry_run": map[string]any{"type": "boolean",
						"description": "true 只校验与试算，不落盘；正式导入前先跑一次"},
					"rule_md": map[string]any{"type": "string",
						"description": "整篇 RULE.md（含 ## 文档索引 表格）；merge 下仅在索引为空时生效"},
					"files": map[string]any{
						"type":        "array",
						"description": "要导入的文件，path 用 topics/<文件名>.md",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"path":    map[string]any{"type": "string", "description": "项目内相对路径，如 topics/01-后端架构.md"},
								"content": map[string]any{"type": "string", "description": "该文件完整原文"},
							},
							"required":             []string{"path", "content"},
							"additionalProperties": false,
						},
					},
					"index": map[string]any{
						"type":        "array",
						"description": "索引行，优先从源项目已有索引表抄；省略则自动生成",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"path":     map[string]any{"type": "string", "description": "对应 files 里的 path"},
								"title":    map[string]any{"type": "string", "description": "索引里的文档名，省略取文件名"},
								"scene":    map[string]any{"type": "string", "description": "什么场景必须读，≤120 字"},
								"examples": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "2~3 个真实提问"},
							},
							"required":             []string{"path", "scene"},
							"additionalProperties": false,
						},
					},
				},
				"required":             []string{"session"},
				"additionalProperties": false,
			},
			"annotations": map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false},
		},
		map[string]any{
			"name":  "skill_list",
			"title": "列出技能（全局 + 项目）",
			"description": "列出全局技能与当前项目技能清单，只读。何时调：用户问「有哪些技能 / 有没有 xx 技能」时；" +
				"要把某技能提升为全局技能前先确认它存在；memory_get_rules 的 global_skills 已给出全局清单，" +
				"需要项目技能清单或技能描述细节时用本工具。自然语言触发：「把 xx 技能提升为全局技能」时，" +
				"先用本工具确认项目里有 xx，再调 skill_promote(action=submit)。" +
				"返回结构：{global:[{scope,name,description,status,source_project,path}], project:[...], hint}；" +
				"每项 path 可直接传给 memory_read 读全文。status：全局为 pending/approved，项目为 local。",
			"inputSchema": map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"additionalProperties": false,
			},
			"annotations": map[string]any{"readOnlyHint": true, "openWorldHint": false},
		},
		map[string]any{
			"name":  "skill_promote",
			"title": "提审 / 审批全局技能",
			"description": "把项目技能提升为全局技能，两阶段：submit（生成全局 pending 副本）→ approve（转 approved，其他项目才可见）；" +
				"reject/cancel 删除 pending。自然语言触发：「把 xx 技能提升为全局技能 / 提审 xx」→ action=submit；" +
				"「通过 xx 技能审批」→ approve；「拒绝 / 撤销 xx 的提审」→ reject / cancel。" +
				"submit 前先用 memory_write 写好 skills/<name>/SKILL.md（frontmatter 含 name 与 description），" +
				"服务端会把来源项目、status=pending、理由写进全局副本；submit 可带 reason 说明提审理由。" +
				"approve 后该技能出现在所有项目的 memory_get_rules 全局技能清单里。必须带 memory_get_rules 返回的 session；" +
				"无鉴权，任意调用方都可 approve/reject。返回结构：{ok, action, name, status, path?, hint?, next?}。",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{"type": "string", "enum": []string{"submit", "approve", "reject", "cancel"},
						"description": "submit 提审 / approve 通过 / reject 拒绝 / cancel 撤销"},
					"name":    map[string]any{"type": "string", "description": "技能名（小写字母、数字、中划线），如 code-review"},
					"reason":  map[string]any{"type": "string", "description": "submit 时说明为什么提审为全局技能，可选"},
					"session": map[string]any{"type": "string", "description": "memory_get_rules 返回的 session"},
				},
				"required":             []string{"action", "name", "session"},
				"additionalProperties": false,
			},
			"annotations": map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false, "openWorldHint": false},
		},
	}
}
