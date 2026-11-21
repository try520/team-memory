package kb

import (
	"strings"
	"testing"
)

const sampleSkill = `---
name: code-review
description: 涉及代码评审、CR 规范、提交前自查时必读
---

# 代码评审
> 统一团队 CR 口径。

## 步骤
1. 先看公开接口。
`

func TestParseSkillDoc(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		wantErr  bool
		wantName string
		wantBody string
	}{
		{"正常", sampleSkill, false, "code-review", "# 代码评审"},
		{"CRLF 与引号", "---\r\nname: \"my-skill\"\r\ndescription: '什么时候用'\r\n---\r\n正文", false, "my-skill", "正文"},
		{"缺 frontmatter", "# 无头", true, "", ""},
		{"未闭合", "---\nname: x\n", true, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc, err := parseSkillDoc([]byte(c.raw))
			if c.wantErr {
				if err == nil {
					t.Fatalf("期望解析失败，得到 %+v", doc)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSkillDoc: %v", err)
			}
			if doc.fm["name"] != c.wantName {
				t.Fatalf("name=%q want %q", doc.fm["name"], c.wantName)
			}
			if !strings.Contains(doc.body, c.wantBody) {
				t.Fatalf("body=%q 不含 %q", doc.body, c.wantBody)
			}
		})
	}
}

func TestValidateSkillDoc(t *testing.T) {
	// 缺 description → 明确错误码。
	_, e := validateSkillDoc([]byte("---\nname: x\n---\n正文"))
	if e == nil || e.Code != "SKILL_FRONTMATTER_REQUIRED" {
		t.Fatalf("缺 description 期望 SKILL_FRONTMATTER_REQUIRED，得到 %+v", e)
	}
	if _, e := validateSkillDoc([]byte(sampleSkill)); e != nil {
		t.Fatalf("合法技能文档校验失败: %+v", e)
	}
}

func TestWriteSkillRequiresFrontmatter(t *testing.T) {
	svc := newSvc(t)
	rules := mustRules(t, svc, "demo")

	// 写技能缺 frontmatter → 400。
	_, e := svc.Write("demo", WriteReq{
		Path: "skills/code-review/SKILL.md", Mode: "create", Session: rules.Session,
		Content: "# 没有 frontmatter 的技能",
	})
	if e == nil || e.Code != "SKILL_FRONTMATTER_REQUIRED" {
		t.Fatalf("期望 SKILL_FRONTMATTER_REQUIRED，得到 %+v", e)
	}

	// 合法技能可写，且 append 后仍通过校验。
	if _, e := svc.Write("demo", WriteReq{
		Path: "skills/code-review/SKILL.md", Mode: "create", Session: rules.Session, Content: sampleSkill,
	}); e != nil {
		t.Fatalf("create skill: %+v", e)
	}
	if _, e := svc.Write("demo", WriteReq{
		Path: "skills/code-review/SKILL.md", Mode: "append", Session: rules.Session, Content: "\n## 补充\n- 追加内容\n",
	}); e != nil {
		t.Fatalf("append skill: %+v", e)
	}
}

func TestWriteGlobalSkillRejected(t *testing.T) {
	svc := newSvc(t)
	rules := mustRules(t, svc, "demo")
	_, e := svc.Write("demo", WriteReq{
		Path: "@global/skills/code-review/SKILL.md", Mode: "create", Session: rules.Session, Content: sampleSkill,
	})
	if e == nil || e.Code != "GLOBAL_SKILL_READONLY" || e.Status != 403 {
		t.Fatalf("期望 GLOBAL_SKILL_READONLY 403，得到 %+v", e)
	}
}

