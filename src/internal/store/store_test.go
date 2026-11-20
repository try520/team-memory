package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s := New(t.TempDir())
	if err := s.EnsureProjectLayout("demo"); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPathWhitelist(t *testing.T) {
	s := newStore(t)
	ok := []string{"RULE.md", "TASKS.md", "topics/支付回调.md", "topics/_index/支付.md", "journal/2026-11/x-1015.md",
		"skills/code-review/SKILL.md", "skills/my-skill2/SKILL.md", "@global/skills/code-review/SKILL.md"}
	for _, p := range ok {
		if _, err := s.Abs("demo", p); err != nil {
			t.Fatalf("%s 应允许: %v", p, err)
		}
	}
	bad := []string{"", "/abs.md", "../x.md", "topics/../../x.md", "topics/a.txt", "other/x.md", ".history/x.md", "topics/./a.md", "foo/bar.md",
		"skills/Bad_Name/SKILL.md", "skills/ok/README.md", "skills/a/b/SKILL.md", "@global/skills/Bad/SKILL.md", "@global/topics/x.md", "@global/../secret.md"}
	for _, p := range bad {
		if _, err := s.Abs("demo", p); err == nil {
			t.Fatalf("%s 应被拒", p)
		}
	}
	if _, err := s.Abs("../evil", "RULE.md"); err == nil {
		t.Fatal("非法项目名应被拒")
	}
}

func TestWriteAtomicReplacesWholeFile(t *testing.T) {
	s := newStore(t)
	long := strings.Repeat("旧内容\n", 500)
	if err := s.WriteAtomic("demo", "TASKS.md", []byte(long)); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteAtomic("demo", "TASKS.md", []byte("新内容\n")); err != nil {
		t.Fatal(err)
	}
	b, _, err := s.ReadFile("demo", "TASKS.md")
	if err != nil || string(b) != "新内容\n" {
		t.Fatalf("replace 后内容不对: %q %v", b, err)
	}
}

func TestSnapshotKeepsFifty(t *testing.T) {
	s := newStore(t)
	for i := 0; i < 60; i++ {
		if err := s.Snapshot("demo", "RULE.md", []byte(fmt.Sprintf("v%d\n", i))); err != nil {
			t.Fatal(err)
		}
	}
	snaps, err := s.Snapshots("demo", "RULE.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 50 {
		t.Fatalf("快照数 = %d, 期望 50", len(snaps))
	}
	last, err := s.ReadSnapshot("demo", "RULE.md", snaps[len(snaps)-1])
	if err != nil || string(last) != "v59\n" {
		t.Fatalf("最新快照应为 v59: %q %v", last, err)
	}
	// 快照目录不能被当作正文列出
	files, err := s.ListFiles("demo")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasPrefix(f.Path, ".history") {
			t.Fatalf("ListFiles 泄露了历史目录: %s", f.Path)
		}
	}
	if err := os.WriteFile(filepath.Join(s.Root(), "projects", "demo", "topics", "a.md"), []byte("# a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	topics, err := s.ListTopics("demo")
	if err != nil || len(topics) != 1 || topics[0] != "topics/a.md" {
		t.Fatalf("ListTopics = %v %v", topics, err)
	}
}
