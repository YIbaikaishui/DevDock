// DevDock —— 本地开发项目进程管理器。
//
// 同一个 exe 提供三个入口，共用一套语义：
//   - 无参数 / serve：启动本机服务并打开浏览器界面
//   - 各类子命令：CLI，供人与脚本使用
//   - mcp：stdio MCP 服务器，供 coding agent 使用
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"devdock/internal/cli"
	"devdock/internal/client"
	"devdock/internal/config"
	"devdock/internal/httpapi"
	"devdock/internal/mcpserver"
	"devdock/internal/model"
	"devdock/internal/runner"
	"devdock/internal/store"
	"devdock/internal/winproc"
	"devdock/web"
)

// version 是构建版本，可通过 -ldflags 注入。
var version = "1.0.0"

// mutexName 是单实例互斥量名。
// 用 Local\ 而不是 Global\：每个登录会话一个实例即可，也不需要额外权限。
const mutexName = `Local\DevDock.SingleInstance.v1`

func main() {
	// MCP 模式下 stdout 是协议通道。这里先把服务端的 stdout 摘干净，
	// 任何意外的打印都改道 stderr，避免破坏 JSON-RPC 握手。
	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		runMCP()
		return
	}

	cli.Version = version
	httpapi.Version = version

	// 把自己注册给 CLI，让 `devdock serve` 复用同一套启动逻辑。
	cli.RegisterServe(func(args []string, stdout, stderr io.Writer) int {
		return runServe(args, stdout, stderr)
	})

	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}

// runMCP 启动 stdio MCP 服务器。
func runMCP() {
	mcpserver.ServerVersion = version
	// 保证 stdout 只被协议写入器使用。
	srv := mcpserver.New(os.Stdin, os.Stdout, os.Stderr)
	if err := srv.Serve(); err != nil {
		fmt.Fprintf(os.Stderr, "[mcp] 服务器退出: %v\n", err)
		os.Exit(1)
	}
	os.Exit(0)
}

// serveOptions 是 serve 子命令的参数。
type serveOptions struct {
	port     int
	noOpen   bool
	quiet    bool
}

// runServe 是 serve 子命令的实现，也承担"无参数启动"的默认行为。
func runServe(args []string, stdout, stderr io.Writer) int {
	opts := serveOptions{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--port", "-p":
			if i+1 < len(args) {
				if n, err := strconv.Atoi(args[i+1]); err == nil {
					opts.port = n
				}
				i++
			}
		case "--no-open":
			opts.noOpen = true
		case "--quiet":
			opts.quiet = true
		case "--help", "-h":
			fmt.Fprint(stdout, "用法：devdock serve [--port N] [--no-open] [--quiet]\n")
			return cli.ExitOK
		}
	}

	// --- 单实例 ---
	mutex, first, err := winproc.AcquireMutex(mutexName)
	if err != nil {
		fmt.Fprintf(stderr, "单实例检查失败：%v\n", err)
		return cli.ExitError
	}
	if !first {
		return handleSecondInstance(opts, stdout, stderr)
	}
	defer mutex.Close()

	return serve(opts, stdout, stderr)
}