func TestSkillPromoteLifecycle(t *testing.T) {
	svc := newSvc(t)
	rules := mustRules(t, svc, "proj-a")
	sess := rules.Session

	// 1. 没项目技能直接提审 → 404。
	if _, e := svc.SkillPromote("proj-a", SkillPromoteReq{Action: "submit", Name: "code-review", Session: sess}); e == nil || e.Code != "NOT_FOUND" {
		t.Fatalf("期望 NOT_FOUND，得到 %+v", e)
	}

	// 2. 建项目技能。
	if _, e := svc.Write("proj-a", WriteReq{
		Path: "skills/code-review/SKILL.md", Mode: "create", Session: sess, Content: sampleSkill,
	}); e != nil {
		t.Fatalf("create skill: %+v", e)
	}

	// 3. skill_list：项目技能可见，全局为空。
	list, e := svc.SkillList("proj-a")
	if e != nil {
		t.Fatalf("SkillList: %+v", e)
	}
	if len(list.Project) != 1 || list.Project[0].Name != "code-review" || list.Project[0].Status != skillStatusLocal {
		t.Fatalf("项目技能清单不对: %+v", list.Project)
	}
	if len(list.Global) != 0 {
		t.Fatalf("提审前全局应为空: %+v", list.Global)
	}

	// 4. submit → 全局 pending，带来源项目与理由。
	sub, e := svc.SkillPromote("proj-a", SkillPromoteReq{Action: "submit", Name: "code-review", Reason: "全团队通用", Session: sess})
	if e != nil || sub.Status != skillStatusPending {
		t.Fatalf("submit: %+v %+v", sub, e)
	}
	globalRaw, _, err := svc.st.ReadFile("@global", "@global/skills/code-review/SKILL.md")
	if err != nil {
		t.Fatalf("读全局技能失败: %v", err)
	}
	for _, want := range []string{"status: pending", "source_project: proj-a", "reason: 全团队通用"} {
		if !strings.Contains(string(globalRaw), want) {
			t.Fatalf("全局技能缺 %q:\n%s", want, globalRaw)
		}
	}

	// 5. 重复 submit → 409。
	if _, e := svc.SkillPromote("proj-a", SkillPromoteReq{Action: "submit", Name: "code-review", Session: sess}); e == nil || e.Code != "ALREADY_PROMOTED" {
		t.Fatalf("重复提审期望 ALREADY_PROMOTED，得到 %+v", e)
	}

	// 6. 提审后 get_rules 的 global_skills 立即出现（pending 也算，便于维护者发现）。
	r2 := mustRules(t, svc, "proj-b")
	if len(r2.GlobalSkills) != 1 || r2.GlobalSkills[0].Name != "code-review" || r2.GlobalSkills[0].Status != skillStatusPending {
		t.Fatalf("get_rules 全局技能清单不对: %+v", r2.GlobalSkills)
	}

	// 7. approve → approved；另一个项目可读全文。
	ap, e := svc.SkillPromote("proj-b", SkillPromoteReq{Action: "approve", Name: "code-review", Session: r2.Session})
	if e != nil || ap.Status != skillStatusApproved {
		t.Fatalf("approve: %+v %+v", ap, e)
	}
	read, re := svc.Read("proj-b", "@global/skills/code-review/SKILL.md", "", 0)
	if re != nil {
		t.Fatalf("读全局技能全文: %+v", re)
	}
	if !strings.Contains(read.Content, "status: approved") {
		t.Fatalf("审批后状态未更新: %s", read.Content)
	}
	// approve 也是移动语义：来源项目里的副本应被移除。
	if svc.st.Exists("proj-a", "skills/code-review/SKILL.md") {
		t.Fatal("approve 后来源项目的技能副本应被移除")
	}

	// 8. 已 approve 再 approve → 409。
	if _, e := svc.SkillPromote("proj-a", SkillPromoteReq{Action: "approve", Name: "code-review", Session: sess}); e == nil || e.Code != "INVALID_STATE" {
		t.Fatalf("重复审批期望 INVALID_STATE，得到 %+v", e)
	}
}

