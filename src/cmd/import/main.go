// import 把目录里的既有 md 一次性同步进团队记忆。
// 它只是 POST /import/<project> 的薄客户端：遍历目录 → 组装 JSON → 打印结果。
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type importFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type importRequest struct {
	Mode   string       `json:"mode"`
	DryRun bool         `json:"dry_run"`
	RuleMD string       `json:"rule_md,omitempty"`
	Files  []importFile `json:"files"`
}

type importResponse struct {
	OK          bool     `json:"ok"`
	Project     string   `json:"project"`
	DryRun      bool     `json:"dry_run"`
	Created     int      `json:"created"`
	Replaced    int      `json:"replaced"`
	Skipped     int      `json:"skipped"`
	RuleWritten bool     `json:"rule_written"`
	IndexAdded  int      `json:"index_added"`
	Warnings    []any    `json:"warnings"`
	Hint        string   `json:"hint"`
	Files       []result `json:"files"`
}

type result struct {
	Path   string `json:"path"`
	Action string `json:"action"`
}

// mdLink 匹配 markdown 链接，用来把旧索引里的路径改写到 topics/。
var mdLink = regexp.MustCompile(`\]\(([^()\s]+\.md)\)`)

func main() {
	url := flag.String("url", "http://127.0.0.1:8080", "team-memory 服务地址")
	project := flag.String("project", "", "目标项目 slug，例如 my-project")
	src := flag.String("src", "", "已有知识目录：递归所有 .md，映射为 topics/<相对路径>")
	rule := flag.String("rule", "", "可选：整篇 RULE.md（如 知识图谱.md），其中指向已导入文件的链接会自动改写到 topics/")
	mode := flag.String("mode", "merge", "merge（跳过已存在）| replace（覆盖）")
	dry := flag.Bool("dry-run", false, "只校验与试算，不落盘")
	flag.Parse()

	if strings.TrimSpace(*project) == "" {
		fatalf("必须指定 -project，例如 -project my-project")
	}
	if strings.TrimSpace(*src) == "" && strings.TrimSpace(*rule) == "" {
		fatalf("至少指定 -src 或 -rule")
	}

	req := importRequest{Mode: *mode, DryRun: *dry, Files: []importFile{}}
	linkMap := map[string]string{} // 文件名 → topics 路径，用于改写索引链接

	if *src != "" {
		files, links, err := collect(*src, *rule)
		if err != nil {
			fatalf("遍历 %s 失败: %v", *src, err)
		}
		req.Files = files
		linkMap = links
	}
	if *rule != "" {
		b, err := os.ReadFile(*rule)
		if err != nil {
			fatalf("读取规则文件失败: %v", err)
		}
		req.RuleMD = rewriteLinks(string(b), linkMap)
	}

	status, body, err := post(strings.TrimRight(*url, "/")+"/import/"+*project, req)
	if err != nil {
		fatalf("请求失败: %v", err)
	}
	var resp importResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		fatalf("响应不是预期 JSON（HTTP %d）: %s", status, truncate(string(body), 500))
	}
	if status >= 300 {
		fatalf("HTTP %d: %s", status, truncate(string(body), 500))
	}

	fmt.Printf("project=%s dry_run=%v created=%d replaced=%d skipped=%d index_added=%d rule_written=%v\n",
		resp.Project, resp.DryRun, resp.Created, resp.Replaced, resp.Skipped, resp.IndexAdded, resp.RuleWritten)
	if resp.Hint != "" {
		fmt.Println("hint:", resp.Hint)
	}
	if len(resp.Warnings) == 0 {
		fmt.Println("warnings: none")
	} else {
		for _, w := range resp.Warnings {
			fmt.Println("warning:", truncate(fmt.Sprint(w), 300))
		}
	}
	skipped := make([]string, 0)
	for _, f := range resp.Files {
		if f.Action == "skipped" {
			skipped = append(skipped, f.Path)
		}
	}
	if len(skipped) > 0 {
		fmt.Printf("skipped(%d): %s\n", len(skipped), truncate(strings.Join(skipped, ", "), 500))
	}
}

func collect(root, rulePath string) ([]importFile, map[string]string, error) {
	var files []importFile
	links := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}
		if rulePath != "" && sameFile(p, rulePath) {
			return nil // 规则文件本身不重复入 topics
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		target := "topics/" + filepath.ToSlash(rel)
		files = append(files, importFile{Path: target, Content: string(b)})
		links[d.Name()] = target
		return nil
	})
	return files, links, err
}

// rewriteLinks 把索引里指向已导入文件的链接统一改成 topics/<路径>，避免 RULE.md 指向旧目录。
func rewriteLinks(md string, links map[string]string) string {
	return mdLink.ReplaceAllStringFunc(md, func(s string) string {
		m := mdLink.FindStringSubmatch(s)
		if len(m) < 2 {
			return s
		}
		target, ok := links[path.Base(filepath.ToSlash(m[1]))]
		if !ok {
			return s
		}
		return "](" + target + ")"
	})
}

func sameFile(a, b string) bool {
	aa, err1 := filepath.Abs(a)
	bb, err2 := filepath.Abs(b)
	return err1 == nil && err2 == nil && strings.EqualFold(filepath.Clean(aa), filepath.Clean(bb))
}

func post(url string, v any) (int, []byte, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return resp.StatusCode, b, err
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
