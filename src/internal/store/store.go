package store

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrBadProject = errors.New("非法的项目名")
	ErrBadPath    = errors.New("非法的路径")
)

// Store 负责所有磁盘操作：路径校验、按路径串行、原子写、历史快照。
type Store struct {
	root  string
	mu    sync.Mutex
	locks map[string]*keyLock
}

type keyLock struct {
	mu   sync.Mutex
	refs int
}

func New(root string) *Store {
	return &Store{root: root, locks: make(map[string]*keyLock)}
}

func (s *Store) Root() string { return s.root }

// ProjectDir 返回项目目录，并校验 slug。
func (s *Store) ProjectDir(slug string) (string, error) {
	if !validSlug(slug) {
		return "", ErrBadProject
	}
	return filepath.Join(s.root, "projects", slug), nil
}

func validSlug(slug string) bool {
	if slug == "" || len(slug) > 64 {
		return false
	}
	for i, r := range slug {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '-' && i > 0 && i < len(slug)-1:
		default:
			return false
		}
	}
	return true
}

func allowedPath(rel string) bool {
	switch rel {
	case "RULE.md", "TASKS.md":
		return true
	}
	if strings.HasPrefix(rel, "topics/") || strings.HasPrefix(rel, "journal/") {
		return true
	}
	// 技能文件：项目技能为 skills/<name>/SKILL.md；全局技能用 @global/ 前缀寻址。
	if isSkillFile(rel) {
		return true
	}
	if strings.HasPrefix(rel, "@global/") {
		return isSkillFile(strings.TrimPrefix(rel, "@global/"))
	}
	return false
}

// isSkillFile 判断是否为合法的技能文件路径 skills/<name>/SKILL.md。
func isSkillFile(rel string) bool {
	parts := strings.Split(rel, "/")
	return len(parts) == 3 && parts[0] == "skills" && parts[2] == "SKILL.md" && ValidSkillName(parts[1])
}

// ValidSkillName 校验技能名：1-64 位小写字母、数字与中间的中划线。
func ValidSkillName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '-' && i > 0 && i < len(name)-1:
		default:
			return false
		}
	}
	return true
}

// Abs 把相对路径解析为绝对路径，同时做白名单与穿越校验。
// @global/ 前缀路由到数据根目录下的全局技能区，其余路径限定在项目目录内。
func (s *Store) Abs(slug, rel string) (string, error) {
	rel = strings.TrimSpace(strings.ReplaceAll(rel, "\\", "/"))
	if rel == "" || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, ".") {
		return "", ErrBadPath
	}
	if path.Clean(rel) != rel {
		return "", ErrBadPath
	}
	if !strings.HasSuffix(rel, ".md") || !allowedPath(rel) {
		return "", ErrBadPath
	}
	if strings.HasPrefix(rel, "@global/") {
		return filepath.Join(s.root, filepath.FromSlash(strings.TrimPrefix(rel, "@global/"))), nil
	}
	dir, err := s.ProjectDir(slug)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, filepath.FromSlash(rel)), nil
}

// Lock 按「项目/路径」串行，返回解锁函数。
func (s *Store) Lock(key string) func() {
	s.mu.Lock()
	l, ok := s.locks[key]
	if !ok {
		l = &keyLock{}
		s.locks[key] = l
	}
	l.refs++
	s.mu.Unlock()

	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		s.mu.Lock()
		l.refs--
		if l.refs == 0 {
			delete(s.locks, key)
		}
		s.mu.Unlock()
	}
}

func (s *Store) ReadFile(slug, rel string) ([]byte, time.Time, error) {
	p, err := s.Abs(slug, rel)
	if err != nil {
		return nil, time.Time{}, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, time.Time{}, err
	}
	st, err := os.Stat(p)
	if err != nil {
		return b, time.Time{}, nil
	}
	return b, st.ModTime(), nil
}

