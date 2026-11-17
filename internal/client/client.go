// Package client 是 CLI 与 MCP 共用的 HTTP 客户端。
// 两者都不自己实现业务逻辑，只负责"找到正在运行的实例 + 调接口"。
package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"time"

	"devdock/internal/config"
	"devdock/internal/model"
)

// 稳定错误码，与 HTTP 层一致。
const (
	CodeNotFound       = "not_found"
	CodeConflict       = "conflict"
	CodePortInUse      = "port_in_use"
	CodeAlreadyRunning = "already_running"
	CodeNotRunning     = "not_running"
	CodeConfigInvalid  = "config_invalid"
	CodeUnauthorized   = "unauthorized"
)

// APIError 是服务端返回的结构化错误。
type APIError struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *APIError) Error() string { return e.Message }

// ErrServiceUnavailable 表示本机没有可用的 DevDock 实例。
var ErrServiceUnavailable = errors.New("DevDock 服务未运行且无法自动启动")

// Client 是与本机 DevDock 服务通信的客户端。
type Client struct {
	BaseURL string
	Token   string
	http    *http.Client
}

// New 创建一个指向给定实例的客户端。
func New(port int, token string) *Client {
	return &Client{
		BaseURL: "http://127.0.0.1:" + strconv.Itoa(port),
		Token:   token,
		http:    &http.Client{Timeout: 60 * time.Second},
	}
}

// Discover 找到正在运行的实例。找不到时按需后台拉起一个。
// 这样 agent 与脚本无需预先启动服务。
func Discover(autoStart bool) (*Client, error) {
	if c := probeRuntime(); c != nil {
		return c, nil
	}
	if !autoStart {
		return nil, ErrServiceUnavailable
	}
	if err := spawnService(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
	}
	// 等待最多 5 秒让新实例写好 runtime.json 并通过健康检查。
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		if c := probeRuntime(); c != nil {
			return c, nil
		}
	}
	return nil, ErrServiceUnavailable
}

// probeRuntime 读取 runtime.json 并验证它指向一个真正活着的实例。
func probeRuntime() *Client {
	rt, err := config.ReadRuntime()
	if err != nil || rt == nil {
		return nil
	}
	c := New(rt.Port, rt.Token)
	if !c.healthy() {
		return nil
	}
	return c
}

// healthy 调用 /api/health 判断实例是否可用。
func (c *Client) healthy() bool {
	hc := &http.Client{Timeout: 1200 * time.Millisecond}
	resp, err := hc.Get(c.BaseURL + "/api/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode == http.StatusOK
}

// spawnService 后台启动一个 serve 进程（不打开浏览器）。
func spawnService() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "serve", "--no-open", "--quiet")
	// 完全脱离当前控制台，避免 CLI 退出时把服务一起带走。
	detach(cmd)
	return cmd.Start()
}

// do 发起一次带令牌的请求。
func (c *Client) do(method, path string, body any, out any) error {
	var buf io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		buf = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, c.BaseURL+path, buf)
	if err != nil {
		return err
	}
	req.Header.Set("X-DevDock-Token", c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("无法连接 DevDock 服务: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode >= 400 {
		var env struct {
			Error APIError `json:"error"`
		}
		if json.Unmarshal(raw, &env) == nil && env.Error.Message != "" {
			apiErr := env.Error
			apiErr.Status = resp.StatusCode
			return &apiErr
		}
		return &APIError{Status: resp.StatusCode, Message: string(raw)}
	}

	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("解析服务响应失败: %w", err)
		}
	}
	return nil
}

// List 返回全部项目及其运行态。
func (c *Client) List() ([]model.View, error) {
	var out struct {
		Projects []model.View `json:"projects"`
	}
	if err := c.do(http.MethodGet, "/api/projects", nil, &out); err != nil {
		return nil, err
	}
	return out.Projects, nil
}

// Get 返回单个项目。
func (c *Client) Get(key string) (model.View, error) {
	var v model.View
	err := c.do(http.MethodGet, "/api/projects/"+key, nil, &v)
	return v, err
}

// State 返回单个项目的运行态。
func (c *Client) State(id string) (model.ProjectState, error) {
	var st model.ProjectState
	err := c.do(http.MethodGet, "/api/projects/"+id+"/state", nil, &st)
	return st, err
}

// Start 启动项目。
func (c *Client) Start(id string) (model.ProjectState, error) {
	var st model.ProjectState
	err := c.do(http.MethodPost, "/api/projects/"+id+"/start", nil, &st)
	return st, err
}

// Stop 停止项目。
func (c *Client) Stop(id string) error {
	return c.do(http.MethodPost, "/api/projects/"+id+"/stop", nil, nil)
}

// Restart 重启项目。
func (c *Client) Restart(id string) (model.ProjectState, error) {
	var st model.ProjectState
	err := c.do(http.MethodPost, "/api/projects/"+id+"/restart", nil, &st)
	return st, err
}

// Logs 返回历史日志。
func (c *Client) Logs(id string, tail int) (*LogsResult, error) {
	var out LogsResult
	err := c.do(http.MethodGet, "/api/projects/"+id+"/logs?tail="+strconv.Itoa(tail), nil, &out)
	return &out, err
}

// LogsResult 是日志接口的响应。
type LogsResult struct {
	ID           string          `json:"id"`
	Lines        []model.LogLine `json:"lines"`
	NextSeq      uint64          `json:"nextSeq"`
	DroppedCount uint64          `json:"droppedCount"`
	Gap          bool            `json:"gap"`
}

// Create 创建项目。
func (c *Client) Create(p model.Project) (model.View, error) {
	var v model.View
	err := c.do(http.MethodPost, "/api/projects", p, &v)
	return v, err
}

// Delete 删除项目。
func (c *Client) Delete(id string, stopFirst bool) error {
	path := "/api/projects/" + id
	if stopFirst {
		path += "?stop=true"
	}
	return c.do(http.MethodDelete, path, nil, nil)
}

// Meta 返回服务元信息。
func (c *Client) Meta() (map[string]any, error) {
	var out map[string]any
	err := c.do(http.MethodGet, "/api/meta", nil, &out)
	return out, err
}

// WaitReady 轮询直到项目就绪、失败或超时。
func (c *Client) WaitReady(id string, timeout time.Duration) (model.ProjectState, error) {
	deadline := time.Now().Add(timeout)
	var last model.ProjectState
	for time.Now().Before(deadline) {
		st, err := c.State(id)
		if err != nil {
			return last, err
		}
		last = st
		switch {
		case st.Readiness == model.ReadinessReady:
			return st, nil
		case st.Lifecycle == model.LifecycleFailed:
			return st, fmt.Errorf("项目启动失败: %s", st.LastError)
		case st.Lifecycle == model.LifecycleStopped:
			return st, fmt.Errorf("项目已退出")
		case st.Readiness == model.ReadinessTimeout:
			return st, fmt.Errorf("就绪探测超时")
		}
		time.Sleep(150 * time.Millisecond)
	}
	return last, fmt.Errorf("等待就绪超过 %s", timeout)
}
