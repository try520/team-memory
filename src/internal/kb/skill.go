package kb

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"team-memory/internal/store"
)

// 本文件实现「技能」能力：项目技能与全局技能。
// 数据形态对齐 Agent Skills 的 SKILL.md：--- 包裹的 YAML frontmatter + Markdown 正文。
// 全局技能位于 <DATA_DIR>/skills/<name>/SKILL.md，项目技能位于 <DATA_DIR>/projects/<slug>/skills/<name>/SKILL.md。
// 读写复用 memory_read / memory_write（路径白名单扩展到 skills/ 与 @global/skills/）；
// 项目技能只能经 skill_promote 提交为全局 pending，再审批为 approved。

const (
	skillStatusPending  = "pending"
	skillStatusApproved = "approved"
	skillStatusLocal    = "local" // 仅项目技能使用：尚未提审

	globalSkillPrefix = "@global/"
)

// SkillInfo 是技能清单里的一项。Path 可直接传给 memory_read 读取全文。
type SkillInfo struct {
	Scope         string `json:"scope"` // global | project
	Name          string `json:"name"`
	Description   string `json:"description"`
	Status        string `json:"status"`                   // global: pending/approved；project: local
	SourceProject string `json:"source_project,omitempty"` // 全局技能来自哪个项目
	Path          string `json:"path"`                     // memory_read 可用的路径
}

// SkillListResp 是 skill_list 的返回：全局技能 + 当前项目技能。
type SkillListResp struct {
	Global  []SkillInfo `json:"global"`
	Project []SkillInfo `json:"project"`
	Hint    string      `json:"hint,omitempty"`
}

// SkillPromoteReq 是 skill_promote 的入参。
type SkillPromoteReq struct {
	Action  string `json:"action"`  // promote | submit | approve | reject | cancel
	Name    string `json:"name"`    // 技能名
	Reason  string `json:"reason"`  // submit 时说明为什么提审
	Session string `json:"session"` // memory_get_rules 返回的 session
}

// SkillPromoteResp 是 skill_promote 的返回。
type SkillPromoteResp struct {
	OK     bool   `json:"ok"`
	Action string `json:"action"`
	Name   string `json:"name"`
	Status string `json:"status"`         // 操作后全局技能状态：pending/approved/删除时为 removed
	Path   string `json:"path,omitempty"` // 全局技能路径，可 memory_read
	Hint   string `json:"hint,omitempty"`
	Next   string `json:"next,omitempty"`
}

// skillDoc 是一次解析的结果：frontmatter 键值 + 正文。
type skillDoc struct {
	fm   map[string]string
	body string
}

// parseSkillDoc 解析 SKILL.md 顶部由 --- 包裹的简单 YAML frontmatter。
// 只支持 key: value（value 可含中文与冒号，按首个冒号切分），去掉成对的引号。
// 缺少包裹时返回错误；fields 缺失由调用方校验。
func parseSkillDoc(raw []byte) (*skillDoc, error) {
	s := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if !strings.HasPrefix(s, "---") {
		return nil, errors.New("SKILL.md 必须以 --- 开头的 frontmatter 区块")
	}
	// 支持首行 --- 后紧跟换行；也兼容文件只有 --- 的情况。
	rest := strings.TrimPrefix(s, "---")
	rest = strings.TrimPrefix(rest, "\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, errors.New("frontmatter 未闭合：缺少结束的 --- 行")
	}
	header := rest[:end]
	body := rest[end+len("\n---"):]
	body = strings.TrimPrefix(body, "\n")

	fm := make(map[string]string)
	for _, line := range strings.Split(header, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.Index(line, ":")
		if i <= 0 {
			continue
		}
		k := strings.TrimSpace(line[:i])
		v := strings.TrimSpace(line[i+1:])
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		fm[k] = v
	}
	return &skillDoc{fm: fm, body: body}, nil
}

