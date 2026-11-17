// Package runner 实现项目进程的生命周期状态机。
//
// 状态严格分为两个正交维度：
//   lifecycle ∈ stopped | starting | running | stopping | failed
//   readiness ∈ none | pending | ready | timeout | error
//
// 最重要的一条规则：就绪失败绝不能变成生命周期失败。
// 进程活着但业务没准备好，是用户最需要区分、也最容易实现错的场景。
package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"devdock/internal/logs"
	"devdock/internal/model"
	"devdock/internal/ready"
	"devdock/internal/winproc"
)

// 运行期错误，供 HTTP 层映射错误码。
var (
	ErrAlreadyRunning = errors.New("项目已在运行")
	ErrNotRunning     = errors.New("项目未在运行")
	ErrPortInUse      = errors.New("端口已被占用")
)

// Timings 汇总所有与时间相关的常量，便于测试时整体调小。
type Timings struct {
	// StopGrace 是 TerminateJobObject 之后等待进程消失的时长。
	StopGrace time.Duration
	// EarlyExitWindow 是"启动即崩"的判定窗口。
	EarlyExitWindow time.Duration
	// LoggerTail 是启动失败时附带的尾部日志行数。
	LoggerTail int
}

// DefaultTimings 返回生产用默认值。
func DefaultTimings() Timings {
	return Timings{
		StopGrace:       5 * time.Second,
		EarlyExitWindow: time.Second,
		LoggerTail:      20,
	}
}

// EventPublisher 把状态变化与日志推给 SSE 订阅者。
// 由 httpapi 通过 SetPublisher 注入，避免 runner 反向依赖 http 层类型。
type EventPublisher interface {
	PublishState(state model.ProjectState)
	PublishLog(projectID string, line model.LogLine)
	PublishReady(projectID, url string, latencyMs int64)
}

// auditSink 接收审计事件。
type auditSink func(ev model.AuditEvent)

// instance 是一个项目的运行期句柄。
type instance struct {
	mu    sync.Mutex
	state model.ProjectState

	job    *winproc.Job
	id     winproc.Identity
	output *winproc.OutputPipe
	// spawn 保留子进程句柄，用于读到真实退出码。停止时必须关闭。
	spawn winproc.SpawnResult

	// cancel 用于取消该项目的就绪探测与早退检测。
	cancelProbes context.CancelFunc

	// stopOnce 保证并发 stop 只有一个真正执行。
	stopping bool
}

// Runner 管理所有项目的进程生命周期。
type Runner struct {
	mu         sync.Mutex
	instances  map[string]*instance
	rings      map[string]*logs.Ring
	hub        *logs.Hub
	publisher  EventPublisher
	timings    Timings
	auditMu    sync.Mutex
	audit      []model.AuditEvent
	auditSink  auditSink
	baseEnv    []string
	// stoppingAll 在进程退出时置位，阻止自动重启类行为。
	shuttingDown bool
}

// auditLimit 是内存审计环的容量。
const auditLimit = 200

// New 创建 Runner。
func New(t Timings) *Runner {
	r := &Runner{
		instances: make(map[string]*instance),
		rings:     make(map[string]*logs.Ring),
		hub:       logs.NewHub(),
		timings:   t,
		baseEnv:   os.Environ(),
	}
	r.auditSink = func(ev model.AuditEvent) { r.recordAudit(ev) }
	return r
}

// SetPublisher 注入事件发布器。必须在开始服务前调用。
func (r *Runner) SetPublisher(pub EventPublisher) { r.publisher = pub }

// Hub 返回日志广播中心。
func (r *Runner) Hub() *logs.Hub { return r.hub }

// State 返回项目当前运行态。
func (r *Runner) State(id string) model.ProjectState {
	inst := r.instance(id)
	if inst == nil {
		return model.ProjectState{ID: id, Lifecycle: model.LifecycleStopped, Readiness: model.ReadinessNone}
	}
	inst.mu.Lock()
	defer inst.mu.Unlock()
	return inst.state
}

// Ring 返回项目的日志环。不存在时按需创建，保证 UI 首次拉取不抛错。
func (r *Runner) Ring(id string) *logs.Ring {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ringLocked(id)
}

func (r *Runner) ringLocked(id string) *logs.Ring {
	ring, ok := r.rings[id]
	if !ok {
		ring = logs.NewRing(logs.DefaultCapacity)
		r.rings[id] = ring
	}
	return ring
}

