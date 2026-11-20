// 连通性冒烟：作为一个真实的 MCP Streamable HTTP 客户端连上 /mcp/<project>，
// 完整走 initialize → notifications/initialized → tools/list → get_rules → 写一条 → 读回，
// 再验证新主题缺索引被拒、并发 append 不丢、reindex 幂等。
//
// 用法：go run ./cmd/smoke -url http://127.0.0.1:8080 -project demo -n 100
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	SessionHeader  = "Mcp-Session-Id"
	ProtocolHeader = "MCP-Protocol-Version"
)

type client struct {
	base     string
	project  string
	http     *http.Client
	session  string
	protocol string
}

func main() {
	url := flag.String("url", "http://127.0.0.1:8080", "服务地址")
	project := flag.String("project", "demo", "项目 slug")
	n := flag.Int("n", 100, "并发 append 次数")
	flag.Parse()

	c := &client{base: strings.TrimRight(*url, "/"), project: *project, http: &http.Client{Timeout: 15 * time.Second}}
	failures := 0
	check := func(name string, err error) {
		if err != nil {
			failures++
			fmt.Printf("FAIL %s: %v\n", name, err)
			return
		}
		fmt.Printf("ok   %s\n", name)
	}

	check("healthz", c.health())
	check("initialize(Mcp-Session-Id)", c.initialize())
	check("notifications/initialized", c.initialized())
	check("tools/list=6", c.toolsList())

	rules, err := c.call("memory_get_rules", map[string]any{})
	check("memory_get_rules", err)
	if err != nil {
		os.Exit(1)
	}
	ruleSession, _ := rules["session"].(string)
	if ruleSession == "" {
		fmt.Println("FAIL get_rules 未返回 rule session")
		os.Exit(1)
	}

	// 新主题缺索引：每次都必须被拒
	rejected := 0
	for i := 0; i < 10; i++ {
		payload, _, err := c.callRaw("memory_write", map[string]any{
			"path":    fmt.Sprintf("topics/smoke-noindex-%d-%d.md", time.Now().UnixNano(), i),
			"content": "# 缺索引\n", "mode": "create", "session": ruleSession,
		})
		if err == nil && payload["error"] == "INDEX_ROW_REQUIRED" {
			rejected++
		}
	}
	if rejected == 10 {
		fmt.Println("ok   缺索引新建被拒 10/10")
	} else {
		failures++
		fmt.Printf("FAIL 缺索引新建只被拒 %d/10\n", rejected)
	}

	// 完整链路：建主题（带索引行）→ 读回
	ts := time.Now().Format("150405")
	topic := fmt.Sprintf("topics/smoke-%s.md", ts)
	_, err = c.call("memory_write", map[string]any{
		"path": topic, "content": "# 冒烟\n> 冒烟测试用。\n", "mode": "create", "session": ruleSession,
		"index": map[string]any{"scene": "冒烟测试", "examples": []string{"冒烟怎么跑？"}},
	})
	check("写主题(带索引行)", err)
	if err != nil {
		os.Exit(1)
	}
	read, err := c.call("memory_read", map[string]any{"path": topic})
	check("读回主题", err)
	if err == nil {
		if content, _ := read["content"].(string); !strings.Contains(content, "冒烟测试用") {
			failures++
			fmt.Println("FAIL 读回内容与写入不一致")
		} else {
			fmt.Println("ok   写入内容读回一致")
		}
	}

	// 并发追加同一主题
	errs := make(chan error, *n)
	var wg sync.WaitGroup
	for i := 0; i < *n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := c.call("memory_write", map[string]any{
				"path": topic, "content": fmt.Sprintf("- smoke-%03d\n", i), "mode": "append", "session": ruleSession,
			})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	writeErr := 0
	for err := range errs {
		if err != nil {
			writeErr++
		}
	}
	if writeErr == 0 {
		fmt.Printf("ok   并发 append %d 次全部成功\n", *n)
	} else {
		failures++
		fmt.Printf("FAIL 并发 append 失败 %d 次\n", writeErr)
	}

	read, err = c.call("memory_read", map[string]any{"path": topic})
	check("并发后读回", err)
	if err == nil {
		content, _ := read["content"].(string)
		missing, dup := 0, 0
		for i := 0; i < *n; i++ {
			switch strings.Count(content, fmt.Sprintf("- smoke-%03d", i)) {
			case 0:
				missing++
			case 1:
			default:
				dup++
			}
		}
		if missing == 0 && dup == 0 {
			fmt.Printf("ok   追加内容无丢失无重复(%d 行)\n", *n)
		} else {
			failures++
			fmt.Printf("FAIL 丢失 %d 行、重复 %d 行\n", missing, dup)
		}
	}

	re, err := c.call("memory_write", map[string]any{"path": "RULE.md", "mode": "reindex", "session": ruleSession})
	check("reindex", err)
	if err == nil {
		if updated, _ := re["index_updated"].(bool); updated {
			failures++
			fmt.Println("FAIL reindex 在无缺失时仍改了索引")
		} else {
			fmt.Println("ok   reindex 幂等（无缺失时不改索引）")
		}
	}

	if failures > 0 {
		fmt.Printf("\n结果：%d 项失败\n", failures)
		os.Exit(1)
	}
	fmt.Println("\n结果：全部通过")
}

