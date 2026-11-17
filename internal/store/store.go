// Package store 维护项目定义的内存索引与持久化。
// 运行态（pid / lifecycle / readiness）不在此持久化 —— 既不必要，
// 也会让重启后的陈旧状态误导用户。
package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"devdock/internal/config"
	"devdock/internal/model"
)

// 错误类别，供 HTTP 层映射为稳定的错误码。
var (
	ErrNotFound   = errors.New("项目不存在")
	ErrConflict   = errors.New("当前状态不允许该操作")
	ErrInvalid    = errors.New("参数不合法")
	ErrDuplicate  = errors.New("项目已存在")
)

// MaxCommandLen 是启动命令的最大长度。
const MaxCommandLen = 8192

// slugPattern 约束项目 id：字母数字与连字符，便于在 URL 中安全使用。
var slugPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

// Store 是项目定义的仓库。
type Store struct {
	mu   sync.RWMutex
	cfg  *config.Store
	byID map[string]model.Project
	order []string
}

// New 从配置装载项目集合。
func New(cfg *config.Store) (*Store, error) {
	s := &Store{
		cfg:  cfg,
		byID: make(map[string]model.Project),
	}
	for _, p := range cfg.Projects() {
		s.byID[p.ID] = p
		s.order = append(s.order, p.ID)
	}
	return s, nil
}

// Config 返回底层配置存储，供上层读取路径等信息。
func (s *Store) Config() *config.Store { return s.cfg }

