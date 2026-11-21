package kb

import (
	"strings"
	"testing"
	"time"
)

func fixedNow() time.Time {
	return time.Date(2026, 11, 21, 10, 30, 0, 0, time.Local)
}

func TestNormalizeTasks(t *testing.T) {
	stamp := "2026-11-21 10:30"
	cases := []struct {
		name     string
		in       string
		session  string
		userName string
		want     []string
		notWant  []string
	}{
		{
			name:    "旧四列表头升级为六列",
			in:      "| 任务 | 状态 | 下一步 | 负责人 |\n| --- | --- | --- | --- |\n| 发版 | 待办 | 打镜像 |  |\n",
			session: "s_abcd",
			want: []string{
				"| 任务 | 状态 | 下一步 | 涉及文件 | 推进者（自报） | 更新时间 |",
				"| 发版 | 待办 | 打镜像 |  | s_abcd | " + stamp + " |",
			},
			notWant: []string{"负责人"},
		},
		{
			name:    "0.1.2 五列表头升级为六列",
			in:      "| 任务 | 状态 | 下一步 | 推进者（自报） | 更新时间 |\n| --- | --- | --- | --- | --- |\n| 补测试 | 进行中 | 写用例 | 田灵 |  |\n",
			session: "s_abcd",
			want: []string{
				"| 任务 | 状态 | 下一步 | 涉及文件 | 推进者（自报） | 更新时间 |",
				"| 补测试 | 进行中 | 写用例 |  | 田灵 | " + stamp + " |",
			},
		},
		{
			name:     "userName 优先于会话标签",
			in:       "| 任务 | 状态 | 下一步 | 涉及文件 | 推进者（自报） | 更新时间 |\n| --- | --- | --- | --- | --- | --- |\n| 改索引 | 进行中 | 写用例 | src/a.go |  |  |\n",
			session:  "s_abcd",
			userName: "田灵",
			want:     []string{"| 改索引 | 进行中 | 写用例 | src/a.go | 田灵 | " + stamp + " |"},
		},
		{
			name:    "涉及文件保留且覆盖手填时间",
			in:      "| 任务 | 状态 | 下一步 | 涉及文件 | 推进者（自报） | 更新时间 |\n| --- | --- | --- | --- | --- | --- |\n| 上线 | 待办 | 观察 | a.go、b.go | s_abcd | 明天 |\n",
			session: "s_abcd",
			want:    []string{"| 上线 | 待办 | 观察 | a.go、b.go | s_abcd | " + stamp + " |"},
			notWant: []string{"明天"},
		},
		{
			name:    "非表格内容原样保留",
			in:      "# 任务\n\n- 纯列表不处理\n",
			session: "s_abcd",
			want:    []string{"- 纯列表不处理"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeTasks(tc.in, tc.session, tc.userName, fixedNow())
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Fatalf("缺少 %q:\n%s", w, got)
				}
			}
			for _, nw := range tc.notWant {
				if strings.Contains(got, nw) {
					t.Fatalf("不应出现 %q:\n%s", nw, got)
				}
			}
		})
	}
}

func TestTasksTemplateSixColumns(t *testing.T) {
	svc := newSvc(t)
	svc.now = fixedNow
	mustRules(t, svc, "demo")

	read, e := svc.Read("demo", "TASKS.md", "", 0)
	if e != nil {
		t.Fatalf("read TASKS: %v", e)
	}
	for _, want := range []string{"| 任务 | 状态 | 下一步 | 涉及文件 | 推进者（自报） | 更新时间 |", "做完立刻删掉", "不要手写"} {
		if !strings.Contains(read.Content, want) {
			t.Fatalf("模板缺少 %q:\n%s", want, read.Content)
		}
	}
}

