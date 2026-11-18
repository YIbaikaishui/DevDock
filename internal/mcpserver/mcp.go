// Package mcpserver 实现最小可用的 stdio MCP 服务器。
//
// 铁律：stdout 是协议通道，只能输出 JSON-RPC 消息。
// 任何日志、panic 栈、调试输出都必须走 stderr —— 一行多余输出就会让客户端握手失败。
package mcpserver

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"devdock/internal/client"
	"devdock/internal/model"
)

// ProtocolVersion 锁定的 MCP 协议版本。
const ProtocolVersion = "2024-11-05"

// ServerVersion 由 main 注入。
var ServerVersion = "dev"

// JSON-RPC 错误码。
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

// Request 是 JSON-RPC 请求。
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response 是 JSON-RPC 响应。
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError 是 JSON-RPC 错误对象。
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// discoverFunc 是服务发现函数，签名与 client.Discover 一致。
// 做成字段而不是直接调用客户端包，是为了让测试能注入桩实现：
// 真实的 Discover 会自动拉起 serve 进程，在测试里那等于把测试程序自己拉起来。
type discoverFunc func(autoStart bool) (*client.Client, error)

// Server 是 stdio MCP 服务器。
type Server struct {
	in       *bufio.Reader
	out      io.Writer
	errOut   io.Writer
	discover discoverFunc
}

// New 创建服务器。in 通常是 os.Stdin，out 必须是 os.Stdout。
func New(in io.Reader, out io.Writer, errOut io.Writer) *Server {
	return &Server{
		in:       bufio.NewReaderSize(in, 1<<16),
		out:      out,
		errOut:   errOut,
		discover: client.Discover,
	}
}

// client 返回用于本次工具调用的客户端。
func (s *Server) client() (*client.Client, error) {
	discover := s.discover
	if discover == nil {
		discover = client.Discover
	}
	return discover(true)
}

// Serve 进入消息循环，直到 stdin 关闭。
func (s *Server) Serve() error {
	for {
		line, err := s.in.ReadBytes('\n')
		if len(line) > 0 {
			trimmed := strings.TrimSpace(string(line))
			if trimmed != "" {
				s.handleLine(trimmed)
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// handleLine 处理一条消息。
func (s *Server) handleLine(raw string) {
	// panic 隔离：单个请求的错误绝不能让整个服务器退出。
	defer func() {
		if rec := recover(); rec != nil {
			fmt.Fprintf(s.errOut, "[mcp] panic: %v\n", rec)
		}
	}()

	var req Request
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		s.writeError(nil, CodeParseError, "消息不是合法的 JSON")
		return
	}

	// 通知（无 id）不需要响应。
	isNotification := len(req.ID) == 0 || string(req.ID) == "null"

	switch req.Method {
	case "initialize":
		s.writeResult(req.ID, map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
			"serverInfo": map[string]any{
				"name":    "devdock",
				"version": ServerVersion,
			},
		})

	case "notifications/initialized":
		// 无需响应。

	case "ping":
		s.writeResult(req.ID, map[string]any{})

	case "tools/list":
		s.writeResult(req.ID, map[string]any{"tools": toolDefs()})

	case "tools/call":
		s.handleToolCall(req)

	default:
		if isNotification {
			return
		}
		s.writeError(req.ID, CodeMethodNotFound, "不支持的方法："+req.Method)
	}
}

// toolDefs 返回工具定义。只有 4 个工具，不随意扩张。
func toolDefs() []map[string]any {
	return []map[string]any{
		{
			"name": "devdock_list",
			"description": "列出 DevDock 中全部本地开发项目，包含运行状态（lifecycle）、" +
				"业务就绪状态（readiness）、端口与 PID。List all managed dev projects with lifecycle and readiness state.",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			"name": "devdock_start",
			"description": "启动指定的本地开发项目。可选等待业务就绪。" +
				"Start a managed dev project; optionally wait until its HTTP readiness check passes.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "项目 ID 或名称 (project id or name)",
					},
					"waitReady": map[string]any{
						"type":        "boolean",
						"description": "是否等待业务就绪后再返回 (wait until readiness passes)",
					},
					"timeoutSec": map[string]any{
						"type":        "number",
						"description": "等待就绪的超时秒数，默认 30 (readiness timeout in seconds)",
					},
				},
				"required": []string{"id"},
			},
		},
		{
			"name": "devdock_stop",
			"description": "停止指定的本地开发项目，整棵进程树都会被终止。" +
				"Stop a managed dev project and terminate its entire process tree.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "项目 ID 或名称 (project id or name)",
					},
				},
				"required": []string{"id"},
			},
		},
		{
			"name": "devdock_logs",
			"description": "读取项目最近的标准输出日志。tail 默认 100，上限 1000。" +
				"Read recent stdout/stderr lines of a project. tail defaults to 100, capped at 1000.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "项目 ID 或名称 (project id or name)",
					},
					"tail": map[string]any{
						"type":        "number",
						"description": "返回最后多少行，默认 100，上限 1000 (number of lines)",
					},
				},
				"required": []string{"id"},
			},
		},
	}
}

