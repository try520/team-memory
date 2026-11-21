package kb

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"team-memory/internal/store"
)

const (
	maxReadBytes    = 32 * 1024
	journalWindow   = 30 * 24 * time.Hour
	sessionTTL      = 12 * time.Hour
	indexMaxRows    = 25
	maxSceneRunes   = 120
	maxExampleRunes = 200
	filesListCap    = 200
	historyKeep     = 50
)

type Error struct {
	Code    string         `json:"error"`
	Status  int            `json:"-"`
	Message string         `json:"message,omitempty"`
	Hint    string         `json:"hint,omitempty"`
	Extra   map[string]any `json:"-"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func errf(code string, status int, msg, hint string) *Error {
	return &Error{Code: code, Status: status, Message: msg, Hint: hint}
}

type session struct {
	project  string
	ruleHash string
	at       time.Time
}

type Service struct {
	st       *store.Store
	now      func() time.Time
	mu       sync.Mutex
	sessions map[string]session
}

func New(st *store.Store) *Service {
	return &Service{st: st, now: time.Now, sessions: make(map[string]session)}
}

type FileInfo struct {
	Path    string    `json:"path"`
	Lines   int       `json:"lines"`
	Updated time.Time `json:"updated"`
}

type Warning struct {
	Type string `json:"type"`
	Path string `json:"path,omitempty"`
	Rows int    `json:"rows,omitempty"`
	Hint string `json:"hint,omitempty"`
}

type Window struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Files int    `json:"files"`
}

type RulesResp struct {
	Project       string     `json:"project"`
	RuleHash      string     `json:"rule_hash"`
	Session       string     `json:"session"`
	ServiceRule   string     `json:"service_rule"`
	Index         string     `json:"index"`
	Files         []FileInfo `json:"files"`
	JournalIndex  []string   `json:"journal_index"`
	Warnings      []Warning  `json:"warnings"`
	JournalWindow Window     `json:"journal_window"`
	// LastWrite / DaysSinceWrite / RecordReminder 用于提醒「该写回了」：
	// 只看 topics/ 与 journal/ 的记录，模板文件不算。
	LastWrite      string `json:"last_write,omitempty"`
	DaysSinceWrite int    `json:"days_since_write"`
	RecordReminder string `json:"record_reminder,omitempty"`
	// GlobalSkills 是全局技能清单，随每次 get_rules 自动列出（含 pending）。
	GlobalSkills []SkillInfo `json:"global_skills"`
}

type ReadResp struct {
	Path       string `json:"path"`
	Rev        string `json:"rev,omitempty"`
	Hash       string `json:"hash"`
	Lines      int    `json:"lines"`
	Bytes      int    `json:"bytes"`
	Truncated  bool   `json:"truncated"`
	NextOffset int    `json:"next_offset,omitempty"`
	Content    string `json:"content"`
}

type IndexRow struct {
	Scene    string   `json:"scene"`
	Examples []string `json:"examples"`
}

type WriteReq struct {
	Path         string    `json:"path"`
	Content      string    `json:"content"`
	Mode         string    `json:"mode"`
	ExpectedHash string    `json:"expectedHash"`
	Session      string    `json:"session"`
	UserName     string    `json:"userName"`
	Index        *IndexRow `json:"index"`
}

type WriteResp struct {
	OK           bool      `json:"ok"`
	Path         string    `json:"path"`
	Hash         string    `json:"hash"`
	Lines        int       `json:"lines"`
	IndexUpdated bool      `json:"index_updated"`
	Warnings     []Warning `json:"warnings,omitempty"`
	Hint         string    `json:"hint,omitempty"`
	Next         string    `json:"next,omitempty"`
}

// EnsureServiceLayout 创建数据目录骨架与服务级规则。
func (s *Service) EnsureServiceLayout() error {
	if err := s.st.EnsureDir(s.st.Root()); err != nil {
		return err
	}
	if err := s.st.EnsureDir(path.Join(s.st.Root(), "projects")); err != nil {
		return err
	}
	if err := s.st.EnsureGlobalSkillsLayout(); err != nil {
		return err
	}
	dir := path.Join(s.st.Root(), "_service")
	if err := s.st.EnsureDir(dir); err != nil {
		return err
	}
	rulesPath := path.Join(dir, "RULES.md")
	if _, err := os.Stat(rulesPath); errors.Is(err, os.ErrNotExist) {
		return os.WriteFile(rulesPath, []byte(serviceRules), 0o644)
	}
	return nil
}

func (s *Service) ServiceRules() (string, error) {
	b, err := os.ReadFile(path.Join(s.st.Root(), "_service", "RULES.md"))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (s *Service) ListProjects() []string {
	p, _ := s.st.ListProjects()
	return p
}

func (s *Service) ensureProject(slug string) *Error {
	if err := s.st.EnsureProjectLayout(slug); err != nil {
		if errors.Is(err, store.ErrBadProject) {
			return errf("BAD_REQUEST", 400, "项目名非法", "slug 只允许小写字母、数字与中划线，例如 my-project")
		}
		return errf("INTERNAL", 500, err.Error(), "")
	}
	if !s.st.Exists(slug, "RULE.md") {
		if err := s.st.WriteAtomic(slug, "RULE.md", []byte(projectRuleTemplate(slug))); err != nil {
			return errf("INTERNAL", 500, err.Error(), "")
		}
	}
	if !s.st.Exists(slug, "TASKS.md") {
		if err := s.st.WriteAtomic(slug, "TASKS.md", []byte(tasksTemplate(slug))); err != nil {
			return errf("INTERNAL", 500, err.Error(), "")
		}
	}
	return nil
}

func (s *Service) ruleHash(slug string) (string, *Error) {
	svc, err := s.ServiceRules()
	if err != nil {
		return "", errf("INTERNAL", 500, "读取服务级规则失败", err.Error())
	}
	rule, _, err := s.st.ReadFile(slug, "RULE.md")
	if err != nil {
		return "", errf("INTERNAL", 500, "读取 RULE.md 失败", err.Error())
	}
	sum := sha256.Sum256(append(append([]byte(svc), 0), rule...))
	return hex.EncodeToString(sum[:]), nil
}

func (s *Service) GetRules(slug string) (*RulesResp, *Error) {
	if e := s.ensureProject(slug); e != nil {
		return nil, e
	}
	svc, err := s.ServiceRules()
	if err != nil {
		return nil, errf("INTERNAL", 500, "读取服务级规则失败", err.Error())
	}
	rule, _, err := s.st.ReadFile(slug, "RULE.md")
	if err != nil {
		return nil, errf("INTERNAL", 500, "读取 RULE.md 失败", err.Error())
	}
	hash, e := s.ruleHash(slug)
	if e != nil {
		return nil, e
	}

	sid := "s_" + randHex(4)
	s.mu.Lock()
	s.gcSessionsLocked()
	s.sessions[sid] = session{project: slug, ruleHash: hash, at: s.now()}
	s.mu.Unlock()

	now := s.now()
	from := now.Add(-journalWindow)
	all, err := s.st.ListFiles(slug)
	if err != nil {
		return nil, errf("INTERNAL", 500, "列文件失败", err.Error())
	}
	files := make([]FileInfo, 0, len(all))
	journalCount := 0
	for _, f := range all {
		if strings.HasPrefix(f.Path, "journal/") {
			if f.Updated.Before(from) {
				continue
			}
			journalCount++
		}
		if len(files) >= filesListCap {
			continue
		}
		files = append(files, FileInfo{Path: f.Path, Lines: f.Lines, Updated: f.Updated})
	}

	// 记录活跃度：只统计 topics/ 与 journal/，模板文件不算「记录」。
	var lastRecord time.Time
	for _, f := range all {
		if strings.HasPrefix(f.Path, "topics/") || strings.HasPrefix(f.Path, "journal/") {
			if f.Updated.After(lastRecord) {
				lastRecord = f.Updated
			}
		}
	}
	days := -1
	reminder := ""
	lastWrite := ""
	if lastRecord.IsZero() {
		reminder = "本项目还没有任何结论记录：完成任务后至少写一条主题结论或 journal（无事也写「本次无需记录：原因」）。"
	} else {
		lastWrite = lastRecord.Format(time.RFC3339)
		days = int(now.Sub(lastRecord).Hours() / 24)
		if days >= 7 {
			reminder = fmt.Sprintf("距上次记录 %d 天：本次若做了取舍、踩了坑或接口有变更，先写回再结束。", days)
		}
	}

	globalSkills, ge := s.globalSkillInfos()
	if ge != nil {
		return nil, ge
	}

	return &RulesResp{
		Project:      slug,
		RuleHash:     hash,
		Session:      sid,
		ServiceRule:  svc,
		Index:        string(rule),
		Files:        files,
		JournalIndex: s.journalIndexFiles(slug, now),
		Warnings:     s.warnings(slug, string(rule)),
		JournalWindow: Window{
			From:  from.Format("2006-01-02"),
			To:    now.Format("2006-01-02"),
			Files: journalCount,
		},
		LastWrite:      lastWrite,
		DaysSinceWrite: days,
		RecordReminder: reminder,
		GlobalSkills:   globalSkills,
	}, nil
}

func (s *Service) journalIndexFiles(slug string, now time.Time) []string {
	cur := now.Format("2006-01")
	prev := now.AddDate(0, -1, 0).Format("2006-01")
	var out []string
	for _, ym := range []string{cur, prev} {
		rel := "journal/" + ym + ".INDEX.md"
		if s.st.Exists(slug, rel) {
			out = append(out, rel)
		}
	}
	return out
}

func (s *Service) gcSessionsLocked() {
	cut := s.now().Add(-sessionTTL)
	for k, v := range s.sessions {
		if v.at.Before(cut) {
			delete(s.sessions, k)
		}
	}
}

func (s *Service) lookupSession(id, slug string) *Error {
	s.mu.Lock()
	v, ok := s.sessions[id]
	s.mu.Unlock()
	if !ok || v.project != slug || s.now().Sub(v.at) > sessionTTL {
		return &Error{
			Code:    "READ_RULES_FIRST",
			Status:  428,
			Message: "写入前必须先读规则与索引",
			Hint:    "先调用 memory_get_rules()，把返回的 session 传给 memory_write。",
			Extra:   map[string]any{"preview": serviceRulePreview()},
		}
	}
	cur, e := s.ruleHash(slug)
	if e != nil {
		return e
	}
	if cur != v.ruleHash {
		return &Error{
			Code:    "RULE_CHANGED",
			Status:  428,
			Message: "RULE.md 已更新，索引变了",
			Hint:    "重新调用 memory_get_rules() 读取最新索引，再用新 session 写入。",
		}
	}
	return nil
}

func serviceRulePreview() string {
	s := strings.TrimSpace(serviceRules)
	if i := strings.Index(s, "\n## "); i > 0 {
		s = s[:i]
	}
	return s
}

func (s *Service) Read(slug, rel, rev string, offset int) (*ReadResp, *Error) {
	var (
		raw   []byte
		err   error
		isRev = rev != ""
	)
	if isRev {
		raw, err = s.st.ReadSnapshot(slug, rel, rev)
	} else {
		raw, _, err = s.st.ReadFile(slug, rel)
	}
	if err != nil {
		if errors.Is(err, store.ErrBadPath) || errors.Is(err, store.ErrBadProject) {
			return nil, errf("PATH_NOT_ALLOWED", 400, "路径不允许", allowedHint())
		}
		if errors.Is(err, os.ErrNotExist) {
			avail := s.available(slug)
			return nil, &Error{Code: "NOT_FOUND", Status: 404, Message: "文件不存在", Extra: map[string]any{"available": avail}}
		}
		return nil, errf("INTERNAL", 500, err.Error(), "")
	}
	if offset < 0 {
		return nil, errf("BAD_REQUEST", 400, "offset 不能为负", "")
	}
	if offset > len(raw) {
		offset = len(raw)
	}
	// 按字节分页不能把多字节字符切开：offset 与 end 都对齐到 UTF-8 字符边界，
	// 否则客户端续读拼回来的中文会出现替换字符，编辑保存时还会把乱码写回。
	for offset > 0 && offset < len(raw) && !utf8.RuneStart(raw[offset]) {
		offset--
	}
	end := offset + maxReadBytes
	if end > len(raw) {
		end = len(raw)
	}
	for end > offset && end < len(raw) && !utf8.RuneStart(raw[end]) {
		end--
	}
	if end <= offset && offset < len(raw) {
		end = offset + 1
		for end < len(raw) && !utf8.RuneStart(raw[end]) {
			end++
		}
	}
	resp := &ReadResp{
		Path:      rel,
		Hash:      sha256Hex(raw),
		Lines:     strings.Count(string(raw), "\n") + 1,
		Bytes:     len(raw),
		Truncated: end < len(raw),
		Content:   string(raw[offset:end]),
	}
	if isRev {
		resp.Rev = rev
	}
	if resp.Truncated {
		resp.NextOffset = end
	}
	if len(raw) == 0 {
		resp.Lines = 0
	}
	return resp, nil
}

func (s *Service) available(slug string) []string {
	files, err := s.st.ListFiles(slug)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(files))
	for i, f := range files {
		if i >= 50 {
			break
		}
		out = append(out, f.Path)
	}
	return out
}

func allowedHint() string {
	return "允许的路径：RULE.md、TASKS.md、topics/**.md、journal/**.md、skills/<name>/SKILL.md，只读 @global/skills/<name>/SKILL.md；不能含 ..、不能是绝对路径"
}

func (s *Service) Write(slug string, req WriteReq) (*WriteResp, *Error) {
	switch req.Mode {
	case "create", "append", "replace", "reindex":
	default:
		return nil, errf("BAD_REQUEST", 400, "mode 非法", `mode 只能是 create / append / replace / reindex`)
	}
	if e := s.lookupSession(req.Session, slug); e != nil {
		return nil, e
	}
	// userName 是调用方自报的名字：仅用于展示与协作提示，不是身份认证。
	name, e := normalizeUserName(req.UserName)
	if e != nil {
		return nil, e
	}
	req.UserName = name
	if req.Mode == "reindex" {
		return s.reindex(slug, req)
	}

	rel := strings.TrimSpace(strings.ReplaceAll(req.Path, "\\", "/"))
	if rel == "" {
		return nil, errf("BAD_REQUEST", 400, "path 必填", allowedHint())
	}
	if _, err := s.st.Abs(slug, rel); err != nil {
		return nil, errf("PATH_NOT_ALLOWED", 400, "路径不允许", allowedHint())
	}
	// 全局技能只读：只能经 skill_promote 的 submit/approve/reject 变更。
	if strings.HasPrefix(rel, globalSkillPrefix) {
		return nil, errf("GLOBAL_SKILL_READONLY", 403, "全局技能不能通过 memory_write 写入",
			"全局技能只能经 skill_promote(action=submit) 提交并审批；修改项目技能请用 skills/<name>/SKILL.md。")
	}

	// journal 文件名由服务端统一为 <姓名或会话标签>-<hhmmss>.md，保证「谁写的」一眼可见；
	// 仅对 create 生效，append/replace 仍按调用方给出的路径。
	if req.Mode == "create" && isJournalPath(rel) {
		rel = s.journalPath(slug, rel, authorName(req.UserName, req.Session))
	}

	unlock := s.st.Lock(slug + "|" + rel)
	defer unlock()

	old, _, rerr := s.st.ReadFile(slug, rel)
	exists := rerr == nil
	oldHash := ""
	if exists {
		oldHash = sha256Hex(old)
	}

	isTopic := strings.HasPrefix(rel, "topics/")
	newTopic := isTopic && !exists

	if req.Mode == "create" && exists {
		return nil, &Error{Code: "ALREADY_EXISTS", Status: 409, Message: "文件已存在", Hint: "要追加内容用 mode=append，要整篇改写用 mode=replace 并带 expectedHash。", Extra: map[string]any{"hash": oldHash, "path": rel}}
	}
	if req.Mode == "replace" {
		if strings.TrimSpace(req.ExpectedHash) == "" {
			return nil, &Error{Code: "EXPECTED_HASH_REQUIRED", Status: 409, Message: "replace 必须带 expectedHash", Hint: "先 memory_read 拿到 hash，再带上 expectedHash 重试。", Extra: map[string]any{"hash": oldHash}}
		}
		if !exists {
			return nil, errf("NOT_FOUND", 404, "文件不存在，无法 replace", "新建文件用 mode=create。")
		}
		if req.ExpectedHash != oldHash {
			return nil, &Error{Code: "HASH_MISMATCH", Status: 409, Message: "内容已被其他人改过", Hint: "重新 memory_read 拿最新内容与 hash，合并后再 replace。", Extra: map[string]any{"hash": oldHash, "path": rel}}
		}
	}
	if req.Mode == "append" && req.Content == "" {
		return nil, errf("BAD_REQUEST", 400, "content 不能为空", "")
	}
	if newTopic && (req.Index == nil || strings.TrimSpace(req.Index.Scene) == "" || len(req.Index.Examples) == 0) {
		return nil, &Error{
			Code:    "INDEX_ROW_REQUIRED",
			Status:  409,
			Message: "新建主题必须同时提供索引行",
			Hint:    "在 index 里给 scene（什么场景必须读）和 examples（2~3 个真实提问），服务端会写进 RULE.md 的索引表。",
			Extra:   map[string]any{"sample": `index: {"scene":"涉及支付回调、重复扣款、幂等键的改动前必读","examples":["回调为什么会重复扣款？","幂等键怎么定？"]}`},
		}
	}

	var newContent []byte
	switch req.Mode {
	case "create":
		newContent = []byte(req.Content)
	case "append":
		buf := append([]byte{}, old...)
		if len(buf) > 0 && buf[len(buf)-1] != '\n' {
			buf = append(buf, '\n')
		}
		buf = append(buf, []byte(req.Content)...)
		newContent = buf
	case "replace":
		newContent = []byte(req.Content)
	}
	if len(newContent) == 0 {
		newContent = []byte(req.Content)
	}

	// 写项目技能时校验 frontmatter：必须含 name 与 description（对齐 Agent Skills 的 SKILL.md）。
	if skillRelName(rel) != "" {
		if _, ve := validateSkillDoc(newContent); ve != nil {
			return nil, ve
		}
	}
	// TASKS.md 由服务端补「推进者（自报）」与「更新时间」，不让模型手填时间。
	if rel == "TASKS.md" {
		newContent = []byte(normalizeTasks(string(newContent), req.Session, req.UserName, s.now()))
	}

	// 只有覆盖写才留快照；append 不改旧内容。
	if exists && req.Mode == "replace" {
		if err := s.st.Snapshot(slug, rel, old); err != nil {
			return nil, errf("INTERNAL", 500, "写历史快照失败", err.Error())
		}
	}
	// append 的快路径要求旧内容前缀不变；TASKS.md 会被规整（表头升级），必须整篇重写。
	if req.Mode == "append" && rel != "TASKS.md" {
		if err := s.st.Append(slug, rel, newContent[len(old):]); err != nil {
			return nil, errf("INTERNAL", 500, "追加失败", err.Error())
		}
	} else {
		if err := s.st.WriteAtomic(slug, rel, newContent); err != nil {
			return nil, errf("INTERNAL", 500, "写入失败", err.Error())
		}
	}

	indexUpdated := false
	if newTopic {
		ok, e := s.insertIndexRow(slug, rel, *req.Index, titleOf(req.Content, rel))
		if e != nil {
			return nil, e
		}
		indexUpdated = ok
	}

	journalIndexed := false
	if isJournalPath(rel) {
		if err := s.appendJournalIndex(slug, rel, newContent, authorName(req.UserName, req.Session)); err == nil {
			journalIndexed = true
		}
	}

	rule, _, _ := s.st.ReadFile(slug, "RULE.md")
	warnings := s.warnings(slug, string(rule))
	// 写入其它文件时，若 TASKS.md 里有其他会话登记了同一路径，追加一条提示（不阻断写入）。
	if rel != "TASKS.md" {
		if tasks, _, e := s.st.ReadFile(slug, "TASKS.md"); e == nil {
			warnings = append(warnings, tasksConflicts(string(tasks), rel, req.Session, req.UserName)...)
		}
	}
	resp := &WriteResp{
		OK:           true,
		Path:         rel,
		Hash:         sha256Hex(newContent),
		Lines:        strings.Count(string(newContent), "\n") + 1,
		IndexUpdated: indexUpdated,
		Warnings:     warnings,
	}
	if journalIndexed {
		resp.Hint = "已追加当月 journal 索引行。"
	}
	resp.Next = nextHint(rel, newTopic, journalIndexed)
	if len(newContent) == 0 {
		resp.Lines = 0
	}
	return resp, nil
}

// nextHint 是写入成功后的下一步提示，避免模型「写完就走」漏掉收尾或索引同步。
func nextHint(rel string, newTopic, journalIndexed bool) string {
	switch {
	case journalIndexed:
		return "journal 只做流水：确认结论已写进对应主题文档；无事可记时写「本次无需记录：原因」即可。"
	case newTopic:
		return "索引行已插入 RULE.md；收尾再写 journal（≤300 字）。"
	case rel == "TASKS.md":
		return "TASKS.md 只保留进行中与待办；完成后删除对应行。"
	case rel == "RULE.md":
		return "索引已更新；若改了主题的适用场景，同步改主题文档里的结论。"
	case strings.HasPrefix(rel, "topics/"):
		return "主题已更新；若适用场景变了，用 replace 同步 RULE.md 对应行。"
	default:
		return ""
	}
}

func (s *Service) insertIndexRow(slug, rel string, row IndexRow, title string) (bool, *Error) {
	unlock := s.st.Lock(slug + "|RULE.md")
	defer unlock()

	rule, _, err := s.st.ReadFile(slug, "RULE.md")
	if err != nil {
		return false, errf("INTERNAL", 500, "读取 RULE.md 失败", err.Error())
	}
	examples := make([]string, 0, len(row.Examples))
	for _, ex := range row.Examples {
		ex = strings.TrimSpace(ex)
		if ex == "" {
			continue
		}
		if !strings.HasPrefix(ex, `"`) {
			ex = `"` + ex + `"`
		}
		examples = append(examples, ex)
	}
	newRule, inserted, ierr := InsertRow(string(rule), Row{
		Title:    title,
		Path:     rel,
		Scene:    row.Scene,
		Examples: strings.Join(examples, " "),
	})
	if ierr != nil {
		return false, errf("BAD_REQUEST", 400, ierr.Error(), "scene 与 examples 都必须非空")
	}
	if !inserted {
		return false, nil
	}
	if err := s.st.Snapshot(slug, "RULE.md", rule); err != nil {
		return false, errf("INTERNAL", 500, "写历史快照失败", err.Error())
	}
	if err := s.st.WriteAtomic(slug, "RULE.md", []byte(newRule)); err != nil {
		return false, errf("INTERNAL", 500, "写 RULE.md 失败", err.Error())
	}
	// 索引是我们自己补的，不算使用者改了规则：把本项目的 session 对齐到新 hash。
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
	return true, nil
}