func (r *Runner) instance(id string) *instance {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.instances[id]
}

// ensureInstance 取得或创建项目运行态。
func (r *Runner) ensureInstance(id string) *instance {
	r.mu.Lock()
	defer r.mu.Unlock()
	inst, ok := r.instances[id]
	if !ok {
		inst = &instance{state: model.ProjectState{
			ID:        id,
			Lifecycle: model.LifecycleStopped,
			Readiness: model.ReadinessNone,
		}}
		r.instances[id] = inst
	}
	return inst
}

// Audit 返回最近的审计事件（新的在前）。
func (r *Runner) Audit() []model.AuditEvent {
	r.auditMu.Lock()
	defer r.auditMu.Unlock()
	out := make([]model.AuditEvent, len(r.audit))
	for i, ev := range r.audit {
		out[len(r.audit)-1-i] = ev
	}
	return out
}

func (r *Runner) recordAudit(ev model.AuditEvent) {
	if ev.At == "" {
		ev.At = time.Now().Format(time.RFC3339Nano)
	}
	r.auditMu.Lock()
	r.audit = append(r.audit, ev)
	if len(r.audit) > auditLimit {
		r.audit = r.audit[len(r.audit)-auditLimit:]
	}
	r.auditMu.Unlock()
}

func (r *Runner) auditEvent(kind, projectID string, pid uint32, note string) {
	r.auditSink(model.AuditEvent{
		At:    time.Now().Format(time.RFC3339Nano),
		Kind:  kind,
		Actor: projectID,
		PID:   pid,
		Note:  note,
	})
	// 同时打到 stderr，方便在控制台观察。绝不写 stdout —— MCP 模式下那是协议通道。
	fmt.Fprintf(os.Stderr, "[audit] %s project=%s pid=%d %s\n", kind, projectID, pid, note)
}

// setState 更新状态并广播。调用方必须持有 inst.mu。
func (r *Runner) setState(inst *instance, mutate func(*model.ProjectState)) {
	mutate(&inst.state)
	snapshot := inst.state
	if r.publisher != nil {
		// 异步广播：状态推送绝不能拖慢进程管理路径。
		go r.publisher.PublishState(snapshot)
	}
}

// CheckPortAvailable 在启动前试绑端口，避免"启动了才发现端口被占"。
// 返回 ErrPortInUse 表示已被占用。
func CheckPortAvailable(port int) error {
	if port <= 0 {
		return nil
	}
	ln, err := netListen(port)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrPortInUse, err)
	}
	ln.Close()
	return nil
}

// buildEnv 构造子进程环境：基础环境 + 项目 env + DevDock 自有变量。
func (r *Runner) buildEnv(p model.Project) []string {
	env := make([]string, 0, len(r.baseEnv)+len(p.Env)+4)
	env = append(env, r.baseEnv...)
	for k, v := range p.Env {
		env = append(env, k+"="+v)
	}
	env = append(env,
		"DEVDOCK=1",
		"DEVDOCK_PROJECT="+p.ID,
	)
	if p.Port > 0 {
		env = append(env, "DEVDOCK_PORT="+strconv.Itoa(p.Port))
	}
	return env
}

