// Package cli 实现 devdock 命令行入口。
// 约定：--json 模式下 stdout 只输出 JSON，任何提示走 stderr。
package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"devdock/internal/client"
	"devdock/internal/detect"
	"devdock/internal/model"
)

// 退出码，agent 依赖这些值，必须稳定。
const (
	ExitOK            = 0
	ExitError         = 1
	ExitUsage         = 2
	ExitNotFound      = 3
	ExitStartFailed   = 4
	ExitReadyTimeout  = 5
	ExitConflict      = 6
	ExitServiceAbsent = 7
)

// Run 执行一个 CLI 子命令，返回退出码。
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return runServeCommand([]string{}, stdout, stderr)
	}
	sub := args[0]
	rest := args[1:]

	switch sub {
	case "serve":
		return runServeCommand(rest, stdout, stderr)
	case "list", "ls":
		return cmdList(rest, stdout, stderr)
	case "status":
		return cmdStatus(rest, stdout, stderr)
	case "start":
		return cmdStart(rest, stdout, stderr)
	case "stop":
		return cmdStop(rest, stdout, stderr)
	case "restart":
		return cmdRestart(rest, stdout, stderr)
	case "logs":
		return cmdLogs(rest, stdout, stderr)
	case "add":
		return cmdAdd(rest, stdout, stderr)
	case "scan":
		return cmdScan(rest, stdout, stderr)
	case "remove", "rm":
		return cmdRemove(rest, stdout, stderr)
	case "open":
		return cmdOpen(rest, stdout, stderr)
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "DevDock %s\n", Version)
		return ExitOK
	case "help", "--help", "-h":
		printHelp(stdout)
		return ExitOK
	default:
		fmt.Fprintf(stderr, "未知命令：%s\n\n", sub)
		printHelp(stderr)
		return ExitUsage
	}
}

// Version 由 main 注入。
var Version = "dev"

// runServeCommand 由 main 注入，避免 cli 包直接依赖服务启动逻辑。
var runServeCommand = func(args []string, stdout, stderr io.Writer) int {
	fmt.Fprintln(stderr, "内部错误：serve 命令未注册")
	return ExitError
}

// RegisterServe 让 main 把 serve 实现注入进来。
func RegisterServe(fn func(args []string, stdout, stderr io.Writer) int) {
	runServeCommand = fn
}

func printHelp(w io.Writer) {
	fmt.Fprint(w, `DevDock —— 本地开发项目进程管理器

用法：
  devdock                                 启动服务并打开浏览器
  devdock serve [--port N] [--no-open]    仅启动服务
  devdock list [--json]                   列出全部项目
  devdock status <名称|ID> [--json]       查看单个项目状态
  devdock start <名称|ID> [--wait-ready] [--timeout 30s] [--json]
  devdock stop <名称|ID>                  停止项目
  devdock restart <名称|ID>               重启项目
  devdock logs <名称|ID> [-f] [--tail 200] [--json]
  devdock add <名称> --cwd <目录> --command <命令> [--port N] [--readiness /路径] [--env K=V]...
  devdock add --auto --cwd <目录>           自动识别并创建项目（名称可用位置参数覆盖，命令/端口可用 --command/--port 覆盖）
  devdock scan [<目录>...] [--root <目录>]... [--depth 3] [--timeout 2s] [--limit 100] [--all] [--json]
                                           扫描本机可识别的开发项目（只读，不创建项目）
  devdock remove <名称|ID> [--stop]       删除项目
  devdock open <名称|ID>                  在浏览器打开项目端口
  devdock mcp                             stdio MCP 服务器
  devdock version                         显示版本

所有查询类命令都支持 --json，此时 stdout 只输出 JSON。
CLI 会在服务未运行时自动拉起后台实例。
`)
}

// openClient 发现或拉起实例。
func openClient(stderr io.Writer) (*client.Client, int) {
	c, err := client.Discover(true)
	if err != nil {
		fmt.Fprintf(stderr, "无法连接 DevDock 服务：%v\n", err)
		return nil, ExitServiceAbsent
	}
	return c, ExitOK
}

