// Package model 定义 DevDock 的纯数据类型。
// 本包不含任何副作用，可被所有其他包安全引用。
package model

import (
	"strconv"
	"time"
)

// Lifecycle 是进程生命周期维度，与就绪维度正交。
type Lifecycle string

const (
	LifecycleStopped  Lifecycle = "stopped"
	LifecycleStarting Lifecycle = "starting"
	LifecycleRunning  Lifecycle = "running"
	LifecycleStopping Lifecycle = "stopping"
	LifecycleFailed   Lifecycle = "failed"
)

// Readiness 是业务就绪维度。进程活着不等于业务可用，二者必须分开呈现。
type Readiness string

const (
	ReadinessNone    Readiness = "none"
	ReadinessPending Readiness = "pending"
	ReadinessReady   Readiness = "ready"
	ReadinessTimeout Readiness = "timeout"
	ReadinessError   Readiness = "error"
)

// ReadinessConfig 描述 HTTP 就绪探测参数。
type ReadinessConfig struct {
	Path         string `json:"path"`
	ExpectStatus []int  `json:"expectStatus,omitempty"`
	TimeoutSec   int    `json:"timeoutSec,omitempty"`
	IntervalMs   int    `json:"intervalMs,omitempty"`
}

// Defaults 补齐零值字段，避免下游反复判 0。
func (r *ReadinessConfig) Defaults() {
	if r.TimeoutSec <= 0 {
		r.TimeoutSec = 30
	}
	if r.IntervalMs <= 0 {
		r.IntervalMs = 200
	}
	if len(r.ExpectStatus) == 0 {
		r.ExpectStatus = []int{200, 399}
	}
}

// Project 是一条可重复执行的启动配方。它会被持久化到 config.json。
type Project struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Cwd       string            `json:"cwd"`
	Command   string            `json:"command"`
	Port      int               `json:"port,omitempty"`
	Readiness *ReadinessConfig  `json:"readiness,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Autostart bool              `json:"autostart"`
	CreatedAt time.Time         `json:"createdAt"`
	UpdatedAt time.Time         `json:"updatedAt"`
}

// ProjectState 是运行态，不落盘。
type ProjectState struct {
	ID             string     `json:"id"`
	Lifecycle      Lifecycle  `json:"lifecycle"`
	Readiness      Readiness  `json:"readiness"`
	PID            uint32     `json:"pid,omitempty"`
	StartedAt      *time.Time `json:"startedAt,omitempty"`
	ReadyAt        *time.Time `json:"readyAt,omitempty"`
	ReadyLatencyMs int64      `json:"readyLatencyMs,omitempty"`
	LastExitCode   *int       `json:"lastExitCode,omitempty"`
	LastError      string     `json:"lastError,omitempty"`
}

// LogLine 是一条日志。Seq 在项目内单调递增，用于断线补拉与缺口检测。
type LogLine struct {
	Seq    uint64 `json:"seq"`
	Ts     string `json:"ts"`
	Stream string `json:"stream"`
	Text   string `json:"text"`
}

// AuditEvent 记录进程生命周期关键动作，便于事后排查误杀 / 清理问题。
type AuditEvent struct {
	At    string `json:"at"`
	Kind  string `json:"kind"`
	Actor string `json:"actor,omitempty"`
	PID   uint32 `json:"pid,omitempty"`
	Note  string `json:"note,omitempty"`
}

// View 是 API 输出的项目聚合物：配方 + 运行态。
type View struct {
	Project
	State ProjectState `json:"state"`
}

// ReadyURL 返回项目声明端口对应的本机地址，未声明端口时返回空串。
// 固定使用 127.0.0.1 而非 localhost，避免解析到 ::1 而项目只听 IPv4 的经典坑。
func (p *Project) ReadyURL() string {
	if p.Port <= 0 {
		return ""
	}
	return "http://127.0.0.1:" + strconv.Itoa(p.Port)
}
