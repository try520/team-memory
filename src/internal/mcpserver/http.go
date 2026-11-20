package mcpserver

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"strings"
	"time"

	"team-memory/internal/kb"
	"team-memory/internal/store"
)

const (
	Version         = "0.1.1"
	DefaultProtocol = "2025-06-18"
	SessionHeader   = "Mcp-Session-Id"
	ProtocolHeader  = "MCP-Protocol-Version"
	maxBodyBytes    = 4 << 20
	maxImportBytes  = 64 << 20
)

var supportedProtocols = map[string]bool{
	"2025-06-18": true,
	"2025-03-26": true,
	"2024-11-05": true,
}

type Server struct {
	svc      *kb.Service
	st       *store.Store
	log      *log.Logger
	started  time.Time
	sessions *sessionStore
}

func NewHandler(svc *kb.Service, st *store.Store, logger *log.Logger) http.Handler {
	s := &Server{svc: svc, st: st, log: logger, started: time.Now(), sessions: newSessionStore()}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/mcp", s.handleMeta)
	mux.HandleFunc("/mcp/", s.handleProject)
	mux.HandleFunc("/import/", s.handleImport)
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeJSON(w, 405, map[string]any{"error": "METHOD_NOT_ALLOWED"})
		return
	}
	writeJSON(w, 200, map[string]any{
		"ok":       true,
		"projects": len(s.svc.ListProjects()),
		"pid":      os.Getpid(),
		"version":  Version,
		"uptime_s": int(time.Since(s.started).Seconds()),
		"sessions": s.sessions.count(),
	})
}

// handleMeta 只是服务发现；真正的 MCP 端点在 /mcp/<project>。
func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeJSON(w, 400, map[string]any{
			"error": "PROJECT_REQUIRED",
			"hint":  "MCP 端点为 /mcp/<project>；未知项目会在首次 get_rules 时自动创建。",
		})
		return
	}
	writeJSON(w, 200, map[string]any{
		"service":  "team-memory",
		"version":  Version,
		"protocol": DefaultProtocol,
		"projects": s.svc.ListProjects(),
		"tools":    []string{"memory_get_rules", "memory_read", "memory_write", "memory_import", "skill_list", "skill_promote"},
		"endpoint": "/mcp/<project>",
		"import":   "/import/<project>",
		"hint":     "项目由 URL 路径绑定，工具不接受 project 参数；初始化历史知识用 POST /import/<project>。",
	})
}

func (s *Server) handleProject(w http.ResponseWriter, r *http.Request) {
	project := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/mcp/"), "/")
	if project == "" || strings.Contains(project, "/") {
		writeJSON(w, 404, map[string]any{"error": "NOT_FOUND", "hint": "端点格式为 /mcp/<project>"})
		return
	}
	if _, err := s.st.ProjectDir(project); err != nil {
		writeJSON(w, 400, map[string]any{"error": "BAD_PROJECT", "hint": "项目名只允许小写字母、数字与中划线，例如 my-project"})
		return
	}
	switch r.Method {
	case http.MethodPost:
		s.handlePost(w, r, project)
	case http.MethodGet:
		// Streamable HTTP 允许服务端不提供 GET 的 SSE 推送通道，用 405 明示。
		w.Header().Set("Allow", "POST, DELETE")
		writeJSON(w, 405, map[string]any{
			"error": "SSE_NOT_SUPPORTED",
			"hint":  "本服务不主动向客户端推送消息，没有 GET SSE 流；请求请用 POST JSON-RPC。",
		})
	case http.MethodDelete:
		s.handleDelete(w, r, project)
	default:
		w.Header().Set("Allow", "POST, GET, DELETE")
		writeJSON(w, 405, map[string]any{"error": "METHOD_NOT_ALLOWED"})
	}
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request, project string) {
	sid := r.Header.Get(SessionHeader)
	if sid == "" {
		writeJSON(w, 400, map[string]any{"error": "MISSING_SESSION", "hint": "DELETE 必须带 Mcp-Session-Id。"})
		return
	}
	if _, ok := s.sessions.get(sid, project); !ok {
		writeJSON(w, 404, map[string]any{"error": "SESSION_NOT_FOUND", "hint": "会话不存在或已过期，重新 initialize 即可。"})
		return
	}
	s.sessions.del(sid)
	w.WriteHeader(http.StatusNoContent)
}

