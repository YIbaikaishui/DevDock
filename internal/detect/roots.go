package detect

import (
	"os"
	"path/filepath"
	"strings"
)

// homeCandidates 是 %USERPROFILE% 下一级的常见开发目录。
// 只取「存在的一级子目录」，不扫整个 home，也不扫盘根。
var homeCandidates = []string{
	"Desktop", "Documents", "Downloads",
	"projects", "code", "dev", "src", "workspace", "work", "repos", "git",
	"开发", "项目",
}

// ideCandidates 是各 IDE 的默认工程位置。
var ideCandidates = []string{
	filepath.Join("source", "repos"), // Visual Studio
	"IdeaProjects",                   // IntelliJ
	"PycharmProjects",                // PyCharm
}

// driveCandidates 是「每个固定本地盘根下的同名子目录」。
var driveCandidates = []string{
	"Desktop", "projects", "code", "dev", "src", "workspace", "work", "repos",
}

// maxDefaultRoots 是自动候选的上限（SPEC A1）。
const maxDefaultRoots = 8

// DefaultRoots 返回默认扫描根：%USERPROFILE% 下一级常见目录 + 各 IDE 默认位置 +
// 每个固定本地盘根下的同名子目录，按此顺序去重后取前 8 个。
//
// 明确不做：不扫盘根、不扫 %USERPROFILE% 整层、不扫非固定盘（网络盘/可移动盘）。
func DefaultRoots() []string {
	seen := make(map[string]bool, maxDefaultRoots)
	out := make([]string, 0, maxDefaultRoots)

	add := func(p string) {
		if len(out) >= maxDefaultRoots {
			return
		}
		info, err := os.Stat(p)
		if err != nil || !info.IsDir() {
			return
		}
		clean := filepath.Clean(p)
		key := strings.ToLower(clean)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, clean)
	}

	if home, err := os.UserHomeDir(); err == nil && home != "" {
		for _, name := range homeCandidates {
			add(filepath.Join(home, name))
		}
		for _, name := range ideCandidates {
			add(filepath.Join(home, name))
		}
	}
	for _, root := range fixedDriveRoots() {
		for _, name := range driveCandidates {
			add(filepath.Join(root, name))
		}
	}
	return out
}
