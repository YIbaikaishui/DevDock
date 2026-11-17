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
	"sort"
	"strconv"
	"strings"
	"time"

	"devdock/internal/client"
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

// exitCodeForError 把错误映射为稳定退出码。
func exitCodeForError(err error) int {
	var apiErr *client.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Code {
		case client.CodeNotFound:
			return ExitNotFound
		case client.CodeAlreadyRunning, client.CodeConflict, client.CodeNotRunning, client.CodePortInUse:
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
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "用法：devdock status <名称|ID>")
		return ExitUsage
	}

	c, code := openClient(stderr)
	if c == nil {
		return code
	}
	v, err := c.Get(fs.Arg(0))
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
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "用法：devdock start <名称|ID> [--wait-ready] [--timeout 30s]")
		return ExitUsage
	}

	c, code := openClient(stderr)
	if c == nil {
		return code
	}

	// 先解析出真实 ID，避免把显示名传给只接受 ID 的接口。
	v, err := c.Get(fs.Arg(0))
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
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "用法：devdock stop <名称|ID>")
		return ExitUsage
	}

	c, code := openClient(stderr)
	if c == nil {
		return code
	}
	v, err := c.Get(fs.Arg(0))
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
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "用法：devdock restart <名称|ID>")
		return ExitUsage
	}

	c, code := openClient(stderr)
	if c == nil {
		return code
	}
	v, err := c.Get(fs.Arg(0))
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
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "用法：devdock logs <名称|ID> [-f] [--tail 200]")
		return ExitUsage
	}

	c, code := openClient(stderr)
	if c == nil {
		return code
	}
	v, err := c.Get(fs.Arg(0))
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

func cmdAdd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "以 JSON 输出")
	cwd := fs.String("cwd", "", "工作目录（必填）")
	command := fs.String("command", "", "启动命令（必填）")
	port := fs.Int("port", 0, "声明端口")
	readiness := fs.String("readiness", "", "HTTP 就绪检查路径，如 /health")
	timeoutSec := fs.Int("ready-timeout", 30, "就绪检查超时（秒）")
	autostart := fs.Bool("autostart", false, "DevDock 启动时自动启动")
	var envs envList
	fs.Var(&envs, "env", "环境变量 KEY=VALUE，可重复")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if fs.NArg() < 1 {
		fmt.Fprintln(stderr, "用法：devdock add <名称> --cwd <目录> --command <命令> [选项]")
		return ExitUsage
	}
	if *cwd == "" || *command == "" {
		fmt.Fprintln(stderr, "必须同时提供 --cwd 与 --command")
		return ExitUsage
	}

	c, code := openClient(stderr)
	if c == nil {
		return code
	}

	p := model.Project{
		Name:      fs.Arg(0),
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

	if *asJSON {
		return emitJSON(stdout, stderr, v)
	}
	fmt.Fprintf(stdout, "已创建项目：%s（ID %s）\n", v.Name, v.ID)
	return ExitOK
}

// --- remove ---

func cmdRemove(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("remove", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "以 JSON 输出")
	stopFirst := fs.Bool("stop", false, "删除前先停止")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "用法：devdock remove <名称|ID> [--stop]")
		return ExitUsage
	}

	c, code := openClient(stderr)
	if c == nil {
		return code
	}
	v, err := c.Get(fs.Arg(0))
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
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "用法：devdock open <名称|ID>")
		return ExitUsage
	}

	c, code := openClient(stderr)
	if c == nil {
		return code
	}
	v, err := c.Get(fs.Arg(0))
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