// parseInterspersed 解析参数，允许位置参数（项目名）与 --flag 交错出现。
//
// 标准库 flag 遇到第一个非 flag 参数就停止解析，于是 README 里最自然的写法
// `devdock add 名称 --cwd ...` 会让 --cwd、--command 完全不被读取，
// 最后只报一句“必须同时提供 --cwd 与 --command”，很难看出是解析顺序问题。
// 这里把位置参数逐个挑出来，剩下的继续交给同一个 FlagSet，两种写法都能用。
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var positionals []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			return nil, err
		}
		remaining := fs.Args()
		if len(remaining) == 0 {
			return positionals, nil
		}
		positionals = append(positionals, remaining[0])
		rest = remaining[1:]
	}
}

// exitCodeForError 把错误映射为稳定退出码。
func exitCodeForError(err error) int {
	var apiErr *client.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Code {
		case client.CodeNotFound:
			return ExitNotFound
		case client.CodeAlreadyRunning, client.CodeConflict, client.CodeNotRunning, client.CodePortInUse, client.CodeScanInProgress:
			return ExitConflict
		}
		return ExitError
	}
	if errors.Is(err, client.ErrServiceUnavailable) {
		return ExitServiceAbsent
	}
	return ExitError
}

// --- list ---

func cmdList(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "以 JSON 输出")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}

	c, code := openClient(stderr)
	if c == nil {
		return code
	}
	views, err := c.List()
	if err != nil {
		fmt.Fprintf(stderr, "获取项目列表失败：%v\n", err)
		return exitCodeForError(err)
	}

	if *asJSON {
		return emitJSON(stdout, stderr, map[string]any{"projects": views})
	}

	if len(views) == 0 {
		fmt.Fprintln(stdout, "还没有任何项目。用 devdock add 添加第一个项目。")
		return ExitOK
	}

	rows := [][]string{{"名称", "状态", "就绪", "端口", "PID"}}
	for _, v := range views {
		rows = append(rows, []string{
			v.Name,
			lifecycleText(v.State),
			readinessText(v.State),
			portText(v.Port),
			pidText(v.State.PID),
		})
	}
	printTable(stdout, rows)
	return ExitOK
}

// --- status ---

func cmdStatus(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "以 JSON 输出")
	rest, err := parseInterspersed(fs, args)
	if err != nil {
		return ExitUsage
	}
	if len(rest) != 1 {
		fmt.Fprintln(stderr, "用法：devdock status <名称|ID>")
		return ExitUsage
	}

	c, code := openClient(stderr)
	if c == nil {
		return code
	}
	v, err := c.Get(rest[0])
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return exitCodeForError(err)
	}

	if *asJSON {
		return emitJSON(stdout, stderr, v)
	}

	fmt.Fprintf(stdout, "名称：%s\n", v.Name)
	fmt.Fprintf(stdout, "ID：%s\n", v.ID)
	fmt.Fprintf(stdout, "目录：%s\n", v.Cwd)
	fmt.Fprintf(stdout, "命令：%s\n", v.Command)
	fmt.Fprintf(stdout, "状态：%s\n", lifecycleText(v.State))
	fmt.Fprintf(stdout, "就绪：%s\n", readinessText(v.State))
	if v.Port > 0 {
		fmt.Fprintf(stdout, "端口：%d\n", v.Port)
	}
	if v.State.PID > 0 {
		fmt.Fprintf(stdout, "PID：%d\n", v.State.PID)
	}
	if v.State.LastError != "" {
		fmt.Fprintf(stdout, "最近错误：%s\n", v.State.LastError)
	}
	return ExitOK
}

// --- start ---

func cmdStart(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "以 JSON 输出")
	waitReady := fs.Bool("wait-ready", false, "等待业务就绪后再返回")
	timeout := fs.Duration("timeout", 30*time.Second, "等待就绪的超时时间")
	rest, err := parseInterspersed(fs, args)
	if err != nil {
		return ExitUsage
	}
	if len(rest) != 1 {
		fmt.Fprintln(stderr, "用法：devdock start <名称|ID> [--wait-ready] [--timeout 30s]")
		return ExitUsage
	}

	c, code := openClient(stderr)
	if c == nil {
		return code
	}

	// 先解析出真实 ID，避免把显示名传给只接受 ID 的接口。
	v, err := c.Get(rest[0])
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return exitCodeForError(err)
	}

	st, err := c.Start(v.ID)
	if err != nil {
		fmt.Fprintf(stderr, "启动失败：%v\n", err)
		return exitCodeForError(err)
	}

	if *waitReady {
		st, err = c.WaitReady(v.ID, *timeout)
		if err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			if st.Readiness == model.ReadinessTimeout {
				return ExitReadyTimeout
			}
			return ExitStartFailed
		}
	}

	if *asJSON {
		return emitJSON(stdout, stderr, st)
	}
	if *waitReady {
		fmt.Fprintf(stdout, "已启动并就绪：%s\n", v.Name)
	} else {
		fmt.Fprintf(stdout, "已启动：%s（状态 %s）\n", v.Name, lifecycleText(st))
	}
	return ExitOK
}

