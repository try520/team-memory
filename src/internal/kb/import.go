package kb

import (
	"fmt"
	"path"
	"strings"
)

// ImportReq 把已有知识一次性灌进项目，用于新建项目时的初始化。
// 与 memory_write 的差别：不需要 session（属管理操作）、支持批量、默认幂等（已存在文件跳过）。
type ImportReq struct {
	Mode   string           `json:"mode"`    // merge（默认，跳过已存在）| replace（覆盖并留快照）
	DryRun bool             `json:"dry_run"` // 只校验与试算，不落盘
	RuleMD string           `json:"rule_md"` // 可选：整篇 RULE.md（含索引表）
	Files  []ImportFile     `json:"files"`   // path 相对项目根，如 topics/xx.md
	Index  []ImportIndexRow `json:"index"`   // 可选：索引行；省略时自动补 <!-- auto --> 行
}

type ImportFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type ImportIndexRow struct {
	Path     string   `json:"path"`
	Title    string   `json:"title,omitempty"`
	Scene    string   `json:"scene"`
	Examples []string `json:"examples"`

	auto bool // 自动补的行带 <!-- auto -->
}

type ImportActionResult struct {
	Path   string `json:"path"`
	Action string `json:"action"` // created | replaced | skipped
	Bytes  int    `json:"bytes,omitempty"`
}

type ImportResp struct {
	OK          bool                 `json:"ok"`
	Project     string               `json:"project"`
	DryRun      bool                 `json:"dry_run"`
	Created     int                  `json:"created"`
	Replaced    int                  `json:"replaced"`
	Skipped     int                  `json:"skipped"`
	RuleWritten bool                 `json:"rule_written"`
	IndexAdded  int                  `json:"index_added"`
	Session     string               `json:"session,omitempty"` // 写入了 rule_md 时返回新 session
	Files       []ImportActionResult `json:"files"`
	Warnings    []Warning            `json:"warnings,omitempty"`
	Hint        string               `json:"hint,omitempty"`
}

type importItem struct {
	rel     string
	content []byte
}

// mintSession 在 RULE.md 被导入接口替换后签发新 session，省掉一次 get_rules。
func (s *Service) mintSession(slug string) string {
	h, e := s.ruleHash(slug)
	if e != nil {
		return ""
	}
	sid := "s_" + randHex(4)
	s.mu.Lock()
	s.gcSessionsLocked()
	s.sessions[sid] = session{project: slug, ruleHash: h, at: s.now()}
	s.mu.Unlock()
	return sid
}

func normalizeImportPath(p string) string {
	p = strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	for strings.HasPrefix(p, "./") {
		p = strings.TrimPrefix(p, "./")
	}
	return p
}

// ImportFromTool 是模型侧入口：先过写入门控（必须先 get_rules），再走同一套导入逻辑。
// 模型不能绕过「先读规则」直接批量覆盖别人的记忆。
func (s *Service) ImportFromTool(slug, session string, req ImportReq) (*ImportResp, *Error) {
	if e := s.lookupSession(session, slug); e != nil {
		return nil, e
	}
	return s.Import(slug, req)
}