// Start 启动项目。非阻塞地完成 spawn，就绪探测在后台进行。
func (r *Runner) Start(ctx context.Context, p model.Project) (model.ProjectState, error) {
	inst := r.ensureInstance(p.ID)

	r.mu.Lock()
	shuttingDown := r.shuttingDown
	r.mu.Unlock()
	if shuttingDown {
		return inst.state, fmt.Errorf("DevDock 正在退出，拒绝启动新项目")
	}

	inst.mu.Lock()
	if inst.state.Lifecycle == model.LifecycleRunning || inst.state.Lifecycle == model.LifecycleStarting {
		inst.mu.Unlock()
		return inst.state, ErrAlreadyRunning
	}
	// 状态迁移：stopped/failed -> starting
	r.setState(inst, func(s *model.ProjectState) {
		s.Lifecycle = model.LifecycleStarting
		s.Readiness = model.ReadinessNone
		s.LastError = ""
		s.LastExitCode = nil
		s.PID = 0
		s.StartedAt = nil
		s.ReadyAt = nil
		s.ReadyLatencyMs = 0
	})
	inst.mu.Unlock()

	// 端口冲突检测放在 starting 之后，保证 UI 能看到"启动中"再到失败。
	if err := CheckPortAvailable(p.Port); err != nil {
		r.failStart(inst, "端口被占用", err)
		return r.State(p.ID), err
	}

	job, err := winproc.NewJob()
	if err != nil {
		r.failStart(inst, "创建进程 Job 失败", err)
		return r.State(p.ID), fmt.Errorf("创建 Job 失败: %w", err)
	}

	res, err := winproc.Spawn(job, winproc.SpawnOptions{
		CommandLine: p.Command,
		Cwd:         p.Cwd,
		Env:         r.buildEnv(p),
	})
	if err != nil {
		job.Close()
		r.failStart(inst, "进程创建失败", err)
		return r.State(p.ID), fmt.Errorf("启动失败: %w", err)
	}

	inst.mu.Lock()
	inst.job = job
	inst.id = res.Identity
	inst.output = res.Output
	inst.spawn = res
	startedAt := time.Now().UTC()
	r.setState(inst, func(s *model.ProjectState) {
		s.Lifecycle = model.LifecycleRunning
		s.PID = res.Identity.PID
		s.StartedAt = &startedAt
		// 没配就绪路径就是"运行中但未配置检查"，不能算 pending。
		if p.Readiness != nil && p.Readiness.Path != "" {
			s.Readiness = model.ReadinessPending
		} else {
			s.Readiness = model.ReadinessNone
		}
	})
	inst.mu.Unlock()

	r.auditEvent("spawned", p.ID, res.Identity.PID, res.Identity.String())

	// 后台读日志。
	go r.pumpOutput(p.ID, res.Output)

	// 后台监控生命周期（自行退出 / 早退）。
	probeCtx, cancel := context.WithCancel(context.Background())
	inst.mu.Lock()
	inst.cancelProbes = cancel
	inst.mu.Unlock()

	go r.watchExit(probeCtx, p, inst, res.Identity, res, startedAt)
	if p.Readiness != nil && p.Readiness.Path != "" && p.Port > 0 {
		cfg := *p.Readiness
		cfg.Defaults()
		go r.probeReadiness(probeCtx, p, inst, cfg)
	}

	return r.State(p.ID), nil
}

// failStart 把项目置为 failed 并附带尾日志，便于用户知道为什么没起来。
func (r *Runner) failStart(inst *instance, prefix string, cause error) {
	tail := r.tailText(inst.state.ID, r.timings.LoggerTail)
	msg := prefix + ": " + cause.Error()
	if tail != "" {
		msg += "\n--- 尾部日志 ---\n" + tail
	}
	inst.mu.Lock()
	r.setState(inst, func(s *model.ProjectState) {
		s.Lifecycle = model.LifecycleFailed
		s.Readiness = model.ReadinessNone
		s.LastError = msg
		s.PID = 0
	})
	inst.mu.Unlock()
	r.auditEvent("start_failed", inst.state.ID, 0, msg)
}

func (r *Runner) tailText(projectID string, n int) string {
	ring := r.Ring(projectID)
	lines := ring.Tail(n)
	var sb strings.Builder
	for _, l := range lines {
		sb.WriteString(l.Text)
		sb.WriteByte('\n')
	}
	return strings.TrimRight(sb.String(), "\n")
}

// pumpOutput 读子进程输出并写入 ring + 广播。
func (r *Runner) pumpOutput(projectID string, out *winproc.OutputPipe) {
	defer out.Close()
	ring := r.Ring(projectID)

	err := logs.ReadLines(out, func(text string) {
		line := ring.Append("out", text)
		if r.publisher != nil {
			r.publisher.PublishLog(projectID, line)
		}
	})
	if err != nil {
		// 管道读失败只记录，不影响进程状态 —— 进程可能仍在正常运行。
		r.auditEvent("log_read_error", projectID, 0, err.Error())
	}
}

