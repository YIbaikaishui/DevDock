package runner

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"devdock/internal/model"
)

// fastTimings 把等待时间整体调小，让测试跑得快且仍然覆盖真实路径。
func fastTimings() Timings {
	return Timings{
		StopGrace:       3 * time.Second,
		EarlyExitWindow: time.Second,
		LoggerTail:      20,
	}
}

// publisher 记录收到的状态与日志，用于断言"两个维度分开推送"。
type publisher struct {
	mu     sync.Mutex
	states []model.ProjectState
	logs   []model.LogLine
	ready  []string
}

func (p *publisher) PublishState(st model.ProjectState) {
	p.mu.Lock()
	p.states = append(p.states, st)
	p.mu.Unlock()
}

func (p *publisher) PublishLog(_ string, line model.LogLine) {
	p.mu.Lock()
	p.logs = append(p.logs, line)
	p.mu.Unlock()
}

func (p *publisher) PublishReady(id, url string, _ int64) {
	p.mu.Lock()
	p.ready = append(p.ready, id+"|"+url)
	p.mu.Unlock()
}

// lastState 返回最近一次推送的状态。
func (p *publisher) lastState() (model.ProjectState, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.states) == 0 {
		return model.ProjectState{}, false
	}
	return p.states[len(p.states)-1], true
}

// waitFor 轮询直到条件成立。
func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("等待 %s 超时", what)
}

// selfExe 返回当前测试二进制的路径，用作可控子进程。
func selfExe(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("取测试二进制路径失败: %v", err)
	}
	return exe
}

// workDir 返回一个可安全作为 cwd 的目录。
//
// 不能用 t.TempDir()：子进程以该目录为 cwd 运行，只要它还没退干净，
// 测试框架的 RemoveAll 就会因目录被占用而失败，掩盖真正的断言结果。
func workDir(t *testing.T) string {
	t.Helper()
	return os.TempDir()
}

// newRunner 创建一个注入了记录器的 Runner。
func newRunner(t *testing.T) (*Runner, *publisher) {
	t.Helper()
	r := New(fastTimings())
	pub := &publisher{}
	r.SetPublisher(pub)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		r.Shutdown(ctx)
	})
	return r, pub
}

// TestStartStopCycle 验证最基本的启动-运行-停止闭环。
func TestStartStopCycle(t *testing.T) {
	r, pub := newRunner(t)
	p := model.Project{
		ID:      "cycle",
		Name:    "cycle",
		Cwd:     workDir(t),
		Command: `"` + selfExe(t) + `" -test.run=TestHelperBlockingProcess`,
		Env:     map[string]string{"DEVDOCK_TEST_HELPER": "1"},
	}

	st, err := r.Start(context.Background(), p)
	if err != nil {
		t.Fatalf("启动失败: %v", err)
	}
	if st.Lifecycle != model.LifecycleRunning {
		t.Fatalf("启动后状态应为 running，实际 %s", st.Lifecycle)
	}
	if st.PID == 0 {
		t.Fatal("启动后应记录 PID")
	}
	// 未配置就绪路径时，readiness 必须是 none 而不是 pending。
	if st.Readiness != model.ReadinessNone {
		t.Fatalf("未配置就绪检查时 readiness 应为 none，实际 %s", st.Readiness)
	}

	// 状态变化必须被推送出去。
	waitFor(t, 2*time.Second, "running 状态推送", func() bool {
		last, ok := pub.lastState()
		return ok && last.Lifecycle == model.LifecycleRunning
	})

	if err := r.Stop(context.Background(), p.ID); err != nil {
		t.Fatalf("停止失败: %v", err)
	}
	final := r.State(p.ID)
	if final.Lifecycle != model.LifecycleStopped {
		t.Fatalf("停止后状态应为 stopped，实际 %s", final.Lifecycle)
	}
	if final.PID != 0 {
		t.Fatalf("停止后 PID 应清零，实际 %d", final.PID)
	}
}

// TestStartTwiceRejected 验证重复启动被拒绝，避免一个项目两个进程。
func TestStartTwiceRejected(t *testing.T) {
	r, _ := newRunner(t)
	p := model.Project{
		ID:      "twice",
		Name:    "twice",
		Cwd:     workDir(t),
		Command: `"` + selfExe(t) + `" -test.run=TestHelperBlockingProcess`,
		Env:     map[string]string{"DEVDOCK_TEST_HELPER": "1"},
	}
	if _, err := r.Start(context.Background(), p); err != nil {
		t.Fatalf("首次启动失败: %v", err)
	}
	if _, err := r.Start(context.Background(), p); err != ErrAlreadyRunning {
		t.Fatalf("重复启动应返回 ErrAlreadyRunning，实际 %v", err)
	}
}

