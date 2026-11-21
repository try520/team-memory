package kb

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	tasksHeader = "| 任务 | 状态 | 下一步 | 涉及文件 | 推进者（自报） | 更新时间 |"
	tasksSep    = "| --- | --- | --- | --- | --- | --- |"
)

// 服务端写入的更新时间格式；不匹配的值（模型手填）会被覆盖。
var taskTimeRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$`)

// normalizeTasks 把 TASKS.md 的表格规整为六列：
//   - 旧四列表头（负责人）与 0.1.2 五列表头统一升级为「涉及文件 | 推进者（自报） | 更新时间」；
//   - 数据行缺列时补齐，推进者留空/为 "-" 时用会话标签兜底（不猜人名）；
//   - 更新时间只由服务端写，模型写的非标准值会被当前时间覆盖。
//
// 只处理「任务/状态」表头之后的行，其余内容原样保留。
func normalizeTasks(content, session, userName string, now time.Time) string {
	lines := strings.Split(content, "\n")
	stamp := now.Format("2006-01-02 15:04")
	author := userName
	if author == "" {
		author = session
	}
	seen := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !seen {
			if isTasksHeaderRow(trimmed) {
				lines[i] = tasksHeader
				seen = true
				if i+1 < len(lines) && isSepRow(strings.TrimSpace(lines[i+1])) {
					lines[i+1] = tasksSep
				}
			}
			continue
		}
		// 表头之后到文件末的所有表格行都规整：模板里的说明文字可能夹在表头与追加行之间。
		if trimmed == "" || !strings.HasPrefix(trimmed, "|") {
			continue
		}
		if isTasksHeaderRow(trimmed) {
			lines[i] = tasksHeader
			continue
		}
		if isSepRow(trimmed) {
			if len(splitRow(trimmed)) < 6 {
				lines[i] = tasksSep
			}
			continue
		}
		lines[i] = normalizeTaskRow(trimmed, session, author, stamp)
	}
	return strings.Join(lines, "\n")
}

func isTasksHeaderRow(line string) bool {
	if !strings.HasPrefix(line, "|") {
		return false
	}
	cells := splitRow(line)
	if len(cells) == 0 || strings.TrimSpace(cells[0]) != "任务" {
		return false
	}
	for _, c := range cells[1:] {
		if strings.TrimSpace(c) == "状态" {
			return true
		}
	}
	return false
}

func isSepRow(line string) bool {
	if !strings.HasPrefix(line, "|") {
		return false
	}
	for _, c := range splitRow(line) {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if strings.Trim(c, "-: ") != "" {
			return false
		}
	}
	return true
}

// splitRow 去掉首尾竖线后按 | 切分。
func splitRow(line string) []string {
	parts := strings.Split(strings.Trim(line, "|"), "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// normalizeTaskRow 只处理四/五/六列，其它列数原样返回，避免破坏自由格式。
func normalizeTaskRow(line, session, author, stamp string) string {
	cells := splitRow(line)
	switch len(cells) {
	case 4:
		// 旧四列：任务/状态/下一步/负责人 → 负责人当推进者，补空涉及文件与时间。
		cells = []string{cells[0], cells[1], cells[2], "", cells[3], ""}
	case 5:
		// 0.1.2 五列：任务/状态/下一步/推进者/更新时间 → 插入空的涉及文件列。
		cells = []string{cells[0], cells[1], cells[2], "", cells[3], cells[4]}
	case 6:
	default:
		return line
	}
	if cells[4] == "" || cells[4] == "-" {
		cells[4] = author
	}
	if !taskTimeRe.MatchString(cells[5]) {
		cells[5] = stamp
	}
	return "| " + strings.Join(cells, " | ") + " |"
}

// 姓名来源，用于 get_rules 回显。
const (
	UserSourceParam   = "param"
	UserSourceHeader  = "header"
	UserSourceSession = "session"
)

// NormalizeUserName 清洗 userName：空值视为未提供；超过 32 字符或含表格/换行会被拒绝，
// 避免写坏 markdown 表格。名字只用于展示与协作提示，不做身份认证。
func NormalizeUserName(raw string) (string, *Error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", nil
	}
	if utf8.RuneCountInString(name) > 32 {
		return "", errf("INVALID_USER_NAME", 400, "userName 过长", "最多 32 个字符，例如「田灵」。")
	}
	for _, r := range name {
		if r == '|' || r == '\n' || r == '\r' || unicode.IsControl(r) {
			return "", errf("INVALID_USER_NAME", 400, "userName 含非法字符", "不能包含竖线或换行，会破坏 TASKS.md 的表格。")
		}
	}
	return name, nil
}

// ResolveUserName 按「工具参数 > 请求头 > 会话标签」取开发者姓名，并返回来源。
// 工具参数非法时返回 400，让调用方显式知道写错了；请求头非法时直接忽略并继续回落，
// 避免一个人把 X-Memory-User 配错导致整个客户端不可用。
func ResolveUserName(param, header, session string) (string, string, *Error) {
	if strings.TrimSpace(param) != "" {
		name, e := NormalizeUserName(param)
		if e != nil {
			return "", "", e
		}
		if name != "" {
			return name, UserSourceParam, nil
		}
	}
	if strings.TrimSpace(header) != "" {
		if name, e := NormalizeUserName(header); e == nil && name != "" {
			return name, UserSourceHeader, nil
		}
	}
	return session, UserSourceSession, nil
}

// tasksConflicts 返回 TASKS.md 里「其他会话」登记了同一路径的任务行。
// 只做提示，不阻断写入；同一会话自己的登记不算冲突。
func tasksConflicts(content, rel, session, userName string) []Warning {
	var out []Warning
	seen := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if !seen {
			if isTasksHeaderRow(trimmed) {
				seen = true
			}
			continue
		}
		if trimmed == "" || !strings.HasPrefix(trimmed, "|") || isTasksHeaderRow(trimmed) || isSepRow(trimmed) {
			continue
		}
		cells := splitRow(trimmed)
		if len(cells) < 6 {
			continue
		}
		owner := cells[4]
		if owner == "" || owner == "-" || owner == session || (userName != "" && owner == userName) {
			continue
		}
		for _, f := range splitFileList(cells[3]) {
			if f == rel {
				out = append(out, Warning{
					Type: "TASK_CONFLICT",
					Path: rel,
					Rows: 1,
					Hint: fmt.Sprintf("TASKS.md 中「%s」由 %s 登记涉及 %s，先沟通或换文件（不是锁，仍可能同时改动）", cells[0], owner, rel),
				})
				break
			}
		}
	}
	return out
}

// splitFileList 拆分「涉及文件」单元格：支持中英文逗号、顿号与分号。
func splitFileList(cell string) []string {
	fields := strings.FieldsFunc(cell, func(r rune) bool {
		switch r {
		case ',', '，', '、', ';', '；':
			return true
		}
		return false
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.TrimPrefix(strings.TrimSpace(f), "./")
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}
