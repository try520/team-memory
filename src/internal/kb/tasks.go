package kb

import (
	"regexp"
	"strings"
	"time"
)

const (
	tasksHeader = "| 任务 | 状态 | 下一步 | 推进者（自报） | 更新时间 |"
	tasksSep    = "| --- | --- | --- | --- | --- |"
)

// 服务端写入的更新时间格式；不匹配的值（模型手填）会被覆盖。
var taskTimeRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$`)

// normalizeTasks 把 TASKS.md 的表格规整为五列：
//   - 旧四列表头升级为「推进者（自报） | 更新时间」；
//   - 数据行缺列时补齐，推进者留空/为 "-" 时用会话标签兜底（不猜人名）；
//   - 更新时间只由服务端写，模型写的非标准值会被当前时间覆盖。
//
// 只处理「任务/状态」表头之后的行，其余内容原样保留。
func normalizeTasks(content, session string, now time.Time) string {
	lines := strings.Split(content, "\n")
	stamp := now.Format("2006-01-02 15:04")
	inTable := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !inTable {
			if isTasksHeaderRow(trimmed) {
				lines[i] = tasksHeader
				inTable = true
				if i+1 < len(lines) && isSepRow(strings.TrimSpace(lines[i+1])) {
					lines[i+1] = tasksSep
				}
			}
			continue
		}
		if trimmed == "" || !strings.HasPrefix(trimmed, "|") {
			inTable = false
			continue
		}
		if isSepRow(trimmed) {
			continue
		}
		lines[i] = normalizeTaskRow(trimmed, session, stamp)
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

// normalizeTaskRow 只处理四列/五列，其它列数原样返回，避免破坏自由格式。
func normalizeTaskRow(line, session, stamp string) string {
	cells := splitRow(line)
	switch len(cells) {
	case 4:
		// 旧四列：第 4 列即原「负责人」，直接当作推进者，再补更新时间。
		cells = append(cells, "")
	case 5:
	default:
		return line
	}
	if cells[3] == "" || cells[3] == "-" {
		cells[3] = session
	}
	if !taskTimeRe.MatchString(cells[4]) {
		cells[4] = stamp
	}
	return "| " + strings.Join(cells, " | ") + " |"
}