func (s *Store) Exists(slug, rel string) bool {
	p, err := s.Abs(slug, rel)
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

// WriteAtomic 用 tmp → fsync → rename 替换文件，保证任何时刻读到的都是完整内容。
func (s *Store) WriteAtomic(slug, rel string, data []byte) error {
	p, err := s.Abs(slug, rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp-" + randHex(4)
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return err
	}
	syncDir(filepath.Dir(p))
	return nil
}

// Append 单次追加写，调用方需已持有该路径的锁。
func (s *Store) Append(slug, rel string, data []byte) error {
	p, err := s.Abs(slug, rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Snapshot 把被覆盖的旧内容存到 .history/<rel>/<时间>.md，每个文件保留最近 50 份。
func (s *Store) Snapshot(slug, rel string, old []byte) error {
	dir, err := s.ProjectDir(slug)
	if err != nil {
		return err
	}
	histDir := filepath.Join(dir, ".history", filepath.FromSlash(rel))
	if err := os.MkdirAll(histDir, 0o755); err != nil {
		return err
	}
	name := time.Now().Format("2006-01-02T15-04-05.000") + ".md"
	dest := filepath.Join(histDir, name)
	// 同一毫秒内多次覆盖时加序号，避免快照互相覆盖。
	for i := 2; ; i++ {
		if _, statErr := os.Stat(dest); errors.Is(statErr, os.ErrNotExist) {
			break
		}
		dest = filepath.Join(histDir, strings.TrimSuffix(name, ".md")+fmt.Sprintf("-%d.md", i))
	}
	if err := os.WriteFile(dest, old, 0o644); err != nil {
		return err
	}
	prune(histDir, 50)
	return nil
}

// Snapshots 列出某个文件的历史快照文件名（旧 → 新）。
func (s *Store) Snapshots(slug, rel string) ([]string, error) {
	dir, err := s.ProjectDir(slug)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(dir, ".history", filepath.FromSlash(rel)))
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func (s *Store) ReadSnapshot(slug, rel, rev string) ([]byte, error) {
	if rev == "" || strings.ContainsAny(rev, `/\`) || path.Clean(rev) != rev {
		return nil, ErrBadPath
	}
	dir, err := s.ProjectDir(slug)
	if err != nil {
		return nil, err
	}
	p := filepath.Join(dir, ".history", filepath.FromSlash(rel), rev)
	return os.ReadFile(p)
}

type FileEntry struct {
	Path    string    `json:"path"`
	Lines   int       `json:"lines"`
	Bytes   int       `json:"bytes"`
	Updated time.Time `json:"updated"`
}

// ListFiles 列出项目内所有可见 md（不含 .history）。
func (s *Store) ListFiles(slug string) ([]FileEntry, error) {
	dir, err := s.ProjectDir(slug)
	if err != nil {
		return nil, err
	}
	var out []FileEntry
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == ".history" || strings.HasPrefix(rel, ".history/") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".md") {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		out = append(out, FileEntry{Path: rel, Lines: countLines(b), Bytes: len(b), Updated: info.ModTime()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (s *Store) ListTopics(slug string) ([]string, error) {
	dir, err := s.ProjectDir(slug)
	if err != nil {
		return nil, err
	}
	root := filepath.Join(dir, "topics")
	var out []string
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

func (s *Store) ListProjects() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(s.root, "projects"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// GlobalSkillsDir 返回全局技能根目录 <root>/skills。
func (s *Store) GlobalSkillsDir() string {
	return filepath.Join(s.root, "skills")
}

// EnsureGlobalSkillsLayout 创建全局技能目录。
func (s *Store) EnsureGlobalSkillsLayout() error {
	return os.MkdirAll(s.GlobalSkillsDir(), 0o755)
}

// ListGlobalSkills 列出全局技能名（仅含带合法 SKILL.md 的目录）。
func (s *Store) ListGlobalSkills() ([]string, error) {
	entries, err := os.ReadDir(s.GlobalSkillsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || !ValidSkillName(e.Name()) {
			continue
		}
		if _, err := os.Stat(filepath.Join(s.GlobalSkillsDir(), e.Name(), "SKILL.md")); err == nil {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// RemoveGlobalSkill 删除一个全局技能目录（用于 reject/cancel）。
func (s *Store) RemoveGlobalSkill(name string) error {
	if !ValidSkillName(name) {
		return ErrBadPath
	}
	return os.RemoveAll(filepath.Join(s.GlobalSkillsDir(), name))
}

// RemoveProjectSkill 删除项目技能目录：提升为全局后「移动」而不是「复制」。
func (s *Store) RemoveProjectSkill(slug, name string) error {
	if !ValidSkillName(name) {
		return ErrBadPath
	}
	dir, err := s.ProjectDir(slug)
	if err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(dir, "skills", name))
}

func (s *Store) EnsureProjectLayout(slug string) error {
	dir, err := s.ProjectDir(slug)
	if err != nil {
		return err
	}
	for _, sub := range []string{"", "topics", "journal", ".history"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) WriteFileIfAbsent(slug, rel string, data []byte) (bool, error) {
	if s.Exists(slug, rel) {
		return false, nil
	}
	return true, s.WriteAtomic(slug, rel, data)
}

func (s *Store) EnsureDir(p string) error { return os.MkdirAll(p, 0o755) }

func countLines(b []byte) int {
	if len(b) == 0 {
		return 0
	}
	n := strings.Count(string(b), "\n")
	if b[len(b)-1] != '\n' {
		n++
	}
	return n
}

func prune(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			names = append(names, e.Name())
		}
	}
	if len(names) <= keep {
		return
	}
	sort.Strings(names)
	for _, n := range names[:len(names)-keep] {
		os.Remove(filepath.Join(dir, n))
	}
}

func syncDir(dir string) {
	f, err := os.Open(dir)
	if err != nil {
		return
	}
	defer f.Close()
	_ = f.Sync()
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