// List 返回按创建时间排序的项目定义。
func (s *Store) List() []model.Project {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.Project, 0, len(s.order))
	for _, id := range s.order {
		if p, ok := s.byID[id]; ok {
			out = append(out, cloneProject(p))
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

// Get 按 id 或名称查找项目。id 精确匹配优先，其次按名称匹配。
func (s *Store) Get(key string) (model.Project, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.getLocked(key)
}

func (s *Store) getLocked(key string) (model.Project, error) {
	if p, ok := s.byID[key]; ok {
		return cloneProject(p), nil
	}
	for _, id := range s.order {
		if p, ok := s.byID[id]; ok && strings.EqualFold(p.Name, key) {
			return cloneProject(p), nil
		}
	}
	return model.Project{}, ErrNotFound
}

// Create 新增项目。ID 为空时由名称自动生成。
func (s *Store) Create(p model.Project) (model.Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if strings.TrimSpace(p.Name) == "" {
		return model.Project{}, fmt.Errorf("%w: 项目名称不能为空", ErrInvalid)
	}
	if p.ID == "" {
		p.ID = slugify(p.Name)
	}
	if !slugPattern.MatchString(p.ID) {
		return model.Project{}, fmt.Errorf("%w: 项目 ID 只能包含字母、数字、下划线和连字符，且不超过 64 个字符", ErrInvalid)
	}
	if _, exists := s.byID[p.ID]; exists {
		return model.Project{}, fmt.Errorf("%w: ID %q 已被占用", ErrDuplicate, p.ID)
	}
	if err := validate(p); err != nil {
		return model.Project{}, err
	}

	now := time.Now().UTC()
	p.CreatedAt = now
	p.UpdatedAt = now
	if p.Env == nil {
		p.Env = map[string]string{}
	}

	s.byID[p.ID] = p
	s.order = append(s.order, p.ID)
	if err := s.persistLocked(); err != nil {
		delete(s.byID, p.ID)
		s.order = s.order[:len(s.order)-1]
		return model.Project{}, err
	}
	return cloneProject(p), nil
}

// Patch 部分更新项目。字段为 nil 表示不修改。
type Patch struct {
	Name      *string
	Cwd       *string
	Command   *string
	Port      *int
	Readiness **model.ReadinessConfig
	Env       *map[string]string
	Autostart *bool
}

// Update 应用部分更新。
func (s *Store) Update(key string, patch Patch) (model.Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, err := s.getLocked(key)
	if err != nil {
		return model.Project{}, err
	}

	if patch.Name != nil {
		if strings.TrimSpace(*patch.Name) == "" {
			return model.Project{}, fmt.Errorf("%w: 项目名称不能为空", ErrInvalid)
		}
		p.Name = *patch.Name
	}
	if patch.Cwd != nil {
		p.Cwd = *patch.Cwd
	}
	if patch.Command != nil {
		p.Command = *patch.Command
	}
	if patch.Port != nil {
		p.Port = *patch.Port
	}
	if patch.Readiness != nil {
		p.Readiness = *patch.Readiness
	}
	if patch.Env != nil {
		p.Env = *patch.Env
	}
	if patch.Autostart != nil {
		p.Autostart = *patch.Autostart
	}
	if err := validate(p); err != nil {
		return model.Project{}, err
	}

	p.UpdatedAt = time.Now().UTC()
	s.byID[p.ID] = p
	if err := s.persistLocked(); err != nil {
		return model.Project{}, err
	}
	return cloneProject(p), nil
}

// Delete 删除项目定义。
func (s *Store) Delete(key string) (model.Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, err := s.getLocked(key)
	if err != nil {
		return model.Project{}, err
	}
	delete(s.byID, p.ID)
	for i, id := range s.order {
		if id == p.ID {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	if err := s.persistLocked(); err != nil {
		// 回滚
		s.byID[p.ID] = p
		s.order = append(s.order, p.ID)
		return model.Project{}, err
	}
	return p, nil
}

// persistLocked 把当前项目集合写入配置。调用方必须已持有写锁。
func (s *Store) persistLocked() error {
	list := make([]model.Project, 0, len(s.order))
	for _, id := range s.order {
		if p, ok := s.byID[id]; ok {
			list = append(list, p)
		}
	}
	return s.cfg.SetProjects(list)
}

// validate 校验项目定义。
func validate(p model.Project) error {
	cmd := strings.TrimSpace(p.Command)
	if cmd == "" {
		return fmt.Errorf("%w: 启动命令不能为空", ErrInvalid)
	}
	if len(p.Command) > MaxCommandLen {
		return fmt.Errorf("%w: 启动命令不能超过 %d 个字符", ErrInvalid, MaxCommandLen)
	}
	if strings.ContainsAny(p.Command, "\r\n") {
		return fmt.Errorf("%w: 启动命令必须是单行", ErrInvalid)
	}
	if strings.TrimSpace(p.Cwd) == "" {
		return fmt.Errorf("%w: 工作目录不能为空", ErrInvalid)
	}
	if !filepath.IsAbs(p.Cwd) {
		return fmt.Errorf("%w: 工作目录必须是绝对路径", ErrInvalid)
	}
	info, err := os.Stat(p.Cwd)
	if err != nil {
		return fmt.Errorf("%w: 工作目录不存在或无法访问：%s", ErrInvalid, p.Cwd)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: 工作目录不是目录：%s", ErrInvalid, p.Cwd)
	}
	if p.Port != 0 && (p.Port < 1 || p.Port > 65535) {
		return fmt.Errorf("%w: 端口必须在 1-65535 之间", ErrInvalid)
	}
	if p.Readiness != nil {
		if p.Readiness.Path != "" && !strings.HasPrefix(p.Readiness.Path, "/") {
			return fmt.Errorf("%w: 就绪检查路径必须以 / 开头", ErrInvalid)
		}
		if p.Readiness.Path != "" && p.Port == 0 {
			return fmt.Errorf("%w: 配置了就绪检查路径时必须声明端口", ErrInvalid)
		}
	}
	return nil
}

// slugify 从名称生成 slug。仅保留 ASCII 字母数字；中文等非 ASCII 名称为空时
// 回退为时间戳，保证总能得到一个合法 id。
func slugify(name string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		case r == '_' || r == '-' || r == ' ':
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = fmt.Sprintf("project-%d", time.Now().UnixNano()%1000000)
	}
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

// cloneProject 深拷贝项目，避免调用方改到内部 map 里的副本。
func cloneProject(p model.Project) model.Project {
	out := p
	if p.Env != nil {
		out.Env = make(map[string]string, len(p.Env))
		for k, v := range p.Env {
			out.Env[k] = v
		}
	}
	if p.Readiness != nil {
		r := *p.Readiness
		r.ExpectStatus = append([]int(nil), p.Readiness.ExpectStatus...)
		out.Readiness = &r
	}
	return out
}
