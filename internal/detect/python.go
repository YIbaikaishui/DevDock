package detect

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	// pyServiceDepRe 是「这个 Python 项目会起常驻服务」的依赖闸门。
	// 没有命中就一律不产出：本机 34 个 Python 候选根里有 10 个是纯库/脚本。
	pyServiceDepRe = regexp.MustCompile(`(?i)\b(fastapi|starlette|uvicorn|aiohttp|flask|django|` +
		`sanic|litestar|hypercorn|quart|tornado)\b`)

	// pyEntryMainRe 判断入口文件是不是真的会跑起来。
	pyEntryMainRe = regexp.MustCompile(`__main__|uvicorn\.run\(|app\.run\(`)

	pyDjangoDepRe = regexp.MustCompile(`(?i)\bdjango\b`)
	pyFlaskDepRe  = regexp.MustCompile(`(?i)\bflask\b`)
	pyAsyncDepRe  = regexp.MustCompile(`(?i)\b(fastapi|starlette|uvicorn|aiohttp|sanic|litestar|hypercorn|quart|tornado)\b`)
)

// pyEntryFiles 是平铺入口文件的探测顺序。
var pyEntryFiles = []string{"main.py", "app.py", "server.py", "run.py"}

// pyMaxPkgScan 是 P3 里最多探查的子目录数，防止在 monorepo 根上做大范围 stat。
const pyMaxPkgScan = 24

// detectPython 实现 SPEC 4.5 P1–P5，依赖闸门优先。
func detectPython(p *dirProbe) *Suggestion {
	deps, hasDeps := readPythonDeps(p)
	if !hasDeps {
		return nil
	}

	// P1：Django 的 manage.py
	if p.has("manage.py") && pyDjangoDepRe.MatchString(deps) {
		return &Suggestion{
			Cwd:         p.dir,
			Kind:        KindPython,
			Framework:   "django",
			Command:     "python manage.py runserver 127.0.0.1:8000",
			CommandFrom: "manage.py",
			Port:        8000,
			PortFrom:    PortFromFrameworkDefault,
			Confidence:  ConfidenceHigh,
		}
	}

	// P5：没有服务框架依赖 → 不产出。
	if !pyServiceDepRe.MatchString(deps) {
		return nil
	}

	fw := pyFrameworkName(deps)

	// P2：平铺入口文件 + 真的会跑起来
	for _, name := range pyEntryFiles {
		text, ok := p.read(name)
		if !ok {
			continue
		}
		if !pyEntryMainRe.MatchString(text) {
			continue
		}
		s := &Suggestion{
			Cwd:         p.dir,
			Kind:        KindPython,
			Framework:   fw,
			Command:     "python " + p.actual(name),
			CommandFrom: p.actual(name),
			Confidence:  ConfidenceMedium,
		}
		if port, ok := pythonSourcePort(text); ok {
			s.Port, s.PortFrom = port, PortFromSource
			s.Confidence = ConfidenceHigh
		} else if fw == "flask" {
			s.Port, s.PortFrom = 5000, PortFromFrameworkDefault
		}
		return s
	}

	// P3：包形态入口 `<pkg>/__main__.py` → python -m <pkg>
	if pkg := findMainPackage(p); pkg != "" {
		return &Suggestion{
			Cwd:         p.dir,
			Kind:        KindPython,
			Framework:   fw,
			Command:     "python -m " + pkg,
			CommandFrom: pkg + "/__main__.py",
			Confidence:  ConfidenceMedium,
		}
	}

	// P4：有服务框架依赖但找不到入口 → 给一条 low 建议，让用户补命令
	return &Suggestion{
		Cwd:        p.dir,
		Kind:       KindPython,
		Framework:  fw,
		Confidence: ConfidenceLow,
		Warnings:   []string{"检测到 Python 服务框架依赖，但找不到入口文件，请手填启动命令"},
	}
}

// readPythonDeps 收集依赖声明文本。用子串匹配即可，不写 TOML 解析器。
func readPythonDeps(p *dirProbe) (string, bool) {
	var sb strings.Builder
	found := false
	for _, name := range []string{"pyproject.toml", "pipfile"} {
		if text, ok := p.read(name); ok {
			sb.WriteString(text)
			sb.WriteByte('\n')
			found = true
		}
	}
	// requirements*.txt（含 requirements-dev.txt 等）
	names := make([]string, 0, 4)
	for lower := range p.files {
		if strings.HasPrefix(lower, "requirements") && strings.HasSuffix(lower, ".txt") {
			names = append(names, lower)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if text, ok := p.read(name); ok {
			sb.WriteString(text)
			sb.WriteByte('\n')
			found = true
		}
	}
	return sb.String(), found
}

// pyFrameworkName 从依赖里挑一个代表框架，用于展示。
func pyFrameworkName(deps string) string {
	switch {
	case pyDjangoDepRe.MatchString(deps):
		return "django"
	case pyFlaskDepRe.MatchString(deps):
		return "flask"
	case pyAsyncDepRe.MatchString(deps):
		return "fastapi"
	default:
		return "python"
	}
}

// findMainPackage 找一个含 __main__.py 的直接子包。
func findMainPackage(p *dirProbe) string {
	subs := append([]string(nil), p.subdirs...)
	sort.Strings(subs)
	if len(subs) > pyMaxPkgScan {
		subs = subs[:pyMaxPkgScan]
	}
	for _, sub := range subs {
		name := filepath.Base(sub)
		if strings.HasPrefix(name, ".") || name == "tests" || name == "test" {
			continue
		}
		if _, err := os.Stat(filepath.Join(sub, "__main__.py")); err == nil {
			return name
		}
	}
	return ""
}
