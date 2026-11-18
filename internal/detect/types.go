// Package detect 自动识别本机可启动的开发项目。
//
// 设计约束（见 docs/AUTO_DETECT_SPEC.md）：
//   - 只用标准库，也不 import internal/runner（端口占用预检自己用 net.Listen 探）
//   - 扫描是只读动作：不写文件、不启动进程、不碰 store/config
//   - 任何情况下都在 budget+200ms 内返回；超时返回 timedOut=true 而不是挂住
package detect

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"devdock/internal/model"
)

// 置信度取值。high 表示命令与端口都有确凿证据，medium 是靠框架默认值推出来的，
// low 是「跑得起来但不是网页服务」或「缺关键信息，需要用户补」。
const (
	ConfidenceHigh   = "high"
	ConfidenceMedium = "medium"
	ConfidenceLow    = "low"
)

// Kind 取值。
const (
	KindNode   = "node"
	KindGo     = "go"
	KindPython = "python"
	KindRust   = "rust"
)

// PortFrom 取值，说明端口是从哪儿推断出来的。
const (
	PortFromFlag             = "flag"
	PortFromViteConfig       = "vite.config"
	PortFromSource           = "source"
	PortFromFrameworkDefault = "framework-default"
)

// 预算上下限。对外（HTTP / CLI）的范围是 100–10000ms，
// 由 httpapi 与 cli 各自把关；本包允许更小的值，方便单测验证看门狗。
const (
	minBudgetMs = 1
	maxBudgetMs = 10000
	maxDepth    = 6
	maxRoots    = 8
	minLimit    = 1
	maxLimit    = 500
	// minMaxDirs 允许取到 1：对外（HTTP/CLI）的合法范围是 100–100000，
	// 由接口层把关；包内允许更小的值，方便单测验证截断行为。
	minMaxDirs  = 1
	maxMaxDirs  = 100000
	minEntries  = 100
	maxEntries  = 10000
	minWorkers  = 1
	maxWorkers  = 32

	// 对外边界值，供 httpapi / cli 在入口处校验（SPEC A15、A18）。
	MaxDepth = maxDepth
	MaxRoots = maxRoots

	defaultBudgetMs = 2000
	defaultDepth    = 3
	defaultLimit    = 100
	defaultMaxDirs  = 20000
	defaultEntries  = 2000
	defaultWorkers  = 8
)

// ErrBusy 表示上一次扫描还没结束。单飞由 Scanner 负责。
var ErrBusy = errors.New("scan_in_progress")

// Suggestion 是一条「可导入的项目建议」。它是纯数据，不含运行态。
//
// 刻意不内嵌 model.Project：建议里 Command 可能为空（Python 判不出入口时），
// 而 store 的校验会拒绝空命令；导入时再走 POST /api/projects 的正常校验。
type Suggestion struct {
	Cwd         string   `json:"cwd"`                   // 绝对路径，已 Clean
	Name        string   `json:"name"`                  // 建议显示名，允许中文
	ID          string   `json:"id"`                    // slug 合法：^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$
	Kind        string   `json:"kind"`                  // node|go|python|rust
	Framework   string   `json:"framework,omitempty"`   // vite|next|nuxt|django|fastapi|...
	Command     string   `json:"command"`               // 完整启动命令；判定不出时为空串
	CommandFrom string   `json:"commandFrom,omitempty"` // 证据：package.json#scripts.dev / main.go
	Port        int      `json:"port"`                  // 0 = 未能推断
	PortFrom    string   `json:"portFrom,omitempty"`    // flag|vite.config|source|framework-default
	Confidence  string   `json:"confidence"`            // high|medium|low
	Warnings    []string `json:"warnings,omitempty"`    // 「脚本依赖 bun，本机未安装」等
	PortInUse   bool     `json:"portInUse,omitempty"`   // 预检发现端口已被占用
	Duplicate   bool     `json:"duplicate,omitempty"`   // 已有项目占用同一 cwd 或同一 ID
	ExistingID  string   `json:"existingId,omitempty"`  // 冲突项目的 ID
	Group       string   `json:"group,omitempty"`       // 最近 .git 祖先（含自身）；无则父目录
}

// Result 是一次扫描的完整结果。
type Result struct {
	Roots       []string     `json:"roots"`
	Suggestions []Suggestion `json:"suggestions"`
	ScannedDirs int          `json:"scannedDirs"`
	ElapsedMs   int64        `json:"elapsedMs"`
	Truncated   bool         `json:"truncated"` // 命中 limit / maxDirs / maxEntries
	TimedOut    bool         `json:"timedOut"`  // 命中时间预算
	Depth       int          `json:"depth"`
}