// watchExit 监控进程是否自行退出，并处理"启动即崩"。
func (r *Runner) watchExit(ctx context.Context, p model.Project, inst *instance, id winproc.Identity, spawn winproc.SpawnResult, startedAt time.Time) {
	// 等进程退出。用子进程句柄而不是轮询 OpenProcess：
	// 句柄会一直有效，退出后仍能读到真实退出码。
	// 句柄的所有权归这个 goroutine：它阻塞等待进程结束，读到退出码后立即关闭。
	// 其它路径（尤其是 terminate）绝不能抢先关闭句柄，否则在这里持有的
	// WaitForSingleObject 会作用在已释放的句柄上。
	exitCh := make(chan uint32, 1)
	go func() {
		code, err := spawn.WaitExitCode()
		spawn.CloseProcess()
		if err != nil {
			// 主动停止时 job 被关闭，进程句柄随之失效，读不到退出码。
			code = 0
		}
		exitCh <- code
	}()

	var exitCode int
	select {
	case <-ctx.Done():
		// 主动停止路径：由 terminate 负责收尾，这里直接退出。
		return
	case code := <-exitCh:
		exitCode = int(code)
	}

	// 稍等片刻让管道里的尾部输出被读完，避免失败原因里缺少最后几行。
	select {
	case <-time.After(80 * time.Millisecond):
	case <-ctx.Done():
		return
	}

	inst.mu.Lock()
	// 已经在 stopping/failed 状态说明是我们的主动操作，不要覆盖。
	if inst.state.Lifecycle != model.LifecycleRunning && inst.state.Lifecycle != model.LifecycleStarting {
		inst.mu.Unlock()
		return
	}
	// 启动即崩：在判定窗口内以非零码退出。这是用户最需要看到的失败。
	early := time.Since(startedAt) < r.timings.EarlyExitWindow && exitCode != 0
	r.setState(inst, func(s *model.ProjectState) {
		s.Lifecycle = model.LifecycleStopped
		s.Readiness = model.ReadinessNone
		s.PID = 0
		s.LastExitCode = &exitCode
		if early {
			s.Lifecycle = model.LifecycleFailed
		}
	})
	inst.mu.Unlock()

	// 关掉 job：进程树可能还有残留后代，一并清理。
	inst.mu.Lock()
	if inst.job != nil {
		inst.job.Close()
		inst.job = nil
	}
	inst.mu.Unlock()

	if early {
		r.auditEvent("early_exit", p.ID, id.PID,
			fmt.Sprintf("启动后 %d 毫秒内以退出码 %d 退出", time.Since(startedAt).Milliseconds(), exitCode))
	} else {
		r.auditEvent("exited", p.ID, id.PID, fmt.Sprintf("退出码 %d", exitCode))
	}
}

// probeReadiness 后台探测就绪。
func (r *Runner) probeReadiness(ctx context.Context, p model.Project, inst *instance, cfg model.ReadinessConfig) {
	res := ready.Wait(ctx, ready.Options{
		Port:         p.Port,
		Path:         cfg.Path,
		ExpectStatus: cfg.ExpectStatus,
		Timeout:      time.Duration(cfg.TimeoutSec) * time.Second,
		Interval:     time.Duration(cfg.IntervalMs) * time.Millisecond,
	})

	inst.mu.Lock()
	// 进程已经不在 running 状态时不要改动 readiness。
	if inst.state.Lifecycle != model.LifecycleRunning {
		inst.mu.Unlock()
		return
	}
	switch {
	case res.Ready:
		now := time.Now().UTC()
		latency := res.Latency.Milliseconds()
		r.setState(inst, func(s *model.ProjectState) {
			// 关键：只改 readiness，绝不改 lifecycle。
			s.Readiness = model.ReadinessReady
			s.ReadyAt = &now
			s.ReadyLatencyMs = latency
		})
		inst.mu.Unlock()
		r.auditEvent("ready", p.ID, inst.state.PID, fmt.Sprintf("状态码 %d，耗时 %dms", res.StatusCode, latency))
		if r.publisher != nil {
			r.publisher.PublishReady(p.ID, p.ReadyURL(), latency)
		}
		return
	case res.TimedOut:
		note := "就绪探测超时"
		if res.Err != nil {
			note += ": " + res.Err.Error()
		}
		r.setState(inst, func(s *model.ProjectState) {
			// 同样只改 readiness。进程仍在运行，用户仍可看日志。
			s.Readiness = model.ReadinessTimeout
		})
		inst.mu.Unlock()
		r.auditEvent("ready_timeout", p.ID, inst.state.PID, note)
	default:
		if res.Err != nil {
			r.setState(inst, func(s *model.ProjectState) {
				s.Readiness = model.ReadinessError
			})
			inst.mu.Unlock()
			r.auditEvent("ready_error", p.ID, inst.state.PID, res.Err.Error())
			return
		}
		inst.mu.Unlock()
	}
}

