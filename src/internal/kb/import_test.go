package kb

import (
	"strings"
	"testing"
)

const importRuleMD = `# demo 知识索引

本索引是项目记忆的唯一入口。

## 文档索引

| 文档 | 适用场景 | 举例 |
| --- | --- | --- |

## 使用规则

1. 先读索引。
`

func TestImportBootstrap(t *testing.T) {
	svc := newSvc(t)
	files := []ImportFile{
		{Path: "topics/后端架构.md", Content: "# 后端架构\n> 路由与模块。\n\n## 结论\n- 路由在 server/app/router.js。\n"},
		{Path: `topics\数据库模型.md`, Content: "# 数据库模型\n\n表定义在 server/app/model。\n"},
	}
	resp, e := svc.Import("demo", ImportReq{
		RuleMD: importRuleMD,
		Files:  files,
		Index: []ImportIndexRow{
			{Path: "topics/后端架构.md", Scene: "涉及后端路由、模块划分的改动前必读", Examples: []string{"路由怎么定义？"}},
			{Path: "topics/数据库模型.md", Scene: "确认表名与模型文件时必读", Examples: []string{"approval_flow 对应哪个模型？"}},
		},
	})
	if e != nil {
		t.Fatalf("Import: %v", e)
	}
	if resp.Created != 2 || resp.Replaced != 0 || resp.Skipped != 0 {
		t.Fatalf("计数不对: %+v", resp)
	}
	if !resp.RuleWritten || resp.IndexAdded != 2 {
		t.Fatalf("rule/index 不对: rule=%v index=%d", resp.RuleWritten, resp.IndexAdded)
	}
	if len(resp.Warnings) != 0 {
		t.Fatalf("不应有 warning: %+v", resp.Warnings)
	}
	if !svc.st.Exists("demo", "topics/数据库模型.md") {
		t.Fatal("反斜杠路径应被规范化后写入")
	}

	// 再跑一次必须幂等：全部 skipped，索引不重复插。
	resp2, e := svc.Import("demo", ImportReq{RuleMD: importRuleMD, Files: files})
	if e != nil {
		t.Fatalf("第二次 Import: %v", e)
	}
	if resp2.Created != 0 || resp2.Skipped != 2 {
		t.Fatalf("merge 应跳过已存在文件: %+v", resp2)
	}
	if resp2.IndexAdded != 0 {
		t.Fatalf("索引不应重复插入: %d", resp2.IndexAdded)
	}
	rule, _, err := svc.st.ReadFile("demo", "RULE.md")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(rule), "topics/后端架构.md") != 1 {
		t.Fatalf("索引行重复: %s", rule)
	}
}

func TestImportDryRunWritesNothing(t *testing.T) {
	svc := newSvc(t)
	resp, e := svc.Import("demo", ImportReq{
		DryRun: true,
		RuleMD: importRuleMD,
		Files:  []ImportFile{{Path: "topics/x.md", Content: "# X\n\n内容"}},
	})
	if e != nil {
		t.Fatalf("Import: %v", e)
	}
	if resp.Created != 1 || resp.DryRun != true {
		t.Fatalf("dry_run 应只试算: %+v", resp)
	}
	if svc.st.Exists("demo", "topics/x.md") {
		t.Fatal("dry_run 不应落盘")
	}
}

func TestImportRejectsBadPath(t *testing.T) {
	svc := newSvc(t)
	for _, p := range []string{"../x.md", "/abs/x.md", "topics/a.txt", "README.md"} {
		_, e := svc.Import("demo", ImportReq{Files: []ImportFile{{Path: p, Content: "x"}}})
		if e == nil || e.Code != "PATH_NOT_ALLOWED" {
			t.Fatalf("%s 应被拒绝，得到 %+v", p, e)
		}
	}
}

func TestImportAutoIndex(t *testing.T) {
	svc := newSvc(t)
	resp, e := svc.Import("demo", ImportReq{
		Files: []ImportFile{
			{Path: "topics/支付回调.md", Content: "# 支付回调\n\n幂等键 = channel + orderNo。\n"},
		},
	})
	if e != nil {
		t.Fatalf("Import: %v", e)
	}
	if resp.IndexAdded != 1 {
		t.Fatalf("未给 index 时应自动补 1 行，得到 %d", resp.IndexAdded)
	}
	rule, _, _ := svc.st.ReadFile("demo", "RULE.md")
	if !strings.Contains(string(rule), "<!-- auto -->") {
		t.Fatalf("自动行应带 <!-- auto -->: %s", rule)
	}
	if len(resp.Warnings) != 0 {
		t.Fatalf("自动补行后不应再报 missing_in_index: %+v", resp.Warnings)
	}
}

func TestImportReplaceKeepsSnapshot(t *testing.T) {
	svc := newSvc(t)
	if _, e := svc.Import("demo", ImportReq{
		Files: []ImportFile{{Path: "topics/x.md", Content: "# X\n\n旧内容"}},
	}); e != nil {
		t.Fatalf("首次 Import: %v", e)
	}
	resp, e := svc.Import("demo", ImportReq{
		Mode:  "replace",
		Files: []ImportFile{{Path: "topics/x.md", Content: "# X\n\n新内容"}},
	})
	if e != nil {
		t.Fatalf("replace Import: %v", e)
	}
	if resp.Replaced != 1 {
		t.Fatalf("replace 计数不对: %+v", resp)
	}
	body, _, _ := svc.st.ReadFile("demo", "topics/x.md")
	if !strings.Contains(string(body), "新内容") {
		t.Fatalf("内容未覆盖: %s", body)
	}
	snaps, err := svc.st.Snapshots("demo", "topics/x.md")
	if err != nil || len(snaps) == 0 {
		t.Fatalf("replace 应留历史快照: %v %v", snaps, err)
	}
}

func TestImportRuleNotOverwrittenInMerge(t *testing.T) {
	svc := newSvc(t)
	if _, e := svc.Import("demo", ImportReq{
		RuleMD: importRuleMD,
		Files:  []ImportFile{{Path: "topics/a.md", Content: "# A\n\n内容"}},
	}); e != nil {
		t.Fatalf("首次 Import: %v", e)
	}
	// 已有索引行时，merge 不能再拿 rule_md 覆盖。
	resp, e := svc.Import("demo", ImportReq{RuleMD: "# 别的索引\n\n## 文档索引\n\n| 文档 | 适用场景 | 举例 |\n| --- | --- | --- |\n"})
	if e != nil {
		t.Fatalf("第二次 Import: %v", e)
	}
	if resp.RuleWritten {
		t.Fatal("merge 不应覆盖已有索引")
	}
	rule, _, _ := svc.st.ReadFile("demo", "RULE.md")
	if strings.Contains(string(rule), "别的索引") {
		t.Fatalf("RULE.md 被覆盖了: %s", rule)
	}
}