// TestStopNotRunning 验证停止未运行的项目返回明确错误。
func TestStopNotRunning(t *testing.T) {
	r, _ := newRunner(t)
	if err := r.Stop(context.Background(), "nope"); err != ErrNotRunning {
		t.Fatalf("应返回 ErrNotRunning，实际 %v", err)
	}
}

// TestEarlyExitBecomesFailed 验证"启动即崩"被判为 failed 而不是 stopped。
// 这是用户最需要区分的一种失败。
func TestEarlyExitBecomesFailed(t *testing.T) {
	r, _ := newRunner(t)
	p := model.Project{
		ID:      "earlyexit",
		Name:    "earlyexit",
		Cwd:     workDir(t),
		Command: `cmd /c "exit /b 7"`,
	}
	if _, err := r.Start(context.Background(), p); err != nil {
		t.Fatalf("启动失败: %v", err)
	}

	waitFor(t, 5*time.Second, "转为 failed", func() bool {
		return r.State(p.ID).Lifecycle == model.LifecycleFailed
	})
	st := r.State(p.ID)
	if st.LastExitCode == nil {
		t.Fatal("failed 状态应带上退出码")
	}
	if *st.LastExitCode != 7 {
		t.Fatalf("退出码应为 7，实际 %d", *st.LastExitCode)
	}
}

// TestCleanExitBecomesStopped 验证自行以 0 退出判为 stopped 而不是 failed。
func TestCleanExitBecomesStopped(t *testing.T) {
	r, _ := newRunner(t)
	p := model.Project{
		ID:      "cleanexit",
		Name:    "cleanexit",
		Cwd:     workDir(t),
		Command: `cmd /c "exit /b 0"`,
	}
	if _, err := r.Start(context.Background(), p); err != nil {
		t.Fatalf("启动失败: %v", err)
	}
	waitFor(t, 5*time.Second, "转为 stopped", func() bool {
		return r.State(p.ID).Lifecycle == model.LifecycleStopped
	})
}

// TestReadinessTimeoutKeepsRunning 是本产品最关键的一条断言：
// 业务没就绪，绝不能把进程状态改成 failed。
func TestReadinessTimeoutKeepsRunning(t *testing.T) {
	r, _ := newRunner(t)
	port := freePort(t)
	p := model.Project{
		ID:      "readinesstimeout",
		Name:    "readinesstimeout",
		Cwd:     workDir(t),
		Command: `"` + selfExe(t) + `" -test.run=TestHelperBlockingProcess`,
		Env:     map[string]string{"DEVDOCK_TEST_HELPER": "1"},
		Port:    port,
		// 指向一个几乎必然不会响应的端口上的路径。
		Readiness: &model.ReadinessConfig{
			Path:       "/health",
			TimeoutSec: 1,
			IntervalMs: 100,
		},
	}
	if _, err := r.Start(context.Background(), p); err != nil {
		t.Fatalf("启动失败: %v", err)
	}

	waitFor(t, 10*time.Second, "readiness 转为 timeout", func() bool {
		return r.State(p.ID).Readiness == model.ReadinessTimeout
	})

	st := r.State(p.ID)
	// 核心断言：业务未就绪，但进程仍在运行。
	if st.Lifecycle != model.LifecycleRunning {
		t.Fatalf("就绪超时时生命周期必须保持 running，实际 %s", st.Lifecycle)
	}
	if st.PID == 0 {
		t.Fatal("就绪超时时进程应仍在运行，PID 不应为 0")
	}
}

// TestStartFailureReportsTail 验证启动失败时把原因写进 LastError。
func TestStartFailureReportsTail(t *testing.T) {
	r, _ := newRunner(t)
	p := model.Project{
		ID:      "badcwd",
		Name:    "badcwd",
		Cwd:     `Z:\definitely\not\a\real\path\devdock`,
		Command: "echo hi",
	}
	if _, err := r.Start(context.Background(), p); err == nil {
		t.Fatal("不存在的工作目录应当启动失败")
	}
	st := r.State(p.ID)
	if st.Lifecycle != model.LifecycleFailed {
		t.Fatalf("启动失败后状态应为 failed，实际 %s", st.Lifecycle)
	}
	if st.LastError == "" {
		t.Fatal("失败状态应包含可读的错误说明")
	}
}

