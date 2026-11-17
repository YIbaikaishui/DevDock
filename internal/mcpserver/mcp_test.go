package mcpserver

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// runSession 用给定的输入行跑一次 MCP 会话，返回解析后的响应列表与原始 stdout。
func runSession(t *testing.T, input string) ([]Response, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer

	srv := New(strings.NewReader(input), &stdout, &stderr)
	if err := srv.Serve(); err != nil {
		t.Fatalf("Serve 返回错误: %v", err)
	}

	raw := stdout.String()

	var out []Response
	sc := bufio.NewScanner(strings.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var resp Response
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			t.Fatalf("stdout 出现非 JSON 内容（会破坏 MCP 握手）:\n  %q\n  错误: %v", line, err)
		}
		out = append(out, resp)
	}
	return out, raw
}

// TestInitialize 验证握手响应符合 MCP 规范。
func TestInitialize(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n"
	resps, _ := runSession(t, input)

	if len(resps) != 1 {
		t.Fatalf("initialize 应产生 1 个响应，实际 %d", len(resps))
	}
	r := resps[0]
	if r.Error != nil {
		t.Fatalf("initialize 不应报错: %+v", r.Error)
	}
	result, ok := r.Result.(map[string]any)
	if !ok {
		t.Fatalf("result 类型异常: %T", r.Result)
	}
	if got := result["protocolVersion"]; got != ProtocolVersion {
		t.Fatalf("协议版本应为 %s，实际 %v", ProtocolVersion, got)
	}
	info, ok := result["serverInfo"].(map[string]any)
	if !ok {
		t.Fatal("缺少 serverInfo")
	}
	if info["name"] != "devdock" {
		t.Fatalf("服务名应为 devdock，实际 %v", info["name"])
	}
	if _, ok := result["capabilities"].(map[string]any)["tools"]; !ok {
		t.Fatal("应声明 tools 能力")
	}
}

// TestToolsList 验证工具定义齐全且只有约定的 4 个。
func TestToolsList(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"
	resps, _ := runSession(t, input)

	if len(resps) != 1 {
		t.Fatalf("应产生 1 个响应，实际 %d", len(resps))
	}
	result := resps[0].Result.(map[string]any)
	tools, ok := result["tools"].([]any)
	if !ok {
		t.Fatalf("tools 字段类型异常: %T", result["tools"])
	}
	if len(tools) != 4 {
		t.Fatalf("工具数量应为 4，实际 %d", len(tools))
	}

	want := map[string]bool{
		"devdock_list":  false,
		"devdock_start": false,
		"devdock_stop":  false,
		"devdock_logs":  false,
	}
	for _, tv := range tools {
		tool := tv.(map[string]any)
		name, _ := tool["name"].(string)
		if _, expected := want[name]; !expected {
			t.Errorf("出现了未约定的工具: %s", name)
			continue
		}
		want[name] = true

		// 每个工具都必须有描述与输入 schema，否则模型无法选择。
		if desc, _ := tool["description"].(string); desc == "" {
			t.Errorf("%s 缺少 description", name)
		}
		schema, ok := tool["inputSchema"].(map[string]any)
		if !ok {
			t.Errorf("%s 缺少 inputSchema", name)
			continue
		}
		if schema["type"] != "object" {
			t.Errorf("%s 的 inputSchema.type 应为 object，实际 %v", name, schema["type"])
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("缺少工具: %s", name)
		}
	}
}

// TestUnknownMethod 验证未知方法返回 -32601。
func TestUnknownMethod(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":3,"method":"no/such/method"}` + "\n"
	resps, _ := runSession(t, input)

	if len(resps) != 1 {
		t.Fatalf("应产生 1 个响应，实际 %d", len(resps))
	}
	if resps[0].Error == nil {
		t.Fatal("未知方法应返回错误")
	}
	if resps[0].Error.Code != CodeMethodNotFound {
		t.Fatalf("错误码应为 %d，实际 %d", CodeMethodNotFound, resps[0].Error.Code)
	}
}

