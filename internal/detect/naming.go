package detect

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// genericNames 是「光看目录名分不清是哪个项目」的泛名。
// 命中时把父目录名拼在前面（本机 48 条建议里 11 条叫 frontend/web/admin/client）。
var genericNames = map[string]bool{
	"frontend": true, "front-end": true, "front": true,
	"web": true, "www": true, "site": true, "page": true, "pages": true,
	"admin": true, "client": true, "app": true, "apps": true,
	"server": true, "backend": true, "back-end": true, "back": true,
	"service": true, "services": true, "api": true,
	"ui": true, "mobile": true, "h5": true, "pc": true, "portal": true,
	"src": true, "main": true,
}

// maxSlugLen 与 store 的 slugPattern 保持一致。
const maxSlugLen = 64

// displayName 决定建议的显示名：
//  1. package.json#name（已在调用方去掉 @scope/ 前缀）——项目自己起的名字原样采用；
//  2. 没有则用目录名（保留中文，如 28高三）；
//  3. 只有第 2 步退回目录名、且目录名是泛名时，才在前面拼上父目录名。
//
// 第 3 步刻意不作用于包名：需求 4.7 的泛名规则限定「目录名」，且 9.1 的 T10
// （a/web、b/web 的包名都叫 web → ID 必须是 web / web-2）只有在包名不被
// 拼父目录时才成立。
func displayName(raw, cwd string) string {
	name := strings.TrimSpace(raw)
	if name != "" {
		return name
	}
	name = baseName(cwd)
	if name == "" {
		return "project"
	}
	if genericNames[strings.ToLower(name)] {
		parent := baseName(filepath.Dir(cwd))
		if parent != "" && !genericNames[strings.ToLower(parent)] {
			return parent + "-" + name
		}
	}
	return name
}

// baseName 取路径最后一段，对盘根（D:\）这类特殊情况回退为空串。
func baseName(p string) string {
	clean := filepath.Clean(p)
	base := filepath.Base(clean)
	if base == "." || base == string(filepath.Separator) || base == "" {
		return ""
	}
	// 盘根：filepath.Base(`D:\`) 在 Windows 上是 `D:\`
	if vol := filepath.VolumeName(clean); vol != "" && strings.TrimSuffix(clean, string(filepath.Separator)) == strings.TrimSuffix(vol, string(filepath.Separator)) {
		return ""
	}
	return base
}

// slugID 生成稳定的建议 ID。
//
// 只保留 ASCII 字母数字；纯中文名会退化成空串，此时用 FNV-1a(cwd) 兜底，
// 保证「同一条目每次扫描得到同一个 ID」（store 的时间戳兜底做不到这点）。
func slugID(name, cwd string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		case r == '_' || r == '-' || r == ' ' || r == '.' || r == '+':
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = fmt.Sprintf("p-%08x", fnv32a(strings.ToLower(filepath.Clean(cwd))))
	}
	if len(out) > maxSlugLen {
		out = strings.Trim(out[:maxSlugLen], "-")
	}
	if out == "" {
		out = "p-00000000"
	}
	return out
}

// fnv32a 是 FNV-1a 32 位哈希，用来给纯中文名生成稳定 ID。
func fnv32a(s string) uint32 {
	const (
		offset uint32 = 2166136261
		prime  uint32 = 16777619
	)
	h := offset
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= prime
	}
	return h
}

// groupOf 从 cwd 向上最多 6 层找第一个含 .git 的目录（含自身）；
// 找不到则退回父目录。用于把 monorepo 的多个 package.json 折叠成一组。
func groupOf(cwd string) string {
	dir := filepath.Clean(cwd)
	for i := 0; i <= 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return filepath.Dir(filepath.Clean(cwd))
}

// confidenceRank 把置信度换算成排序权重，high 最小（排前面）。
func confidenceRank(c string) int {
	switch c {
	case ConfidenceHigh:
		return 0
	case ConfidenceMedium:
		return 1
	default:
		return 2
	}
}

// sortSuggestions 按 SPEC 4.7 的排序键排序，保证结果完全确定：
// Group 升序 → 有端口优先 → 置信度 high→low → Cwd 升序。
func sortSuggestions(list []Suggestion) {
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if a.Group != b.Group {
			return a.Group < b.Group
		}
		ah, bh := a.Port > 0, b.Port > 0
		if ah != bh {
			return ah
		}
		ar, br := confidenceRank(a.Confidence), confidenceRank(b.Confidence)
		if ar != br {
			return ar < br
		}
		return a.Cwd < b.Cwd
	})
}

// assignUniqueIDs 处理同批次 ID 冲突：后者追加 -2、-3，必然收敛。
func assignUniqueIDs(list []Suggestion) {
	used := make(map[string]bool, len(list))
	for i := range list {
		base := list[i].ID
		if !used[base] {
			used[base] = true
			continue
		}
		prefix := base
		if len(prefix) > maxSlugLen-4 {
			prefix = strings.Trim(prefix[:maxSlugLen-4], "-")
		}
		for n := 2; ; n++ {
			cand := fmt.Sprintf("%s-%d", prefix, n)
			if !used[cand] {
				list[i].ID = cand
				used[cand] = true
				break
			}
		}
	}
}