// handleImport 是初始化入口：把已有知识一次性灌进项目。
// 不属于 MCP 工具，因为它是管理操作：不需要 session、批量、幂等。
func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	project := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/import/"), "/")
	if project == "" || strings.Contains(project, "/") {
		writeJSON(w, 404, map[string]any{"error": "NOT_FOUND", "hint": "端点格式为 /import/<project>"})
		return
	}
	if _, err := s.st.ProjectDir(project); err != nil {
		writeJSON(w, 400, map[string]any{"error": "BAD_PROJECT", "hint": "项目名只允许小写字母、数字与中划线，例如 my-project"})
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeJSON(w, 405, map[string]any{"error": "METHOD_NOT_ALLOWED", "hint": "初始化只接受 POST JSON。", "import": "/import/<project>"})
		return
	}
	if !isJSONContentType(r.Header.Get("Content-Type")) {
		writeJSON(w, 415, map[string]any{"error": "UNSUPPORTED_MEDIA_TYPE", "hint": "Content-Type 必须是 application/json。"})
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxImportBytes))
	if err != nil {
		writeJSON(w, 413, map[string]any{"error": "PAYLOAD_TOO_LARGE", "hint": "单次导入上限 64MB；更大请分批。"})
		return
	}
	var req kb.ImportReq
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, 400, map[string]any{"error": "BAD_JSON", "message": err.Error(), "hint": `需要 {mode?, dry_run?, rule_md?, files:[{path,content}], index?:[{path,scene,examples}]}`})
		return
	}
	resp, e := s.svc.Import(project, req)
	if e != nil {
		payload := map[string]any{"error": e.Code}
		if e.Message != "" {
			payload["message"] = e.Message
		}
		if e.Hint != "" {
			payload["hint"] = e.Hint
		}
		writeJSON(w, e.Status, payload)
		return
	}
	writeJSON(w, 200, resp)
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type msgOutcome struct {
	resp       map[string]any
	status     int
	newSession string
}

func (s *Server) handlePost(w http.ResponseWriter, r *http.Request, project string) {
	if !isJSONContentType(r.Header.Get("Content-Type")) {
		writeJSON(w, 415, map[string]any{
			"error": "UNSUPPORTED_MEDIA_TYPE",
			"hint":  "Content-Type 必须是 application/json（可带 charset）。",
		})
		return
	}
	if acc := r.Header.Get("Accept"); acc != "" && !acceptsJSON(acc) {
		writeJSON(w, 406, map[string]any{
			"error": "NOT_ACCEPTABLE",
			"hint":  "本服务用 application/json 回包；请带 Accept: application/json, text/event-stream。",
		})
		return
	}
	if pv := r.Header.Get(ProtocolHeader); pv != "" && !supportedProtocols[pv] {
		writeRPCError(w, 400, nil, -32600, "不支持的 MCP-Protocol-Version: "+pv,
			map[string]any{"supported": []string{"2025-06-18", "2025-03-26", "2024-11-05"}})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": "BAD_REQUEST", "hint": "读取请求体失败"})
		return
	}
	if len(body) > maxBodyBytes {
		writeJSON(w, 413, map[string]any{"error": "PAYLOAD_TOO_LARGE", "hint": "单条 JSON-RPC 请求上限 4MB"})
		return
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		writeRPCError(w, 400, nil, -32700, "空请求体", nil)
		return
	}
	if trimmed[0] == '[' {
		s.handleBatch(w, r, project, trimmed)
		return
	}

	var req rpcRequest
	if err := json.Unmarshal(trimmed, &req); err != nil {
		writeRPCError(w, 400, nil, -32700, "解析 JSON 失败: "+err.Error(), nil)
		return
	}
	out := s.handleMessage(&req, project, r)
	if out.newSession != "" {
		w.Header().Set(SessionHeader, out.newSession)
	}
	if out.resp == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	status := out.status
	if status == 0 {
		status = 200
	}
	writeJSON(w, status, out.resp)
}