func TestTasksAppendUpgradeAndUserName(t *testing.T) {
	svc := newSvc(t)
	svc.now = fixedNow
	rules := mustRules(t, svc, "demo")

	legacy := "| 任务 | 状态 | 下一步 | 负责人 |\n| --- | --- | --- | --- |\n| 补测试 | 进行中 | 写用例 |  |\n"
	if err := svc.st.WriteAtomic("demo", "TASKS.md", []byte(legacy)); err != nil {
		t.Fatalf("写旧格式: %v", err)
	}

	// 旧文件读取不做解析，原样返回，不乱码。
	read, e := svc.Read("demo", "TASKS.md", "", 0)
	if e != nil || read.Content != legacy {
		t.Fatalf("旧文件应原样读出: err=%v\n%s", e, read.Content)
	}

	if _, e := svc.Write("demo", WriteReq{
		Path: "TASKS.md", Mode: "append", Session: rules.Session, UserName: "田灵",
		Content: "| 发版 | 待办 | 打 0.1.4 | src/Dockerfile, README.md | 田灵 | |\n",
	}); e != nil {
		t.Fatalf("append: %v", e)
	}

	read, _ = svc.Read("demo", "TASKS.md", "", 0)
	for _, want := range []string{
		"| 任务 | 状态 | 下一步 | 涉及文件 | 推进者（自报） | 更新时间 |",
		"| 补测试 | 进行中 | 写用例 |  | 田灵 | 2026-11-21 10:30 |",
		"| 发版 | 待办 | 打 0.1.4 | src/Dockerfile, README.md | 田灵 | 2026-11-21 10:30 |",
	} {
		if !strings.Contains(read.Content, want) {
			t.Fatalf("缺少 %q:\n%s", want, read.Content)
		}
	}
}

func TestTasksDeleteRow(t *testing.T) {
	svc := newSvc(t)
	svc.now = fixedNow
	rules := mustRules(t, svc, "demo")

	if _, e := svc.Write("demo", WriteReq{
		Path: "TASKS.md", Mode: "append", Session: rules.Session,
		Content: "| 任务A | 待办 | 无 | src/a.go | 田灵 |\n| 任务B | 待办 | 无 | src/b.go | 田灵 |\n",
	}); e != nil {
		t.Fatalf("append: %v", e)
	}

	read, _ := svc.Read("demo", "TASKS.md", "", 0)
	var kept []string
	for _, line := range strings.Split(read.Content, "\n") {
		if strings.Contains(line, "任务A") {
			continue
		}
		kept = append(kept, line)
	}
	next := strings.Join(kept, "\n")

	if _, e := svc.Write("demo", WriteReq{
		Path: "TASKS.md", Mode: "replace", Session: rules.Session,
		ExpectedHash: read.Hash, Content: next,
	}); e != nil {
		t.Fatalf("replace: %v", e)
	}

	read, _ = svc.Read("demo", "TASKS.md", "", 0)
	if strings.Contains(read.Content, "任务A") {
		t.Fatalf("已完成的任务应从表中删除:\n%s", read.Content)
	}
	if !strings.Contains(read.Content, "任务B") {
		t.Fatalf("未完成的任务应保留:\n%s", read.Content)
	}
}

func TestTasksConflictWarnings(t *testing.T) {
	svc := newSvc(t)
	svc.now = fixedNow
	s1 := mustRules(t, svc, "demo")
	s2 := mustRules(t, svc, "demo")

	// 会话 1 用姓名登记：要改 topics/索引重构.md。
	if _, e := svc.Write("demo", WriteReq{
		Path: "TASKS.md", Mode: "append", Session: s1.Session, UserName: "张三",
		Content: "| 重构索引 | 进行中 | 改解析 | topics/索引重构.md | | |\n",
	}); e != nil {
		t.Fatalf("append TASKS: %v", e)
	}

	conflicts := func(ws []Warning) []Warning {
		var out []Warning
		for _, w := range ws {
			if w.Type == "TASK_CONFLICT" {
				out = append(out, w)
			}
		}
		return out
	}

	// 会话 2 写同一文件 → 提醒，指出哪一行、谁登记。
	resp, e := svc.Write("demo", WriteReq{
		Path: "topics/索引重构.md", Mode: "create", Session: s2.Session,
		Content: "# 索引重构\n结论。\n",
		Index:   &IndexRow{Scene: "改索引前必读", Examples: []string{"索引怎么改？"}},
	})
	if e != nil {
		t.Fatalf("write topic: %v", e)
	}
	ws := conflicts(resp.Warnings)
	if len(ws) == 0 || !strings.Contains(ws[0].Hint, "张三") || !strings.Contains(ws[0].Hint, "重构索引") {
		t.Fatalf("应提示他人登记的同路径任务，实际 warnings=%+v", resp.Warnings)
	}

	// 同一会话自己写登记过的文件（同样带姓名）→ 不提醒。
	resp, e = svc.Write("demo", WriteReq{
		Path: "topics/索引重构.md", Mode: "append", Session: s1.Session, UserName: "张三",
		Content: "\n补充。\n",
	})
	if e != nil {
		t.Fatalf("append topic: %v", e)
	}
	if len(conflicts(resp.Warnings)) != 0 {
		t.Fatalf("自己登记的不应提醒: %+v", resp.Warnings)
	}

	// 另一个会话但同一 userName → 视为同一开发者，不提醒。
	resp, e = svc.Write("demo", WriteReq{
		Path: "topics/索引重构.md", Mode: "append", Session: s2.Session, UserName: "张三",
		Content: "\n再补。\n",
	})
	if e != nil {
		t.Fatalf("append topic: %v", e)
	}
	if len(conflicts(resp.Warnings)) != 0 {
		t.Fatalf("同名开发者不应提醒: %+v", resp.Warnings)
	}
}

