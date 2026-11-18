package detect

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"devdock/internal/model"
)

// slugIDPattern 与 store 的 slugPattern 保持一致。
var slugIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

// --- 测试辅助 ---

// writeFile 在临时树里写一个文件，自动创建父目录。
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("创建目录失败: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写文件 %s 失败: %v", path, err)
	}
}

// writePackageJSON 写一个 package.json。
func writePackageJSON(t *testing.T, dir, name string, scripts map[string]string) {
	t.Helper()
	body := map[string]any{"scripts": scripts}
	if name != "" {
		body["name"] = name
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("序列化 package.json 失败: %v", err)
	}
	writeFile(t, dir, "package.json", string(data))
}

// scanTree 以给定参数扫一遍，失败即终止用例。
func scanTree(t *testing.T, o Options) *Result {
	t.Helper()
	res, err := Scan(context.Background(), o)
	if err != nil {
		t.Fatalf("扫描失败: %v", err)
	}
	return res
}

// scanOne 只判定 dir 本身（depth=0），用于单项目规则用例。
func scanOne(t *testing.T, dir string, mut func(*Options)) *Result {
	t.Helper()
	o := DefaultOptions()
	o.Roots = []string{dir}
	o.Depth = 0
	o.IncludeLow = true
	if mut != nil {
		mut(&o)
	}
	return scanTree(t, o)
}

// only 断言结果里恰好一条建议并返回它。
func only(t *testing.T, res *Result) Suggestion {
	t.Helper()
	if len(res.Suggestions) != 1 {
		t.Fatalf("期望 1 条建议，实际 %d 条：%+v", len(res.Suggestions), res.Suggestions)
	}
	return res.Suggestions[0]
}

// none 断言结果里一条建议都没有。
func none(t *testing.T, res *Result) {
	t.Helper()
	if len(res.Suggestions) != 0 {
		t.Fatalf("期望 0 条建议，实际 %d 条：%+v", len(res.Suggestions), res.Suggestions)
	}
}

// mkdirs 造 n 个空目录。
func mkdirs(t *testing.T, root string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := os.Mkdir(filepath.Join(root, fmt.Sprintf("d%04d", i)), 0o755); err != nil {
			t.Fatalf("造目录失败: %v", err)
		}
	}
}

// --- T1–T9：Node 规则 ---

// T1 Vite 默认端口。
func TestT01ViteDefaultPort(t *testing.T) {
	dir := t.TempDir()
	writePackageJSON(t, dir, "web-app", map[string]string{"dev": "vite"})

	s := only(t, scanOne(t, dir, nil))
	if s.Command != "npm run dev" {
		t.Fatalf("command 应为 npm run dev，实际 %q", s.Command)
	}
	if s.Framework != "vite" {
		t.Fatalf("framework 应为 vite，实际 %q", s.Framework)
	}
	if s.Port != 5173 || s.PortFrom != PortFromFrameworkDefault {
		t.Fatalf("端口应为 5173/framework-default，实际 %d/%s", s.Port, s.PortFrom)
	}
	if s.Confidence != ConfidenceMedium {
		t.Fatalf("靠框架默认值推出的端口置信度应为 medium，实际 %q", s.Confidence)
	}
}

// T2 构建脚本不算服务（vite build --watch 是本机最容易误判的一类，共 10 例）。
func TestT02BuildScriptIsNotServer(t *testing.T) {
	dir := t.TempDir()
	writePackageJSON(t, dir, "ext-image", map[string]string{
		"dev": "vite build --config viteConfig.ts --watch",
	})
	none(t, scanOne(t, dir, nil))
}

// T3 && 串联 + chcp 前缀。
func TestT03ChcpPrefixAndChain(t *testing.T) {
	dir := t.TempDir()
	writePackageJSON(t, dir, "chcp-project", map[string]string{"dev": "chcp 65001 && vite"})

	s := only(t, scanOne(t, dir, nil))
	if s.Framework != "vite" {
		t.Fatalf("应识别为 vite，实际 %q（command=%q）", s.Framework, s.Command)
	}
}