// handleBatch 支持 2025-03-26 的批量消息；2025-06-18 虽已移除 batch，但兼容旧客户端无害。
func (s *Server) handleBatch(w http.ResponseWriter, r *http.Request, project string, body []byte) {
	var raws []json.RawMessage
	if err := json.Unmarshal(body, &raws); err != nil {
		writeRPCError(w, 400, nil, -32700, "解析批量 JSON 失败", nil)
		return
	}
	if len(raws) == 0 {
		writeRPCError(w, 400, nil, -32600, "批量消息不能为空", nil)
		return
	}
	var out []any
	status := 200
	for _, raw := range raws {
		var req rpcRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			out = append(out, errorBody(nil, -32600, "批量中的元素不是合法 JSON-RPC 对象", nil))
			continue
		}
		o := s.handleMessage(&req, project, r)
		if o.newSession != "" {
			w.Header().Set(SessionHeader, o.newSession)
		}
		if o.status != 0 && o.status != 200 && status == 200 {
			status = o.status
		}
		if o.resp != nil {
			out = append(out, o.resp)
		}
	}
	if len(out) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeJSON(w, status, out)
}

func (s *Server) handleMessage(req *rpcRequest, project string, r *http.Request) msgOutcome {
	if req.JSONRPC != "2.0" {
		return msgOutcome{resp: errorBody(req.ID, -32600, `jsonrpc 必须是 "2.0"`, nil)}
	}
	if strings.TrimSpace(req.Method) == "" {
		return msgOutcome{resp: errorBody(req.ID, -32600, "method 不能为空", nil)}
	}
	isNotification := len(req.ID) == 0 || string(req.ID) == "null"

	if req.Method == "initialize" {
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		proto := negotiateProtocol(p.ProtocolVersion)
		sid := s.sessions.create(project, proto)
		return msgOutcome{
			status:     200,
			newSession: sid,
			resp: okBody(req.ID, map[string]any{
				"protocolVersion": proto,
				"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
				"serverInfo":      map[string]any{"name": "team-memory", "version": Version},
				"instructions": "使用顺序：先 memory_get_rules 读服务级规则与项目索引（返回的 session 供写入），" +
					"按索引「适用场景」挑 1~2 篇 memory_read；需要源码与字段时按文档里的路径读仓库、按表名查库，不要一次读完 topics/。" +
					"任务收尾必须回写：命中服务级规则第 4 条任一硬条件（定了方案/踩坑解决/接口或表结构变更/关键结论/任务状态变化/纠正旧结论）就立即 memory_write；" +
					"每次会话都写 journal，确实无事可记也写「本次无需记录：原因」；写入响应里的 next 会告诉你下一步。" +
					"首次接入已有项目时，用你自己的文件工具读源文档，分批调 memory_import（先 dry_run 预演），把历史知识导入，再用 memory_write 记本次结论。" +
					"技能：用户说「根据之前的操作生成 xx 技能」用 memory_write 写 skills/<name>/SKILL.md（frontmatter 含 name/description）；" +
					"「修改 / 完善 xx 技能」用 memory_write 的 append/replace；「把 xx 技能提升为全局技能」用 skill_promote(action=submit) 提审，" +
					"再由维护者 approve；全局技能会自动列在各项目 get_rules 的 global_skills 中，可用 skill_list 查看。",
			}),
		}
	}

	sid := r.Header.Get(SessionHeader)
	if sid == "" {
		return msgOutcome{
			status: 400,
			resp: errorBody(req.ID, -32600, "缺少 Mcp-Session-Id：先 POST initialize", map[string]any{
				"hint": "initialize 的响应头会返回 Mcp-Session-Id，后续每个请求都要带上。",
			}),
		}
	}
	if _, ok := s.sessions.get(sid, project); !ok {
		return msgOutcome{
			status: 404,
			resp: errorBody(req.ID, -32001, "会话不存在、已过期或不属于本项目", map[string]any{
				"hint": "收到 404 时重新 POST initialize，再重试。",
			}),
		}
	}
	if isNotification {
		// notifications/initialized 等通知无需回包。
		return msgOutcome{}
	}

	switch req.Method {
	case "ping":
		return msgOutcome{resp: okBody(req.ID, map[string]any{})}
	case "tools/list":
		return msgOutcome{resp: okBody(req.ID, map[string]any{"tools": toolDefs()})}
	case "tools/call":
		result, rerr := s.callTool(req.Params, project)
		if rerr != nil {
			return msgOutcome{resp: errorBody(req.ID, rerr.Code, rerr.Message, rerr.Data)}
		}
		return msgOutcome{resp: okBody(req.ID, result)}
	default:
		return msgOutcome{resp: errorBody(req.ID, -32601, "未知方法: "+req.Method, nil)}
	}
}

