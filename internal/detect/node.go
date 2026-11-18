package detect

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// 脚本键名的优先级：dev 优先于 start（start 常是 next start 这类需要先 build 的产物入口）。
var scriptKeys = []string{"dev", "develop", "start", "serve", "preview"}

var (
	// serverCmdRe 是「这段命令在起服务」的白名单。
	serverCmdRe = regexp.MustCompile(`(?i)\b(vite|next\s+(dev|start)|nuxt|nuxi\s+dev|` +
		`astro\s+(dev|preview)|react-scripts\s+start|react-app-rewired\s+start|` +
		`vue-cli-service\s+serve|ng\s+serve|svelte-kit\s+dev|webpack(-dev-server|\s+serve)|` +
		`parcel|rsbuild\s+dev|nest\s+start|remotion\s+studio|react-native\s+start|` +
		`tsx|ts-node|nodemon|node|bun|deno|python|uvicorn|gunicorn|flask|manage\.py|serve)\b`)

	// notServerRe 是「这段命令不是在起服务」的黑名单。
	//
	// 刻意不把 watch 放进来：`tsx watch src/index.ts` 是合法的服务命令；
	// 而 `vite build --watch`、`tsgo -p ... --watch` 已经被 build/tsgo 排除掉了。
	notServerRe = regexp.MustCompile(`(?i)\b(build|tsc|tsgo|typecheck|type-check|eslint|prettier|` +
		`vitest|jest|mocha|playwright|cypress|lint|audit|codegen|generate|electron|` +
		`remotion\s+render)\b`)

	// runScriptRe 匹配「间接调用另一个 npm script」的形态。
	runScriptRe = regexp.MustCompile(`(?i)^(npm|pnpm|yarn|bun)\s+(?:run\s+)?([\w:.\-]+)$`)

	// transpilerRe 匹配「一次性转译执行器」：tsx / ts-node 不带 watch 时
	// 跑完即退出，是 CLI 工具而不是常驻服务（本机 open-design\tools\* 四例）。
	transpilerRe = regexp.MustCompile(`(?i)^(tsx|ts-node)(\s|$)`)

	// watchWordRe 用来区分 `tsx watch src/index.ts`（服务）与 `tsx src/index.ts`（一次性）。
	watchWordRe = regexp.MustCompile(`(?i)\b(watch|serve)\b`)

	// segmentSplitRe 按 && 与单个 & 拆段（不改写引号内的内容，够用即可）。
	segmentSplitRe = regexp.MustCompile(`\s*(?:&&|&)\s*`)

	// envAssignRe 匹配 `VAR=value` 前缀。
	envAssignRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
)

// packageJSON 只解析判定需要的那几个字段。
type packageJSON struct {
	Name    string            `json:"name"`
	Scripts map[string]string `json:"scripts"`
}

// framework 描述一个可识别的 Node 框架。
type framework struct {
	name string
	re   *regexp.Regexp
	port int // 0 表示没有众所周知的默认端口
}

