package kb

import (
	"errors"
	"strings"
)

const indexHeading = "## 文档索引"

type Row struct {
	Title    string
	Path     string
	Scene    string
	Examples string
	Auto     bool
	Line     string
}

type Index struct {
	Rows []Row
}

// ParseIndex 从 RULE.md 的「## 文档索引」小节里解析表格行。
func ParseIndex(md string) Index {
	lines := strings.Split(md, "\n")
	var idx Index
	inside := false
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if !inside {
			if line == indexHeading {
				inside = true
			}
			continue
		}
		if strings.HasPrefix(line, "## ") {
			break
		}
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := splitCells(line)
		if len(cells) < 3 || isSeparator(cells) || cells[0] == "文档" {
			continue
		}
		title, p := parseLink(cells[0])
		if p == "" {
			continue
		}
		idx.Rows = append(idx.Rows, Row{
			Title:    title,
			Path:     p,
			Scene:    cells[1],
			Examples: cells[2],
			Auto:     strings.Contains(line, "<!-- auto -->"),
			Line:     line,
		})
	}
	return idx
}

func (idx Index) Find(p string) (Row, bool) {
	for _, r := range idx.Rows {
		if r.Path == p {
			return r, true
		}
	}
	return Row{}, false
}

// InsertRow 把索引行按固定格式插到表格末尾；已有同路径行时不重复插入。
// 返回新文档内容与是否实际插入。
func InsertRow(md string, r Row) (string, bool, error) {
	if strings.TrimSpace(r.Path) == "" || strings.TrimSpace(r.Scene) == "" {
		return md, false, errors.New("索引行缺少路径或适用场景")
	}
	if idx := ParseIndex(md); len(idx.Rows) > 0 {
		if _, ok := idx.Find(r.Path); ok {
			return md, false, nil
		}
	}
	line := formatRow(r)
	lines := strings.Split(md, "\n")

	headingAt := -1
	for i, raw := range lines {
		if strings.TrimSpace(raw) == indexHeading {
			headingAt = i
			break
		}
	}
	if headingAt < 0 {
		// 没有索引小节：补一个完整表格
		block := []string{"", indexHeading, "", "| 文档 | 适用场景 | 举例 |", "| --- | --- | --- |", line}
		return strings.Join(append(lines, block...), "\n"), true, nil
	}

	sectionEnd := len(lines)
	for i := headingAt + 1; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "## ") {
			sectionEnd = i
			break
		}
	}
	lastRow := -1
	hasTable := false
	for i := headingAt + 1; i < sectionEnd; i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "|") {
			hasTable = true
			lastRow = i
		}
	}
	if !hasTable {
		block := []string{"", "| 文档 | 适用场景 | 举例 |", "| --- | --- | --- |", line}
		out := append([]string{}, lines[:headingAt+1]...)
		out = append(out, block...)
		out = append(out, lines[headingAt+1:]...)
		return strings.Join(out, "\n"), true, nil
	}
	out := append([]string{}, lines[:lastRow+1]...)
	out = append(out, line)
	out = append(out, lines[lastRow+1:]...)
	return strings.Join(out, "\n"), true, nil
}

func formatRow(r Row) string {
	line := "| [" + r.Title + "](" + r.Path + ") | " + cleanCell(r.Scene) + " | " + cleanCell(r.Examples) + " |"
	if r.Auto {
		line += " <!-- auto -->"
	}
	return line
}

func cleanCell(s string) string {
	s = strings.ReplaceAll(s, "|", "／")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}

func splitCells(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	parts := strings.Split(line, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func isSeparator(cells []string) bool {
	for _, c := range cells {
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

func parseLink(cell string) (string, string) {
	open := strings.Index(cell, "[")
	mid := strings.Index(cell, "](")
	end := strings.LastIndex(cell, ")")
	if open < 0 || mid <= open || end <= mid {
		return "", ""
	}
	title := strings.TrimSpace(cell[open+1 : mid])
	p := strings.TrimSpace(cell[mid+2 : end])
	return title, p
}