func negotiateProtocol(v string) string {
	if supportedProtocols[v] {
		return v
	}
	return DefaultProtocol
}

type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (s *Server) callTool(raw json.RawMessage, project string) (any, *rpcError) {
	var p callParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &rpcError{Code: -32602, Message: "arguments 不是合法对象", Data: map[string]any{
			"hint": `tools/call 需要 {"name":"…","arguments":{…}}`,
		}}
	}
	if p.Name == "" {
		return nil, &rpcError{Code: -32602, Message: "缺少工具名 name"}
	}
	start := time.Now()
	defer func() {
		s.log.Printf("project=%s tool=%s cost=%s", project, p.Name, time.Since(start).Round(time.Millisecond))
	}()

	switch p.Name {
	case "memory_get_rules":
		resp, e := s.svc.GetRules(project)
		if e != nil {
			return toolError(e), nil
		}
		return toolOK(resp), nil
	case "memory_read":
		var a struct {
			Path   string `json:"path"`
			Rev    string `json:"rev"`
			Offset int    `json:"offset"`
		}
		if err := json.Unmarshal(p.Arguments, &a); err != nil {
			return nil, &rpcError{Code: -32602, Message: "arguments 解析失败", Data: map[string]any{"hint": "需要 {path, rev?, offset?}"}}
		}
		if strings.TrimSpace(a.Path) == "" {
			return nil, &rpcError{Code: -32602, Message: "path 必填", Data: map[string]any{"hint": "例如 topics/支付回调.md"}}
		}
		resp, e := s.svc.Read(project, a.Path, a.Rev, a.Offset)
		if e != nil {
			return toolError(e), nil
		}
		return toolOK(resp), nil
	case "memory_write":
		var a struct {
			Path            string       `json:"path"`
			Content         string       `json:"content"`
			Mode            string       `json:"mode"`
			ExpectedHash    string       `json:"expectedHash"`
			ExpectedHashAlt string       `json:"expected_hash"`
			Session         string       `json:"session"`
			Index           *kb.IndexRow `json:"index"`
		}
		if err := json.Unmarshal(p.Arguments, &a); err != nil {
			return nil, &rpcError{Code: -32602, Message: "arguments 解析失败", Data: map[string]any{
				"hint": "需要 {path, content, mode, expectedHash?, session, index?}",
			}}
		}
		if strings.TrimSpace(a.Path) == "" {
			return nil, &rpcError{Code: -32602, Message: "path 必填", Data: map[string]any{"hint": "例如 topics/支付回调.md"}}
		}
		if strings.TrimSpace(a.Mode) == "" {
			return nil, &rpcError{Code: -32602, Message: "mode 必填", Data: map[string]any{"hint": "create / append / replace / reindex"}}
		}
		expected := a.ExpectedHash
		if expected == "" {
			expected = a.ExpectedHashAlt
		}
		if a.Index != nil && len(a.Index.Examples) == 0 {
			a.Index.Examples = []string{}
		}
		resp, e := s.svc.Write(project, kb.WriteReq{
			Path: a.Path, Content: a.Content, Mode: a.Mode,
			ExpectedHash: expected, Session: a.Session, Index: a.Index,
		})
		if e != nil {
			return toolError(e), nil
		}
		return toolOK(resp), nil
	case "memory_import":
		var a struct {
			Session string              `json:"session"`
			Mode    string              `json:"mode"`
			DryRun  bool                `json:"dry_run"`
			RuleMD  string              `json:"rule_md"`
			Files   []kb.ImportFile     `json:"files"`
			Index   []kb.ImportIndexRow `json:"index"`
		}
		if err := json.Unmarshal(p.Arguments, &a); err != nil {
			return nil, &rpcError{Code: -32602, Message: "arguments 解析失败", Data: map[string]any{
				"hint": "需要 {session, mode?, dry_run?, rule_md?, files:[{path,content}], index?:[{path,title?,scene,examples}]}",
			}}
		}
		resp, e := s.svc.ImportFromTool(project, a.Session, kb.ImportReq{
			Mode: a.Mode, DryRun: a.DryRun, RuleMD: a.RuleMD, Files: a.Files, Index: a.Index,
		})
		if e != nil {
			return toolError(e), nil
		}
		return toolOK(resp), nil
	case "skill_list":
		resp, e := s.svc.SkillList(project)
		if e != nil {
			return toolError(e), nil
		}
		return toolOK(resp), nil
	case "skill_promote":
		var a struct {
			Action  string `json:"action"`
			Name    string `json:"name"`
			Reason  string `json:"reason"`
			Session string `json:"session"`
		}
		if err := json.Unmarshal(p.Arguments, &a); err != nil {
			return nil, &rpcError{Code: -32602, Message: "arguments 解析失败", Data: map[string]any{
				"hint": "需要 {action, name, session, reason?}",
			}}
		}
		resp, e := s.svc.SkillPromote(project, kb.SkillPromoteReq{
			Action: a.Action, Name: a.Name, Reason: a.Reason, Session: a.Session,
		})
		if e != nil {
			return toolError(e), nil
		}
		return toolOK(resp), nil
	default:
		return nil, &rpcError{Code: -32602, Message: "未知工具: " + p.Name, Data: map[string]any{
			"available": []string{"memory_get_rules", "memory_read", "memory_write", "memory_import", "skill_list", "skill_promote"},
		}}
	}
}