// callParams 是 tools/call 的参数。
type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// handleToolCall 分发工具调用。
func (s *Server) handleToolCall(req Request) {
	var p callParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		s.writeError(req.ID, CodeInvalidParams, "tools/call 参数格式不正确")
		return
	}

	// MCP 允许工具返回业务错误（isError=true）而不是协议错误。
	// 这样 agent 能看到"项目不存在"这类可理解的信息，而不是协议层失败。
	text, isErr := s.runTool(p.Name, p.Arguments)

	s.writeResult(req.ID, map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": text},
		},
		"isError": isErr,
	})
}

// runTool 执行工具并返回文本结果。
func (s *Server) runTool(name string, rawArgs json.RawMessage) (string, bool) {
	c, err := s.client()
	if err != nil {
		return "无法连接 DevDock 服务：" + err.Error(), true
	}

	switch name {
	case "devdock_list":
		return s.toolList(c)

	case "devdock_start":
		var args struct {
			ID         string  `json:"id"`
			WaitReady  bool    `json:"waitReady"`
			TimeoutSec float64 `json:"timeoutSec"`
		}
		if err := json.Unmarshal(rawArgs, &args); err != nil || args.ID == "" {
			return "参数错误：需要提供 id", true
		}
		return s.toolStart(c, args.ID, args.WaitReady, args.TimeoutSec)

	case "devdock_stop":
		var args struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(rawArgs, &args); err != nil || args.ID == "" {
			return "参数错误：需要提供 id", true
		}
		return s.toolStop(c, args.ID)

	case "devdock_logs":
		var args struct {
			ID   string  `json:"id"`
			Tail float64 `json:"tail"`
		}
		if err := json.Unmarshal(rawArgs, &args); err != nil || args.ID == "" {
			return "参数错误：需要提供 id", true
		}
		tail := int(args.Tail)
		return s.toolLogs(c, args.ID, tail)

	default:
		return "未知工具：" + name, true
	}
}

// maxLogTail 是 devdock_logs 的行数硬上限，防止塞爆 agent 上下文。
const maxLogTail = 1000

// defaultLogTail 是默认行数。
const defaultLogTail = 100

func (s *Server) toolList(c *client.Client) (string, bool) {
	views, err := c.List()
	if err != nil {
		return "获取项目列表失败：" + err.Error(), true
	}

	type row struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Lifecycle string `json:"lifecycle"`
		Readiness string `json:"readiness"`
		Port      int    `json:"port,omitempty"`
		PID       uint32 `json:"pid,omitempty"`
		URL       string `json:"url,omitempty"`
	}
	rows := make([]row, 0, len(views))
	for _, v := range views {
		rows = append(rows, row{
			ID:        v.ID,
			Name:      v.Name,
			Lifecycle: string(v.State.Lifecycle),
			Readiness: string(v.State.Readiness),
			Port:      v.Port,
			PID:       v.State.PID,
			URL:       v.ReadyURL(),
		})
	}
	return mustJSON(map[string]any{"projects": rows}), false
}

