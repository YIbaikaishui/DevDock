package detect

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"devdock/internal/model"
)

// maxFileBytes 是单个文件的最大读取量。package.json / vite.config 都远小于它，
// 设上限只是为了不因为一个畸形大文件把整次扫描拖慢。
const maxFileBytes = 256 << 10

// pruneDirs 是不会包含项目源码的目录名（小写比较）。命中即整棵剪掉。
var pruneDirs = map[string]bool{
	"node_modules": true, "dist": true, "build": true, "out": true,
	"coverage": true, "vendor": true, "target": true, "bin": true, "obj": true,
	"__pycache__": true, "venv": true, "env": true, "pods": true,
	"deriveddata": true, "$recycle.bin": true, "system volume information": true,
	"windows": true, "program files": true, "program files (x86)": true,
	"appdata": true, "windowsapps": true, "steamapps": true, "steamlibrary": true,
}

// Scanner 是扫描入口，保证「全局同时只有一次扫描在跑」。
// 第二个并发请求立刻拿到 ErrBusy（不排队，避免 CLI 卡死）。
type Scanner struct {
	mu      sync.Mutex
	running bool
}

// NewScanner 创建一个扫描器单例。
func NewScanner() *Scanner { return &Scanner{} }

// Scan 执行一次扫描；已有扫描在跑时返回 ErrBusy。
func (s *Scanner) Scan(ctx context.Context, opts Options) (*Result, error) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil, ErrBusy
	}
	s.running = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()
	return Scan(ctx, opts)
}

// dirProbe 是一个目录的只读快照：文件名集合 + 待访问的子目录。
// 所有判定函数只吃这个结构，保证「每个目录只 readdir 一次」。
type dirProbe struct {
	dir     string
	files   map[string]string // 小写文件名 -> 目录项里的真实名字（只收普通文件）
	subdirs []string          // 已剪枝的子目录绝对路径
}

// has 判断目录里是否有某个文件（大小写不敏感）。
func (p *dirProbe) has(name string) bool {
	_, ok := p.files[strings.ToLower(name)]
	return ok
}

// actual 返回目录里的真实文件名，用于拼路径。
func (p *dirProbe) actual(name string) string {
	if real, ok := p.files[strings.ToLower(name)]; ok {
		return real
	}
	return name
}

// read 读取目录里的文本文件；不在目录里或读失败时返回 false。
func (p *dirProbe) read(name string) (string, bool) {
	if !p.has(name) {
		return "", false
	}
	data, err := os.ReadFile(filepath.Join(p.dir, p.actual(name)))
	if err != nil {
		return "", false
	}
	if len(data) > maxFileBytes {
		data = data[:maxFileBytes]
	}
	return strings.TrimPrefix(string(data), "\ufeff"), true
}

// dirOutcome 是单个目录的处理结果，通过 channel 回传给主循环，
// 这样即使某个 readdir 卡住（休眠盘 / 长路径），主循环也能靠看门狗按时返回。
type dirOutcome struct {
	found   *Suggestion
	subdirs []string
}

// walkState 汇总跨目录的计数与中止标志。
type walkState struct {
	mu        sync.Mutex
	scanned   int
	truncated bool
	stop      bool
}

// claim 为一个目录名额；返回 false 表示本次扫描已经结束。
func (w *walkState) claim(maxDirs int) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stop {
		return false
	}
	if w.scanned >= maxDirs {
		w.truncated = true
		w.stop = true
		return false
	}
	w.scanned++
	return true
}

func (w *walkState) markTruncated() {
	w.mu.Lock()
	w.truncated = true
	w.mu.Unlock()
}

func (w *walkState) halt() {
	w.mu.Lock()
	w.stop = true
	w.mu.Unlock()
}

func (w *walkState) stopped() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stop
}

func (w *walkState) snapshot() (int, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.scanned, w.truncated
}

// Scan 扫描一批根目录并返回建议。它不改任何全局状态，可重复调用。
//
// 返回顺序：Group ↑ → 有端口优先 → 置信度 high→low → Cwd ↑（完全确定）。
func Scan(ctx context.Context, opts Options) (*Result, error) {
	o, err := opts.normalize()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	start := time.Now()
	deadline := start.Add(o.budget())
	st := &walkState{}
	found := make([]Suggestion, 0, 32)
	timedOut := false

	for _, root := range o.Roots {
		if st.stopped() {
			break
		}
		frontier := []string{filepath.Clean(root)}
		for depth := 0; depth <= o.Depth && len(frontier) > 0; depth++ {
			if st.stopped() {
				break
			}
			if !time.Now().Before(deadline) {
				timedOut = true
				st.halt()
				break
			}
			sort.Strings(frontier)

			// 有界并发：worker 数固定，结果走缓冲 channel 回传。
			out := make(chan dirOutcome, len(frontier))
			sem := make(chan struct{}, o.Workers)
			go func(dirs []string) {
				var wg sync.WaitGroup
				for _, dir := range dirs {
					if st.stopped() {
						break
					}
					sem <- struct{}{}
					wg.Add(1)
					go func(dir string) {
						defer wg.Done()
						defer func() { <-sem }()
						out <- inspectDir(dir, o, st)
					}(dir)
				}
				wg.Wait()
			}(frontier)

			remaining := len(frontier)
			next := make([]string, 0, len(frontier))
			timer := time.NewTimer(time.Until(deadline))
			aborted := false
		collect:
			for remaining > 0 {
				select {
				case oc := <-out:
					remaining--
					if oc.found != nil {
						found = append(found, *oc.found)
					}
					next = append(next, oc.subdirs...)
				case <-ctx.Done():
					timedOut = true
					aborted = true
					st.halt()
					break collect
				case <-timer.C:
					timedOut = true
					aborted = true
					st.halt()
					break collect
				}
			}
			timer.Stop()
			if aborted {
				break
			}
			frontier = next
		}
	}

	scanned, truncated := st.snapshot()
	list := finalize(found, o)

	// 端口占用预检放在最后、串行执行，并按端口缓存结果：
	// 并发探同一个端口会互相把对方挤成「被占用」，串行才准。
	probeCache := make(map[int]bool, 8)
	for i := range list {
		port := list[i].Port
		if port <= 0 {
			continue
		}
		inUse, ok := probeCache[port]
		if !ok {
			inUse = probePortInUse(port)
			probeCache[port] = inUse
		}
		list[i].PortInUse = inUse
	}

	if !o.IncludeLow {
		kept := make([]Suggestion, 0, len(list))
		for _, s := range list {
			if s.Confidence == ConfidenceLow {
				continue
			}
			kept = append(kept, s)
		}
		list = kept
	}
	if len(list) > o.Limit {
		list = list[:o.Limit]
		truncated = true
	}

	return &Result{
		Roots:       o.Roots,
		Suggestions: list,
		ScannedDirs: scanned,
		ElapsedMs:   time.Since(start).Milliseconds(),
		Truncated:   truncated,
		TimedOut:    timedOut,
		Depth:       o.Depth,
	}, nil
}

