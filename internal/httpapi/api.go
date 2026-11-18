package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"devdock/internal/detect"
	"devdock/internal/logs"
	"devdock/internal/model"
	"devdock/internal/runner"
	"devdock/internal/store"
)

// Version 由 main 注入，用于 health 与 meta 输出。
var Version = "dev"

// API 汇总 HTTP 层依赖。
type API struct {
	store  *store.Store
	runner *runner.Runner
	scanner *detect.Scanner
	token  string
	port   int
	start  time.Time
	// warn 是加载配置时的非致命警告，通过 /api/meta 暴露一次。
	warn string
}

// New 创建 API。
func New(st *store.Store, rn *runner.Runner, token string, port int, warn string) *API {
	return &API{
		store:   st,
		runner:  rn,
		scanner: detect.NewScanner(),
		token:   token,
		port:    port,
		start:   time.Now(),
		warn:    warn,
	}
}

// Handler 返回挂好全部路由与安全中间件的 http.Handler。
func (a *API) Handler(web http.Handler) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/health", a.handleHealth)
	mux.HandleFunc("/api/meta", a.handleMeta)
	mux.HandleFunc("/api/projects", a.handleProjects)
	mux.HandleFunc("/api/projects/", a.handleProjectSub)
	mux.HandleFunc("/api/scan", a.handleScan)
	mux.HandleFunc("/api/events", func(w http.ResponseWriter, r *http.Request) {
		a.handleEvents(w, r, "")
	})

	// 静态资源：其余所有路径交给 embed 文件服务。
	mux.Handle("/", web)

	return security(mux, SecurityConfig{Token: a.token, Port: a.port})
}

// --- 事件发布器实现（供 runner 注入）---

// PublishState 广播项目状态变化。
func (a *API) PublishState(st model.ProjectState) {
	a.runner.Hub().Publish(logs.Event{Type: "state", ProjectID: st.ID, State: st})
}

// PublishLog 广播一行日志。
func (a *API) PublishLog(projectID string, line model.LogLine) {
	a.runner.Hub().Publish(logs.Event{Type: "log", ProjectID: projectID, Log: line})
}

// PublishReady 广播就绪事件。
func (a *API) PublishReady(projectID, url string, latencyMs int64) {
	a.runner.Hub().Publish(logs.Event{
		Type:      "ready",
		ProjectID: projectID,
		Data: map[string]any{
			"id":        projectID,
			"url":       url,
			"latencyMs": latencyMs,
		},
	})
}

// --- 基础端点 ---

func (a *API) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, CodeConflict, "仅支持 GET")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"version":    Version,
		"uptimeSec":  int64(time.Since(a.start).Seconds()),
		"configPath": a.store.Config().Path(),
		"pid":        os.Getpid(),
	})
}

func (a *API) handleMeta(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, CodeConflict, "仅支持 GET")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version":    Version,
		"mcp":        true,
		"cli":        true,
		"port":       a.port,
		"warning":    a.warn,
		"sseClients": a.runner.Hub().Count(),
		"audit":      a.runner.Audit(),
	})
}

// --- 项目集合 ---

func (a *API) handleProjects(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		projects := a.store.List()
		views := make([]model.View, 0, len(projects))
		for _, p := range projects {
			views = append(views, model.View{Project: p, State: a.runner.State(p.ID)})
		}
		writeJSON(w, http.StatusOK, map[string]any{"projects": views})

	case http.MethodPost:
		var req projectRequest
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, CodeConfigInvalid, err.Error())
			return
		}
		p, err := req.toProject()
		if err != nil {
			writeError(w, http.StatusBadRequest, CodeConfigInvalid, err.Error())
			return
		}
		created, err := a.store.Create(p)
		if err != nil {
			mapError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, model.View{Project: created, State: a.runner.State(created.ID)})

	default:
		writeError(w, http.StatusMethodNotAllowed, CodeConflict, "不支持的方法")
	}
}

// --- 单项目及其子路径 ---

func (a *API) handleProjectSub(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/projects/")
	if rest == "" {
		a.handleProjects(w, r)
		return
	}
	parts := strings.Split(rest, "/")
	id := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	switch action {
	case "":
		a.handleProjectItem(w, r, id)
	case "start":
		a.handleStart(w, r, id)
	case "stop":
		a.handleStop(w, r, id)
	case "restart":
		a.handleRestart(w, r, id)
	case "logs":
		a.handleLogs(w, r, id)
	case "events":
		a.handleEvents(w, r, id)
	case "state":
		a.handleState(w, r, id)
	case "open-folder":
		a.handleOpenFolder(w, r, id)
	default:
		writeError(w, http.StatusNotFound, CodeNotFound, "未知的接口路径")
	}
}

