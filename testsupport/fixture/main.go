// Command fixture 是 DevDock 的验收夹具：一个行为完全可控的小服务。
//
// 它只用于测试，不参与产品分发。各开关让验收脚本能精确构造以下场景：
//   - 业务就绪延迟（进程已起但 /health 先返回 503）
//   - 永远不就绪（用于验证"进程运行中 · 就绪超时"）
//   - 启动即崩（非零退出码）
//   - 派生出脱离父进程的孙进程（用于验证整棵进程树被清理）
//
// 输出约定：所有自身信息都打到 stdout，前缀 FIXTURE_，便于脚本抓取。
package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

func main() {
	var (
		port          = flag.Int("port", 0, "监听端口，0 表示由系统分配")
		healthOKAfter = flag.Int("health-ok-after", 0, "启动后多少毫秒 /health 才开始返回 200")
		healthNeverOK = flag.Bool("health-never-ok", false, "/health 永远返回 503")
		logLines      = flag.Int("logs", 0, "启动后打印多少行日志")
		logIntervalMs = flag.Int("log-interval", 20, "日志行间隔毫秒")
		grandchild    = flag.Bool("grandchild", false, "派生一个长期存活的孙进程")
		child         = flag.Bool("child", false, "以孙进程身份运行（内部使用）")
		exitCode      = flag.Int("exit-code", 0, "立即以该退出码退出")
		holdMs        = flag.Int("hold-ms", 0, "退出前停留毫秒，用于构造启动即崩")
		readyBody     = flag.String("ready-body", "ok", "/health 的响应体")
	)
	flag.Parse()

	if *child {
		// 孙进程：报告 PID 后长期存活。它由夹具直接创建，
		// 因此与夹具同属一个 Job Object —— 这正是要验证的清理范围。
		fmt.Printf("FIXTURE_GRANDCHILD_PID=%d\n", os.Getpid())
		os.Stdout.Sync()
		waitForever()
		return
	}

	if *exitCode != 0 {
		if *holdMs > 0 {
			time.Sleep(time.Duration(*holdMs) * time.Millisecond)
		}
		fmt.Printf("FIXTURE_EXITING code=%d\n", *exitCode)
		os.Stdout.Sync()
		os.Exit(*exitCode)
	}

	if *grandchild {
		spawnGrandchild()
	}

	if *logLines > 0 {
		go emitLogs(*logLines, *logIntervalMs)
	}

	startedAt := time.Now()
	readyGate := func() bool {
		if *healthNeverOK {
			return false
		}
		if *healthOKAfter <= 0 {
			return true
		}
		return time.Since(startedAt) >= time.Duration(*healthOKAfter)*time.Millisecond
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if !readyGate() {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, "starting")
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, *readyBody)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "fixture pid=%d path=%s\n", os.Getpid(), r.URL.Path)
	})

	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(*port)))
	if err != nil {
		fmt.Printf("FIXTURE_ERROR listen: %v\n", err)
		os.Exit(1)
	}
	actual := ln.Addr().(*net.TCPAddr).Port
	fmt.Printf("FIXTURE_PID=%d\n", os.Getpid())
	fmt.Printf("FIXTURE_PORT=%d\n", actual)
	os.Stdout.Sync()

	srv := &http.Server{Handler: mux}
	// 收到 SIGTERM 时干净退出，模拟正常停止。
	idle := make(chan struct{})
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM, os.Interrupt)
		<-sig
		srv.Close()
		close(idle)
	}()

	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		fmt.Printf("FIXTURE_ERROR serve: %v\n", err)
		os.Exit(1)
	}
	<-idle
}

// emitLogs 周期性打印日志，用于验证环形缓冲与 SSE。
func emitLogs(n, intervalMs int) {
	if intervalMs <= 0 {
		intervalMs = 20
	}
	for i := 1; i <= n; i++ {
		fmt.Printf("fixture log line %d\n", i)
		os.Stdout.Sync()
		time.Sleep(time.Duration(intervalMs) * time.Millisecond)
	}
}

// spawnGrandchild 派生子进程并打印其 PID。
//
// 用 cmd.exe 作为中间层是有意的：真实项目的命令都经过 cmd 执行，
// 这样构造的层级是 cmd -> fixture -> grandchild，更贴近实际。
func spawnGrandchild() {
	self, err := os.Executable()
	if err != nil {
		fmt.Printf("FIXTURE_ERROR executable: %v\n", err)
		return
	}
	cmd := exec.Command(self, "-child")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		fmt.Printf("FIXTURE_ERROR grandchild: %v\n", err)
		return
	}
	fmt.Printf("FIXTURE_SPAWNED_GRANDCHILD=%d\n", cmd.Process.Pid)
	os.Stdout.Sync()
	// 不 Wait：孙进程本就应当比父进程活得久，这样才谈得上"清理"。
}

// waitForever 阻塞直到进程被终止。
func waitForever() {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, os.Interrupt)
	select {
	case <-sig:
	case <-time.After(10 * time.Minute):
	}
}