func TestUserNameValidationAndJournalLabel(t *testing.T) {
	svc := newSvc(t)
	svc.now = fixedNow
	rules := mustRules(t, svc, "demo")

	bad := []string{strings.Repeat("长", 33), "张|三", "张\n三"}
	for _, name := range bad {
		if _, e := svc.Write("demo", WriteReq{
			Path: "TASKS.md", Mode: "append", Session: rules.Session, UserName: name,
			Content: "| x | 待办 | 无 | a.go | |\n",
		}); e == nil || e.Code != "INVALID_USER_NAME" {
			t.Fatalf("userName %q 应被拒绝，得到 %+v", name, e)
		}
	}

	// 空串视为未提供，回落会话标签。
	if _, e := svc.Write("demo", WriteReq{
		Path: "TASKS.md", Mode: "append", Session: rules.Session, UserName: "   ",
		Content: "| 回落 | 待办 | 无 | b.go | | |\n",
	}); e != nil {
		t.Fatalf("空 userName 不应报错: %v", e)
	}
	read, _ := svc.Read("demo", "TASKS.md", "", 0)
	if !strings.Contains(read.Content, "| 回落 | 待办 | 无 | b.go | "+rules.Session+" |") {
		t.Fatalf("空 userName 应回落会话标签:\n%s", read.Content)
	}

	// journal 索引的会话标签用 userName。
	if _, e := svc.Write("demo", WriteReq{
		Path: "journal/2026-11/named-1500.md", Mode: "create", Session: rules.Session, UserName: "李四",
		Content: "# 命名\n结论。\n",
	}); e != nil {
		t.Fatalf("journal: %v", e)
	}
	idx, _ := svc.Read("demo", "journal/2026-11.INDEX.md", "", 0)
	if !strings.Contains(idx.Content, "| 2026-11-21 | 李四 |") {
		t.Fatalf("索引会话标签应为 userName:\n%s", idx.Content)
	}
}

func TestJournalFileNameFromUserName(t *testing.T) {
	svc := newSvc(t)
	svc.now = fixedNow
	rules := mustRules(t, svc, "demo")

	// 中文与空格保留，路径分隔符被清洗。
	resp, e := svc.Write("demo", WriteReq{
		Path: "journal/2026-11/whatever.md", Mode: "create", Session: rules.Session, UserName: "Zhang 三 /x",
		Content: "# 命名\n结论。\n",
	})
	if e != nil {
		t.Fatalf("journal: %v", e)
	}
	if resp.Path != "journal/2026-11/Zhang 三 x-命名-103000.md" {
		t.Fatalf("文件名清洗结果不对: %s", resp.Path)
	}

	// 同一人、同一标题、同一秒再写 → -2 去重。
	resp, e = svc.Write("demo", WriteReq{
		Path: "journal/2026-11/whatever.md", Mode: "create", Session: rules.Session, UserName: "Zhang 三 /x",
		Content: "# 命名\n补充结论。\n",
	})
	if e != nil || resp.Path != "journal/2026-11/Zhang 三 x-命名-103000-2.md" {
		t.Fatalf("同名去重失败: %+v %v", resp, e)
	}

	// 未提供 userName → 回落到会话标签。
	resp, e = svc.Write("demo", WriteReq{
		Path: "journal/2026-11/whatever.md", Mode: "create", Session: rules.Session,
		Content: "# 回落\n结论。\n",
	})
	if e != nil || resp.Path != "journal/2026-11/"+rules.Session+"-回落-103000.md" {
		t.Fatalf("缺省应回落会话标签: %+v %v", resp, e)
	}

	// 索引里的「会话标签」与文件名前缀一致。
	idx, _ := svc.Read("demo", "journal/2026-11.INDEX.md", "", 0)
	if !strings.Contains(idx.Content, "| 2026-11-21 | Zhang 三 x |") || !strings.Contains(idx.Content, "| "+rules.Session+" |") {
		t.Fatalf("索引标签与文件名不一致:\n%s", idx.Content)
	}
}