// T4 命令里的显式端口优先。
func TestT04ExplicitPortFlag(t *testing.T) {
	dir := t.TempDir()
	writePackageJSON(t, dir, "readlecture", map[string]string{"dev": "next dev -p 3001"})

	s := only(t, scanOne(t, dir, nil))
	if s.Port != 3001 || s.PortFrom != PortFromFlag {
		t.Fatalf("端口应为 3001/flag，实际 %d/%s", s.Port, s.PortFrom)
	}
	if s.Confidence != ConfidenceHigh {
		t.Fatalf("显式端口置信度应为 high，实际 %q", s.Confidence)
	}
}

// T5 vite.config 里的 server.port。
func TestT05ViteConfigPort(t *testing.T) {
	dir := t.TempDir()
	writePackageJSON(t, dir, "workshop-web", map[string]string{"dev": "vite --host"})
	writeFile(t, dir, "vite.config.ts", "import { defineConfig } from 'vite'\n"+
		"export default defineConfig({\n  server: { port: 3100, strictPort: true },\n})\n")

	s := only(t, scanOne(t, dir, nil))
	if s.Port != 3100 || s.PortFrom != PortFromViteConfig {
		t.Fatalf("端口应为 3100/vite.config，实际 %d/%s", s.Port, s.PortFrom)
	}
}

// T6 dev 优先于 start。
func TestT06DevWinsOverStart(t *testing.T) {
	dir := t.TempDir()
	writePackageJSON(t, dir, "pc-web", map[string]string{
		"dev":   "next dev",
		"start": "next start -p 3001",
	})

	s := only(t, scanOne(t, dir, nil))
	if s.Command != "npm run dev" {
		t.Fatalf("command 应为 npm run dev，实际 %q", s.Command)
	}
	if s.Port != 3000 || s.PortFrom != PortFromFrameworkDefault {
		t.Fatalf("端口应为 3000/framework-default，实际 %d/%s", s.Port, s.PortFrom)
	}
}

// T7 递归解析 npm run X（真实存在：dev 是 npm run dev:chrome）。
func TestT07RecursiveScript(t *testing.T) {
	dir := t.TempDir()
	writePackageJSON(t, dir, "clipper", map[string]string{
		"dev":       "npm run dev:web",
		"dev:web":   "vite",
		"typecheck": "tsc --noEmit",
	})

	s := only(t, scanOne(t, dir, nil))
	if s.Framework != "vite" || s.Command != "npm run dev" {
		t.Fatalf("应识别 dev → dev:web → vite，实际 framework=%q command=%q", s.Framework, s.Command)
	}
}

// T8 electron 桌面应用必须排除。
func TestT08ElectronExcluded(t *testing.T) {
	dir := t.TempDir()
	writePackageJSON(t, dir, "huobao-drama-desktop", map[string]string{
		"dev":           "npm run build:backend && npm run build:main && electron .",
		"build:backend": "node scripts/build-backend.mjs",
		"build:main":    "node scripts/build-main.mjs",
	})
	none(t, scanOne(t, dir, nil))
}

// T08b 不带 watch 的 tsx/ts-node 跑完即退出，是 CLI 工具而不是常驻服务
// （本机 open-design\tools\{dev,pack,release,serve} 四例，调研 3.3 明确点名）。
func TestT08bOneShotTsxExcluded(t *testing.T) {
	for _, cmd := range []string{"tsx ./src/index.ts", "ts-node src/cli.ts"} {
		dir := t.TempDir()
		writePackageJSON(t, dir, "cli-tool", map[string]string{"dev": cmd})
		none(t, scanOne(t, dir, nil))
	}
}