// validateSkillDoc 解析并要求 name、description 非空。
func validateSkillDoc(raw []byte) (*skillDoc, *Error) {
	doc, err := parseSkillDoc(raw)
	if err != nil {
		return nil, errf("SKILL_FRONTMATTER_REQUIRED", 400, "技能文件缺少合法的 frontmatter", err.Error())
	}
	if strings.TrimSpace(doc.fm["name"]) == "" || strings.TrimSpace(doc.fm["description"]) == "" {
		return nil, &Error{
			Code:    "SKILL_FRONTMATTER_REQUIRED",
			Status:  400,
			Message: "技能 frontmatter 必须包含 name 与 description",
			Hint:    "SKILL.md 顶部用 --- 包裹，写明 name（技能名）与 description（何时该用这个技能），例如：\n---\nname: code-review\ndescription: 涉及代码评审、CR 规范时必读\n---",
		}
	}
	return doc, nil
}

// skillRelName 从项目技能相对路径 skills/<name>/SKILL.md 取出技能名；非技能路径返回空。
func skillRelName(rel string) string {
	parts := strings.Split(rel, "/")
	if len(parts) == 3 && parts[0] == "skills" && parts[2] == "SKILL.md" && store.ValidSkillName(parts[1]) {
		return parts[1]
	}
	return ""
}

// renderSkillDoc 按固定字段顺序重新生成 SKILL.md，避免多次审批后字段顺序漂移。
func renderSkillDoc(doc *skillDoc) []byte {
	keys := []string{"name", "description", "source_project", "status", "submitted_at", "reviewed_at", "reason"}
	var b strings.Builder
	b.WriteString("---\n")
	for _, k := range keys {
		v := strings.TrimSpace(doc.fm[k])
		if v == "" {
			continue
		}
		b.WriteString(k + ": " + v + "\n")
	}
	b.WriteString("---\n")
	if doc.body != "" {
		b.WriteString("\n")
		b.WriteString(doc.body)
	}
	return []byte(b.String())
}

// skillInfoFrom 把 frontmatter 转成 SkillInfo；scope 与 path 由调用方给定。
func skillInfoFrom(scope, name, rel string, fm map[string]string) SkillInfo {
	status := strings.TrimSpace(fm["status"])
	if status == "" {
		status = skillStatusLocal
		if scope == "global" {
			status = skillStatusApproved
		}
	}
	return SkillInfo{
		Scope:         scope,
		Name:          name,
		Description:   strings.TrimSpace(fm["description"]),
		Status:        status,
		SourceProject: strings.TrimSpace(fm["source_project"]),
		Path:          rel,
	}
}

// globalSkillInfos 返回所有全局技能（供 skill_list 与 get_rules 复用）。
func (s *Service) globalSkillInfos() ([]SkillInfo, *Error) {
	names, err := s.st.ListGlobalSkills()
	if err != nil {
		return nil, errf("INTERNAL", 500, "列全局技能失败", err.Error())
	}
	out := make([]SkillInfo, 0, len(names))
	for _, name := range names {
		rel := globalSkillPrefix + "skills/" + name + "/SKILL.md"
		raw, _, rerr := s.st.ReadFile("@global", rel)
		if rerr != nil {
			continue
		}
		doc, perr := parseSkillDoc(raw)
		if perr != nil || strings.TrimSpace(doc.fm["name"]) == "" {
			continue
		}
		out = append(out, skillInfoFrom("global", name, rel, doc.fm))
	}
	return out, nil
}

// SkillList 列出全局技能与当前项目技能，只读。
func (s *Service) SkillList(slug string) (*SkillListResp, *Error) {
	if e := s.ensureProject(slug); e != nil {
		return nil, e
	}
	global, e := s.globalSkillInfos()
	if e != nil {
		return nil, e
	}
	local, err := s.projectSkillInfos(slug)
	if err != nil {
		return nil, errf("INTERNAL", 500, "列项目技能失败", err.Error())
	}
	return &SkillListResp{
		Global:  global,
		Project: local,
		Hint:    "读技能全文用 memory_read(path=返回的 path)；写/改项目技能用 memory_write(path=skills/<name>/SKILL.md)。把项目技能一步提升为全局技能用 skill_promote(action=promote)，需要审批则用 submit→approve。",
	}, nil
}