func TestSkillPromoteMovesToGlobal(t *testing.T) {
	svc := newSvc(t)
	rules := mustRules(t, svc, "proj-a")

	if _, e := svc.Write("proj-a", WriteReq{
		Path: "skills/demo-skill/SKILL.md", Mode: "create", Session: rules.Session,
		Content: "---\nname: demo-skill\ndescription: 演示技能\n---\n\n# demo\n",
	}); e != nil {
		t.Fatalf("create skill: %+v", e)
	}

	res, e := svc.SkillPromote("proj-a", SkillPromoteReq{
		Action: "promote", Name: "demo-skill", Session: rules.Session, Reason: "跨项目通用",
	})
	if e != nil || res.Status != skillStatusApproved {
		t.Fatalf("promote: %+v %+v", res, e)
	}
	if svc.st.Exists("proj-a", "skills/demo-skill/SKILL.md") {
		t.Fatal("promote 后项目副本应被移除（移动语义）")
	}
	if !svc.st.Exists("@global", "@global/skills/demo-skill/SKILL.md") {
		t.Fatal("promote 后全局技能应存在")
	}
	raw, _, err := svc.st.ReadFile("@global", "@global/skills/demo-skill/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"status: approved", "source_project: proj-a"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("全局技能缺 %q:\n%s", want, raw)
		}
	}

	list, _ := svc.SkillList("proj-a")
	if len(list.Project) != 0 || len(list.Global) != 1 || list.Global[0].Status != skillStatusApproved {
		t.Fatalf("清单不对: project=%+v global=%+v", list.Project, list.Global)
	}

	// 已提升后再 promote → 409。
	if _, e := svc.SkillPromote("proj-a", SkillPromoteReq{Action: "promote", Name: "demo-skill", Session: rules.Session}); e == nil || e.Code != "NOT_FOUND" {
		t.Fatalf("项目副本已移除，promote 应返回 NOT_FOUND，得到 %+v", e)
	}
}

func TestSkillPromoteRejectAndCancel(t *testing.T) {
	for _, action := range []string{"reject", "cancel"} {
		t.Run(action, func(t *testing.T) {
			svc := newSvc(t)
			rules := mustRules(t, svc, "p")
			if _, e := svc.Write("p", WriteReq{
				Path: "skills/demo-skill/SKILL.md", Mode: "create", Session: rules.Session, Content: sampleSkill,
			}); e != nil {
				t.Fatalf("create skill: %+v", e)
			}
			if _, e := svc.SkillPromote("p", SkillPromoteReq{Action: "submit", Name: "demo-skill", Session: rules.Session}); e != nil {
				t.Fatalf("submit: %+v", e)
			}
			res, e := svc.SkillPromote("p", SkillPromoteReq{Action: action, Name: "demo-skill", Session: rules.Session})
			if e != nil || res.Status != "removed" {
				t.Fatalf("%s: %+v %+v", action, res, e)
			}
			if svc.st.Exists("p", "@global/skills/demo-skill/SKILL.md") {
				t.Fatal("撤销/拒绝后全局技能应被删除")
			}
			// 撤销后可重新提审。
			if _, e := svc.SkillPromote("p", SkillPromoteReq{Action: "submit", Name: "demo-skill", Session: rules.Session}); e != nil {
				t.Fatalf("重新提审: %+v", e)
			}
		})
	}
}

func TestSkillPromoteValidation(t *testing.T) {
	svc := newSvc(t)
	rules := mustRules(t, svc, "p")

	cases := []struct {
		name    string
		req     SkillPromoteReq
		wantErr string
	}{
		{"坏 action", SkillPromoteReq{Action: "bogus", Name: "x", Session: rules.Session}, "BAD_REQUEST"},
		{"坏技能名", SkillPromoteReq{Action: "submit", Name: "Bad_Name", Session: rules.Session}, "BAD_REQUEST"},
		{"无 session", SkillPromoteReq{Action: "submit", Name: "code-review"}, "READ_RULES_FIRST"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, e := svc.SkillPromote("p", c.req)
			if e == nil || e.Code != c.wantErr {
				t.Fatalf("期望 %s，得到 %+v", c.wantErr, e)
			}
		})
	}
}