// T08c 带 watch 的 tsx 是合法服务（本机 huobao-drama\backend 的 dev 脚本）。
func TestT08cTsxWatchIsServer(t *testing.T) {
	dir := t.TempDir()
	writePackageJSON(t, dir, "backend", map[string]string{"dev": "tsx watch src/index.ts"})
	s := only(t, scanOne(t, dir, nil))
	if s.Command != "npm run dev" {
		t.Fatalf("命令应为 npm run dev，实际 %q", s.Command)
	}
	if s.Port != 0 {
		t.Fatalf("无框架无端口线索时应留空，实际 %d", s.Port)
	}
}

// T9 锁文件推断启动器 + LookPath 回退（二分断言，不写死本机有没有 yarn）。
func TestT09RunnerFallback(t *testing.T) {
	dir := t.TempDir()
	writePackageJSON(t, dir, "yarn-project", map[string]string{"dev": "vite"})
	writeFile(t, dir, "yarn.lock", "# yarn lockfile v1\n")

	s := only(t, scanOne(t, dir, nil))
	if _, err := exec.LookPath("yarn"); err == nil {
		if s.Command != "yarn run dev" {
			t.Fatalf("本机有 yarn 时 command 应为 yarn run dev，实际 %q", s.Command)
		}
		if len(s.Warnings) != 0 {
			t.Fatalf("本机有 yarn 时不应有 warning，实际 %v", s.Warnings)
		}
		return
	}
	if s.Command != "npm run dev" {
		t.Fatalf("本机无 yarn 时应回退 npm run dev，实际 %q", s.Command)
	}
	if !strings.Contains(strings.Join(s.Warnings, " "), "yarn") {
		t.Fatalf("回退时 warnings 应提到 yarn，实际 %v", s.Warnings)
	}
}

// --- T10–T11：命名与 ID ---

// T10 同批次 ID 冲突追加 -2。两个目录 a/web、b/web 的 package.json 名字都是 web，
// 名称完全相同时 ID 必须靠 -2 区分（需求 9.1 T10）。
func TestT10IDDeduplication(t *testing.T) {
	root := t.TempDir()
	writePackageJSON(t, filepath.Join(root, "a", "web"), "web", map[string]string{"dev": "vite"})
	writePackageJSON(t, filepath.Join(root, "b", "web"), "web", map[string]string{"dev": "vite"})

	o := DefaultOptions()
	o.Roots = []string{root}
	o.Depth = 2
	res := scanTree(t, o)

	if len(res.Suggestions) != 2 {
		t.Fatalf("期望 2 条建议，实际 %d 条：%+v", len(res.Suggestions), res.Suggestions)
	}
	ids := []string{res.Suggestions[0].ID, res.Suggestions[1].ID}
	want := []string{"web", "web-2"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("ID 应为 %v，实际 %v", want, ids)
	}
}

// T11 中文目录名。
func TestT11ChineseDirectoryName(t *testing.T) {
	root := t.TempDir()
	chinese := filepath.Join(root, "28高三")
	writeFile(t, chinese, "package.json", `{"scripts":{"dev":"vite"}}`)

	res := scanOne(t, chinese, nil)
	s := only(t, res)
	if s.Name != "28高三" {
		t.Fatalf("name 应保留中文 28高三，实际 %q", s.Name)
	}
	if !slugIDPattern.MatchString(s.ID) {
		t.Fatalf("纯中文名的兜底 ID %q 不匹配 slug 规则", s.ID)
	}
	if s.ID != slugID("28高三", s.Cwd) {
		t.Fatalf("同一条目的 ID 必须稳定")
	}
}

// --- T12–T14：Go 规则 ---

// T12 Go 根目录 main 包 → go run .
func TestT12GoRootMain(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module demo\n\ngo 1.25\n")
	writeFile(t, dir, "main.go", "package main\n\nfunc main() {}\n")

	s := only(t, scanOne(t, dir, nil))
	if s.Command != "go run ." {
		t.Fatalf("command 应为 go run .，实际 %q", s.Command)
	}
	if s.Port != 0 {
		t.Fatalf("Go 项目端口应留空（0），实际 %d", s.Port)
	}
	if s.Kind != KindGo {
		t.Fatalf("kind 应为 go，实际 %q", s.Kind)
	}
}