func (s *Server) toolStart(c *client.Client, key string, waitReady bool, timeoutSec float64) (string, bool) {
	v, err := c.Get(key)
	if err != nil {
		return "项目不存在：" + key, true
	}
	st, err := c.Start(v.ID)
	if err != nil {
		return "启动失败：" + err.Error(), true
	}

	result := map[string]any{
		"id":        v.ID,
		"lifecycle": string(st.Lifecycle),
		"readiness": string(st.Readiness),
		"pid":       st.PID,
	}
	if waitReady {
		if timeoutSec <= 0 {
			timeoutSec = 30
		}
		// 上限 300 秒，避免 agent 无限阻塞。
		if timeoutSec > 300 {
			timeoutSec = 300
		}
		final, werr := c.WaitReady(v.ID, time.Duration(timeoutSec)*time.Second)
		result["lifecycle"] = string(final.Lifecycle)
		result["readiness"] = string(final.Readiness)
		result["pid"] = final.PID
		if final.ReadyLatencyMs > 0 {
			result["readyLatencyMs"] = final.ReadyLatencyMs
		}
		if werr != nil {
			// 进程可能仍在运行 —— 明确区分"进程失败"与"就绪未达成"。
			result["note"] = werr.Error()
			if final.Lifecycle == model.LifecycleRunning {
				result["note"] = "进程仍在运行，但业务未就绪：" + werr.Error()
			}
			return mustJSON(result), final.Lifecycle != model.LifecycleRunning
		}
	}
	return mustJSON(result), false
}

func (s *Server) toolStop(c *client.Client, key string) (string, bool) {
	v, err := c.Get(key)
	if err != nil {
		return "项目不存在：" + key, true
	}
	if err := c.Stop(v.ID); err != nil {
		if apiErr, ok := err.(*client.APIError); ok && apiErr.Code == client.CodeNotRunning {
			return mustJSON(map[string]any{
				"id":        v.ID,
				"lifecycle": string(model.LifecycleStopped),
				"note":      "项目本来就未在运行",
			}), false
		}
		return "停止失败：" + err.Error(), true
	}
	st, _ := c.State(v.ID)
	return mustJSON(map[string]any{
		"id":        v.ID,
		"lifecycle": string(st.Lifecycle),
	}), false
}

func (s *Server) toolLogs(c *client.Client, key string, tail int) (string, bool) {
	if tail <= 0 {
		tail = defaultLogTail
	}
	if tail > maxLogTail {
		tail = maxLogTail
	}

	v, err := c.Get(key)
	if err != nil {
		return "项目不存在：" + key, true
	}
	res, err := c.Logs(v.ID, tail)
	if err != nil {
		return "读取日志失败：" + err.Error(), true
	}

	lines := make([]string, 0, len(res.Lines))
	for _, l := range res.Lines {
		lines = append(lines, l.Text)
	}
	return mustJSON(map[string]any{
		"id":      v.ID,
		"lines":   lines,
		"dropped": res.DroppedCount > 0 || res.Gap,
	}), false
}

// mustJSON 序列化为单行 JSON 字符串。
func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `{"error":"内部序列化失败"}`
	}
	return string(b)
}

// writeResult 输出成功响应。
func (s *Server) writeResult(id json.RawMessage, result any) {
	if len(id) == 0 {
		return // 通知不响应
	}
	resp := Response{JSONRPC: "2.0", ID: id, Result: result}
	s.write(resp)
}

// writeError 输出错误响应。
func (s *Server) writeError(id json.RawMessage, code int, msg string) {
	resp := Response{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &RPCError{Code: code, Message: msg},
	}
	if len(id) == 0 {
		resp.ID = json.RawMessage("null")
	}
	s.write(resp)
}

// write 把响应写到 stdout，一行一条。
func (s *Server) write(resp Response) {
	data, err := json.Marshal(resp)
	if err != nil {
		fmt.Fprintf(s.errOut, "[mcp] 序列化响应失败: %v\n", err)
		return
	}
	if _, err := s.out.Write(append(data, '\n')); err != nil {
		fmt.Fprintf(s.errOut, "[mcp] 写 stdout 失败: %v\n", err)
	}
	if f, ok := s.out.(*os.File); ok {
		_ = f.Sync()
	}
}
