package kb

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"team-memory/internal/store"
)

func newSvc(t *testing.T) *Service {
	t.Helper()
	root := t.TempDir()
	svc := New(store.New(root))
	if err := svc.EnsureServiceLayout(); err != nil {
		t.Fatalf("EnsureServiceLayout: %v", err)
	}
	return svc
}

func mustRules(t *testing.T, svc *Service, slug string) *RulesResp {
	t.Helper()
	r, e := svc.GetRules(slug)
	if e != nil {
		t.Fatalf("GetRules: %v", e)
	}
	return r
}

func TestFullChain(t *testing.T) {
	svc := newSvc(t)
	rules := mustRules(t, svc, "demo")
	if rules.Session == "" || rules.RuleHash == "" {
		t.Fatal("session/rule_hash 为空")
	}
	if !strings.Contains(rules.Index, "## 文档索引") {
		t.Fatalf("索引不完整: %s", rules.Index)
	}

	// 1. 新主题缺索引行 → 409
	_, e := svc.Write("demo", WriteReq{
		Path: "topics/支付回调.md", Mode: "create", Session: rules.Session,
		Content: "# 支付回调\n> 渠道通知。\n\n## 结论\n- 幂等键 = channel + orderNo。\n",
	})
	if e == nil || e.Code != "INDEX_ROW_REQUIRED" || e.Status != 409 {
		t.Fatalf("期望 INDEX_ROW_REQUIRED 409，得到 %+v", e)
	}

	// 2. 带索引行 → 成功且索引表出现该行
	w, e := svc.Write("demo", WriteReq{
		Path: "topics/支付回调.md", Mode: "create", Session: rules.Session,
		Content: "# 支付回调\n> 渠道通知。\n\n## 结论\n- 幂等键 = channel + orderNo。\n",
		Index:   &IndexRow{Scene: "涉及渠道回调、重复扣款、幂等键的改动前必读", Examples: []string{"回调为什么会重复扣款？", "幂等键怎么定？"}},
	})
	if e != nil {
		t.Fatalf("create topic: %v", e)
	}
	if !w.IndexUpdated {
		t.Fatal("索引未更新")
	}
	ruleMD, _, err := svc.st.ReadFile("demo", "RULE.md")
	if err != nil {
		t.Fatal(err)
	}
	idx := ParseIndex(string(ruleMD))
	if _, ok := idx.Find("topics/支付回调.md"); !ok {
		t.Fatalf("索引里没有新主题:\n%s", ruleMD)
	}
	if !strings.Contains(string(ruleMD), "回调为什么会重复扣款？") {
		t.Fatal("索引行缺少举例")
	}

	// 3. 读主题
	rd, e := svc.Read("demo", "topics/支付回调.md", "", 0)
	if e != nil || !strings.Contains(rd.Content, "channel + orderNo") {
		t.Fatalf("read: %+v %v", rd, e)
	}

	// 4. journal + 月度索引自动追加
	if _, e := svc.Write("demo", WriteReq{
		Path: "journal/2026-11/zhangsan-1015.md", Mode: "create", Session: rules.Session,
		Content: "# 回调重复扣款定位\n幂等键缺失导致重复入账，已补唯一索引。\n",
	}); e != nil {
		t.Fatalf("journal: %v", e)
	}
	ji, _, err := svc.st.ReadFile("demo", "journal/2026-11.INDEX.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ji), rules.Session) || !strings.Contains(string(ji), "回调重复扣款定位") || !strings.Contains(string(ji), ".md |") {
		t.Fatalf("月度索引不对:\n%s", ji)
	}

	// 5. replace 必须带 hash；hash 不符 409；相符后留快照
	_, e = svc.Write("demo", WriteReq{Path: "topics/支付回调.md", Mode: "replace", Session: rules.Session, Content: "x"})
	if e == nil || e.Code != "EXPECTED_HASH_REQUIRED" {
		t.Fatalf("期望 EXPECTED_HASH_REQUIRED，得到 %+v", e)
	}
	_, e = svc.Write("demo", WriteReq{Path: "topics/支付回调.md", Mode: "replace", Session: rules.Session, Content: "x", ExpectedHash: "deadbeef"})
	if e == nil || e.Code != "HASH_MISMATCH" || e.Status != 409 {
		t.Fatalf("期望 HASH_MISMATCH 409，得到 %+v", e)
	}
	cur, _, _ := svc.st.ReadFile("demo", "topics/支付回调.md")
	newBody := strings.Replace(string(cur), "channel + orderNo", "channel + orderNo + 渠道单号", 1)
	if _, e := svc.Write("demo", WriteReq{
		Path: "topics/支付回调.md", Mode: "replace", Session: rules.Session,
		Content: newBody, ExpectedHash: sha256Hex(cur),
	}); e != nil {
		t.Fatalf("replace: %v", e)
	}
	snaps, err := svc.st.Snapshots("demo", "topics/支付回调.md")
	if err != nil || len(snaps) == 0 {
		t.Fatalf("缺少历史快照: %v %v", snaps, err)
	}
	old, err := svc.st.ReadSnapshot("demo", "topics/支付回调.md", snaps[0])
	if err != nil {
		t.Fatalf("读快照: %v", err)
	}
	if !strings.Contains(string(old), "channel + orderNo") || strings.Contains(string(old), "渠道单号") {
		t.Fatal("快照应为覆盖前的旧内容")
	}

	// 6. 未读 rule 就写 → 428
	_, e = svc.Write("demo", WriteReq{Path: "TASKS.md", Mode: "append", Session: "s_nope", Content: "- x\n"})
	if e == nil || e.Code != "READ_RULES_FIRST" || e.Status != 428 {
		t.Fatalf("期望 READ_RULES_FIRST 428，得到 %+v", e)
	}

	// 7. 人工改 RULE.md 后旧 session 失效
	sid := rules.Session
	ruleNow, _, _ := svc.st.ReadFile("demo", "RULE.md")
	if _, e := svc.Write("demo", WriteReq{
		Path: "RULE.md", Mode: "replace", Session: sid,
		Content: string(ruleNow) + "\n<!-- manual -->\n", ExpectedHash: sha256Hex(ruleNow),
	}); e != nil {
		t.Fatalf("手工改 RULE: %v", e)
	}
	_, e = svc.Write("demo", WriteReq{Path: "TASKS.md", Mode: "append", Session: sid, Content: "- x\n"})
	if e == nil || e.Code != "RULE_CHANGED" {
		t.Fatalf("期望 RULE_CHANGED，得到 %+v", e)
	}
}