func (a *API) handleProjectItem(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		p, err := a.store.Get(id)
		if err != nil {
			mapError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, model.View{Project: p, State: a.runner.State(p.ID)})

	case http.MethodPatch:
		a.handlePatch(w, r, id)

	case http.MethodDelete:
		p, err := a.store.Get(id)
		if err != nil {
			mapError(w, err)
			return
		}
		// 运行中禁止直接删除，避免留下无人管理的进程。
		st := a.runner.State(p.ID)
		if st.Lifecycle == model.LifecycleRunning || st.Lifecycle == model.LifecycleStarting {
			if r.URL.Query().Get("stop") == "true" {
				if err := a.runner.Stop(r.Context(), p.ID); err != nil {
					mapError(w, err)
					return
				}
			} else {
				writeError(w, http.StatusConflict, CodeConflict,
					"项目正在运行，请先停止，或使用 stop=true 参数先停再删")
				return
			}
		}
		deleted, err := a.store.Delete(p.ID)
		if err != nil {
			mapError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted.ID})

	default:
		writeError(w, http.StatusMethodNotAllowed, CodeConflict, "不支持的方法")
	}
}

func (a *API) handlePatch(w http.ResponseWriter, r *http.Request, id string) {
	p, err := a.store.Get(id)
	if err != nil {
		mapError(w, err)
		return
	}

	var raw map[string]json.RawMessage
	if err := decodeJSON(r, &raw); err != nil {
		writeError(w, http.StatusBadRequest, CodeConfigInvalid, err.Error())
		return
	}

	// 运行中不允许改会改变进程身份或端口的字段。
	st := a.runner.State(p.ID)
	running := st.Lifecycle == model.LifecycleRunning || st.Lifecycle == model.LifecycleStarting
	if running {
		for _, key := range []string{"cwd", "command", "port", "env"} {
			if _, ok := raw[key]; ok {
				writeError(w, http.StatusConflict, CodeConflict,
					"项目正在运行，无法修改工作目录、命令、端口或环境变量，请先停止")
				return
			}
		}
	}

	var patch store.Patch
	if v, ok := raw["name"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			writeError(w, http.StatusBadRequest, CodeConfigInvalid, "name 必须是字符串")
			return
		}
		patch.Name = &s
	}
	if v, ok := raw["cwd"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			writeError(w, http.StatusBadRequest, CodeConfigInvalid, "cwd 必须是字符串")
			return
		}
		patch.Cwd = &s
	}
	if v, ok := raw["command"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			writeError(w, http.StatusBadRequest, CodeConfigInvalid, "command 必须是字符串")
			return
		}
		patch.Command = &s
	}
	if v, ok := raw["port"]; ok {
		var n int
		if err := json.Unmarshal(v, &n); err != nil {
			writeError(w, http.StatusBadRequest, CodeConfigInvalid, "port 必须是整数")
			return
		}
		patch.Port = &n
	}
	if v, ok := raw["readiness"]; ok {
		var rc *model.ReadinessConfig
		if string(v) != "null" {
			rc = &model.ReadinessConfig{}
			if err := json.Unmarshal(v, rc); err != nil {
				writeError(w, http.StatusBadRequest, CodeConfigInvalid, "readiness 格式不正确")
				return
			}
		}
		patch.Readiness = &rc
	}
	if v, ok := raw["env"]; ok {
		var env map[string]string
		if err := json.Unmarshal(v, &env); err != nil {
			writeError(w, http.StatusBadRequest, CodeConfigInvalid, "env 必须是键值对")
			return
		}
		patch.Env = &env
	}
	if v, ok := raw["autostart"]; ok {
		var b bool
		if err := json.Unmarshal(v, &b); err != nil {
			writeError(w, http.StatusBadRequest, CodeConfigInvalid, "autostart 必须是布尔值")
			return
		}
		patch.Autostart = &b
	}

	updated, err := a.store.Update(p.ID, patch)
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, model.View{Project: updated, State: a.runner.State(updated.ID)})
}

func (a *API) handleState(w http.ResponseWriter, r *http.Request, id string) {
	if _, err := a.store.Get(id); err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a.runner.State(id))
}

// --- 动作 ---

func (a *API) handleStart(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, CodeConflict, "仅支持 POST")
		return
	}
	p, err := a.store.Get(id)
	if err != nil {
		mapError(w, err)
		return
	}
	st, err := a.runner.Start(r.Context(), p)
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, st)
}

func (a *API) handleStop(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, CodeConflict, "仅支持 POST")
		return
	}
	if _, err := a.store.Get(id); err != nil {
		mapError(w, err)
		return
	}
	if err := a.runner.Stop(r.Context(), id); err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, a.runner.State(id))
}

func (a *API) handleRestart(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, CodeConflict, "仅支持 POST")
		return
	}
	p, err := a.store.Get(id)
	if err != nil {
		mapError(w, err)
		return
	}
	st, err := a.runner.Restart(r.Context(), p)
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, st)
}