// handleSecondInstance 在已有实例运行时打开其界面，而不是静默退出。
func handleSecondInstance(opts serveOptions, stdout, stderr io.Writer) int {
	rt, err := config.ReadRuntime()
	if err != nil || rt == nil {
		fmt.Fprintln(stderr, "DevDock 已在运行，但读不到它的运行信息（runtime.json 缺失）")
		return cli.ExitConflict
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/?t=%s", rt.Port, rt.Token)

	fmt.Fprintf(stdout, "DevDock 已在运行（端口 %d），已为你打开界面。\n", rt.Port)
	if !opts.noOpen {
		if err := client.OpenInBrowser(url); err != nil {
			fmt.Fprintf(stderr, "打开浏览器失败：%v\n界面地址：%s\n", err, url)
		}
	} else {
		fmt.Fprintf(stdout, "界面地址：%s\n", url)
	}
	return cli.ExitOK
}

// serve 真正启动服务。
func serve(opts serveOptions, stdout, stderr io.Writer) int {
	// 配置文件损坏不阻止启动，只提示。
	cfgStore, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "加载配置失败：%v\n", err)
		return cli.ExitError
	}
	if w := cfgStore.Warning(); w != "" {
		fmt.Fprintf(stderr, "注意：%s\n", w)
	}

	st, err := store.New(cfgStore)
	if err != nil {
		fmt.Fprintf(stderr, "初始化项目仓库失败：%v\n", err)
		return cli.ExitError
	}

	// 仅监听回环地址。LoopbackListener 内部断言地址是回环，否则拒绝启动。
	port := opts.port
	if port == 0 {
		snap := cfgStore.Snapshot()
		port = snap.Server.PreferredPort
	}
	ln, err := runner.LoopbackListener(port)
	if err != nil {
		// 首选端口被占时回退到系统分配端口。
		ln, err = runner.LoopbackListener(0)
		if err != nil {
			fmt.Fprintf(stderr, "无法监听本机端口：%v\n", err)
			return cli.ExitError
		}
	}
	actualPort := ln.Addr().(*net.TCPAddr).Port

	token, err := httpapi.NewToken()
	if err != nil {
		fmt.Fprintf(stderr, "生成访问令牌失败：%v\n", err)
		return cli.ExitError
	}

	rn := runner.New(runner.DefaultTimings())
	api := httpapi.New(st, rn, token, actualPort, cfgStore.Warning())
	rn.SetPublisher(api)

	handler := api.Handler(web.Handler())

	srv := &http.Server{
		Handler:      handler,
		ReadTimeout:  0, // SSE 长连接不能有读超时
		WriteTimeout: 0, // 同上，靠心跳检测死连接
		IdleTimeout:  120 * time.Second,
	}

	// 记录自身身份，供清理陈旧 runtime.json 时校验。
	selfIdentity, idErr := winproc.CaptureIdentity(uint32(os.Getpid()))
	rt := config.Runtime{
		Port:      actualPort,
		Token:     token,
		PID:       uint32(os.Getpid()),
		Version:   version,
		StartedAt: time.Now().Format(time.RFC3339),
	}
	if idErr == nil {
		rt.CreationTime = selfIdentity.CreationTime
	}
	if err := config.WriteRuntime(rt); err != nil {
		fmt.Fprintf(stderr, "写入运行信息失败：%v\n", err)
		// 不致命：只是 CLI/MCP 无法自动发现实例。
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/?t=%s", actualPort, token)

	if !opts.quiet {
		fmt.Fprintf(stdout, "DevDock %s 已启动\n", version)
		fmt.Fprintf(stdout, "界面地址：%s\n", url)
		fmt.Fprintf(stdout, "配置文件：%s\n", cfgStore.Path())
		fmt.Fprintln(stdout, "按 Ctrl+C 退出（会先停止所有项目）")
	}

	// 自动启动标记为 autostart 的项目。
	for _, p := range st.List() {
		if p.Autostart {
			go func(project model.Project) {
				if _, err := rn.Start(context.Background(), project); err != nil {
					fmt.Fprintf(stderr, "自动启动 %s 失败：%v\n", project.ID, err)
				}
			}(p)
		}
	}

	if !opts.noOpen {
		go func() {
			// 等监听器就绪后再开浏览器，避免白屏。
			time.Sleep(120 * time.Millisecond)
			if err := client.OpenInBrowser(url); err != nil {
				fmt.Fprintf(stderr, "打开浏览器失败：%v\n请手动访问：%s\n", err, url)
			}
		}()
	}

	errCh := make(chan error, 1)
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	// 等待退出信号。
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	select {
	case <-sigCh:
		if !opts.quiet {
			fmt.Fprintln(stdout, "正在停止所有项目…")
		}
	case err := <-errCh:
		fmt.Fprintf(stderr, "服务异常退出：%v\n", err)
		rn.Shutdown(context.Background())
		config.RemoveRuntime()
		return cli.ExitError
	}

	// 优雅退出：先停项目再关服务，最后删 runtime.json。
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		rn.Shutdown(shutdownCtx)
	}()

	_ = srv.Shutdown(shutdownCtx)
	wg.Wait()

	config.RemoveRuntime()
	if !opts.quiet {
		fmt.Fprintln(stdout, "已退出")
	}
	return cli.ExitOK
}

var _ = strings.TrimSpace