func (s *Service) reindex(slug string, req WriteReq) (*WriteResp, *Error) {
	if strings.TrimSpace(req.Path) != "RULE.md" {
		return nil, errf("BAD_REQUEST", 400, "reindex 只能作用于 RULE.md", "")
	}
	unlock := s.st.Lock(slug + "|RULE.md")
	defer unlock()

	rule, _, err := s.st.ReadFile(slug, "RULE.md")
	if err != nil {
		return nil, errf("NOT_FOUND", 404, "RULE.md 不存在", "")
	}
	md := string(rule)
	idx := ParseIndex(md)
	topics, err := s.st.ListTopics(slug)
	if err != nil {
		return nil, errf("INTERNAL", 500, "列主题失败", err.Error())
	}
	kept := len(idx.Rows)
	var added []string
	for _, t := range topics {
		if _, ok := idx.Find(t); ok {
			continue
		}
		body, _, rerr := s.st.ReadFile(slug, t)
		if rerr != nil {
			continue
		}
		title := strings.TrimSuffix(path.Base(t), ".md")
		scene := title + "：" + firstParagraph(string(body), 60)
		examples := `"` + title + `里有哪些内容？"`
		newMd, _, ierr := InsertRow(md, Row{Title: title, Path: t, Scene: scene, Examples: examples, Auto: true})
		if ierr != nil || newMd == md {
			continue
		}
		md = newMd
		added = append(added, t)
		idx = ParseIndex(md)
	}
	changed := len(added) > 0
	if changed {
		if err := s.st.Snapshot(slug, "RULE.md", rule); err != nil {
			return nil, errf("INTERNAL", 500, "写历史快照失败", err.Error())
		}
		if err := s.st.WriteAtomic(slug, "RULE.md", []byte(md)); err != nil {
			return nil, errf("INTERNAL", 500, "写 RULE.md 失败", err.Error())
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
	}
	h := sha256Hex([]byte(md))
	resp := &WriteResp{
		OK:           true,
		Path:         "RULE.md",
		Hash:         h,
		Lines:        strings.Count(md, "\n") + 1,
		IndexUpdated: changed,
		Warnings:     s.warnings(slug, md),
		Hint:         fmt.Sprintf("新增 %d 行，保留 %d 行；不删除任何已有行。", len(added), kept),
	}
	if len(added) > 0 {
		resp.Next = "补的行带 <!-- auto -->：请把场景与举例替换成真实内容，否则模型会读错文档。"
	}
	return resp, nil
}

func (s *Service) appendJournalIndex(slug, rel string, content []byte, author string) error {
	rest := strings.TrimPrefix(rel, "journal/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 {
		return nil
	}
	ym, base := parts[0], parts[1]
	if !isYearMonth(ym) {
		return nil
	}
	indexRel := "journal/" + ym + ".INDEX.md"
	unlock := s.st.Lock(slug + "|" + indexRel)
	defer unlock()

	cur, _, err := s.st.ReadFile(slug, indexRel)
	if err != nil {
		cur = []byte(journalIndexHeader(ym))
	}
	label, _ := splitJournalName(base)
	if author != "" {
		label = sanitizeFilePart(author)
	}
	line := fmt.Sprintf("| %s | %s | %s | %s |\n", s.now().Format("2006-01-02"), label, summarize(content), base)
	return s.st.WriteAtomic(slug, indexRel, append(cur, []byte(line)...))
}

func isJournalPath(rel string) bool {
	if !strings.HasPrefix(rel, "journal/") || strings.HasSuffix(rel, ".INDEX.md") {
		return false
	}
	return strings.Count(strings.TrimPrefix(rel, "journal/"), "/") == 1
}

// authorName 返回写入者标签：优先 userName，缺省回落到会话标签。
func authorName(userName, session string) string {
	if userName != "" {
		return userName
	}
	return session
}

// journalPath 把 journal 路径改写成 journal/<YYYY-MM>/<作者>-<hhmmss>.md；
// 同一作者同一秒重复写入时追加 -2、-3 去重。
func (s *Service) journalPath(slug, rel, author string) string {
	parts := strings.Split(strings.TrimPrefix(rel, "journal/"), "/")
	if len(parts) != 2 || !isYearMonth(parts[0]) {
		return rel
	}
	ym := parts[0]
	base := sanitizeFilePart(author) + "-" + s.now().Format("150405")
	candidate := "journal/" + ym + "/" + base + ".md"
	for i := 2; ; i++ {
		if _, _, err := s.st.ReadFile(slug, candidate); err != nil {
			return candidate
		}
		candidate = fmt.Sprintf("journal/%s/%s-%d.md", ym, base, i)
	}
}

// sanitizeFilePart 只做最小清洗：去掉路径分隔符、控制字符和会破坏表格/路径的字符，
// 保留中文与内部空格；清洗后为空则用 anon 兜底。
func sanitizeFilePart(author string) string {
	var b strings.Builder
	for _, r := range author {
		if r == '/' || r == '\\' || r == '|' || r == ':' || r == '*' || r == '?' ||
			r == '"' || r == '<' || r == '>' || unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	name := strings.Trim(strings.TrimSpace(b.String()), ".")
	if name == "" {
		return "anon"
	}
	return truncateRunes(name, 32)
}

func isYearMonth(s string) bool {
	if len(s) != 7 || s[4] != '-' {
		return false
	}
	for i, r := range s {
		if i == 4 {
			continue
		}
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func splitJournalName(base string) (label, hhmm string) {
	name := strings.TrimSuffix(base, ".md")
	if i := strings.LastIndex(name, "-"); i > 0 {
		return name[:i], name[i+1:]
	}
	return name, ""
}

func summarize(content []byte) string {
	for _, raw := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		line = strings.TrimLeft(line, "#>")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		return truncateRunes(line, 40)
	}
	return ""
}

func titleOf(content, rel string) string {
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return strings.TrimSuffix(path.Base(rel), ".md")
}

func firstParagraph(content string, limit int) string {
	inHeading := false
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "#") {
			inHeading = true
			continue
		}
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, ">") {
			return truncateRunes(strings.TrimSpace(strings.TrimPrefix(line, ">")), limit)
		}
		if inHeading || line != "" {
			return truncateRunes(line, limit)
		}
	}
	return ""
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

func (s *Service) warnings(slug, ruleMD string) []Warning {
	var out []Warning
	idx := ParseIndex(ruleMD)
	topics, _ := s.st.ListTopics(slug)
	for _, t := range topics {
		if _, ok := idx.Find(t); !ok {
			out = append(out, Warning{
				Type: "missing_in_index",
				Path: t,
				Hint: "这篇主题还没登记到 RULE.md 索引表，模型找不到它。用 memory_write(mode=replace) 补行，或 mode=reindex 批量补默认行。",
			})
		}
	}
	for _, r := range idx.Rows {
		if !s.st.Exists(slug, r.Path) {
			out = append(out, Warning{
				Type: "missing_file",
				Path: r.Path,
				Hint: "索引指向的主题文件不存在（改过名或删了）。补文件或手工删掉这行索引；reindex 不会自动删行。",
			})
		}
	}
	if len(idx.Rows) > indexMaxRows {
		out = append(out, Warning{
			Type: "index_too_large",
			Rows: len(idx.Rows),
			Hint: "索引超过 25 行，建议按领域拆到 topics/_index/<领域>.md，主索引只保留领域行。",
		})
	}
	for _, r := range idx.Rows {
		if utf8.RuneCountInString(r.Scene) > maxSceneRunes || utf8.RuneCountInString(r.Examples) > maxExampleRunes {
			out = append(out, Warning{
				Type: "row_too_long",
				Path: r.Path,
				Hint: "这一行的「适用场景」超过 120 字或「举例」超过 200 字，建议缩短成触发条件 + 2~3 个提问。",
			})
		}
	}
	return out
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
