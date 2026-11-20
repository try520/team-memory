package kb

import "fmt"

// serviceRules 是服务级规则，首次启动写入 DATA_DIR/_service/RULES.md。
// 这里是「什么时候必须写」的唯一权威清单；工具描述与项目 RULE.md 只做指向与补充。
const serviceRules = `# 团队研发记忆服务 · 服务级规则

1. 动手前先调 memory_get_rules，用索引的「适用场景/举例」挑 1~2 篇 memory_read，不要全读 topics/。
2. 只写结论：代码位置、表名、取舍及原因、坑与规避；不写过程流水、不贴大段代码、不写密钥与客户真实数据。
3. 写前先读：改已有主题先 memory_read（replace 必须带返回的 hash）；新建主题必须带 index{scene, examples}，
   服务端会按固定格式插入 RULE.md 索引表。

4. 必须记录——满足任意一条就立刻 memory_write，不要等被问、不要攒到「下次」：
   - 定了方案或做了取舍：选了什么、否掉了什么、为什么（含约束条件）。
   - 踩坑并解决：现象、根因、规避办法，至少能回答「下次怎么避免」。
   - 接口、表结构、配置项、依赖版本发生变更：改了什么、新旧值、影响范围。
   - 查到影响实现的关键结论：外部系统的行为怪癖、限制、版本约束、责任人/入口。
   - 任务状态变化：开始做、完成、阻塞（写明卡在哪）、交接（交接给谁、下一步）。
   - 有人纠正了记忆里的旧结论：新结论写在前，旧的保留并标「（已废弃 YYYY-MM-DD，原因：…）」。
   - 调试超过 30 分钟才定位到原因的问题：无论最终是否改代码，都要把定位过程写成坑。

5. 会话收尾必写 journal：journal/YYYY-MM/<label>-<hhmm>.md，≤300 字，写「做了什么、结论、未完成」
   （hhmm 用当前时间估一个即可，不要为取名跑命令）。
   本次确实没有任何可记内容，也要写一行「本次无需记录：<原因>」——沉默无法区分「判断过」和「忘了」。

6. 索引是地图：新建主题必须给 scene（什么场景必须读，≤120 字）与 examples（2~3 个真实提问）；
   主题的适用场景变了，用 replace 同步 RULE.md 对应行；不要删除别人的行，缺行用 reindex 补。

7. 技能是可复用的操作手册，放在 skills/<name>/SKILL.md（顶部 YAML frontmatter 必须有 name 与 description，
   description 写清「什么时候该用这个技能」）。三种自然语言请求的固定处理方式：
   - 「根据之前的操作生成 xx 技能」：用 memory_write(mode=create) 写 skills/<name>/SKILL.md，按下面的技能骨架写。
   - 「修改 / 完善 xx 技能」：先 memory_read 读全文，再 memory_write 的 append（追加）或 replace（带 hash 覆盖）。
   - 「把 xx 技能提升为全局技能」：先 skill_list 确认项目里有该技能，再 skill_promote(action=submit) 提审（可带 reason）；
     维护者用 skill_promote(action=approve) 通过后，它才出现在各项目 get_rules 的 global_skills 清单里，
     其他项目用 memory_read(path=@global/skills/<name>/SKILL.md) 读取全文。
   - 「拒绝 / 撤销 xx 的提审」：skill_promote(action=reject / cancel)。
   全局技能只读：不能 memory_write 到 @global/ 路径；改全局技能要先在项目里改技能再重新提审。
   skill_list 列出全局 + 项目技能，拿不准有没有某技能时先查它。

## 技能文档骨架（新建 skills/<name>/SKILL.md 照此写）

---
name: <小写字母数字中划线，与目录名一致>
description: <什么时候必须启用这个技能，触发条件 + 场景，≤200 字>
---

# <技能标题>
> <一句话：这个技能帮模型完成什么>

## 使用时机
- <什么情况下加载它；反面：什么情况不需要>

## 步骤
1. <可执行的操作，带具体命令/路径/参数>

## 坑
1. <现象 → 根因 → 规避>（YYYY-MM）

## 相关
- [<相关主题或技能>](<路径>)

## 主题文档骨架（新建 topics/<主题>.md 照此写，五段不要省）

# <主题>
> <一句话定位：这个主题覆盖什么，≤40 字>

## 结论（现在就该这么做）
- <可执行的规则，带具体值/表名/参数，不写「注意」「尽量」>

## 为什么
- <原因与被否掉的方案；让人知道为什么不能改回去>

## 坑
1. <现象 → 根因 → 规避>（YYYY-MM）

## 代码位置
- <可直接打开的相对路径>；表：<表名>

## 相关
- [<相关主题>](<相关主题>.md)
`

func projectRuleTemplate(slug string) string {
	return fmt.Sprintf(`# %s 知识索引

本索引是项目记忆的唯一入口：按「适用场景」挑 1~2 篇读，不要全读。代码位置写在主题文档里，字段请直接查库。
写到什么程度、什么时候必须写，以服务级规则第 4 条为准；本文件只负责「哪篇、索引行怎么填」。

## 文档索引

| 文档 | 适用场景 | 举例 |
| --- | --- | --- |

## 使用规则

1. 动手前先读本索引，按场景挑 1~2 篇；不要全读文档目录。
2. 需要源码时按主题文档「代码位置」里的路径直接读仓库文件，不要在本服务里找代码。
3. 需要字段、索引或表结构时，按文档里写的表名直接查库，不要凭记忆。
4. 命中服务级规则第 4 条的任一条件，立即更新对应主题：追加坑/结论用 append，改已有段落用 replace；
   找不到对应主题才新建，新建必须同时给 index.scene 与 index.examples，例如
   {"scene":"涉及退款、部分退款、退款回调的改动前必读","examples":["部分退款怎么退？","退款回调失败会重试几次？"]}。
5. 新旧结论冲突：新结论写在前，旧结论保留并标「（已废弃 YYYY-MM-DD，原因：…）」。
6. 会话收尾写 journal（journal/YYYY-MM/<label>-<hhmm>.md，≤300 字）；无事可记写「本次无需记录：原因」。
7. 索引里的行与 topics/ 对不上时：缺行用 mode=reindex 补（只补不删），有行无文件先确认再人工删行。
8. 技能是可复用操作手册，放 skills/<name>/SKILL.md；「生成/修改/完善 xx 技能」按服务级规则第 7 条处理，
   「把 xx 技能提升为全局技能」用 skill_promote 提审；全局技能清单见 get_rules 的 global_skills。
`, slug)
}

func tasksTemplate(slug string) string {
	return fmt.Sprintf(`# %s 任务

> 只放进行中与待办；完成后删除该行。一行一个任务。

| 任务 | 状态 | 下一步 | 负责人 |
| --- | --- | --- | --- |
`, slug)
}

func journalIndexHeader(ym string) string {
	return fmt.Sprintf(`# %s 会话索引

| 日期 | 人 | 一句话 | 文件 |
| --- | --- | --- | --- |
`, ym)
}