func TestReindexOnlyAdds(t *testing.T) {
	svc := newSvc(t)
	rules := mustRules(t, svc, "demo")
	if _, e := svc.Write("demo", WriteReq{
		Path: "topics/退款.md", Mode: "create", Session: rules.Session,
		Content: "# 退款\n> 退款申请与回调。\n",
		Index:   &IndexRow{Scene: "涉及退款", Examples: []string{"退款怎么退？"}},
	}); e != nil {
		t.Fatal(e)
	}
	// 绕过服务直接放一篇，模拟外部编辑
	p := filepath.Join(svc.st.Root(), "projects", "demo", "topics", "对账.md")
	if err := os.WriteFile(p, []byte("# 对账\n> 日终对账。\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rules2 := mustRules(t, svc, "demo")
	if !hasWarning(rules2.Warnings, "missing_in_index", "topics/对账.md") {
		t.Fatalf("应报 missing_in_index: %+v", rules2.Warnings)
	}
	before, _, _ := svc.st.ReadFile("demo", "RULE.md")
	w, e := svc.Write("demo", WriteReq{Path: "RULE.md", Mode: "reindex", Session: rules2.Session})
	if e != nil {
		t.Fatalf("reindex: %v", e)
	}
	if !w.IndexUpdated {
		t.Fatal("reindex 应新增行")
	}
	after, _, _ := svc.st.ReadFile("demo", "RULE.md")
	if !strings.Contains(string(after), "topics/对账.md") || !strings.Contains(string(after), "<!-- auto -->") {
		t.Fatalf("reindex 结果不对:\n%s", after)
	}
	// 已有行必须原样保留（不删）
	if !strings.Contains(string(after), "topics/退款.md") || strings.Count(string(after), "topics/退款.md") != 1 {
		t.Fatal("reindex 动了已有行")
	}
	if len(after) < len(before) {
		t.Fatal("reindex 后内容变短")
	}
	// 再跑一次应幂等
	w2, e := svc.Write("demo", WriteReq{Path: "RULE.md", Mode: "reindex", Session: rules2.Session})
	if e != nil || w2.IndexUpdated {
		t.Fatalf("reindex 不幂等: %+v %v", w2, e)
	}
}

func TestIndexScaleWarnings(t *testing.T) {
	svc := newSvc(t)
	rules := mustRules(t, svc, "demo")
	for i := 0; i < indexMaxRows+1; i++ {
		name := fmt.Sprintf("topics/t%02d.md", i)
		if _, e := svc.Write("demo", WriteReq{
			Path: name, Mode: "create", Session: rules.Session, Content: fmt.Sprintf("# t%02d\n", i),
			Index: &IndexRow{Scene: "场景 " + strings.Repeat("长", maxSceneRunes+5), Examples: []string{"问？"}},
		}); e != nil {
			t.Fatalf("write %s: %v", name, e)
		}
	}
	rules2 := mustRules(t, svc, "demo")
	if !hasWarning(rules2.Warnings, "index_too_large", "") {
		t.Fatalf("应报 index_too_large: %+v", rules2.Warnings)
	}
	if !hasWarning(rules2.Warnings, "row_too_long", "topics/t00.md") {
		t.Fatalf("应报 row_too_long: %+v", rules2.Warnings)
	}
}

func TestConcurrentAppendNoLoss(t *testing.T) {
	svc := newSvc(t)
	rules := mustRules(t, svc, "demo")
	if _, e := svc.Write("demo", WriteReq{
		Path: "topics/并发.md", Mode: "create", Session: rules.Session, Content: "# 并发\n",
		Index: &IndexRow{Scene: "并发测试", Examples: []string{"并发？"}},
	}); e != nil {
		t.Fatal(e)
	}
	const n = 40
	var wg sync.WaitGroup
	errs := make(chan *Error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, e := svc.Write("demo", WriteReq{
				Path: "topics/并发.md", Mode: "append", Session: rules.Session,
				Content: fmt.Sprintf("- line-%02d\n", i),
			})
			errs <- e
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatalf("并发追加失败: %v", e)
		}
	}
	body, _, _ := svc.st.ReadFile("demo", "topics/并发.md")
	for i := 0; i < n; i++ {
		if strings.Count(string(body), fmt.Sprintf("- line-%02d", i)) != 1 {
			t.Fatalf("第 %d 行丢失或重复", i)
		}
	}
}

func TestPathTraversalRejected(t *testing.T) {
	svc := newSvc(t)
	rules := mustRules(t, svc, "demo")
	bad := []string{"../RULE.md", "/etc/passwd.md", "topics/../../x.md", "topics/x.txt", "_service/RULES.md", ".history/RULE.md/x.md"}
	for _, p := range bad {
		if _, e := svc.Write("demo", WriteReq{Path: p, Mode: "create", Session: rules.Session, Content: "x"}); e == nil || e.Code != "PATH_NOT_ALLOWED" {
			t.Fatalf("%s 应被拒，得到 %+v", p, e)
		}
	}
	if _, e := svc.Read("demo", "../../etc/passwd.md", "", 0); e == nil || e.Code != "PATH_NOT_ALLOWED" {
		t.Fatalf("read 越界应被拒: %+v", e)
	}
	if _, e := svc.GetRules("../evil"); e == nil {
		t.Fatal("非法项目名应被拒")
	}
}

func TestIndexWarningsAndJournalIndex(t *testing.T) {
	svc := newSvc(t)
	rules := mustRules(t, svc, "demo")
	if _, e := svc.Write("demo", WriteReq{
		Path: "topics/退款.md", Mode: "create", Session: rules.Session,
		Content: "# 退款\n> 退款申请与回调。\n",
		Index:   &IndexRow{Scene: "涉及退款", Examples: []string{"退款怎么退？"}},
	}); e != nil {
		t.Fatal(e)
	}
	// 索引有行但文件被删 → missing_file，且 reindex 不自动删行
	if err := os.Remove(filepath.Join(svc.st.Root(), "projects", "demo", "topics", "退款.md")); err != nil {
		t.Fatal(err)
	}
	rules2 := mustRules(t, svc, "demo")
	if !hasWarning(rules2.Warnings, "missing_file", "topics/退款.md") {
		t.Fatalf("应报 missing_file: %+v", rules2.Warnings)
	}
	if _, e := svc.Write("demo", WriteReq{Path: "RULE.md", Mode: "reindex", Session: rules2.Session}); e != nil {
		t.Fatalf("reindex: %v", e)
	}
	after, _, _ := svc.st.ReadFile("demo", "RULE.md")
	if !strings.Contains(string(after), "topics/退款.md") {
		t.Fatal("reindex 不应删除 missing_file 的行")
	}

	// journal 月度索引：同月两次写入各追加一行，且 .INDEX.md 自身不再被索引
	ym := time.Now().Format("2006-01")
	j1 := "journal/" + ym + "/zhangsan-1015.md"
	j2 := "journal/" + ym + "/lisi-1420.md"
	for name, summary := range map[string]string{j1: "# 回调重复扣款定位\n结论：已定位。\n", j2: "# 退款重试结论\n结论：已确认。\n"} {
		if _, e := svc.Write("demo", WriteReq{
			Path: name, Mode: "create", Session: rules2.Session, Content: summary,
		}); e != nil {
			t.Fatalf("journal %s: %v", name, e)
		}
	}
	ji, _, err := svc.st.ReadFile("demo", "journal/"+ym+".INDEX.md")
	if err != nil {
		t.Fatal(err)
	}
	body := string(ji)
	if strings.Count(body, "| "+rules2.Session+" |") != 2 {
		t.Fatalf("月度索引行数不对:\n%s", body)
	}
	if strings.Contains(body, ".INDEX.md") {
		t.Fatalf("月度索引不应把自身再登记一行:\n%s", body)
	}
	rules3 := mustRules(t, svc, "demo")
	wantIndex := "journal/" + ym + ".INDEX.md"
	found := false
	for _, p := range rules3.JournalIndex {
		if p == wantIndex {
			found = true
		}
	}
	if !found {
		t.Fatalf("get_rules.journal_index 应含当月索引: %v", rules3.JournalIndex)
	}
	if rules3.JournalWindow.Files < 2 {
		t.Fatalf("journal_window.files = %d", rules3.JournalWindow.Files)
	}
}

func TestRecordReminderAndNextHints(t *testing.T) {
	svc := newSvc(t)
	rules := mustRules(t, svc, "demo")
	if rules.DaysSinceWrite != -1 || rules.LastWrite != "" {
		t.Fatalf("新项目应为 days=-1 / last_write 空: %+v", rules)
	}
	if !strings.Contains(rules.RecordReminder, "还没有任何结论记录") {
		t.Fatalf("新项目应提示还没记录: %q", rules.RecordReminder)
	}

	w, e := svc.Write("demo", WriteReq{
		Path: "topics/支付回调.md", Mode: "create", Session: rules.Session,
		Content: "# 支付回调\n> 渠道通知。\n\n## 结论\n- 幂等键 = channel + orderNo。\n",
		Index:   &IndexRow{Scene: "涉及渠道回调的改动前必读", Examples: []string{"回调为什么会重复扣款？"}},
	})
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(w.Next, "journal") {
		t.Fatalf("新建主题后 next 应提示写 journal: %q", w.Next)
	}

	rules2 := mustRules(t, svc, "demo")
	if rules2.DaysSinceWrite != 0 || rules2.LastWrite == "" || rules2.RecordReminder != "" {
		t.Fatalf("刚写完应为 0 天、有 last_write、无提醒: %+v", rules2)
	}

	// 把主题文件 mtime 拨回 10 天前：应触发「距上次记录」提醒。
	p := filepath.Join(svc.st.Root(), "projects", "demo", "topics", "支付回调.md")
	old := time.Now().Add(-10 * 24 * time.Hour)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	rules3 := mustRules(t, svc, "demo")
	if rules3.DaysSinceWrite < 9 || !strings.Contains(rules3.RecordReminder, "距上次记录") {
		t.Fatalf("10 天未写应提醒: days=%d reminder=%q", rules3.DaysSinceWrite, rules3.RecordReminder)
	}

	ym := time.Now().Format("2006-01")
	jw, e := svc.Write("demo", WriteReq{
		Path: "journal/" + ym + "/zhangsan-1015.md", Mode: "create", Session: rules.Session,
		Content: "# 定位结论\n已定位。\n",
	})
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(jw.Next, "本次无需记录") {
		t.Fatalf("journal 后 next 应提示确认结论或写明无需记录: %q", jw.Next)
	}
}

func TestReindexNextHint(t *testing.T) {
	svc := newSvc(t)
	rules := mustRules(t, svc, "demo")
	if err := svc.st.WriteAtomic("demo", "topics/未登记.md", []byte("# 未登记\n> 直接写入。\n")); err != nil {
		t.Fatal(err)
	}
	w, e := svc.Write("demo", WriteReq{Path: "RULE.md", Mode: "reindex", Session: rules.Session})
	if e != nil {
		t.Fatal(e)
	}
	if !w.IndexUpdated || !strings.Contains(w.Next, "auto") {
		t.Fatalf("reindex 应提示替换 <!-- auto --> 行: %+v", w)
	}
}

func TestRulesTemplatesExplainHowToWrite(t *testing.T) {
	rules := mustRules(t, newSvc(t), "demo")
	for _, want := range []string{
		"必须记录", "定了方案或做了取舍", "踩坑并解决", "接口、表结构、配置项、依赖版本发生变更",
		"任务状态变化", "纠正了记忆里的旧结论", "本次无需记录",
		"## 主题文档骨架", "## 结论（现在就该这么做）", "## 代码位置",
	} {
		if !strings.Contains(rules.ServiceRule, want) {
			t.Errorf("服务级规则缺少 %q", want)
		}
	}
	for _, want := range []string{"以服务级规则第 4 条为准", `"scene":`, "本次无需记录", "reindex"} {
		if !strings.Contains(rules.Index, want) {
			t.Errorf("RULE.md 模板缺少 %q", want)
		}
	}
}

func hasWarning(ws []Warning, typ, path string) bool {
	for _, w := range ws {
		if w.Type == typ && (path == "" || w.Path == path) {
			return true
		}
	}
	return false
}

// 按字节分页不能把多字节字符切开：否则客户端续读拼回来的中文会出现乱码，
// 编辑保存还会把乱码写回服务端。
func TestReadPagingKeepsRunes(t *testing.T) {
	svc := newSvc(t)
	rules := mustRules(t, svc, "demo")
	body := "# 大文件\n\n" + strings.Repeat("中文分页读取测试。", 9000)
	if _, e := svc.Write("demo", WriteReq{
		Path: "topics/大文件.md", Mode: "create", Session: rules.Session, Content: body,
		Index: &IndexRow{Scene: "验证大文件分页读取", Examples: []string{"大文件能读全吗？"}},
	}); e != nil {
		t.Fatalf("写入大文件: %v", e)
	}

	var got strings.Builder
	offset := 0
	for page := 0; page < 50; page++ {
		resp, e := svc.Read("demo", "topics/大文件.md", "", offset)
		if e != nil {
			t.Fatalf("第 %d 页读取失败: %v", page, e)
		}
		if strings.ContainsRune(resp.Content, utf8.RuneError) {
			t.Fatalf("第 %d 页出现替换字符（offset=%d）", page, offset)
		}
		got.WriteString(resp.Content)
		if !resp.Truncated {
			break
		}
		offset = resp.NextOffset
	}
	if got.String() != body {
		t.Fatalf("续读拼接与原内容不一致：原 %d 字符，读回 %d 字符", len(body), got.Len())
	}
}

func TestJournalIndexHeaderAndLegacyCompat(t *testing.T) {
	svc := newSvc(t)
	rules := mustRules(t, svc, "demo")

	// 新建月索引：表头用「会话标签」
	if _, e := svc.Write("demo", WriteReq{
		Path: "journal/2026-07/zhangsan-0930.md", Mode: "create", Session: rules.Session,
		Content: "# 新表头\n结论。\n",
	}); e != nil {
		t.Fatalf("journal: %v", e)
	}
	body, _, err := svc.st.ReadFile("demo", "journal/2026-07.INDEX.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "| 日期 | 会话标签 | 一句话 | 文件 |") {
		t.Fatalf("新索引表头应为「会话标签」:\n%s", body)
	}

	// 旧月索引：保留「人」表头，只追加行，不重写整表
	legacy := "| 日期 | 人 | 一句话 | 文件 |\n| --- | --- | --- | --- |\n| 2026-06-01 | zhangsan | 旧行 | old.md |\n"
	if e := svc.st.WriteAtomic("demo", "journal/2026-06.INDEX.md", []byte(legacy)); e != nil {
		t.Fatalf("写旧索引: %v", e)
	}
	if _, e := svc.Write("demo", WriteReq{
		Path: "journal/2026-06/lisi-1010.md", Mode: "create", Session: rules.Session,
		Content: "# 旧表头追加\n结论。\n",
	}); e != nil {
		t.Fatalf("journal: %v", e)
	}
	old, _, err := svc.st.ReadFile("demo", "journal/2026-06.INDEX.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(old), "| 日期 | 人 | 一句话 | 文件 |") || strings.Contains(string(old), "会话标签") {
		t.Fatalf("旧表头不应被重写:\n%s", old)
	}
	if !strings.Contains(string(old), "old.md") || !strings.Contains(string(old), rules.Session) {
		t.Fatalf("旧索引应保留旧行并追加新行:\n%s", old)
	}
}
