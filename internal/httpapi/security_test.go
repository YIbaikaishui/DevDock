package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// okHandler 是一个恒返回 200 的下游处理器。
func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	})
}

// newTestServer 构造挂了安全中间件的测试服务。
func newTestServer(t *testing.T, token string, port int) (*httptest.Server, func(req *http.Request)) {
	t.Helper()
	h := security(okHandler(), SecurityConfig{Token: token, Port: port})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	// 返回一个把 Host 改成指定端口的辅助函数。
	return srv, func(req *http.Request) {
		req.Host = "127.0.0.1:" + itoa(port)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestSecurityRejectsForeignHost 验证 DNS rebinding 防护：
// Host 头不是本机回环时必须 403。
func TestSecurityRejectsForeignHost(t *testing.T) {
	const port = 12345
	const token = "test-token"
	srv, setHost := newTestServer(t, token, port)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	req.Host = "evil.example.com"
	req.Header.Set("X-DevDock-Token", token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("外部 Host 应被拒绝，实际状态码 %d", resp.StatusCode)
	}

	// 正向：本机 Host 通过。
	req2, _ := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	setHost(req2)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("本机 Host 应通过，实际状态码 %d", resp2.StatusCode)
	}
}

// TestSecurityRequiresToken 验证 /api/ 请求必须带令牌。
func TestSecurityRequiresToken(t *testing.T) {
	const port = 12346
	const token = "secret-token"
	srv, setHost := newTestServer(t, token, port)

	// 无令牌 → 403
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/projects", nil)
	setHost(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("无令牌应被拒绝，实际 %d", resp.StatusCode)
	}

	// 错误令牌 → 403
	reqBad, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/projects", nil)
	setHost(reqBad)
	reqBad.Header.Set("X-DevDock-Token", "wrong")
	respBad, err := http.DefaultClient.Do(reqBad)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer respBad.Body.Close()
	if respBad.StatusCode != http.StatusForbidden {
		t.Fatalf("错误令牌应被拒绝，实际 %d", respBad.StatusCode)
	}

	// 正确令牌 → 200
	reqOK, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/projects", nil)
	setHost(reqOK)
	reqOK.Header.Set("X-DevDock-Token", token)
	respOK, err := http.DefaultClient.Do(reqOK)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer respOK.Body.Close()
	if respOK.StatusCode != http.StatusOK {
		t.Fatalf("正确令牌应通过，实际 %d", respOK.StatusCode)
	}
}

// TestSecurityTokenViaQuery 验证 EventSource 用 ?t= 的路径可用。
func TestSecurityTokenViaQuery(t *testing.T) {
	const port = 12347
	const token = "query-token"
	srv, setHost := newTestServer(t, token, port)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/projects?t="+token, nil)
	setHost(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("查询参数令牌应通过，实际 %d", resp.StatusCode)
	}
}

// TestSecurityHealthNoToken 验证 health 免令牌，供 CLI 探测存活。
func TestSecurityHealthNoToken(t *testing.T) {
	const port = 12348
	srv, setHost := newTestServer(t, "tok", port)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/health", nil)
	setHost(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health 应免令牌，实际 %d", resp.StatusCode)
	}
}

// TestSecurityRejectsCrossOrigin 验证跨站写操作被拒绝（CSRF 防护）。
func TestSecurityRejectsCrossOrigin(t *testing.T) {
	const port = 12349
	const token = "csrf-token"
	srv, setHost := newTestServer(t, token, port)

	// 外部 Origin + 正确令牌 → 仍然 403（Origin 优先于令牌）。
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/projects", strings.NewReader("{}"))
	setHost(req)
	req.Header.Set("X-DevDock-Token", token)
	req.Header.Set("Origin", "http://evil.example.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("跨站写操作应被拒绝，实际 %d", resp.StatusCode)
	}

	// 同源 Origin → 放行。
	reqOK, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/projects", strings.NewReader("{}"))
	setHost(reqOK)
	reqOK.Header.Set("X-DevDock-Token", token)
	reqOK.Header.Set("Origin", "http://127.0.0.1:"+itoa(port))
	respOK, err := http.DefaultClient.Do(reqOK)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer respOK.Body.Close()
	if respOK.StatusCode != http.StatusOK {
		t.Fatalf("同源写操作应通过，实际 %d", respOK.StatusCode)
	}
}

// TestSecurityHeaders 验证安全响应头存在。
func TestSecurityHeaders(t *testing.T) {
	const port = 12350
	srv, setHost := newTestServer(t, "tok", port)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/health", nil)
	setHost(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()

	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "no-referrer",
		"X-Frame-Options":        "DENY",
	}
	for k, v := range want {
		if got := resp.Header.Get(k); got != v {
			t.Errorf("响应头 %s = %q，期望 %q", k, got, v)
		}
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
		t.Errorf("CSP 不正确: %q", csp)
	}
}

// TestErrorEnvelopeShape 验证错误信封结构稳定。
func TestErrorEnvelopeShape(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(rec, http.StatusConflict, CodePortInUse, "端口 5173 已被占用")

	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("Content-Type 不正确: %q", ct)
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("错误响应不是合法 JSON: %v", err)
	}
	if env.Error.Code != CodePortInUse {
		t.Fatalf("错误码应为 %s，实际 %s", CodePortInUse, env.Error.Code)
	}
	if env.Error.Message == "" {
		t.Fatal("错误消息不能为空")
	}
}

// TestNewTokenUniqueness 验证令牌随机且足够长。
func TestNewTokenUniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 50; i++ {
		tok, err := NewToken()
		if err != nil {
			t.Fatalf("生成令牌失败: %v", err)
		}
		// 32 字节 base64url 无填充 = 43 字符。
		if len(tok) < 40 {
			t.Fatalf("令牌太短: %q", tok)
		}
		if seen[tok] {
			t.Fatalf("令牌重复: %q", tok)
		}
		seen[tok] = true
	}
}