// T13 Go cmd/ 单入口 → go run ./cmd/X
func TestT13GoCmdSingle(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module demo\n\ngo 1.25\n")
	writeFile(t, dir, "cmd/svc/main.go", "package main\n\nfunc main() {}\n")

	s := only(t, scanOne(t, dir, nil))
	if s.Command != "go run ./cmd/svc" {
		t.Fatalf("command 应为 go run ./cmd/svc，实际 %q", s.Command)
	}
}

// T14 Go cmd/ 多入口且无唯一命中 → 不产出。
func TestT14GoCmdAmbiguous(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module demo\n\ngo 1.25\n")
	writeFile(t, dir, "cmd/migrate/main.go", "package main\n\nfunc main() {}\n")
	writeFile(t, dir, "cmd/host-updater/main.go", "package main\n\nfunc main() {}\n")
	none(t, scanOne(t, dir, nil))
}

// --- T19–T28：扫描行为 ---

// T19 只有 index.html 的静态目录不产出。
func TestT19StaticIndexOnly(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "index.html", "<!DOCTYPE html><html><body>hi</body></html>")
	none(t, scanOne(t, dir, nil))
}

// T20 .env 完全不读：DB_PORT 不得被当成服务端口。
func TestT20EnvFileIsNotRead(t *testing.T) {
	dir := t.TempDir()
	writePackageJSON(t, dir, "env-trap", map[string]string{"dev": "node server.mjs"})
	writeFile(t, dir, ".env", "DB_PORT=3306\nREDISPORT=6379\n")

	s := only(t, scanOne(t, dir, nil))
	if s.Port != 0 {
		t.Fatalf("不该从 .env 推端口，期望 0，实际 %d（portFrom=%s）", s.Port, s.PortFrom)
	}
}

// T21 剪枝：node_modules / 点目录 / dist 里的诱饵都不能出现。
func TestT21Pruning(t *testing.T) {
	root := t.TempDir()
	writePackageJSON(t, filepath.Join(root, "node_modules", "x"), "decoy-nm", map[string]string{"dev": "vite"})
	writePackageJSON(t, filepath.Join(root, ".git"), "decoy-git", map[string]string{"dev": "vite"})
	writePackageJSON(t, filepath.Join(root, "dist"), "decoy-dist", map[string]string{"dev": "vite"})
	writePackageJSON(t, filepath.Join(root, "real"), "real-app", map[string]string{"dev": "vite"})

	o := DefaultOptions()
	o.Roots = []string{root}
	o.Depth = 3
	res := scanTree(t, o)

	if len(res.Suggestions) != 1 || res.Suggestions[0].Name != "real-app" {
		t.Fatalf("只应命中 real-app，实际 %+v", res.Suggestions)
	}
}

// T22 深度上限。
func TestT22DepthLimit(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "l1", "l2", "l3", "l4", "l5")
	writePackageJSON(t, deep, "deep-app", map[string]string{"dev": "vite"})

	o := DefaultOptions()
	o.Roots = []string{root}
	o.Depth = 3
	none(t, scanTree(t, o))

	o.Depth = 6
	res := scanTree(t, o)
	if len(res.Suggestions) != 1 {
		t.Fatalf("depth=6 时应命中 1 条，实际 %d 条：%+v", len(res.Suggestions), res.Suggestions)
	}
}

// T23 时间预算到点必须立刻返回，且 timedOut=true。
func TestT23BudgetWatchdog(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root, 2000)

	o := DefaultOptions()
	o.Roots = []string{root}
	o.Depth = 1
	o.BudgetMs = 1

	start := time.Now()
	res := scanTree(t, o)
	elapsed := time.Since(start)

	if !res.TimedOut {
		t.Fatalf("预算只有 1ms，应返回 timedOut=true")
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("超时后必须在 200ms 内返回，实际 %v", elapsed)
	}
}

