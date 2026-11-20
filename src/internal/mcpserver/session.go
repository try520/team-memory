package mcpserver

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

const mcpSessionTTL = 24 * time.Hour

// mcpSession 是 Streamable HTTP 传输层会话，与 kb 的「rule session」是两回事：
// 前者由 Mcp-Session-Id 头承载，只证明「这条连接初始化过」；
// 后者由 memory_get_rules 签发，证明「规则确实进了模型上下文」。
type mcpSession struct {
	project  string
	protocol string
	created  time.Time
	lastSeen time.Time
}

type sessionStore struct {
	mu  sync.Mutex
	m   map[string]*mcpSession
	now func() time.Time
	ttl time.Duration
}

func newSessionStore() *sessionStore {
	return &sessionStore{m: make(map[string]*mcpSession), now: time.Now, ttl: mcpSessionTTL}
}

// create 签发全局唯一、仅含可见 ASCII 的会话 ID。
func (s *sessionStore) create(project, protocol string) string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand 不可用: " + err.Error())
	}
	id := hex.EncodeToString(b)
	now := s.now()
	s.mu.Lock()
	s.gcLocked()
	s.m[id] = &mcpSession{project: project, protocol: protocol, created: now, lastSeen: now}
	s.mu.Unlock()
	return id
}

// get 取会话并续期；项目不匹配视同不存在（端点即项目边界）。
func (s *sessionStore) get(id, project string) (*mcpSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()
	v, ok := s.m[id]
	if !ok || v.project != project {
		return nil, false
	}
	v.lastSeen = s.now()
	return v, true
}

func (s *sessionStore) del(id string) {
	s.mu.Lock()
	delete(s.m, id)
	s.mu.Unlock()
}

func (s *sessionStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()
	return len(s.m)
}

func (s *sessionStore) gcLocked() {
	cut := s.now().Add(-s.ttl)
	for k, v := range s.m {
		if v.lastSeen.Before(cut) {
			delete(s.m, k)
		}
	}
}
