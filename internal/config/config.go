// Package config 负责配置文件的加载、原子写入与损坏降级。
//
// 设计约束：
//   - 配置文件放在 %APPDATA%\DevDock\，不放 exe 旁边（exe 可能位于只读目录）
//   - 支持便携模式：exe 同级存在 devdock.portable 时用 exeDir\data\
//   - 原子写入用 MoveFileEx + MOVEFILE_REPLACE_EXISTING（Windows 上 os.Rename
//     无法覆盖已存在文件）
//   - 配置损坏绝不阻止启动：改名备份后用默认配置继续
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"devdock/internal/model"
)

// SchemaVersion 是当前配置结构版本。
const SchemaVersion = 1

// portableMarker 是便携模式标记文件名。
const portableMarker = "devdock.portable"

// ServerConfig 是服务端设置。
type ServerConfig struct {
	PreferredPort     int  `json:"preferredPort"`
	OpenBrowserOnStart bool `json:"openBrowserOnStart"`
}

// UIConfig 是界面偏好。
type UIConfig struct {
	Theme              string `json:"theme"`
	LastSelectedProject string `json:"lastSelectedProject"`
}

// Settings 是非项目类的用户设置。磁盘日志默认关闭（零泄漏优先）。
type Settings struct {
	// LogToDisk 控制是否把日志同时写入磁盘。默认 false。
	LogToDisk bool `json:"logToDisk"`
}

// File 是 config.json 的完整结构。
type File struct {
	SchemaVersion int               `json:"schemaVersion"`
	Server        ServerConfig      `json:"server"`
	UI            UIConfig          `json:"ui"`
	Settings      Settings          `json:"settings"`
	Projects      []model.Project   `json:"projects"`
}

// Default 返回一份默认配置。
func Default() *File {
	return &File{
		SchemaVersion: SchemaVersion,
		Server: ServerConfig{
			PreferredPort:      0,
			OpenBrowserOnStart: true,
		},
		UI:   UIConfig{Theme: "system"},
		Settings: Settings{LogToDisk: false},
		Projects: []model.Project{},
	}
}

// Store 持有配置路径与内存副本，负责持久化。
type Store struct {
	mu       sync.Mutex
	path     string
	data     *File
	// Warning 保存加载期间产生的非致命问题（例如配置损坏已降级）。
	warning string
}

var (
	procMoveFileExW = syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW")
)

const (
	movefileReplaceExisting = 0x1
	movefileWriteThrough    = 0x8
)

// pathFor 计算配置目录与文件路径。
func pathFor() (dir string, path string, err error) {
	exe, exeErr := os.Executable()
	if exeErr == nil {
		exeDir := filepath.Dir(exe)
		if _, statErr := os.Stat(filepath.Join(exeDir, portableMarker)); statErr == nil {
			dir = filepath.Join(exeDir, "data")
			return dir, filepath.Join(dir, "config.json"), nil
		}
	}
	appData := os.Getenv("APPDATA")
	if appData == "" {
		if exeErr != nil {
			return "", "", fmt.Errorf("无法确定配置目录：%w", exeErr)
		}
		appData = filepath.Dir(exe)
	}
	dir = filepath.Join(appData, "DevDock")
	return dir, filepath.Join(dir, "config.json"), nil
}

// Path 返回配置文件路径。
func Path() (string, error) {
	_, p, err := pathFor()
	return p, err
}

// Dir 返回配置文件所在目录。
func Dir() (string, error) {
	d, _, err := pathFor()
	return d, err
}

// Load 读取配置。文件不存在时返回默认配置且不创建文件。
// JSON 损坏时把原文件改名备份并以默认配置继续，同时记录 Warning。
func Load() (*Store, error) {
	dir, path, err := pathFor()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建配置目录失败: %w", err)
	}

	s := &Store{path: path, data: Default()}

	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return nil, fmt.Errorf("读取配置失败: %w", err)
	}
	if len(raw) == 0 {
		s.warning = "配置文件为空，已按默认配置启动"
		return s, nil
	}

	var f File
	if err := json.Unmarshal(raw, &f); err != nil {
		backup := fmt.Sprintf("%s.corrupt-%d", path, time.Now().Unix())
		if rerr := os.Rename(path, backup); rerr != nil {
			s.warning = fmt.Sprintf("配置损坏且备份失败（%v），已按默认配置启动", rerr)
		} else {
			s.warning = fmt.Sprintf("配置损坏，已备份为 %s 并按默认配置启动", filepath.Base(backup))
		}
		return s, nil
	}

	// 补齐可能缺失的字段。
	if f.SchemaVersion == 0 {
		f.SchemaVersion = SchemaVersion
	}
	if f.Projects == nil {
		f.Projects = []model.Project{}
	}
	s.data = &f
	return s, nil
}

// Warning 返回加载期间的非致命警告，无警告时返回空串。
func (s *Store) Warning() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.warning
}

// Path 返回配置文件的绝对路径。
func (s *Store) Path() string { return s.path }

// Snapshot 返回配置副本，避免调用方直接改动内部状态。
func (s *Store) Snapshot() File {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := *s.data
	out.Projects = append([]model.Project(nil), s.data.Projects...)
	return out
}

// Projects 返回项目副本。
func (s *Store) Projects() []model.Project {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]model.Project(nil), s.data.Projects...)
}

// SetProjects 替换项目集合并立即持久化。
func (s *Store) SetProjects(projects []model.Project) error {
	s.mu.Lock()
	cp := append([]model.Project(nil), projects...)
	s.data.Projects = cp
	s.mu.Unlock()
	return s.persist()
}

// Update 在锁内就地修改配置并持久化。
func (s *Store) Update(fn func(*File)) error {
	s.mu.Lock()
	fn(s.data)
	s.mu.Unlock()
	return s.persist()
}

// persist 原子写入配置文件。
func (s *Store) persist() error {
	s.mu.Lock()
	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("序列化配置失败: %w", err)
	}
	data = append(data, '\n')
	path := s.path
	s.mu.Unlock()

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("写入临时配置文件失败: %w", err)
	}

	// MoveFileExW 支持覆盖已存在目标；os.Rename 在 Windows 上会失败。
	src, err := syscall.UTF16PtrFromString(tmp)
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("临时文件路径非法: %w", err)
	}
	dst, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("配置路径非法: %w", err)
	}
	r, _, e := procMoveFileExW.Call(uintptr(unsafe.Pointer(src)), uintptr(unsafe.Pointer(dst)),
		movefileReplaceExisting|movefileWriteThrough)
	if r == 0 {
		os.Remove(tmp)
		return fmt.Errorf("替换配置文件失败: %w", e)
	}
	return nil
}
