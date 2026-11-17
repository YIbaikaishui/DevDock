// Package httpapi 提供本地 HTTP 接口、SSE 流与安全中间件。
//
// 威胁边界（诚实声明）：DevDock 是单机单用户工具，信任边界就是当前 Windows
// 用户账户。它不防御同账户下的恶意本机进程（那些进程本来就能直接执行任意命令）。
// 它防的是来自浏览器/网页的跨站请求和 DNS rebinding。
package httpapi

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net"
	"net/http"
	"strconv"
	"strings"
)

// NewToken 生成 32 字节随机会话令牌。
func NewToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// SecurityConfig 描述安全中间件需要的参数。
type SecurityConfig struct {
	Token string
	Port  int
}

// security 是安全中间件的核心。
//
// 四层校验，任一不通过即 403：
//  1. Host 必须是我们自己的回环地址（防 DNS rebinding：攻击者把自己的域名
//     解析到 127.0.0.1，浏览器就会带着 Host: evil.com 打过来）
//  2. 写操作必须有 Origin 且同源，或者带正确的 token
//  3. 所有 /api/ 请求都要 token（health 除外，供 CLI 与验收脚本探测存活）
//  4. 统一响应头 + CSP，进一步收紧浏览器侧能力
func security(next http.Handler, cfg SecurityConfig) http.Handler {
	allowedHosts := map[string]bool{
		"127.0.0.1:" + strconv.Itoa(cfg.Port): true,
		"localhost:" + strconv.Itoa(cfg.Port): true,
		// IPv6 回环：虽然我们只监听 IPv4，但浏览器可能用 [::1] 访问后失败重试，
		// 这里不额外放行，保持严格。
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setSecurityHeaders(w)

		// --- 1. Host 校验 ---
		host := strings.ToLower(r.Host)
		if !allowedHosts[host] {
			writeError(w, http.StatusForbidden, "unauthorized", "Host 头不被允许："+r.Host)
			return
		}

		// --- 2. Origin 校验（仅写方法）---
		method := r.Method
		isWrite := method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
		origin := r.Header.Get("Origin")
		if isWrite && origin != "" {
			if !originAllowed(origin, cfg.Port) {
				writeError(w, http.StatusForbidden, "unauthorized", "跨站请求已被拒绝")
				return
			}
		}

		// --- 3. token 校验 ---
		// health 端点免 token：CLI 与验收脚本需要在不持有 token 时判断服务是否活着。
		if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/health" {
			if !validToken(r, cfg.Token) {
				writeError(w, http.StatusForbidden, "unauthorized", "缺少或无效的访问令牌")
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// originAllowed 判断 Origin 是否与我们的回环服务同源。
func originAllowed(origin string, port int) bool {
	// Origin 形如 http://127.0.0.1:51234
	lower := strings.ToLower(origin)
	for _, prefix := range []string{"http://", "https://"} {
		if strings.HasPrefix(lower, prefix) {
			hostPart := strings.TrimPrefix(lower, prefix)
			return hostPart == "127.0.0.1:"+strconv.Itoa(port) ||
				hostPart == "localhost:"+strconv.Itoa(port)
		}
	}
	// "null" 等其它形式一律拒绝。
	return false
}

// validToken 校验请求携带的令牌。
// EventSource 无法自定义 header，所以 SSE 走 ?t= 查询参数。
func validToken(r *http.Request, want string) bool {
	if want == "" {
		return false
	}
	candidates := []string{
		r.Header.Get("X-DevDock-Token"),
		r.URL.Query().Get("t"),
	}
	for _, got := range candidates {
		if got == "" {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1 {
			return true
		}
	}
	return false
}

// setSecurityHeaders 设置统一响应头。
func setSecurityHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
	// CSP：只允许自身资源，禁内联脚本以外的外部来源。
	h.Set("Content-Security-Policy",
		"default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
}

// isLoopbackAddr 判断监听地址是否为回环。
func isLoopbackAddr(addr net.Addr) bool {
	tcp, ok := addr.(*net.TCPAddr)
	if !ok {
		return false
	}
	return tcp.IP.IsLoopback()
}