// frameworks 按「越具体越靠前」排列：remix vite:dev 必须排在 vite 之前。
var frameworks = []framework{
	{name: "react-native", re: regexp.MustCompile(`(?i)\breact-native\s+start\b`), port: 8081},
	{name: "remotion", re: regexp.MustCompile(`(?i)\bremotion\s+studio\b`), port: 3000},
	{name: "remix", re: regexp.MustCompile(`(?i)\bremix\s+(dev|vite:dev)\b`), port: 3000},
	{name: "sveltekit", re: regexp.MustCompile(`(?i)\bsvelte-kit\s+dev\b`), port: 5173},
	{name: "astro", re: regexp.MustCompile(`(?i)\bastro\s+(dev|preview)\b`), port: 4321},
	{name: "next", re: regexp.MustCompile(`(?i)\bnext\s+(dev|start)\b`), port: 3000},
	{name: "nuxt", re: regexp.MustCompile(`(?i)\b(nuxt|nuxi)\b`), port: 3000},
	{name: "cra", re: regexp.MustCompile(`(?i)\b(react-scripts|react-app-rewired)\s+start\b`), port: 3000},
	{name: "vue-cli", re: regexp.MustCompile(`(?i)\bvue-cli-service\s+serve\b`), port: 8080},
	{name: "angular", re: regexp.MustCompile(`(?i)\bng\s+serve\b`), port: 4200},
	{name: "webpack", re: regexp.MustCompile(`(?i)\bwebpack(-dev-server|\s+serve)\b`), port: 8080},
	{name: "parcel", re: regexp.MustCompile(`(?i)\bparcel\b`), port: 1234},
	{name: "rsbuild", re: regexp.MustCompile(`(?i)\brsbuild\s+dev\b`), port: 3000},
	{name: "nest", re: regexp.MustCompile(`(?i)\bnest\s+start\b`), port: 3000},
	{name: "vite", re: regexp.MustCompile(`(?i)\bvite\b`), port: 5173},
}

// nonWebFrameworks 是「端口确实可访问但不是网页预览」的一类，按 SPEC A6 标 low + warning。
var nonWebFrameworks = map[string]string{
	"react-native": "React Native 的 Metro 打包服务不是网页预览，默认端口 8081 仅供调试",
	"remotion":     "Remotion Studio 是视频预览工具，不是网页服务",
}

// detectNode 实现 SPEC 4.3 N1–N8。
func detectNode(p *dirProbe) *Suggestion {
	if !p.has("package.json") {
		return nil
	}
	raw, ok := p.read("package.json")
	if !ok {
		return nil
	}
	var pkg packageJSON
	if err := json.Unmarshal([]byte(raw), &pkg); err != nil {
		return nil // 畸形 package.json 不当项目，交给其它生态的判定
	}
	if len(pkg.Scripts) == 0 {
		return nil
	}

	for _, key := range scriptKeys {
		cmd, ok := pkg.Scripts[key]
		if !ok || strings.TrimSpace(cmd) == "" {
			continue
		}
		seg, ok := resolveServerSegment(pkg.Scripts, cmd, 0)
		if !ok {
			continue // 这个键不是起服务的，试下一个键
		}
		return buildNodeSuggestion(p, pkg, key, seg, cmd)
	}
	return nil
}

// buildNodeSuggestion 拼出一条 Node 建议。
func buildNodeSuggestion(p *dirProbe, pkg packageJSON, key, seg, rawCmd string) *Suggestion {
	runner, warnings := pickRunner(p)
	command := runner + " run " + key

	fw := detectFramework(seg)
	s := &Suggestion{
		Cwd:         p.dir,
		Kind:        KindNode,
		Framework:   fw.name,
		Command:     command,
		CommandFrom: "package.json#scripts." + key,
		Confidence:  ConfidenceMedium,
		Warnings:    warnings,
	}
	if name := packageName(pkg.Name); name != "" {
		s.Name = name
	}

	s.Port, s.PortFrom = nodePort(seg, rawCmd, fw, p)

	switch {
	case nonWebFrameworks[fw.name] != "":
		s.Confidence = ConfidenceLow
		s.Warnings = append(s.Warnings, nonWebFrameworks[fw.name])
	case usesBunWithoutBinary(rawCmd):
		s.Confidence = ConfidenceLow
		s.Warnings = append(s.Warnings, "该脚本依赖 bun，本机未安装 bun")
	case s.PortFrom == PortFromFlag || s.PortFrom == PortFromViteConfig:
		s.Confidence = ConfidenceHigh
	default:
		s.Confidence = ConfidenceMedium
	}
	return s
}

