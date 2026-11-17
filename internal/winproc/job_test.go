package winproc

import (
	"os"
	"strings"
	"testing"
	"time"
)

// waitGone 轮询等待令牌描述的进程消失，返回是否在期限内消失。
func waitGone(id Identity, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !IsAlive(id) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return !IsAlive(id)
}

// TestJobTerminateKillsTree 是整棵进程树清理的核心测试。
//
// 构造三层：cmd.exe -> 中间 cmd -> 长驻子进程。
// 关闭 job 后三层必须全部消失，这是"不留孤儿进程"的唯一可信证据。
func TestJobTerminateKillsTree(t *testing.T) {
	job, err := NewJob()
	if err != nil {
		t.Fatalf("创建 job 失败: %v", err)
	}
	defer job.Close()

	// 中间层再派生一个长驻进程，形成真正的子孙链。
	res, err := Spawn(job, SpawnOptions{
		CommandLine: `cmd /c "ping -n 60 127.0.0.1 >nul & ping -n 60 127.0.0.1 >nul"`,
	})
	if err != nil {
		t.Fatalf("Spawn 失败: %v", err)
	}

	if !res.Identity.Valid() {
		t.Fatalf("身份令牌无效: %+v", res.Identity)
	}
	if !IsAlive(res.Identity) {
		t.Fatal("刚启动的进程应当存活")
	}

	// 收集当前全部后代，稍后逐一确认消失。
	time.Sleep(300 * time.Millisecond)
	descendants := Descendants(res.Identity.PID)

	// 关闭 job：KILL_ON_JOB_CLOSE 由内核保证整树终止。
	job.Close()

	if !waitGone(res.Identity, 5*time.Second) {
		t.Fatalf("根进程 %d 在关闭 job 后仍存活", res.Identity.PID)
	}

	// 逐个确认后代也已消失。
	for _, pid := range descendants {
		id, err := CaptureIdentity(pid)
		if err != nil {
			continue // 已经不存在，正是期望结果
		}
		if waitGone(id, 3*time.Second) {
			continue
		}
		t.Errorf("后代进程 %d 在关闭 job 后仍存活 —— 存在孤儿进程", pid)
	}

	// 关闭后 job 内活跃进程数必须为 0。
	// 注意 job 已 Close，句柄无效，ActiveProcesses 返回错误属正常。
	if n, err := job.ActiveProcesses(); err == nil && n != 0 {
		t.Fatalf("job 内仍有 %d 个活跃进程", n)
	}

	res.Output.Close()
}

// TestSpawnInJob 验证进程一出生就在 job 内，不存在逃逸窗口。
func TestSpawnInJob(t *testing.T) {
	job, err := NewJob()
	if err != nil {
		t.Fatalf("创建 job 失败: %v", err)
	}
	defer job.Close()

	res, err := Spawn(job, SpawnOptions{
		CommandLine: `cmd /c "ping -n 30 127.0.0.1 >nul"`,
	})
	if err != nil {
		t.Fatalf("Spawn 失败: %v", err)
	}
	defer res.Output.Close()

	// 进程此刻应当已经在 job 内。
	time.Sleep(200 * time.Millisecond)
	n, err := job.ActiveProcesses()
	if err != nil {
		t.Fatalf("读取 job 活跃进程数失败: %v", err)
	}
	if n < 1 {
		t.Fatalf("job 内活跃进程数为 %d，说明进程未进入 job", n)
	}
}

// TestSpawnCapturesOutput 验证 stdout 被正确捕获（日志功能的前提）。
func TestSpawnCapturesOutput(t *testing.T) {
	job, err := NewJob()
	if err != nil {
		t.Fatalf("创建 job 失败: %v", err)
	}
	defer job.Close()

	res, err := Spawn(job, SpawnOptions{
		CommandLine: `cmd /c "echo devdock-hello"`,
	})
	if err != nil {
		t.Fatalf("Spawn 失败: %v", err)
	}
	defer res.Output.Close()

	var sb strings.Builder
	buf := make([]byte, 4096)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		n, err := res.Output.Read(buf)
		if n > 0 {
			sb.Write(buf[:n])
		}
		if err != nil {
			break
		}
		if strings.Contains(sb.String(), "devdock-hello") {
			break
		}
	}
	if !strings.Contains(sb.String(), "devdock-hello") {
		t.Fatalf("未捕获到子进程输出，实际收到: %q", sb.String())
	}
}

// TestSpawnRejectsEmptyCommand 验证空命令被拒绝而不是启动一个空 shell。
func TestSpawnRejectsEmptyCommand(t *testing.T) {
	job, err := NewJob()
	if err != nil {
		t.Fatalf("创建 job 失败: %v", err)
	}
	defer job.Close()

	if _, err := Spawn(job, SpawnOptions{CommandLine: ""}); err == nil {
		t.Fatal("空命令应当被拒绝")
	}
}

// TestSpawnRejectsNilJob 验证没有 job 时拒绝启动。
// 这是安全底线：不能启动一个无法保证清理的进程。
func TestSpawnRejectsNilJob(t *testing.T) {
	if _, err := Spawn(nil, SpawnOptions{CommandLine: "echo hi"}); err == nil {
		t.Fatal("job 为 nil 时应当拒绝启动")
	}
}

// TestNestedJob 验证宿主进程已在 job 内时仍能建内层 job 并只杀自己子树。
// 真实场景：DevDock 可能被 IDE 或 CI 的 job 包着运行。
func TestNestedJob(t *testing.T) {
	outer, err := NewJob()
	if err != nil {
		t.Fatalf("创建外层 job 失败: %v", err)
	}
	defer outer.Close()

	inner, err := NewJob()
	if err != nil {
		t.Fatalf("创建内层 job 失败: %v", err)
	}

	res, err := Spawn(inner, SpawnOptions{CommandLine: `cmd /c "ping -n 60 127.0.0.1 >nul"`})
	if err != nil {
		inner.Close()
		t.Fatalf("在内层 job 中 Spawn 失败: %v", err)
	}
	defer res.Output.Close()

	if !IsAlive(res.Identity) {
		inner.Close()
		t.Fatal("内层 job 中的进程应当存活")
	}

	// 只关内层：自己必须活着，子进程必须消失。
	inner.Close()
	if !waitGone(res.Identity, 5*time.Second) {
		t.Fatal("关闭内层 job 后子进程仍存活")
	}
	if !IsAlive(mustSelf(t)) {
		t.Fatal("关闭内层 job 误杀了测试进程自身")
	}
}

// mustSelf 返回测试进程自身的身份令牌。
func mustSelf(t *testing.T) Identity {
	t.Helper()
	id, err := CaptureIdentity(uint32(os.Getpid()))
	if err != nil {
		t.Fatalf("抓取自身身份失败: %v", err)
	}
	return id
}