// projectSkillInfos 列出当前项目技能（读 skills/<name>/SKILL.md 的 frontmatter）。
func (s *Service) projectSkillInfos(slug string) ([]SkillInfo, error) {
	files, err := s.st.ListFiles(slug)
	if err != nil {
		return nil, err
	}
	local := make([]SkillInfo, 0)
	for _, f := range files {
		parts := strings.Split(f.Path, "/")
		if len(parts) != 3 || parts[0] != "skills" || parts[2] != "SKILL.md" {
			continue
		}
		raw, _, rerr := s.st.ReadFile(slug, f.Path)
		if rerr != nil {
			continue
		}
		doc, perr := parseSkillDoc(raw)
		if perr != nil || strings.TrimSpace(doc.fm["name"]) == "" {
			continue
		}
		local = append(local, skillInfoFrom("project", parts[1], f.Path, doc.fm))
	}
	return local, nil
}

// syncSkillIndex 把项目技能清单写回 RULE.md 的「## 技能」段，
// 这样模型只读项目索引就能发现有哪些技能，不用额外调 skill_list。
// RULE.md 是服务端自己改的，写完把该项目的 session 对齐到新 hash，避免下一次写入报 RULE_CHANGED。
func (s *Service) syncSkillIndex(slug string) *Error {
	rule, _, err := s.st.ReadFile(slug, "RULE.md")
	if err != nil {
		return errf("INTERNAL", 500, "读取 RULE.md 失败", err.Error())
	}
	skills, lerr := s.projectSkillInfos(slug)
	if lerr != nil {
		return errf("INTERNAL", 500, "列项目技能失败", lerr.Error())
	}
	updated := replaceSection(string(rule), skillSectionHeading, renderSkillSection(skills))
	if updated == string(rule) {
		return nil
	}
	if err := s.st.Snapshot(slug, "RULE.md", rule); err != nil {
		return errf("INTERNAL", 500, "写历史快照失败", err.Error())
	}
	if err := s.st.WriteAtomic(slug, "RULE.md", []byte(updated)); err != nil {
		return errf("INTERNAL", 500, "写 RULE.md 失败", err.Error())
	}
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
	return nil
}

const skillSectionHeading = "## 技能"

func renderSkillSection(skills []SkillInfo) string {
	var b strings.Builder
	b.WriteString(skillSectionHeading + "\n\n")
	if len(skills) == 0 {
		b.WriteString("（暂无项目技能；用 memory_write 写 skills/<name>/SKILL.md 新建。）\n")
		return b.String()
	}
	b.WriteString("| 技能 | 用途 |\n| --- | --- |\n")
	for _, sk := range skills {
		desc := strings.TrimSpace(sk.Description)
		if desc == "" {
			desc = "—"
		}
		fmt.Fprintf(&b, "| [%s](%s) | %s |\n", sk.Name, sk.Path, desc)
	}
	return b.String()
}

// replaceSection 用 block 替换同名二级标题段；没有该段就插在「## 使用规则」前，再没有就追加到文末。
func replaceSection(doc, heading, block string) string {
	lines := strings.Split(doc, "\n")
	blockLines := strings.Split(strings.TrimRight(block, "\n"), "\n")
	start := -1
	for i, ln := range lines {
		if strings.TrimSpace(ln) == heading {
			start = i
			break
		}
	}
	if start >= 0 {
		end := len(lines)
		for j := start + 1; j < len(lines); j++ {
			if strings.HasPrefix(strings.TrimSpace(lines[j]), "## ") {
				end = j
				break
			}
		}
		out := append([]string{}, lines[:start]...)
		out = append(out, blockLines...)
		if end < len(lines) {
			out = append(out, "")
		}
		out = append(out, lines[end:]...)
		return strings.Join(out, "\n")
	}
	for i, ln := range lines {
		if strings.TrimSpace(ln) == "## 使用规则" {
			out := append([]string{}, lines[:i]...)
			out = append(out, blockLines...)
			out = append(out, "")
			out = append(out, lines[i:]...)
			return strings.Join(out, "\n")
		}
	}
	return strings.TrimRight(doc, "\n") + "\n\n" + strings.TrimRight(block, "\n") + "\n"
}