func (c *client) health() error {
	resp, err := c.http.Get(c.base + "/healthz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !bytes.Contains(body, []byte(`"ok":true`)) {
		return fmt.Errorf("status=%d body=%s", resp.StatusCode, body)
	}
	return nil
}

func (c *client) initialize() error {
	res, header, err := c.post(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "team-memory-smoke", "version": "1.0.0"},
		},
	})
	if err != nil {
		return err
	}
	c.session = header.Get(SessionHeader)
	if c.session == "" {
		return fmt.Errorf("响应头缺少 Mcp-Session-Id")
	}
	result, _ := res["result"].(map[string]any)
	if info, _ := result["serverInfo"].(map[string]any); info["name"] != "team-memory" {
		return fmt.Errorf("serverInfo=%v", result["serverInfo"])
	}
	c.protocol, _ = result["protocolVersion"].(string)
	if c.protocol == "" {
		return fmt.Errorf("protocolVersion 为空")
	}
	return nil
}

func (c *client) initialized() error {
	resp, err := c.postRaw(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("status=%d body=%s", resp.StatusCode, body)
	}
	return nil
}

func (c *client) toolsList() error {
	res, err := c.rpc("tools/list", nil)
	if err != nil {
		return err
	}
	result, _ := res["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	if len(tools) != 6 {
		return fmt.Errorf("工具数=%d", len(tools))
	}
	for _, raw := range tools {
		if m, _ := raw.(map[string]any); m["name"] == "memory_import" {
			return nil
		}
	}
	return fmt.Errorf("缺少 memory_import")
}

func (c *client) rpc(method string, params any) (map[string]any, error) {
	req := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method}
	if params != nil {
		req["params"] = params
	}
	res, _, err := c.post(req)
	return res, err
}

// post 发送一条 JSON-RPC 消息；session 已存在时自动带上。
func (c *client) post(payload any) (map[string]any, http.Header, error) {
	resp, err := c.postRaw(payload)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, resp.Header, fmt.Errorf("status=%d body=%s", resp.StatusCode, raw)
	}
	var out struct {
		Result map[string]any `json:"result"`
		Error  map[string]any `json:"error"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, resp.Header, fmt.Errorf("解析响应失败: %v body=%s", err, raw)
	}
	if out.Error != nil {
		return nil, resp.Header, fmt.Errorf("rpc error: %v", out.Error)
	}
	return map[string]any{"result": out.Result}, resp.Header, nil
}

func (c *client) postRaw(payload any) (*http.Response, error) {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, c.base+"/mcp/"+c.project, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if c.protocol != "" {
		req.Header.Set(ProtocolHeader, c.protocol)
	}
	if c.session != "" {
		req.Header.Set(SessionHeader, c.session)
	}
	return c.http.Do(req)
}

// call 调用工具；工具返回 isError=true 时转成 Go error。
func (c *client) call(name string, args map[string]any) (map[string]any, error) {
	payload, isErr, err := c.callRaw(name, args)
	if err != nil {
		return nil, err
	}
	if isErr {
		return payload, fmt.Errorf("%s 返回错误: %v", name, payload)
	}
	return payload, nil
}

func (c *client) callRaw(name string, args map[string]any) (map[string]any, bool, error) {
	res, err := c.rpc("tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		return nil, false, err
	}
	result, _ := res["result"].(map[string]any)
	isErr, _ := result["isError"].(bool)
	if sc, ok := result["structuredContent"].(map[string]any); ok && sc != nil {
		return sc, isErr, nil
	}
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		return nil, isErr, fmt.Errorf("空 content")
	}
	item, _ := content[0].(map[string]any)
	text, _ := item["text"].(string)
	var payload map[string]any
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return nil, isErr, fmt.Errorf("解析工具输出失败: %v", err)
	}
	return payload, isErr, nil
}