// --- stop ---

func cmdStop(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("stop", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "以 JSON 输出")
	rest, err := parseInterspersed(fs, args)
	if err != nil {
		return ExitUsage
	}
	if len(rest) != 1 {
		fmt.Fprintln(stderr, "用法：devdock stop <名称|ID>")
		return ExitUsage
	}

	c, code := openClient(stderr)
	if c == nil {
		return code
	}
	v, err := c.Get(rest[0])
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return exitCodeForError(err)
	}
	if err := c.Stop(v.ID); err != nil {
		fmt.Fprintf(stderr, "停止失败：%v\n", err)
		return exitCodeForError(err)
	}

	st, _ := c.State(v.ID)
	if *asJSON {
		return emitJSON(stdout, stderr, st)
	}
	fmt.Fprintf(stdout, "已停止：%s\n", v.Name)
	return ExitOK
}

// --- restart ---

func cmdRestart(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("restart", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "以 JSON 输出")
	rest, err := parseInterspersed(fs, args)
	if err != nil {
		return ExitUsage
	}
	if len(rest) != 1 {
		fmt.Fprintln(stderr, "用法：devdock restart <名称|ID>")
		return ExitUsage
	}

	c, code := openClient(stderr)
	if c == nil {
		return code
	}
	v, err := c.Get(rest[0])
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return exitCodeForError(err)
	}
	st, err := c.Restart(v.ID)
	if err != nil {
		fmt.Fprintf(stderr, "重启失败：%v\n", err)
		return exitCodeForError(err)
	}
	if *asJSON {
		return emitJSON(stdout, stderr, st)
	}
	fmt.Fprintf(stdout, "已重启：%s\n", v.Name)
	return ExitOK
}

// --- logs ---

func cmdLogs(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "以 JSON 输出")
	follow := fs.Bool("f", false, "持续跟随输出")
	tail := fs.Int("tail", 200, "显示最后多少行")
	rest, err := parseInterspersed(fs, args)
	if err != nil {
		return ExitUsage
	}
	if len(rest) != 1 {
		fmt.Fprintln(stderr, "用法：devdock logs <名称|ID> [-f] [--tail 200]")
		return ExitUsage
	}

	c, code := openClient(stderr)
	if c == nil {
		return code
	}
	v, err := c.Get(rest[0])
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return exitCodeForError(err)
	}

	res, err := c.Logs(v.ID, *tail)
	if err != nil {
		fmt.Fprintf(stderr, "读取日志失败：%v\n", err)
		return exitCodeForError(err)
	}

	if *asJSON {
		return emitJSON(stdout, stderr, res)
	}

	for _, l := range res.Lines {
		fmt.Fprintln(stdout, l.Text)
	}
	if !*follow {
		return ExitOK
	}

	// 跟随模式：订阅 SSE。Ctrl+C 时干净退出。
	return followLogs(c, v.ID, stdout, stderr)
}

// followLogs 订阅 SSE 并逐行打印，直到中断或连接结束。
func followLogs(c *client.Client, projectID string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(nil, os.Interrupt)
	defer stop()

	ch, errCh := c.StreamEvents(projectID)
	for {
		select {
		case <-ctx.Done():
			return ExitOK
		case err, ok := <-errCh:
			if ok && err != nil {
				fmt.Fprintf(stderr, "日志流中断：%v\n", err)
				return ExitError
			}
			return ExitOK
		case line, ok := <-ch:
			if !ok {
				return ExitOK
			}
			fmt.Fprintln(stdout, line)
		}
	}
}

// --- add ---