// Import 是初始化的唯一入口：批量落盘 + 可选的整篇 RULE.md + 索引行。
// 先整体校验路径再写，避免写到一半才发现某个路径非法。
func (s *Service) Import(slug string, req ImportReq) (*ImportResp, *Error) {
	mode := strings.TrimSpace(req.Mode)
	if mode == "" {
		mode = "merge"
	}
	if mode != "merge" && mode != "replace" {
		return nil, errf("BAD_REQUEST", 400, "mode 非法", "mode 只能是 merge（跳过已存在）或 replace（覆盖）")
	}
	if len(req.Files) == 0 && strings.TrimSpace(req.RuleMD) == "" && len(req.Index) == 0 {
		return nil, errf("BAD_REQUEST", 400, "没有可导入的内容", "files / rule_md / index 至少给一项")
	}

	if req.DryRun {
		if _, err := s.st.ProjectDir(slug); err != nil {
			return nil, errf("BAD_REQUEST", 400, "项目名非法", "slug 只允许小写字母、数字与中划线")
		}
	} else if e := s.ensureProject(slug); e != nil {
		return nil, e
	}

	items := make([]importItem, 0, len(req.Files))
	seen := make(map[string]bool, len(req.Files))
	for _, f := range req.Files {
		rel := normalizeImportPath(f.Path)
		if _, err := s.st.Abs(slug, rel); err != nil {
			return nil, errf("PATH_NOT_ALLOWED", 400, "路径不允许："+rel, allowedHint())
		}
		if seen[rel] {
			return nil, errf("BAD_REQUEST", 400, "同一个路径出现多次："+rel, "每个 path 只能出现一次")
		}
		seen[rel] = true
		items = append(items, importItem{rel: rel, content: []byte(f.Content)})
	}

	resp := &ImportResp{OK: true, Project: slug, DryRun: req.DryRun, Files: make([]ImportActionResult, 0, len(items))}

	// 1) RULE.md：merge 只在当前索引为空时应用，避免把后来人工补的索引覆盖掉。
	if strings.TrimSpace(req.RuleMD) != "" {
		cur, _, rerr := s.st.ReadFile(slug, "RULE.md")
		apply := req.DryRun || mode == "replace" || rerr != nil || len(ParseIndex(string(cur)).Rows) == 0
		if !apply {
			resp.Hint = "RULE.md 已有索引行，merge 模式不覆盖；需要整篇替换请用 mode=replace。"
		} else if req.DryRun {
			resp.Hint = "dry_run：rule_md 将写入 RULE.md。"
		} else {
			unlock := s.st.Lock(slug + "|RULE.md")
			if rerr == nil {
				_ = s.st.Snapshot(slug, "RULE.md", cur)
			}
			werr := s.st.WriteAtomic(slug, "RULE.md", []byte(req.RuleMD))
			unlock()
			if werr != nil {
				return nil, errf("INTERNAL", 500, "写 RULE.md 失败", werr.Error())
			}
			resp.RuleWritten = true
			resp.Session = s.mintSession(slug)
		}
	}

	// 2) 文件：merge 跳过已存在（幂等），replace 覆盖并留 .history 快照。
	for _, it := range items {
		unlock := func() {}
		if !req.DryRun {
			unlock = s.st.Lock(slug + "|" + it.rel)
		}
		action := "created"
		if s.st.Exists(slug, it.rel) {
			action = "skipped"
			if mode == "replace" {
				action = "replaced"
			}
		}
		if !req.DryRun && action != "skipped" {
			if action == "replaced" {
				if old, _, err := s.st.ReadFile(slug, it.rel); err == nil {
					_ = s.st.Snapshot(slug, it.rel, old)
				}
			}
			werr := s.st.WriteAtomic(slug, it.rel, it.content)
			if werr == nil && isJournalPath(it.rel) {
				werr = s.appendJournalIndex(slug, it.rel, it.content, "")
			}
			if werr != nil {
				unlock()
				return nil, errf("INTERNAL", 500, "写入失败："+it.rel, werr.Error())
			}
		}
		unlock()

		switch action {
		case "created":
			resp.Created++
		case "replaced":
			resp.Replaced++
		case "skipped":
			resp.Skipped++
		}
		resp.Files = append(resp.Files, ImportActionResult{Path: it.rel, Action: action, Bytes: len(it.content)})
	}

	// 3) 索引：给了 index 就用；没给就按 reindex 的口径给未登记主题补 <!-- auto --> 行。
	// dry_run 时磁盘上还没有新文件/新 RULE.md，用请求内容做基线试算。
	baseMD := ""
	if req.DryRun {
		baseMD = req.RuleMD
	}
	rows := req.Index
	if len(rows) == 0 {
		rows = s.autoIndexRows(slug, baseMD, items)
	}
	added, e := s.importIndexRows(slug, baseMD, rows, !req.DryRun)
	if e != nil {
		return nil, e
	}
	resp.IndexAdded = added

	if rule, _, err := s.st.ReadFile(slug, "RULE.md"); err == nil {
		resp.Warnings = s.warnings(slug, string(rule))
	}
	summary := fmt.Sprintf("created=%d replaced=%d skipped=%d index_added=%d", resp.Created, resp.Replaced, resp.Skipped, resp.IndexAdded)
	if resp.Hint != "" {
		summary = resp.Hint + " " + summary
	}
	if resp.IndexAdded > 0 && len(req.Index) == 0 {
		summary += "；自动索引行带 <!-- auto -->，场景是文档首段截取，建议改成真实触发条件。"
	}
	resp.Hint = summary
	return resp, nil
}