// Stop 停止项目：终止整棵进程树，并确认真的都消失了。
func (r *Runner) Stop(ctx context.Context, projectID string) error {
	inst := r.instance(projectID)
	if inst == nil {
		return ErrNotRunning
	}

	inst.mu.Lock()
	if inst.state.Lifecycle != model.LifecycleRunning && inst.state.Lifecycle != model.LifecycleStarting {
		inst.mu.Unlock()
		return ErrNotRunning
	}
	if inst.stopping {
		inst.mu.Unlock()
		return ErrNotRunning
	}
	inst.stopping = true
	r.setState(inst, func(s *model.ProjectState) {
		s.Lifecycle = model.LifecycleStopping
	})
	job := inst.job
	id := inst.id
	cancel := inst.cancelProbes
	inst.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	return r.terminate(inst, projectID, job, id, true)
}

// terminate 是停止的实际执行体。forceJobCloseFirst 用于兜底路径。
func (r *Runner) terminate(inst *instance, projectID string, job *winproc.Job, id winproc.Identity, graceful bool) error {
	if job == nil {
		inst.mu.Lock()
		r.setState(inst, func(s *model.ProjectState) {
			s.Lifecycle = model.LifecycleStopped
			s.Readiness = model.ReadinessNone
			s.PID = 0
		})
		inst.stopping = false
		inst.mu.Unlock()
		return nil
	}

	// 身份校验：确认要杀的确实是当初那个进程。
	// 若 PID 已被系统复用，身份不符，此时只关 job（job 内的进程由内核按归属清理，
	// 与 PID 复用无关），绝不对该 PID 发任何定向终止。
	identityMatch := winproc.VerifyIdentity(id)
	if !identityMatch && id.Valid() {
		r.auditEvent("identity_mismatch", projectID, id.PID,
			"PID 创建时间与记录不符，跳过定向终止，仅按 Job 归属清理")
	}

	var terr error
	if graceful {
		if err := job.Terminate(); err != nil {
			terr = err
			r.auditEvent("terminate_error", projectID, id.PID, err.Error())
		}
	}

	// 等待 job 内进程全部消失。
	deadline := time.Now().Add(r.timings.StopGrace)
	for time.Now().Before(deadline) {
		n, err := job.ActiveProcesses()
		if err != nil || n == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// 兜底：关闭 job 句柄，KILL_ON_JOB_CLOSE 会终止全部残留。
	if n, err := job.ActiveProcesses(); err == nil && n > 0 {
		r.auditEvent("force_kill", projectID, id.PID,
			fmt.Sprintf("TerminateJobObject 后仍有 %d 个进程，关闭 Job 兜底", n))
	}
	job.Close()

	// 最终确认。
	for i := 0; i < 100 && id.Valid() && winproc.IsAlive(id); i++ {
		time.Sleep(20 * time.Millisecond)
	}

	// 注意：这里不关闭子进程句柄。句柄由 watchExit 独占持有并负责关闭，
	// 抢先关闭会让那边阻塞中的 WaitForSingleObject 作用在已释放的句柄上。
	inst.mu.Lock()
	inst.job = nil
	inst.id = winproc.Identity{}
	inst.output = nil
	inst.spawn = winproc.SpawnResult{}
	inst.stopping = false
	r.setState(inst, func(s *model.ProjectState) {
		s.Lifecycle = model.LifecycleStopped
		s.Readiness = model.ReadinessNone
		s.PID = 0
	})
	inst.mu.Unlock()

	r.auditEvent("terminated", projectID, id.PID, "进程树已清理")
	return terr
}

// Restart 重启项目：先停再起。
func (r *Runner) Restart(ctx context.Context, p model.Project) (model.ProjectState, error) {
	if st := r.State(p.ID); st.Lifecycle == model.LifecycleRunning || st.Lifecycle == model.LifecycleStarting {
		if err := r.Stop(ctx, p.ID); err != nil && !errors.Is(err, ErrNotRunning) {
			return r.State(p.ID), err
		}
	}
	return r.Start(ctx, p)
}

// StopAll 停止全部项目，用于优雅退出。
func (r *Runner) StopAll(ctx context.Context) {
	r.mu.Lock()
	r.shuttingDown = true
	ids := make([]string, 0, len(r.instances))
	for id := range r.instances {
		ids = append(ids, id)
	}
	r.mu.Unlock()

	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(projectID string) {
			defer wg.Done()
			_ = r.Stop(ctx, projectID)
		}(id)
	}
	wg.Wait()
}

// Shutdown 停止全部项目并关闭日志广播。
func (r *Runner) Shutdown(ctx context.Context) {
	r.StopAll(ctx)
	r.hub.Close()
}