// handleLogs 返回历史日志。支持 tail 与 since 两种取法。
func (a *API) handleLogs(w http.ResponseWriter, r *http.Request, id string) {
	if _, err := a.store.Get(id); err != nil {
		mapError(w, err)
		return
	}
	q := r.URL.Query()
	ring := a.runner.Ring(id)

	var lines []model.LogLine
	gap := false
	if sinceStr := q.Get("since"); sinceStr != "" {
		since, _ := strconv.ParseUint(sinceStr, 10, 64)
		lines, gap = ring.Since(since)
	} else {
		tail := 200
		if t := q.Get("tail"); t != "" {
			if n, err := strconv.Atoi(t); err == nil && n > 0 {
				tail = n
			}
		}
		if tail > 5000 {
			tail = 5000
		}
		lines = ring.Tail(tail)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":           id,
		"lines":        lines,
		"nextSeq":      ring.NextSeq(),
		"droppedCount": ring.Dropped(),
		"gap":          gap,
	})
}

// handleOpenFolder 在资源管理器中打开项目工作目录。
func (a *API) handleOpenFolder(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, CodeConflict, "仅支持 POST")
		return
	}
	p, err := a.store.Get(id)
	if err != nil {
		mapError(w, err)
		return
	}
	// 用 explorer 直接打开目录。失败不致命：某些环境下 explorer 返回非零码。
	cmd := exec.Command("explorer", p.Cwd)
	_ = cmd.Start()
	go func() { _ = cmd.Wait() }()
	writeJSON(w, http.StatusOK, map[string]any{"opened": p.Cwd})
}

// --- 扫描 ---

// scanRequest 是 POST /api/scan 的请求体（SPEC 6.3）。
// Depth 用指针区分「未传（默认 3）」与「显式 0（只判定根目录自身）」（SPEC A15）。
type scanRequest struct {
	Roots      []string `json:"roots"`
	Depth      *int     `json:"depth"`
	BudgetMs   int      `json:"budgetMs"`
	Limit      int      `json:"limit"`
	IncludeLow bool     `json:"includeLow"`
}

// handleScan 只读扫描本机项目并返回建议，不创建任何项目、不写任何文件。
func (a *API) handleScan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, CodeConflict, "仅支持 POST")
		return
	}

	var req scanRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, CodeConfigInvalid, err.Error())
		return
	}
	if len(req.Roots) > detect.MaxRoots {
		writeError(w, http.StatusBadRequest, CodeConfigInvalid, fmt.Sprintf("根目录最多 %d 个", detect.MaxRoots))
		return
	}
	if req.Depth != nil && (*req.Depth < 0 || *req.Depth > detect.MaxDepth) {
		writeError(w, http.StatusBadRequest, CodeConfigInvalid, fmt.Sprintf("扫描深度必须在 0-%d 之间", detect.MaxDepth))
		return
	}
	if req.BudgetMs != 0 && (req.BudgetMs < 100 || req.BudgetMs > 10000) {
		writeError(w, http.StatusBadRequest, CodeConfigInvalid, "时间预算必须在 100-10000 毫秒之间")
		return
	}
	if req.Limit != 0 && (req.Limit < 1 || req.Limit > 500) {
		writeError(w, http.StatusBadRequest, CodeConfigInvalid, "建议条数上限必须在 1-500 之间")
		return
	}

	opts := detect.DefaultOptions()
	opts.Roots = req.Roots
	opts.IncludeLow = req.IncludeLow
	opts.Existing = a.store.List()
	if req.Depth != nil {
		opts.Depth = *req.Depth
	}
	if req.BudgetMs != 0 {
		opts.BudgetMs = req.BudgetMs
	}
	if req.Limit != 0 {
		opts.Limit = req.Limit
	}

	res, err := a.scanner.Scan(r.Context(), opts)
	if err != nil {
		if errors.Is(err, detect.ErrBusy) {
			writeError(w, http.StatusConflict, CodeScanInProgress, "已有一次扫描正在进行，请稍后再试")
			return
		}
		writeError(w, http.StatusBadRequest, CodeConfigInvalid, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// --- 请求体解析 ---

// projectRequest 是创建项目的请求体。
type projectRequest struct {
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Cwd       string                 `json:"cwd"`
	Command   string                 `json:"command"`
	Port      int                    `json:"port"`
	Readiness *model.ReadinessConfig `json:"readiness"`
	Env       map[string]string      `json:"env"`
	Autostart bool                   `json:"autostart"`
}

func (r projectRequest) toProject() (model.Project, error) {
	if r.Cwd == "" {
		return model.Project{}, fmt.Errorf("工作目录不能为空")
	}
	p := model.Project{
		ID:        r.ID,
		Name:      r.Name,
		Cwd:       r.Cwd,
		Command:   r.Command,
		Port:      r.Port,
		Readiness: r.Readiness,
		Env:       r.Env,
		Autostart: r.Autostart,
	}
	if r.Name == "" {
		p.Name = r.ID
	}
	return p, nil
}

// decodeJSON 解析请求体，并限制大小。
func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("请求格式不正确：%v", err)
	}
	return nil
}

// Shutdown 供 main 在退出时调用。
func (a *API) Shutdown(ctx context.Context) error {
	a.runner.Shutdown(ctx)
	return nil
}