// SkillPromote 把项目技能提升为全局技能：promote 一步到位（移动），
// 或 submit→approve 两阶段审批（approve 时同样移动）；reject/cancel 删除 pending。
// 无鉴权：任何持有 session 的调用方都可以 approve/reject（团队内部信任模型）。
func (s *Service) SkillPromote(slug string, req SkillPromoteReq) (*SkillPromoteResp, *Error) {
	switch req.Action {
	case "promote", "submit", "approve", "reject", "cancel":
	default:
		return nil, errf("BAD_REQUEST", 400, "action 非法", "action 只能是 promote / submit / approve / reject / cancel")
	}
	name := strings.TrimSpace(req.Name)
	if !store.ValidSkillName(name) {
		return nil, errf("BAD_REQUEST", 400, "技能名非法", "只允许小写字母、数字与中间的中划线，例如 code-review")
	}
	if e := s.lookupSession(req.Session, slug); e != nil {
		return nil, e
	}

	unlock := s.st.Lock("global|skill|" + name)
	defer unlock()

	globalRel := globalSkillPrefix + "skills/" + name + "/SKILL.md"
	exists := s.st.Exists(slug, globalRel)
	var (
		globalDoc *skillDoc
		status    string
	)
	if exists {
		raw, _, rerr := s.st.ReadFile(slug, globalRel)
		if rerr != nil {
			return nil, errf("INTERNAL", 500, "读取全局技能失败", rerr.Error())
		}
		globalDoc, _ = parseSkillDoc(raw)
		if globalDoc != nil {
			status = strings.TrimSpace(globalDoc.fm["status"])
		}
	}

	switch req.Action {
	case "submit":
		projectRel := "skills/" + name + "/SKILL.md"
		raw, _, rerr := s.st.ReadFile(slug, projectRel)
		if rerr != nil {
			if errors.Is(rerr, os.ErrNotExist) {
				return nil, errf("NOT_FOUND", 404, "项目技能不存在", "先用 memory_write 写 skills/"+name+"/SKILL.md（含 name 与 description 的 frontmatter）。")
			}
			return nil, errf("INTERNAL", 500, "读取项目技能失败", rerr.Error())
		}
		doc, ve := validateSkillDoc(raw)
		if ve != nil {
			return nil, ve
		}
		if exists && status != "" {
			return nil, &Error{
				Code:    "ALREADY_PROMOTED",
				Status:  409,
				Message: "该技能已提审或已成为全局技能",
				Hint:    "当前状态 " + status + "；要重新提审先用 skill_promote(action=cancel) 撤销。",
			}
		}
		doc.fm["source_project"] = slug
		doc.fm["status"] = skillStatusPending
		doc.fm["submitted_at"] = s.now().Format(time.RFC3339)
		doc.fm["reviewed_at"] = ""
		doc.fm["reason"] = strings.TrimSpace(req.Reason)
		if err := s.st.WriteAtomic(slug, globalRel, renderSkillDoc(doc)); err != nil {
			return nil, errf("INTERNAL", 500, "提交全局技能失败", err.Error())
		}
		return &SkillPromoteResp{
			OK: true, Action: req.Action, Name: name, Status: skillStatusPending, Path: globalRel,
			Hint: "已提交待审批，全局技能暂不可被其他项目使用。",
			Next: "由维护者调用 skill_promote(action=approve) 通过后，该技能才会出现在各项目的 get_rules 全局技能清单里。",
		}, nil

	case "promote":
		// 一步到位：项目技能 → 全局 approved，并把项目内副本移除（移动，不是复制）。
		projectRel := "skills/" + name + "/SKILL.md"
		raw, _, rerr := s.st.ReadFile(slug, projectRel)
		if rerr != nil {
			if errors.Is(rerr, os.ErrNotExist) {
				return nil, errf("NOT_FOUND", 404, "项目技能不存在", "先用 memory_write 写 skills/"+name+"/SKILL.md（含 name 与 description 的 frontmatter）。")
			}
			return nil, errf("INTERNAL", 500, "读取项目技能失败", rerr.Error())
		}
		doc, ve := validateSkillDoc(raw)
		if ve != nil {
			return nil, ve
		}
		if exists && status != "" {
			return nil, &Error{
				Code:    "ALREADY_PROMOTED",
				Status:  409,
				Message: "该技能已提审或已成为全局技能",
				Hint:    "当前状态 " + status + "；全局技能只读，改内容请先在项目里改一份再处理。",
			}
		}
		now := s.now().Format(time.RFC3339)
		doc.fm["source_project"] = slug
		doc.fm["status"] = skillStatusApproved
		doc.fm["submitted_at"] = now
		doc.fm["reviewed_at"] = now
		doc.fm["reason"] = strings.TrimSpace(req.Reason)
		if err := s.st.WriteAtomic(slug, globalRel, renderSkillDoc(doc)); err != nil {
			return nil, errf("INTERNAL", 500, "写入全局技能失败", err.Error())
		}
		if err := s.st.RemoveProjectSkill(slug, name); err != nil {
			return nil, errf("INTERNAL", 500, "移除项目技能失败", err.Error())
		}
		if e := s.syncSkillIndex(slug); e != nil {
			return nil, e
		}
		return &SkillPromoteResp{
			OK: true, Action: req.Action, Name: name, Status: skillStatusApproved, Path: globalRel,
			Hint: "已提升为全局技能，项目内副本已移除（移动，不是复制）。",
			Next: "该技能已出现在所有项目的 get_rules.global_skills 里；用 memory_read(path=@global/skills/" + name + "/SKILL.md) 读全文。",
		}, nil

	case "approve":
		if !exists {
			return nil, errf("NOT_FOUND", 404, "全局技能不存在", "先由项目方调用 skill_promote(action=submit) 提交。")
		}
		if status != skillStatusPending {
			return nil, &Error{Code: "INVALID_STATE", Status: 409, Message: "只有 pending 状态的技能可以审批", Hint: "当前状态 " + status + "。"}
		}
		globalDoc.fm["status"] = skillStatusApproved
		globalDoc.fm["reviewed_at"] = s.now().Format(time.RFC3339)
		if err := s.st.WriteAtomic(slug, globalRel, renderSkillDoc(globalDoc)); err != nil {
			return nil, errf("INTERNAL", 500, "审批全局技能失败", err.Error())
		}
		// 通过后把来源项目里的副本移除（移动语义，避免两份内容漂移）。
		if src := strings.TrimSpace(globalDoc.fm["source_project"]); src != "" {
			if err := s.st.RemoveProjectSkill(src, name); err != nil {
				return nil, errf("INTERNAL", 500, "移除项目技能失败", err.Error())
			}
			if e := s.syncSkillIndex(src); e != nil {
				return nil, e
			}
		}
		return &SkillPromoteResp{
			OK: true, Action: req.Action, Name: name, Status: skillStatusApproved, Path: globalRel,
			Hint: "已批准，该技能会在所有项目的 memory_get_rules 全局技能清单中列出；来源项目里的副本已移除。",
		}, nil

	default: // reject / cancel：删除 pending 全局技能
		if !exists {
			return nil, errf("NOT_FOUND", 404, "全局技能不存在", "没有可撤销/拒绝的提审。")
		}
		if status != skillStatusPending {
			return nil, &Error{Code: "INVALID_STATE", Status: 409, Message: "只有 pending 状态的技能可以撤销或拒绝", Hint: "当前状态 " + status + "。"}
		}
		if err := s.st.RemoveGlobalSkill(name); err != nil {
			return nil, errf("INTERNAL", 500, "删除全局技能失败", err.Error())
		}
		return &SkillPromoteResp{
			OK: true, Action: req.Action, Name: name, Status: "removed",
			Hint: "已撤销/拒绝，未生成全局技能。",
		}, nil
	}
}