// envList 收集可重复的 --env 参数。
type envList []string

func (e *envList) String() string { return strings.Join(*e, ",") }
func (e *envList) Set(v string) error {
	*e = append(*e, v)
	return nil
}

// stringList 收集可重复的 --root 参数。
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func intPtr(n int) *int { return &n }

func cmdAdd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "以 JSON 输出")
	auto := fs.Bool("auto", false, "从 --cwd 自动识别名称、命令与端口")
	cwd := fs.String("cwd", "", "工作目录（必填）")
	command := fs.String("command", "", "启动命令（必填）")
	port := fs.Int("port", 0, "声明端口")
	readiness := fs.String("readiness", "", "HTTP 就绪检查路径，如 /health")
	timeoutSec := fs.Int("ready-timeout", 30, "就绪检查超时（秒）")
	autostart := fs.Bool("autostart", false, "DevDock 启动时自动启动")
	var envs envList
	fs.Var(&envs, "env", "环境变量 KEY=VALUE，可重复")
	rest, err := parseInterspersed(fs, args)
	if err != nil {
		return ExitUsage
	}

	name := ""
	if len(rest) > 0 {
		name = rest[0]
	}

	// --auto：先看 --cwd 本身（depth 0，不扫子目录），识别出名称/命令/端口。
	// 旧用法（不带 --auto）的校验与后续路径逐字节保持原样。
	var recognized *detect.Suggestion
	if *auto {
		if *cwd == "" {
			fmt.Fprintln(stderr, "用法：devdock add --auto --cwd <目录> [选项]")
			return ExitUsage
		}
		c, code := openClient(stderr)
		if c == nil {
			return code
		}
		res, err := c.Scan(client.ScanRequest{
			Roots:      []string{*cwd},
			Depth:      intPtr(0),
			BudgetMs:   2000,
			Limit:      1,
			IncludeLow: true,
		})
		if err != nil {
			fmt.Fprintf(stderr, "自动识别失败：%v\n", err)
			return exitCodeForError(err)
		}
		if len(res.Suggestions) == 0 {
			fmt.Fprintf(stderr, "在 %s 没有识别到可启动的项目：没有 package.json、go.mod 或服务框架依赖等信号。\n", *cwd)
			return ExitError
		}
		sug := res.Suggestions[0]
		if strings.TrimSpace(sug.Command) == "" {
			fmt.Fprintf(stderr, "在 %s 识别到了 %s（%s），但判不出启动命令，请用 --command 手填。\n", *cwd, sug.Name, sug.Kind)
			return ExitError
		}
		recognized = &sug
		if name == "" {
			name = sug.Name
		}
		if *command == "" {
			*command = sug.Command
		}
		if *port == 0 {
			*port = sug.Port
		}
	} else {
		if len(rest) < 1 {
			fmt.Fprintln(stderr, "用法：devdock add <名称> --cwd <目录> --command <命令> [选项]")
			return ExitUsage
		}
		if *cwd == "" || *command == "" {
			fmt.Fprintln(stderr, "必须同时提供 --cwd 与 --command")
			return ExitUsage
		}
	}

	c, code := openClient(stderr)
	if c == nil {
		return code
	}

	p := model.Project{
		Name:      name,
		Cwd:       *cwd,
		Command:   *command,
		Port:      *port,
		Autostart: *autostart,
		Env:       map[string]string{},
	}
	for _, kv := range envs {
		if i := strings.Index(kv, "="); i > 0 {
			p.Env[kv[:i]] = kv[i+1:]
		}
	}
	if *readiness != "" {
		p.Readiness = &model.ReadinessConfig{
			Path:       *readiness,
			TimeoutSec: *timeoutSec,
		}
	}

	v, err := c.Create(p)
	if err != nil {
		fmt.Fprintf(stderr, "创建失败：%v\n", err)
		return exitCodeForError(err)
	}

	// 识别结论走 stderr：--json 时 stdout 必须只有 JSON（SPEC B2、红线 5）。
	if recognized != nil {
		fmt.Fprintf(stderr, "识别结论：目录 %s → 名称 %s，命令 %s，端口 %d（%s，置信度 %s）\n",
			*cwd, recognized.Name, recognized.Command, recognized.Port, recognized.Kind, recognized.Confidence)
		for _, w := range recognized.Warnings {
			fmt.Fprintf(stderr, "  警告：%s\n", w)
		}
	}

	if *asJSON {
		return emitJSON(stdout, stderr, v)
	}
	fmt.Fprintf(stdout, "已创建项目：%s（ID %s）\n", v.Name, v.ID)
	return ExitOK
}

