package mcpserver

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"team-memory/internal/kb"
	"team-memory/internal/store"
)

type testClient struct {
	t       *testing.T
	h       http.Handler
	project string
	session string
}

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	st := store.New(t.TempDir())
	svc := kb.New(st)
	if err := svc.EnsureServiceLayout(); err != nil {
		t.Fatalf("EnsureServiceLayout: %v", err)
	}
	return NewHandler(svc, st, log.New(io.Discard, "", 0))
}

func newTestClient(t *testing.T) *testClient {
	t.Helper()
	c := &testClient{t: t, h: newTestHandler(t), project: "demo"}
	c.initialize(DefaultProtocol)
	return c
}

func (c *testClient) initialize(version string) map[string]any {
	c.t.Helper()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"protocolVersion": version},
	})
	rec := c.do(body, nil, true)
	if rec.Code != http.StatusOK {
		c.t.Fatalf("initialize status=%d body=%s", rec.Code, rec.Body.String())
	}
	c.session = rec.Header().Get(SessionHeader)
	if c.session == "" {
		c.t.Fatal("initialize 响应缺少 Mcp-Session-Id")
	}
	var env struct {
		Result map[string]any `json:"result"`
		Error  map[string]any `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		c.t.Fatalf("initialize decode: %v", err)
	}
	if env.Error != nil {
		c.t.Fatalf("initialize error: %v", env.Error)
	}
	return env.Result
}

func (c *testClient) do(body []byte, override map[string]string, skipSession bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/mcp/"+c.project, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if !skipSession && c.session != "" {
		req.Header.Set(SessionHeader, c.session)
	}
	for k, v := range override {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	return rec
}

func (c *testClient) rpc(method string, params any) (map[string]any, int) {
	c.t.Helper()
	req := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method}
	if params != nil {
		req["params"] = params
	}
	body, _ := json.Marshal(req)
	rec := c.do(body, nil, false)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out, rec.Code
}

// call 返回 (结构化结果, isError)。协议层错误直接 fail。
func (c *testClient) call(name string, args any) (map[string]any, bool) {
	c.t.Helper()
	res, status := c.rpc("tools/call", map[string]any{"name": name, "arguments": args})
	if status != 200 {
		c.t.Fatalf("%s HTTP status=%d body=%v", name, status, res)
	}
	if e, ok := res["error"]; ok {
		c.t.Fatalf("%s 协议错误: %v", name, e)
	}
	result, _ := res["result"].(map[string]any)
	if result == nil {
		c.t.Fatalf("%s 缺少 result: %v", name, res)
	}
	isErr, _ := result["isError"].(bool)
	payload, _ := result["structuredContent"].(map[string]any)
	if payload == nil {
		content, _ := result["content"].([]any)
		if len(content) > 0 {
			item, _ := content[0].(map[string]any)
			text, _ := item["text"].(string)
			_ = json.Unmarshal([]byte(text), &payload)
		}
	}
	if payload == nil {
		c.t.Fatalf("%s 空结果: %v", name, result)
	}
	return payload, isErr
}

func TestHTTPHandshakeAndTools(t *testing.T) {
	c := newTestClient(t)

	res := c.initialize(DefaultProtocol)
	if res["protocolVersion"] != DefaultProtocol {
		t.Fatalf("protocolVersion = %v", res["protocolVersion"])
	}
	info, _ := res["serverInfo"].(map[string]any)
	if info["name"] != "team-memory" {
		t.Fatalf("serverInfo = %v", res["serverInfo"])
	}
	instructions, _ := res["instructions"].(string)
	for _, want := range []string{"memory_get_rules", "必须回写", "本次无需记录"} {
		if !bytes.Contains([]byte(instructions), []byte(want)) {
			t.Fatalf("instructions 缺少 %q: %q", want, instructions)
		}
	}

	list, status := c.rpc("tools/list", nil)
	if status != 200 {
		t.Fatalf("tools/list status=%d", status)
	}
	result, _ := list["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	if len(tools) != 6 {
		t.Fatalf("tools/list = %d tools, want 6", len(tools))
	}
	names := map[string]bool{}
	for _, raw := range tools {
		tool, _ := raw.(map[string]any)
		names[tool["name"].(string)] = true
		if tool["description"] == "" || tool["title"] == "" {
			t.Fatalf("工具缺少 title/description: %v", tool["name"])
		}
		if tool["name"] == "memory_write" {
			desc, _ := tool["description"].(string)
			for _, want := range []string{"何时写", "next"} {
				if !strings.Contains(desc, want) {
					t.Fatalf("memory_write 描述缺少 %q: %q", want, desc)
				}
			}
		}
	}
	for _, want := range []string{"memory_get_rules", "memory_read", "memory_write", "memory_import", "skill_list", "skill_promote"} {
		if !names[want] {
			t.Fatalf("missing tool %s in %v", want, names)
		}
	}

	health := httptest.NewRecorder()
	c.h.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("healthz status=%d", health.Code)
	}
	var hb map[string]any
	_ = json.Unmarshal(health.Body.Bytes(), &hb)
	if hb["ok"] != true || hb["sessions"].(float64) < 1 {
		t.Fatalf("healthz = %s", health.Body.String())
	}

	meta := httptest.NewRecorder()
	c.h.ServeHTTP(meta, httptest.NewRequest(http.MethodGet, "/mcp", nil))
	if meta.Code != 200 {
		t.Fatalf("GET /mcp status=%d", meta.Code)
	}
}

func TestHTTPImportTool(t *testing.T) {
	c := newTestClient(t)

	// 没读规则直接导 → 428，模型不能绕过门控。
	_, isErr := c.call("memory_import", map[string]any{
		"files": []any{map[string]any{"path": "topics/a.md", "content": "# A\n\n内容"}},
	})
	if !isErr {
		t.Fatal("无 session 的 import 应被拒绝")
	}

	rules, isErr := c.call("memory_get_rules", map[string]any{})
	if isErr {
		t.Fatalf("get_rules: %v", rules)
	}
	session, _ := rules["session"].(string)

	// dry_run 不落盘。
	dry, isErr := c.call("memory_import", map[string]any{
		"session": session, "dry_run": true,
		"files": []any{map[string]any{"path": "topics/导入测试.md", "content": "# 导入测试\n\n结论。"}},
	})
	if isErr || dry["created"] != float64(1) || dry["index_added"] != float64(1) {
		t.Fatalf("dry_run 预演不对: %v", dry)
	}
	if _, isErr := c.call("memory_read", map[string]any{"path": "topics/导入测试.md"}); !isErr {
		t.Fatal("dry_run 不应落盘")
	}

	// 正式导入 + 指定索引行。
	done, isErr := c.call("memory_import", map[string]any{
		"session": session, "mode": "merge",
		"files": []any{map[string]any{"path": "topics/导入测试.md", "content": "# 导入测试\n\n结论。"}},
		"index": []any{map[string]any{
			"path": "topics/导入测试.md", "scene": "验证导入链路的改动前必读",
			"examples": []string{"导入怎么用？"},
		}},
	})
	if isErr || done["created"] != float64(1) || done["index_added"] != float64(1) {
		t.Fatalf("import 失败: %v", done)
	}
	read, isErr := c.call("memory_read", map[string]any{"path": "topics/导入测试.md"})
	if isErr || !bytes.Contains([]byte(read["content"].(string)), []byte("结论。")) {
		t.Fatalf("导入后读不到: %v", read)
	}

	// 再导一次：merge 幂等。
	again, isErr := c.call("memory_import", map[string]any{
		"session": session,
		"files":   []any{map[string]any{"path": "topics/导入测试.md", "content": "# 覆盖？\n"}},
	})
	if isErr || again["skipped"] != float64(1) {
		t.Fatalf("merge 应跳过已存在: %v", again)
	}
}

func TestHTTPFullChain(t *testing.T) {
	c := newTestClient(t)

	rules, isErr := c.call("memory_get_rules", map[string]any{})
	if isErr {
		t.Fatalf("get_rules failed: %v", rules)
	}
	session, _ := rules["session"].(string)
	if session == "" {
		t.Fatalf("get_rules missing session: %v", rules)
	}

	noIndex, isErr := c.call("memory_write", map[string]any{
		"path": "topics/支付回调.md", "content": "# 支付回调\n\n## 结论\n- 幂等键 = channel + orderNo\n",
		"mode": "create", "session": session,
	})
	if !isErr || noIndex["error"] != "INDEX_ROW_REQUIRED" {
		t.Fatalf("want INDEX_ROW_REQUIRED, got isError=%v %v", isErr, noIndex)
	}

	created, isErr := c.call("memory_write", map[string]any{
		"path":    "topics/支付回调.md",
		"content": "# 支付回调\n\n> 渠道异步通知的接收、验签、幂等与重试。\n\n## 结论\n- 幂等键 = channel + orderNo\n",
		"mode":    "create", "session": session,
		"index": map[string]any{
			"scene":    "涉及渠道回调、重复扣款、幂等键的改动前必读",
			"examples": []string{"回调为什么会重复扣款？", "幂等键怎么定？"},
		},
	})
	if isErr || created["ok"] != true || created["index_updated"] != true {
		t.Fatalf("create topic failed: %v", created)
	}

	read, isErr := c.call("memory_read", map[string]any{"path": "topics/支付回调.md"})
	if isErr || !bytes.Contains([]byte(read["content"].(string)), []byte("幂等键 = channel + orderNo")) {
		t.Fatalf("read topic: %v", read)
	}

	if _, isErr := c.call("memory_write", map[string]any{
		"path": "TASKS.md", "content": "- [ ] 补回调重试测试\n", "mode": "append", "session": session,
	}); isErr {
		t.Fatal("append TASKS 失败")
	}

	ruleRead, _ := c.call("memory_read", map[string]any{"path": "RULE.md"})
	if !bytes.Contains([]byte(ruleRead["content"].(string)), []byte("topics/支付回调.md")) {
		t.Fatalf("RULE.md missing inserted row:\n%s", ruleRead["content"])
	}
}

func TestHTTPErrorShapes(t *testing.T) {
	c := newTestClient(t)

	res, isErr := c.call("memory_write", map[string]any{
		"path": "TASKS.md", "content": "x", "mode": "append", "session": "s_bogus",
	})
	if !isErr || res["error"] != "READ_RULES_FIRST" {
		t.Fatalf("want READ_RULES_FIRST, got isError=%v %v", isErr, res)
	}
	if _, ok := res["preview"]; !ok {
		t.Fatalf("READ_RULES_FIRST should carry preview: %v", res)
	}

	rules, _ := c.call("memory_get_rules", map[string]any{})
	session := rules["session"].(string)

	res, isErr = c.call("memory_write", map[string]any{
		"path": "TASKS.md", "content": "x", "mode": "replace", "session": session,
	})
	if !isErr || res["error"] != "EXPECTED_HASH_REQUIRED" {
		t.Fatalf("want EXPECTED_HASH_REQUIRED, got %v", res)
	}

	res, isErr = c.call("memory_read", map[string]any{"path": "topics/不存在.md"})
	if !isErr || res["error"] != "NOT_FOUND" {
		t.Fatalf("want NOT_FOUND, got %v", res)
	}
	if _, ok := res["available"].([]any); !ok {
		t.Fatalf("NOT_FOUND should carry available list: %v", res)
	}

	res, isErr = c.call("memory_read", map[string]any{"path": "../etc/passwd"})
	if !isErr || res["error"] != "PATH_NOT_ALLOWED" {
		t.Fatalf("want PATH_NOT_ALLOWED, got %v", res)
	}

	res, isErr = c.call("memory_write", map[string]any{
		"path": "TASKS.md", "content": "x", "mode": "upsert", "session": session,
	})
	if !isErr || res["error"] != "BAD_REQUEST" {
		t.Fatalf("want BAD_REQUEST for bad mode, got %v", res)
	}
}

func TestHTTPProtocolMatrix(t *testing.T) {
	c := newTestClient(t)

	// 1. 通知 → 202，无响应体
	note := []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if rec := c.do(note, nil, false); rec.Code != http.StatusAccepted || rec.Body.Len() != 0 {
		t.Fatalf("notification status=%d body=%q", rec.Code, rec.Body.String())
	}

	// 2. 未知方法 → 200 + -32601
	res, _ := c.rpc("resources/list", nil)
	if e, _ := res["error"].(map[string]any); e["code"] != float64(-32601) {
		t.Fatalf("want -32601, got %v", res)
	}

	// 3. ping
	res, _ = c.rpc("ping", nil)
	if res["result"] == nil {
		t.Fatalf("ping failed: %v", res)
	}

	// 4. 缺 session → 400 + -32600
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	rec := c.do(body, nil, true)
	if rec.Code != 400 {
		t.Fatalf("missing session status=%d body=%s", rec.Code, rec.Body.String())
	}
	res = decodeBody(t, rec)
	if e, _ := res["error"].(map[string]any); e["code"] != float64(-32600) {
		t.Fatalf("want -32600, got %v", res)
	}

	// 5. 未知 session → 404 + -32001
	rec = c.do(body, map[string]string{SessionHeader: "deadbeef"}, false)
	if rec.Code != 404 {
		t.Fatalf("unknown session status=%d", rec.Code)
	}
	res = decodeBody(t, rec)
	if e, _ := res["error"].(map[string]any); e["code"] != float64(-32001) {
		t.Fatalf("want -32001, got %v", res)
	}

	// 6. JSON 解析失败 → 400 + -32700
	rec = c.do([]byte(`{"jsonrpc":`), nil, false)
	if rec.Code != 400 {
		t.Fatalf("bad json status=%d", rec.Code)
	}
	res = decodeBody(t, rec)
	if e, _ := res["error"].(map[string]any); e["code"] != float64(-32700) {
		t.Fatalf("want -32700, got %v", res)
	}

	// 7. Content-Type 不对 → 415
	rec = c.do(body, map[string]string{"Content-Type": "text/plain"}, false)
	if rec.Code != 415 {
		t.Fatalf("bad content-type status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 8. Accept 只要 SSE → 406
	rec = c.do(body, map[string]string{"Accept": "text/event-stream"}, false)
	if rec.Code != 406 {
		t.Fatalf("sse-only accept status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 9. 未知协议版本头 → 400
	rec = c.do(body, map[string]string{ProtocolHeader: "1999-01-01"}, false)
	if rec.Code != 400 {
		t.Fatalf("bad protocol header status=%d", rec.Code)
	}

	// 10. initialize 请求不支持的版本 → 协商回服务端默认版本
	init := c.initialize("1999-01-01")
	if init["protocolVersion"] != DefaultProtocol {
		t.Fatalf("negotiated = %v, want %s", init["protocolVersion"], DefaultProtocol)
	}

	// 11. 批量消息：一个 ping + 一个通知 → 200 数组，只有 ping 的响应
	batch := []byte(`[{"jsonrpc":"2.0","id":7,"method":"ping"},{"jsonrpc":"2.0","method":"notifications/initialized"}]`)
	rec = c.do(batch, nil, false)
	if rec.Code != 200 {
		t.Fatalf("batch status=%d body=%s", rec.Code, rec.Body.String())
	}
	var arr []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &arr); err != nil || len(arr) != 1 {
		t.Fatalf("batch response = %s", rec.Body.String())
	}

	// 12. GET 不提供 SSE → 405
	getRec := httptest.NewRecorder()
	c.h.ServeHTTP(getRec, httptest.NewRequest(http.MethodGet, "/mcp/demo", nil))
	if getRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status=%d", getRec.Code)
	}

	// 13. DELETE 结束会话 → 204，再用同一 session → 404
	delReq := httptest.NewRequest(http.MethodDelete, "/mcp/demo", nil)
	delReq.Header.Set(SessionHeader, c.session)
	delRec := httptest.NewRecorder()
	c.h.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("DELETE status=%d body=%s", delRec.Code, delRec.Body.String())
	}
	rec = c.do(body, nil, false)
	if rec.Code != 404 {
		t.Fatalf("after DELETE status=%d, want 404", rec.Code)
	}

	// 14. 跨项目复用 session → 404
	other := &testClient{t: t, h: c.h, project: "other"}
	other.initialize(DefaultProtocol)
	req := httptest.NewRequest(http.MethodPost, "/mcp/demo", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(SessionHeader, other.session)
	rec = httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatalf("cross-project session status=%d, want 404", rec.Code)
	}
}

func TestHTTPToolArgumentErrors(t *testing.T) {
	c := newTestClient(t)

	// 未知工具 → -32602（协议错误，不是 isError）
	res, _ := c.rpc("tools/call", map[string]any{"name": "memory_delete", "arguments": map[string]any{}})
	if e, _ := res["error"].(map[string]any); e["code"] != float64(-32602) {
		t.Fatalf("want -32602 for unknown tool, got %v", res)
	}
	// 缺 path → -32602
	res, _ = c.rpc("tools/call", map[string]any{"name": "memory_read", "arguments": map[string]any{}})
	if e, _ := res["error"].(map[string]any); e["code"] != float64(-32602) {
		t.Fatalf("want -32602 for missing path, got %v", res)
	}
	// 业务错误（未读规则）→ HTTP 200 + result.isError
	rules, _ := c.call("memory_get_rules", map[string]any{})
	if _, isErr := c.call("memory_write", map[string]any{
		"path": "TASKS.md", "content": "x", "mode": "append", "session": "s_nope",
	}); !isErr {
		t.Fatalf("规则会话缺失应该是 isError 而非协议错误（rules=%v）", rules)
	}
}

func TestHTTPProjectPathValidation(t *testing.T) {
	h := newTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/mcp/a/b", bytes.NewReader([]byte("{}")))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("deep path status=%d, want 404", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/mcp/Bad_Project", bytes.NewReader([]byte("{}")))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad project status=%d, want 400", rec.Code)
	}
}

func TestHTTPImportEndpoint(t *testing.T) {
	h := newTestHandler(t)

	body := `{"rule_md":"# demo 知识索引\n\n## 文档索引\n\n| 文档 | 适用场景 | 举例 |\n| --- | --- | --- |\n",` +
		`"files":[{"path":"topics/后端架构.md","content":"# 后端架构\n\n路由在 server/app/router.js。\n"}]}`
	req := httptest.NewRequest(http.MethodPost, "/import/demo", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("import status=%d body=%s", rec.Code, rec.Body.String())
	}
	out := decodeBody(t, rec)
	if out["created"] != float64(1) || out["index_added"] != float64(1) {
		t.Fatalf("import 结果不对: %v", out)
	}

	// GET 不允许
	req = httptest.NewRequest(http.MethodGet, "/import/demo", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /import status=%d want 405", rec.Code)
	}

	// 非 JSON 请求体 → 415
	req = httptest.NewRequest(http.MethodPost, "/import/demo", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "text/plain")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("text/plain status=%d want 415", rec.Code)
	}
}

func TestHTTPSkillTools(t *testing.T) {
	c := newTestClient(t)

	const skillMD = "---\nname: code-review\ndescription: 涉及代码评审、CR 规范时必读\n---\n\n# 代码评审\n> 统一口径。\n"

	// 1. skill_list 无需 session 即可读，初始为空。
	list, isErr := c.call("skill_list", map[string]any{})
	if isErr || list["global"] == nil || list["project"] == nil {
		t.Fatalf("skill_list: %v %v", list, isErr)
	}

	// 2. 没读规则就 promote → 428 门控。
	_, isErr = c.call("skill_promote", map[string]any{"action": "submit", "name": "code-review"})
	if !isErr {
		t.Fatal("无 session 的 promote 应被拒绝")
	}

	// 3. 读规则拿 session。
	rules, isErr := c.call("memory_get_rules", map[string]any{})
	if isErr {
		t.Fatalf("get_rules: %v", rules)
	}
	sess, _ := rules["session"].(string)
	if rules["global_skills"] == nil {
		t.Fatal("get_rules 应返回 global_skills 字段")
	}

	// 4. memory_write 建项目技能（frontmatter 校验复用 Write）。
	if _, isErr := c.call("memory_write", map[string]any{
		"path": "skills/code-review/SKILL.md", "mode": "create", "session": sess, "content": skillMD,
	}); isErr {
		t.Fatal("create skill 应成功")
	}

	// 5. memory_read 读项目技能全文。
	read, isErr := c.call("memory_read", map[string]any{"path": "skills/code-review/SKILL.md"})
	if isErr || !strings.Contains(read["content"].(string), "name: code-review") {
		t.Fatalf("memory_read skill: %v %v", read, isErr)
	}

	// 6. skill_list 现在能看到项目技能。
	list, _ = c.call("skill_list", map[string]any{})
	proj, _ := list["project"].([]any)
	if len(proj) != 1 {
		t.Fatalf("项目技能数 = %d want 1: %v", len(proj), list)
	}

	// 7. submit → approve。
	sub, isErr := c.call("skill_promote", map[string]any{
		"action": "submit", "name": "code-review", "reason": "通用", "session": sess,
	})
	if isErr || sub["status"] != "pending" {
		t.Fatalf("submit: %v %v", sub, isErr)
	}
	ap, isErr := c.call("skill_promote", map[string]any{
		"action": "approve", "name": "code-review", "session": sess,
	})
	if isErr || ap["status"] != "approved" {
		t.Fatalf("approve: %v %v", ap, isErr)
	}

	// 8. 再读规则，global_skills 里出现 approved 技能。
	rules, _ = c.call("memory_get_rules", map[string]any{})
	gs, _ := rules["global_skills"].([]any)
	if len(gs) != 1 {
		t.Fatalf("global_skills 数 = %d want 1: %v", len(gs), rules["global_skills"])
	}

	// 9. 全局技能只读：memory_write 到 @global 被拒。
	_, isErr = c.call("memory_write", map[string]any{
		"path": "@global/skills/code-review/SKILL.md", "mode": "create", "session": sess, "content": skillMD,
	})
	if !isErr {
		t.Fatal("memory_write 到 @global 应被拒绝")
	}
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	return out
}