// autoIndexRows 列出尚未登记的主题，按首段生成默认索引行。
// baseMD 非空时以它为基线（dry_run 用请求里的 rule_md），否则读磁盘。
// extra 是本次请求里的文件（dry_run 时还没落盘，只能靠它试算）。
func (s *Service) autoIndexRows(slug, baseMD string, extra []importItem) []ImportIndexRow {
	var idx Index
	if baseMD != "" {
		idx = ParseIndex(baseMD)
	} else if rule, _, err := s.st.ReadFile(slug, "RULE.md"); err == nil {
		idx = ParseIndex(string(rule))
	}
	topics, _ := s.st.ListTopics(slug)
	rows := make([]ImportIndexRow, 0, len(topics)+len(extra))
	seen := make(map[string]bool, len(topics)+len(extra))
	add := func(rel string, body []byte) {
		if seen[rel] {
			return
		}
		if _, ok := idx.Find(rel); ok {
			return
		}
		seen[rel] = true
		title := strings.TrimSuffix(path.Base(rel), ".md")
		rows = append(rows, ImportIndexRow{
			Path:     rel,
			Title:    title,
			Scene:    title + "：" + firstParagraph(string(body), 60),
			Examples: []string{title + "里有哪些内容？"},
			auto:     true,
		})
	}
	for _, t := range topics {
		body, _, rerr := s.st.ReadFile(slug, t)
		if rerr != nil {
			continue
		}
		add(t, body)
	}
	for _, it := range extra {
		if strings.HasPrefix(it.rel, "topics/") {
			add(it.rel, it.content)
		}
	}
	return rows
}

// importIndexRows 一次锁、一次快照地批量插行；write=false 时只校验与计数。
func (s *Service) importIndexRows(slug, baseMD string, rows []ImportIndexRow, write bool) (int, *Error) {
	if len(rows) == 0 {
		return 0, nil
	}
	unlock := s.st.Lock(slug + "|RULE.md")
	defer unlock()

	var cur []byte
	if baseMD != "" {
		cur = []byte(baseMD)
	} else {
		b, _, err := s.st.ReadFile(slug, "RULE.md")
		if err != nil {
			if !write {
				// 项目还没建：没有已有行可去重，全部按新增试算。
				for _, r := range rows {
					if _, aerr := s.st.Abs(slug, normalizeImportPath(r.Path)); aerr != nil {
						return 0, errf("PATH_NOT_ALLOWED", 400, "索引行路径不允许："+r.Path, allowedHint())
					}
					if strings.TrimSpace(r.Scene) == "" {
						return 0, errf("BAD_REQUEST", 400, "索引行缺少 scene："+r.Path, "scene 写「什么场景必须读」")
					}
				}
				return len(rows), nil
			}
			return 0, errf("NOT_FOUND", 404, "RULE.md 不存在", "先创建项目，或随 files 一起传 rule_md")
		}
		cur = b
	}
	md := string(cur)
	added := 0
	for _, r := range rows {
		rel := normalizeImportPath(r.Path)
		if _, err := s.st.Abs(slug, rel); err != nil {
			return 0, errf("PATH_NOT_ALLOWED", 400, "索引行路径不允许："+rel, allowedHint())
		}
		if strings.TrimSpace(r.Scene) == "" {
			return 0, errf("BAD_REQUEST", 400, "索引行缺少 scene："+rel, "scene 写「什么场景必须读」")
		}
		title := strings.TrimSpace(r.Title)
		if title == "" {
			title = strings.TrimSuffix(path.Base(rel), ".md")
		}
		examples := make([]string, 0, len(r.Examples))
		for _, ex := range r.Examples {
			ex = strings.TrimSpace(ex)
			if ex == "" {
				continue
			}
			if !strings.HasPrefix(ex, `"`) {
				ex = `"` + ex + `"`
			}
			examples = append(examples, ex)
		}
		newMd, inserted, ierr := InsertRow(md, Row{
			Title: title, Path: rel, Scene: r.Scene,
			Examples: strings.Join(examples, " "), Auto: r.auto,
		})
		if ierr != nil {
			return 0, errf("BAD_REQUEST", 400, ierr.Error(), "scene 与 examples 都必须非空")
		}
		if !inserted {
			continue
		}
		md = newMd
		added++
	}
	if added == 0 || !write {
		return added, nil
	}
	if err := s.st.Snapshot(slug, "RULE.md", cur); err != nil {
		return 0, errf("INTERNAL", 500, "写历史快照失败", err.Error())
	}
	if err := s.st.WriteAtomic(slug, "RULE.md", []byte(md)); err != nil {
		return 0, errf("INTERNAL", 500, "写 RULE.md 失败", err.Error())
	}
	// 索引是我们自己补的，不算使用者改了规则：把本项目 session 对齐到新 hash。
	if h, e := s.ruleHash(slug); e == nil {
		s.mu.Lock()
		for k, v := range s.sessions {
			if v.project == slug {
				v.ruleHash = h
				s.sessions[k] = v
			}
		}
		s.mu.Unlock()
	}
	return added, nil
}
