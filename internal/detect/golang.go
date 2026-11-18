package detect

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	// goMainPackageRe / goMainFuncRe 一起用来判断根目录是不是一个可执行入口。
	goMainPackageRe = regexp.MustCompile(`(?m)^\s*package\s+main\s*$`)
	goMainFuncRe    = regexp.MustCompile(`(?m)^\s*func\s+main\s*\(`)

	// goCMDServerNameRe 是多入口时用来挑「像服务」的那一个。
	goCMDServerNameRe = regexp.MustCompile(`(?i)(server|api|web|app|gateway|cmd)`)
)

// detectGo 实现 SPEC 4.4 G1–G5。
func detectGo(p *dirProbe) *Suggestion {
	if !p.has("go.mod") {
		return nil
	}

	// G1：根目录有 package main + func main → go run .
	if hasGoMainPackage(p) {
		return &Suggestion{
			Cwd:         p.dir,
			Kind:        KindGo,
			Command:     "go run .",
			CommandFrom: "go.mod + 根目录 main 包",
			Confidence:  ConfidenceHigh,
		}
	}

	// G2/G3：看 cmd/ 下有几个入口。
	entries, err := os.ReadDir(filepath.Join(p.dir, "cmd"))
	if err != nil {
		return nil // G5：只有 go.mod、没有 main 包的库模块
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() && e.Type()&os.ModeSymlink == 0 && !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	if len(names) == 0 {
		return nil
	}
	if len(names) == 1 {
		return &Suggestion{
			Cwd:         p.dir,
			Kind:        KindGo,
			Command:     "go run ./cmd/" + names[0],
			CommandFrom: "go.mod + cmd/" + names[0],
			Confidence:  ConfidenceHigh,
		}
	}

	// G3：多个入口时只有「名字唯一命中 server/api/web/...」才产出，否则 G4 不产出。
	matched := make([]string, 0, 2)
	for _, name := range names {
		if goCMDServerNameRe.MatchString(name) {
			matched = append(matched, name)
		}
	}
	if len(matched) != 1 {
		return nil
	}
	return &Suggestion{
		Cwd:         p.dir,
		Kind:        KindGo,
		Command:     "go run ./cmd/" + matched[0],
		CommandFrom: "go.mod + cmd/" + matched[0],
		Confidence:  ConfidenceMedium,
	}
}

// hasGoMainPackage 判断目录里是否有同时含 `package main` 与 `func main(` 的 .go 文件。
func hasGoMainPackage(p *dirProbe) bool {
	for lower := range p.files {
		if !strings.HasSuffix(lower, ".go") || strings.HasSuffix(lower, "_test.go") {
			continue
		}
		text, ok := p.read(lower)
		if !ok {
			continue
		}
		if goMainPackageRe.MatchString(text) && goMainFuncRe.MatchString(text) {
			return true
		}
	}
	return false
}