// T24 目录数上限。
func TestT24MaxDirs(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root, 300)

	o := DefaultOptions()
	o.Roots = []string{root}
	o.Depth = 1
	o.MaxDirs = 10
	res := scanTree(t, o)

	if !res.Truncated {
		t.Fatalf("命中 maxDirs 时应置 truncated=true")
	}
	if res.ScannedDirs > 10 {
		t.Fatalf("scannedDirs 不得超过 maxDirs=10，实际 %d", res.ScannedDirs)
	}
}

// T25 不可读目录不崩：扫描器对 readdir 失败必须静默跳过。
func TestT25UnreadableDirDoesNotPanic(t *testing.T) {
	// Windows 上用 os.Chmod 造不出真正的不可读目录（ACL 不走 POSIX 权限位），
	// 这里改用「目录不存在」模拟 readdir 失败：扫描里的错误分支与此完全同一条路径。
	missing := filepath.Join(t.TempDir(), "gone")
	probe, truncated, err := readDirProbe(missing, 100)
	if err == nil || probe != nil || truncated {
		t.Fatalf("不存在的目录应返回错误且无 probe，实际 probe=%v truncated=%v err=%v", probe, truncated, err)
	}

	o := DefaultOptions()
	st := &walkState{}
	got := inspectDir(missing, o, st)
	if got.found != nil || len(got.subdirs) != 0 {
		t.Fatalf("不可读目录不应产出任何结果，实际 %+v", got)
	}
}

// T26 已管理项目标重。
func TestT26MarkDuplicate(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "web-a")
	writePackageJSON(t, target, "web-a", map[string]string{"dev": "vite"})

	o := DefaultOptions()
	o.Roots = []string{root}
	o.Depth = 1
	o.Existing = []model.Project{{ID: "existing-web-a", Cwd: target}}

	s := only(t, scanTree(t, o))
	if !s.Duplicate {
		t.Fatalf("已管理项目的同名 cwd 应标 duplicate=true")
	}
	if s.ExistingID != "existing-web-a" {
		t.Fatalf("existingId 应为 existing-web-a，实际 %q", s.ExistingID)
	}
}

// T27 确定性：同一棵树扫两次，建议逐字段相等。
func TestT27Deterministic(t *testing.T) {
	root := t.TempDir()
	writePackageJSON(t, filepath.Join(root, "z-web"), "web", map[string]string{"dev": "vite"})
	writePackageJSON(t, filepath.Join(root, "a-web"), "web", map[string]string{"dev": "next dev -p 3001"})
	writeFile(t, filepath.Join(root, "g"), "go.mod", "module g\n\ngo 1.25\n")
	writeFile(t, filepath.Join(root, "g"), "main.go", "package main\n\nfunc main() {}\n")

	o := DefaultOptions()
	o.Roots = []string{root}
	o.Depth = 2
	first := scanTree(t, o)
	second := scanTree(t, o)

	if !reflect.DeepEqual(first.Suggestions, second.Suggestions) {
		t.Fatalf("两次扫描结果不一致：\n第一次 %+v\n第二次 %+v", first.Suggestions, second.Suggestions)
	}
	if first.ScannedDirs != second.ScannedDirs {
		t.Fatalf("两次扫描目录数不一致：%d / %d", first.ScannedDirs, second.ScannedDirs)
	}
}

// T28 端口占用预检。需要真占住 5173，占不到就跳过（SPEC D1 允许）。
func TestT28PortInUse(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:5173")
	if err != nil {
		t.Skipf("5173 已被别的进程占用或无法监听（%v），跳过端口预检用例", err)
	}
	defer ln.Close()

	dir := t.TempDir()
	writePackageJSON(t, dir, "vite-default", map[string]string{"dev": "vite"})
	s := only(t, scanOne(t, dir, nil))
	if s.Port != 5173 {
		t.Fatalf("用例前提是默认端口 5173，实际 %d", s.Port)
	}
	if !s.PortInUse {
		t.Fatalf("5173 被占用时 portInUse 应为 true")
	}
}