// Options 是一次扫描的参数。
type Options struct {
	// Roots 为空时使用 DefaultRoots() 的白名单候选。
	Roots []string
	// Depth 是相对根的最大深度：0 表示只判定根目录自身，最大 6。
	Depth int
	// BudgetMs 是单次扫描的总时长硬预算（整个扫描共享一个 deadline）。
	BudgetMs int
	// Limit 是返回建议条数上限。
	Limit int
	// MaxDirs 是访问目录总数上限。
	MaxDirs int
	// MaxEntriesPerDir 是单目录条目读取上限。
	MaxEntriesPerDir int
	// Workers 是并发读目录的 goroutine 数。
	Workers int
	// IncludeLow 为 false 时过滤掉 confidence=low 的建议。
	IncludeLow bool
	// Existing 是已管理的项目，用于标记 Duplicate/ExistingID。
	// 用调用方传进来的快照而不是直接读 store，是为了让本包保持无副作用、可单测。
	Existing []model.Project
}

// DefaultOptions 返回默认参数（depth 3 / budget 2000ms / limit 100）。
func DefaultOptions() Options {
	return Options{
		Depth:            defaultDepth,
		BudgetMs:         defaultBudgetMs,
		Limit:            defaultLimit,
		MaxDirs:          defaultMaxDirs,
		MaxEntriesPerDir: defaultEntries,
		Workers:          defaultWorkers,
	}
}

// normalize 补齐默认值并校验越界。
func (o Options) normalize() (Options, error) {
	if o.Depth < 0 || o.Depth > maxDepth {
		return Options{}, fmt.Errorf("扫描深度必须在 0-%d 之间", maxDepth)
	}
	if o.BudgetMs == 0 {
		o.BudgetMs = defaultBudgetMs
	}
	if o.BudgetMs < minBudgetMs || o.BudgetMs > maxBudgetMs {
		return Options{}, fmt.Errorf("时间预算必须在 %d-%d 毫秒之间", minBudgetMs, maxBudgetMs)
	}
	if o.Limit == 0 {
		o.Limit = defaultLimit
	}
	if o.Limit < minLimit || o.Limit > maxLimit {
		return Options{}, fmt.Errorf("建议条数上限必须在 %d-%d 之间", minLimit, maxLimit)
	}
	if o.MaxDirs == 0 {
		o.MaxDirs = defaultMaxDirs
	}
	if o.MaxDirs < minMaxDirs || o.MaxDirs > maxMaxDirs {
		return Options{}, fmt.Errorf("目录数上限必须在 %d-%d 之间", minMaxDirs, maxMaxDirs)
	}
	if o.MaxEntriesPerDir == 0 {
		o.MaxEntriesPerDir = defaultEntries
	}
	if o.MaxEntriesPerDir < minEntries || o.MaxEntriesPerDir > maxEntries {
		return Options{}, fmt.Errorf("单目录条目上限必须在 %d-%d 之间", minEntries, maxEntries)
	}
	if o.Workers == 0 {
		o.Workers = defaultWorkers
	}
	if o.Workers < minWorkers || o.Workers > maxWorkers {
		return Options{}, fmt.Errorf("并发数必须在 %d-%d 之间", minWorkers, maxWorkers)
	}

	roots := dedupRoots(o.Roots)
	if len(roots) == 0 {
		roots = DefaultRoots()
	}
	if len(roots) == 0 {
		return Options{}, errors.New("没有可扫描的根目录，请用 --root 指定")
	}
	if len(roots) > maxRoots {
		return Options{}, fmt.Errorf("最多支持 %d 个根目录", maxRoots)
	}
	for _, r := range roots {
		info, err := os.Stat(r)
		if err != nil {
			return Options{}, fmt.Errorf("根目录不存在或无法访问：%s", r)
		}
		if !info.IsDir() {
			return Options{}, fmt.Errorf("根路径不是目录：%s", r)
		}
	}
	o.Roots = roots
	return o, nil
}

// dedupRoots 去重（Windows 大小写不敏感）并清理路径。
func dedupRoots(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, r := range in {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		abs, err := filepath.Abs(r)
		if err != nil {
			abs = r
		}
		abs = filepath.Clean(abs)
		key := strings.ToLower(abs)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, abs)
	}
	return out
}

// budget 把毫秒换算成 duration。
func (o Options) budget() time.Duration {
	return time.Duration(o.BudgetMs) * time.Millisecond
}