// pickRunner 用锁文件推断启动器，并校验本机是否真的装了它（SPEC A7）。
func pickRunner(p *dirProbe) (string, []string) {
	type lockfile struct{ file, runner string }
	candidates := []lockfile{
		{"pnpm-lock.yaml", "pnpm"},
		{"yarn.lock", "yarn"},
		{"bun.lock", "bun"},
		{"bun.lockb", "bun"},
		{"package-lock.json", "npm"},
	}
	for _, c := range candidates {
		if !p.has(c.file) {
			continue
		}
		if _, err := exec.LookPath(c.runner); err == nil {
			return c.runner, nil
		}
		return "npm", []string{
			fmt.Sprintf("检测到 %s 锁文件，但本机没有安装 %s，已改用 npm 运行", c.file, c.runner),
		}
	}
	return "npm", nil
}

// usesBunWithoutBinary 判断脚本是否依赖 bun 而本机没有 bun。
func usesBunWithoutBinary(cmd string) bool {
	if !strings.Contains(cmd, "bun ") && !strings.HasPrefix(cmd, "bun ") {
		return false
	}
	_, err := exec.LookPath("bun")
	return err != nil
}

// detectFramework 命中框架表里的第一个。
func detectFramework(seg string) framework {
	for _, fw := range frameworks {
		if fw.re.MatchString(seg) {
			return fw
		}
	}
	return framework{}
}

// nodePort 按 SPEC 4.8 的统一优先级推端口。
func nodePort(seg, rawCmd string, fw framework, p *dirProbe) (int, string) {
	if n, ok := portFromFlags(seg); ok {
		return n, PortFromFlag
	}
	if n, ok := portFromEnvPrefix(rawCmd); ok {
		return n, PortFromFlag
	}
	if fw.name == "vite" {
		if n, ok := viteConfigPort(p); ok {
			return n, PortFromViteConfig
		}
	}
	if fw.port > 0 {
		return fw.port, PortFromFrameworkDefault
	}
	return 0, ""
}

// resolveServerSegment 判断一段脚本命令是不是在起服务；是则返回真正起服务的那一段。
//
// 按 SPEC N3 处理四种真实形态：`&&` 串联、`VAR=x cmd` 前缀、`chcp N &&`、
// 以及 `npm run X` 间接调用（递归深度 ≤ 2，防环）。
func resolveServerSegment(scripts map[string]string, cmd string, depth int) (string, bool) {
	for _, seg := range splitSegments(cmd) {
		seg = stripSegmentPrefix(seg)
		if seg == "" {
			continue
		}
		if notServerRe.MatchString(seg) {
			continue
		}
		if transpilerRe.MatchString(seg) && !watchWordRe.MatchString(seg) {
			continue
		}
		if serverCmdRe.MatchString(seg) {
			return seg, true
		}
		if m := runScriptRe.FindStringSubmatch(seg); m != nil && depth < 2 {
			sub, ok := scripts[m[2]]
			if !ok {
				continue
			}
			if found, ok := resolveServerSegment(scripts, sub, depth+1); ok {
				return found, true
			}
		}
	}
	return "", false
}

// splitSegments 按 && / & 拆段。
func splitSegments(cmd string) []string {
	parts := segmentSplitRe.Split(cmd, -1)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// stripSegmentPrefix 剥掉段首的 `VAR=value` 前缀与 `chcp 65001`。
func stripSegmentPrefix(seg string) string {
	fields := strings.Fields(seg)
	i := 0
	for i < len(fields) {
		f := fields[i]
		if envAssignRe.MatchString(f) {
			i++
			continue
		}
		if strings.EqualFold(f, "chcp") {
			i++
			// 紧随其后的代码页数字一起丢掉
			if i < len(fields) && isDigits(fields[i]) {
				i++
			}
			continue
		}
		if strings.EqualFold(f, "@echo") {
			i++
			if i < len(fields) && strings.EqualFold(fields[i], "off") {
				i++
			}
			continue
		}
		break
	}
	return strings.Join(fields[i:], " ")
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// packageName 去掉 npm scope（@scope/name → name），保留原大小写。
func packageName(raw string) string {
	name := strings.TrimSpace(raw)
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	return name
}
