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
		name    string
		in      string
		session string
		want    []string
		notWant []string
	}{
		{
			name:    "旧四列表头升级",
			in:      "| 任务 | 状态 | 下一步 | 负责人 |\n| --- | --- | --- | --- |\n| 发版 | 待办 | 打镜像 |  |\n",
			session: "s_abcd",
			want:    []string{"| 任务 | 状态 | 下一步 | 推进者（自报） | 更新时间 |", "| 发版 | 待办 | 打镜像 | s_abcd | " + stamp + " |"},
			notWant: []string{"负责人"},
		},
		{
			name:    "保留显式推进者",
			in:      "| 任务 | 状态 | 下一步 | 推进者（自报） | 更新时间 |\n| --- | --- | --- | --- | --- |\n| 补测试 | 进行中 | 写用例 | 田灵 |  |\n",
			session: "s_abcd",
			want:    []string{"| 补测试 | 进行中 | 写用例 | 田灵 | " + stamp + " |"},
		},
		{
			name:    "覆盖手填时间",
			in:      "| 任务 | 状态 | 下一步 | 推进者（自报） | 更新时间 |\n| --- | --- | --- | --- | --- |\n| 上线 | 待办 | 观察 | s_abcd | 明天 |\n",
			session: "s_abcd",
			want:    []string{"| 上线 | 待办 | 观察 | s_abcd | " + stamp + " |"},
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
			got := normalizeTasks(tc.in, tc.session, fixedNow())
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

func TestTasksCreateUsesFiveColumns(t *testing.T) {
	svc := newSvc(t)
	svc.now = fixedNow
	mustRules(t, svc, "demo")

	read, e := svc.Read("demo", "TASKS.md", "", 0)
	if e != nil {
		t.Fatalf("read TASKS: %v", e)
	}
	for _, want := range []string{"| 任务 | 状态 | 下一步 | 推进者（自报） | 更新时间 |", "不要手写"} {
		if !strings.Contains(read.Content, want) {
			t.Fatalf("模板缺少 %q:\n%s", want, read.Content)
		}
	}
}

func TestTasksAppendFillsSessionAndTime(t *testing.T) {
	svc := newSvc(t)
	svc.now = fixedNow
	rules := mustRules(t, svc, "demo")

	legacy := "| 任务 | 状态 | 下一步 | 负责人 |\n| --- | --- | --- | --- |\n| 补测试 | 进行中 | 写用例 |  |\n"
	if err := svc.st.WriteAtomic("demo", "TASKS.md", []byte(legacy)); err != nil {
		t.Fatalf("写旧格式: %v", err)
	}

	// 旧文件读取不做解析，原样返回。
	read, e := svc.Read("demo", "TASKS.md", "", 0)
	if e != nil || read.Content != legacy {
		t.Fatalf("旧文件应原样读出: err=%v\n%s", e, read.Content)
	}

	if _, e := svc.Write("demo", WriteReq{
		Path: "TASKS.md", Mode: "append", Session: rules.Session,
		Content: "| 发版 | 待办 | 打 0.1.2 | 田灵 |\n",
	}); e != nil {
		t.Fatalf("append: %v", e)
	}

	read, _ = svc.Read("demo", "TASKS.md", "", 0)
	for _, want := range []string{
		"| 任务 | 状态 | 下一步 | 推进者（自报） | 更新时间 |",
		"| 补测试 | 进行中 | 写用例 | " + rules.Session + " | 2026-11-21 10:30 |",
		"| 发版 | 待办 | 打 0.1.2 | 田灵 | 2026-11-21 10:30 |",
	} {
		if !strings.Contains(read.Content, want) {
			t.Fatalf("缺少 %q:\n%s", want, read.Content)
		}
	}
}