// --- scan ---

// cmdScan 只读扫描本机项目并打印建议。它不创建任何项目。
func cmdScan(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "以 JSON 输出")
	depth := fs.Int("depth", 3, "扫描深度 1-6")
	timeout := fs.Duration("timeout", 2*time.Second, "单次扫描时间预算")
	limit := fs.Int("limit", 100, "建议条数上限")
	all := fs.Bool("all", false, "连 confidence=low 一起输出")
	var rootFlags stringList
	fs.Var(&rootFlags, "root", "扫描根目录，可重复")
	rest, err := parseInterspersed(fs, args)
	if err != nil {
		return ExitUsage
	}
	if *depth < 1 || *depth > detect.MaxDepth {
		fmt.Fprintf(stderr, "扫描深度必须在 1-%d 之间\n", detect.MaxDepth)
		return ExitUsage
	}
	if *limit < 1 || *limit > 500 {
		fmt.Fprintln(stderr, "建议条数上限必须在 1-500 之间")
		return ExitUsage
	}

	roots := append(append([]string{}, rest...), rootFlags...)
	if len(roots) > detect.MaxRoots {
		fmt.Fprintf(stderr, "根目录最多 %d 个\n", detect.MaxRoots)
		return ExitUsage
	}

	// CLI 的 --timeout 与服务端 budgetMs 统一（SPEC A18），并夹到服务端合法区间。
	budgetMs := int(timeout.Milliseconds())
	if budgetMs < 100 {
		budgetMs = 100
	}
	if budgetMs > 10000 {
		budgetMs = 10000
	}

	c, code := openClient(stderr)
	if c == nil {
		return code
	}
	res, err := c.Scan(client.ScanRequest{
		Roots:      roots,
		Depth:      intPtr(*depth),
		BudgetMs:   budgetMs,
		Limit:      *limit,
		IncludeLow: *all,
	})
	if err != nil {
		fmt.Fprintf(stderr, "扫描失败：%v\n", err)
		return exitCodeForError(err)
	}

	if *asJSON {
		return emitJSON(stdout, stderr, res)
	}

	if len(res.Suggestions) == 0 {
		fmt.Fprintf(stdout, "没有发现可识别的项目（扫描 %d 个目录，用时 %dms）。可以指定 --root 再试。\n",
			res.ScannedDirs, res.ElapsedMs)
		return ExitOK
	}

	rows := [][]string{{"名称", "类型", "框架", "端口", "命令", "目录"}}
	for _, s := range res.Suggestions {
		rows = append(rows, []string{
			s.Name, s.Kind, s.Framework, portText(s.Port), s.Command, relToRoots(s.Cwd, res.Roots),
		})
	}
	printTable(stdout, rows)
	fmt.Fprintf(stdout, "共 %d 条建议（扫描 %d 个目录，用时 %dms）\n",
		len(res.Suggestions), res.ScannedDirs, res.ElapsedMs)
	if res.Truncated {
		fmt.Fprintln(stdout, "提示：结果已截断（命中条数或目录数上限）。")
	}
	if res.TimedOut {
		fmt.Fprintln(stdout, "提示：扫描已达时间预算，结果可能不完整。")
	}
	return ExitOK
}

// relToRoots 把建议的 cwd 显示成相对某个扫描根的位置，便于阅读。
func relToRoots(cwd string, roots []string) string {
	for _, r := range roots {
		rel, err := filepath.Rel(r, cwd)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		return rel
	}
	return cwd
}

// --- remove ---