// TestParseError 验证非法 JSON 返回 -32700。
func TestParseError(t *testing.T) {
	input := `{not json at all}` + "\n"
	resps, _ := runSession(t, input)

	if len(resps) != 1 {
		t.Fatalf("应产生 1 个响应，实际 %d", len(resps))
	}
	if resps[0].Error == nil || resps[0].Error.Code != CodeParseError {
		t.Fatalf("应为解析错误 -32700，实际 %+v", resps[0].Error)
	}
}

// TestNotificationNoResponse 验证通知（无 id）不产生响应。
func TestNotificationNoResponse(t *testing.T) {
	input := `{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"
	resps, raw := runSession(t, input)

	if len(resps) != 0 {
		t.Fatalf("通知不应产生响应，实际 %d 个", len(resps))
	}
	if strings.TrimSpace(raw) != "" {
		t.Fatalf("通知不应写 stdout，实际: %q", raw)
	}
}

// TestPing 验证 ping 返回空 result。
func TestPing(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":9,"method":"ping"}` + "\n"
	resps, _ := runSession(t, input)

	if len(resps) != 1 {
		t.Fatalf("应产生 1 个响应，实际 %d", len(resps))
	}
	if resps[0].Error != nil {
		t.Fatalf("ping 不应报错: %+v", resps[0].Error)
	}
}

// TestStdoutIsProtocolOnly 是 MCP 的铁律测试：
// 混合多种消息后，stdout 的每一行都必须是合法 JSON-RPC。
// 一行多余输出就会让客户端握手失败。
func TestStdoutIsProtocolOnly(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":4,"method":"unknown"}`,
	}, "\n") + "\n"

	resps, raw := runSession(t, input)

	// 4 个有 id 的请求都应得到响应（通知不响应）。
	if len(resps) != 4 {
		t.Fatalf("应有 4 个响应，实际 %d", len(resps))
	}
	// runSession 内部已在解析失败时 t.Fatalf，这里再显式确认一次。
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("stdout 出现非协议内容: %q", line)
		}
		if m["jsonrpc"] != "2.0" {
			t.Fatalf("响应缺少 jsonrpc 2.0 标记: %q", line)
		}
	}
}

// TestToolsCallMissingID 验证缺参数的 tools/call 返回业务错误而非崩溃。
func TestToolsCallMissingID(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"devdock_start","arguments":{}}}` + "\n"
	resps, _ := runSession(t, input)

	if len(resps) != 1 {
		t.Fatalf("应产生 1 个响应，实际 %d", len(resps))
	}
	// MCP 约定：工具业务错误走 result.isError，而不是协议错误。
	result, ok := resps[0].Result.(map[string]any)
	if !ok {
		t.Fatalf("缺参数时应返回 result（isError=true），实际: %+v", resps[0])
	}
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Fatal("缺参数时应标记 isError=true")
	}
}

// TestToolsCallUnknownTool 验证未知工具名不会导致服务器崩溃。
func TestToolsCallUnknownTool(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"no_such_tool","arguments":{}}}` + "\n"
	resps, _ := runSession(t, input)

	if len(resps) != 1 {
		t.Fatalf("应产生 1 个响应，实际 %d", len(resps))
	}
	result, ok := resps[0].Result.(map[string]any)
	if !ok {
		t.Fatalf("应返回 result，实际 %+v", resps[0])
	}
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Fatal("未知工具应标记 isError=true")
	}
}

// TestServeHandlesEOF 验证 stdin 关闭后干净退出。
func TestServeHandlesEOF(t *testing.T) {
	var stdout, stderr bytes.Buffer
	srv := New(strings.NewReader(""), &stdout, &stderr)
	if err := srv.Serve(); err != io.EOF && err != nil {
		t.Fatalf("stdin 为空时应正常返回，实际: %v", err)
	}
}