func okBody(id json.RawMessage, result any) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": idOrNull(id), "result": result}
}

func errorBody(id json.RawMessage, code int, msg string, data any) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": idOrNull(id), "error": rpcError{Code: code, Message: msg, Data: data}}
}

func idOrNull(id json.RawMessage) any {
	if len(id) == 0 {
		return nil
	}
	return id
}

func writeRPCError(w http.ResponseWriter, status int, id json.RawMessage, code int, msg string, data any) {
	writeJSON(w, status, errorBody(id, code, msg, data))
}

type content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// toolOK 把结构化结果同时放进 text 与 structuredContent，兼容不同客户端。
func toolOK(v any) map[string]any {
	text, _ := json.Marshal(v)
	return map[string]any{
		"content":           []content{{Type: "text", Text: string(text)}},
		"structuredContent": v,
		"isError":           false,
	}
}

// toolError 表示「工具执行失败」而不是「协议错误」：HTTP/JSON-RPC 都是成功的，
// 由模型读 isError 与 error 码自行纠正。
func toolError(e *kb.Error) map[string]any {
	payload := map[string]any{"error": e.Code}
	if e.Message != "" {
		payload["message"] = e.Message
	}
	if e.Hint != "" {
		payload["hint"] = e.Hint
	}
	for k, v := range e.Extra {
		payload[k] = v
	}
	text, _ := json.Marshal(payload)
	return map[string]any{
		"content": []content{{Type: "text", Text: string(text)}},
		"isError": true,
	}
}

func isJSONContentType(v string) bool {
	if strings.TrimSpace(v) == "" {
		return false
	}
	mt, _, err := mime.ParseMediaType(v)
	if err != nil {
		return false
	}
	return mt == "application/json"
}

func acceptsJSON(accept string) bool {
	a := strings.ToLower(accept)
	return strings.Contains(a, "application/json") || strings.Contains(a, "*/*") || strings.Contains(a, "application/*")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