// inspectDir 读一个目录，剪枝、判定，并把子目录回传。
func inspectDir(dir string, o Options, st *walkState) dirOutcome {
	var oc dirOutcome
	if !st.claim(o.MaxDirs) {
		return oc
	}
	probe, truncated, err := readDirProbe(dir, o.MaxEntriesPerDir)
	if err != nil {
		// 不可读 / 扫描途中被删掉的目录：静默跳过，不中断整次扫描。
		return oc
	}
	if truncated {
		st.markTruncated()
	}
	oc.subdirs = probe.subdirs
	oc.found = detectDir(probe)
	return oc
}

// readDirProbe 读取一个目录并按剪枝规则过滤。
// 不跟随符号链接 / junction；跳过所有以 . 开头的目录与已知产物目录。
func readDirProbe(dir string, maxEntriesPerDir int) (*dirProbe, bool, error) {
	entries, err := os.ReadDir(dir) // 已按文件名排序，截断后仍然确定
	if err != nil {
		return nil, false, err
	}
	truncated := false
	if len(entries) > maxEntriesPerDir {
		entries = entries[:maxEntriesPerDir]
		truncated = true
	}
	p := &dirProbe{dir: dir, files: make(map[string]string, len(entries))}
	for _, e := range entries {
		name := e.Name()
		if e.Type()&os.ModeSymlink != 0 {
			continue // SPEC S1：junction 会造成环，一律不进
		}
		if e.IsDir() {
			if shouldSkipDir(name) {
				continue
			}
			p.subdirs = append(p.subdirs, filepath.Join(dir, name))
			continue
		}
		p.files[strings.ToLower(name)] = name
	}
	return p, truncated, nil
}

// shouldSkipDir 判断这个子目录是否要整棵剪掉。
func shouldSkipDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true // SPEC S2：所有点目录都不进
	}
	return pruneDirs[strings.ToLower(name)]
}

// detectDir 按 Node → Go → Python → Rust 的顺序判定一个目录，命中即停。
func detectDir(p *dirProbe) *Suggestion {
	// 移动端构建目录（android/ 或含 com.android.application 的 gradle 工程）
	// 不是可启动的网页服务，直接排除（SPEC 4.6 J3）。
	if isAndroidGradle(p) {
		return nil
	}
	if s := detectNode(p); s != nil {
		return s
	}
	if s := detectGo(p); s != nil {
		return s
	}
	if s := detectPython(p); s != nil {
		return s
	}
	if s := detectRust(p); s != nil {
		return s
	}
	return nil
}

// finalize 做跨根去重、命名、分组、排序、ID 去重与标记。
func finalize(found []Suggestion, o Options) []Suggestion {
	seenCwd := make(map[string]bool, len(found))
	list := make([]Suggestion, 0, len(found))
	for _, s := range found {
		cwd := filepath.Clean(s.Cwd)
		key := strings.ToLower(cwd)
		if seenCwd[key] {
			continue // 同一个目录被多个根覆盖时只留第一条
		}
		seenCwd[key] = true
		s.Cwd = cwd
		s.Name = displayName(s.Name, cwd)
		s.ID = slugID(s.Name, cwd)
		s.Group = groupOf(cwd)
		list = append(list, s)
	}
	sortSuggestions(list)
	assignUniqueIDs(list)
	markDuplicates(list, o.Existing)
	return list
}

// markDuplicates 把「已有项目占用同一 cwd」和「ID 已被占用」的建议标出来。
// 两种情况都意味着直接导入会失败，所以都标 Duplicate 交给 UI 灰显。
func markDuplicates(list []Suggestion, existing []model.Project) {
	if len(existing) == 0 {
		return
	}
	byCwd := make(map[string]string, len(existing))
	byID := make(map[string]bool, len(existing))
	for _, p := range existing {
		byCwd[strings.ToLower(filepath.Clean(p.Cwd))] = p.ID
		byID[p.ID] = true
	}
	for i := range list {
		if id, ok := byCwd[strings.ToLower(list[i].Cwd)]; ok {
			list[i].Duplicate = true
			list[i].ExistingID = id
			continue
		}
		if byID[list[i].ID] {
			list[i].Duplicate = true
			list[i].ExistingID = list[i].ID
		}
	}
}