func cmdRemove(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("remove", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "以 JSON 输出")
	stopFirst := fs.Bool("stop", false, "删除前先停止")
	rest, err := parseInterspersed(fs, args)
	if err != nil {
		return ExitUsage
	}
	if len(rest) != 1 {
		fmt.Fprintln(stderr, "用法：devdock remove <名称|ID> [--stop]")
		return ExitUsage
	}

	c, code := openClient(stderr)
	if c == nil {
		return code
	}
	v, err := c.Get(rest[0])
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return exitCodeForError(err)
	}
	if err := c.Delete(v.ID, *stopFirst); err != nil {
		fmt.Fprintf(stderr, "删除失败：%v\n", err)
		return exitCodeForError(err)
	}
	if *asJSON {
		return emitJSON(stdout, stderr, map[string]any{"deleted": v.ID})
	}
	fmt.Fprintf(stdout, "已删除项目：%s\n", v.Name)
	return ExitOK
}

// --- open ---

func cmdOpen(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("open", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rest, err := parseInterspersed(fs, args)
	if err != nil {
		return ExitUsage
	}
	if len(rest) != 1 {
		fmt.Fprintln(stderr, "用法：devdock open <名称|ID>")
		return ExitUsage
	}

	c, code := openClient(stderr)
	if c == nil {
		return code
	}
	v, err := c.Get(rest[0])
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return exitCodeForError(err)
	}
	url := v.ReadyURL()
	if url == "" {
		fmt.Fprintln(stderr, "该项目未声明端口，无法打开")
		return ExitUsage
	}
	if err := client.OpenInBrowser(url); err != nil {
		fmt.Fprintf(stderr, "打开浏览器失败：%v\n", err)
		return ExitError
	}
	fmt.Fprintf(stdout, "已在浏览器打开 %s\n", url)
	return ExitOK
}

// --- 输出辅助 ---

func emitJSON(stdout, stderr io.Writer, v any) int {
	enc := json.NewEncoder(stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintf(stderr, "输出 JSON 失败：%v\n", err)
		return ExitError
	}
	return ExitOK
}

// printTable 打印对齐的中文表格。中文字符按双宽度计算。
func printTable(w io.Writer, rows [][]string) {
	if len(rows) == 0 {
		return
	}
	widths := make([]int, len(rows[0]))
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) {
				if n := displayWidth(cell); n > widths[i] {
					widths[i] = n
				}
			}
		}
	}
	for _, row := range rows {
		var sb strings.Builder
		for i, cell := range row {
			sb.WriteString(cell)
			if i < len(row)-1 {
				pad := widths[i] - displayWidth(cell) + 2
				sb.WriteString(strings.Repeat(" ", pad))
			}
		}
		fmt.Fprintln(w, sb.String())
	}
}

// displayWidth 估算显示宽度：CJK 字符占两列。
func displayWidth(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x1100 && (r <= 0x115F ||
			r == 0x2329 || r == 0x232A ||
			(r >= 0x2E80 && r <= 0xA4CF) ||
			(r >= 0xAC00 && r <= 0xD7A3) ||
			(r >= 0xF900 && r <= 0xFAFF) ||
			(r >= 0xFE30 && r <= 0xFE6F) ||
			(r >= 0xFF00 && r <= 0xFF60) ||
			(r >= 0xFFE0 && r <= 0xFFE6)) {
			n += 2
		} else {
			n++
		}
	}
	return n
}

func lifecycleText(st model.ProjectState) string {
	switch st.Lifecycle {
	case model.LifecycleRunning:
		return "运行中"
	case model.LifecycleStarting:
		return "启动中"
	case model.LifecycleStopping:
		return "停止中"
	case model.LifecycleFailed:
		if st.LastExitCode != nil {
			return "失败（退出码 " + strconv.Itoa(*st.LastExitCode) + "）"
		}
		return "失败"
	default:
		return "已停止"
	}
}

func readinessText(st model.ProjectState) string {
	switch st.Readiness {
	case model.ReadinessReady:
		return "就绪"
	case model.ReadinessPending:
		return "等待中"
	case model.ReadinessTimeout:
		return "超时"
	case model.ReadinessError:
		return "出错"
	default:
		return "—"
	}
}

func portText(p int) string {
	if p <= 0 {
		return "—"
	}
	return strconv.Itoa(p)
}

func pidText(pid uint32) string {
	if pid == 0 {
		return "—"
	}
	return strconv.FormatUint(uint64(pid), 10)
}

// SortViews 让 --json 输出顺序稳定，便于脚本比对。
func SortViews(views []model.View) {
	sort.SliceStable(views, func(i, j int) bool { return views[i].ID < views[j].ID })
}