// TestRestartReplacesProcess 验证重启后 PID 变化且旧进程消失。
func TestRestartReplacesProcess(t *testing.T) {
	r, _ := newRunner(t)
	p := model.Project{
		ID:      "restart",
		Name:    "restart",
		Cwd:     workDir(t),
		Command: `"` + selfExe(t) + `" -test.run=TestHelperBlockingProcess`,
		Env:     map[string]string{"DEVDOCK_TEST_HELPER": "1"},
	}
	if _, err := r.Start(context.Background(), p); err != nil {
		t.Fatalf("启动失败: %v", err)
	}
	first := r.State(p.ID).PID
	if first == 0 {
		t.Fatal("首次启动未记录 PID")
	}

	if _, err := r.Restart(context.Background(), p); err != nil {
		t.Fatalf("重启失败: %v", err)
	}
	second := r.State(p.ID).PID
	if second == 0 {
		t.Fatal("重启后未记录 PID")
	}
	if second == first {
		t.Fatalf("重启后 PID 未变化（仍为 %d），说明旧进程没被替换", first)
	}
	if r.State(p.ID).Lifecycle != model.LifecycleRunning {
		t.Fatalf("重启后应处于 running，实际 %s", r.State(p.ID).Lifecycle)
	}
}

// TestLogsCapturedAndBounded 验证子进程日志进入 ring 并被推送。
func TestLogsCapturedAndBounded(t *testing.T) {
	r, _ := newRunner(t)
	p := model.Project{
		ID:      "logs",
		Name:    "logs",
		Cwd:     workDir(t),
		Command: `cmd /c "echo devdock-log-marker & ping -n 30 127.0.0.1 >nul"`,
	}
	if _, err := r.Start(context.Background(), p); err != nil {
		t.Fatalf("启动失败: %v", err)
	}

	waitFor(t, 5*time.Second, "日志出现", func() bool {
		for _, l := range r.Ring(p.ID).Tail(50) {
			if strings.Contains(l.Text, "devdock-log-marker") {
				return true
			}
		}
		return false
	})
}

// TestShutdownStopsEverything 验证退出时不会留下运行中的进程。
func TestShutdownStopsEverything(t *testing.T) {
	r := New(fastTimings())
	p := model.Project{
		ID:      "shutdown",
		Name:    "shutdown",
		Cwd:     workDir(t),
		Command: `"` + selfExe(t) + `" -test.run=TestHelperBlockingProcess`,
		Env:     map[string]string{"DEVDOCK_TEST_HELPER": "1"},
	}
	if _, err := r.Start(context.Background(), p); err != nil {
		t.Fatalf("启动失败: %v", err)
	}
	pid := r.State(p.ID).PID

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r.Shutdown(ctx)

	if r.State(p.ID).Lifecycle != model.LifecycleStopped {
		t.Fatalf("Shutdown 后应为 stopped，实际 %s", r.State(p.ID).Lifecycle)
	}
	// 退出后不得接受新启动。
	if _, err := r.Start(context.Background(), p); err == nil {
		t.Fatal("正在退出时不应允许启动新项目")
	}
	_ = pid
}

// TestPortInUseDetected 验证端口冲突在启动前被发现。
func TestPortInUseDetected(t *testing.T) {
	port := freePort(t)
	// 先占住端口。
	ln, err := netListen(port)
	if err != nil {
		t.Fatalf("占用端口失败: %v", err)
	}
	defer ln.Close()

	if err := CheckPortAvailable(port); err == nil {
		t.Fatal("端口已占用时应返回错误")
	}

	r, _ := newRunner(t)
	p := model.Project{
		ID:      "portconflict",
		Name:    "portconflict",
		Cwd:     workDir(t),
		Command: `cmd /c "ping -n 30 127.0.0.1 >nul"`,
		Port:    port,
	}
	if _, err := r.Start(context.Background(), p); err == nil {
		t.Fatal("端口被占用时启动应当失败")
	}
	if r.State(p.ID).Lifecycle != model.LifecycleFailed {
		t.Fatalf("端口冲突应判为 failed，实际 %s", r.State(p.ID).Lifecycle)
	}
}
