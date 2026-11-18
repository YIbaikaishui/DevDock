package detect

import (
	"net"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// 端口推断的统一优先级（SPEC 4.8，所有生态共用）：
//  1. 命令里的 --port N / --port=N
//  2. 命令里的 -p N（仅当该段命令是白名单里的服务启动器）
//  3. 命令行内 PORT=N 环境前缀
//  4. vite.config.* 的 server.port / preview.port
//  5. Python 源码里的 port=N
//  6. 框架默认值（置信度降为 medium）
//  7. 都没有 → port=0，绝不猜。
var (
	portLongRe  = regexp.MustCompile(`--port[ =](\d{2,5})`)
	portShortRe = regexp.MustCompile(`(?:^|\s)-p[ =](\d{2,5})`)
	portEnvRe   = regexp.MustCompile(`(?:^|\s)PORT=(\d{2,5})(?:\s|$)`)
	pyPortRe    = regexp.MustCompile(`\bport\s*=\s*(\d{4,5})`)
)

// shortPortAllow 是允许用 `-p N` 声明端口的命令（首 token）。
// 其它命令的 -p 语义各不相同（例如某些 CLI 用它表示路径），不能当端口读。
var shortPortAllow = map[string]bool{
	"vite": true, "next": true, "nuxt": true, "nuxi": true, "serve": true,
	"astro": true, "remix": true, "webpack": true, "webpack-dev-server": true,
	"rsbuild": true, "http-server": true, "ng": true,
}

// viteConfigFiles 是按优先级尝试的 vite 配置文件。
var viteConfigFiles = []string{
	"vite.config.ts", "vite.config.js", "vite.config.mjs",
	"vite.config.mts", "vite.config.cjs", "vite.config.cts",
}

var (
	viteServerPortRe  = regexp.MustCompile(`(?s)server\s*:\s*\{[^}]*?\bport\s*:\s*(\d{2,5})`)
	vitePreviewPortRe = regexp.MustCompile(`(?s)preview\s*:\s*\{[^}]*?\bport\s*:\s*(\d{2,5})`)
	viteAnyPortRe     = regexp.MustCompile(`\bport\s*:\s*(\d{2,5})`)
)

// portFromFlags 按优先级 1、2 从一段命令行里取端口。
func portFromFlags(seg string) (int, bool) {
	if m := portLongRe.FindStringSubmatch(seg); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 && n <= 65535 {
			return n, true
		}
	}
	if !shortPortAllow[commandName(seg)] {
		return 0, false
	}
	if m := portShortRe.FindStringSubmatch(seg); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 && n <= 65535 {
			return n, true
		}
	}
	return 0, false
}

// portFromEnvPrefix 取命令行里的 `PORT=8080` 环境前缀。
// 注意只认键名严格等于 PORT，绝不子串匹配 DB_PORT / REDISPORT。
func portFromEnvPrefix(cmd string) (int, bool) {
	if m := portEnvRe.FindStringSubmatch(cmd); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 && n <= 65535 {
			return n, true
		}
	}
	return 0, false
}

// commandName 取一段命令的首个可执行名（去掉路径与 .cmd/.exe 后缀）。
func commandName(seg string) string {
	fields := strings.Fields(seg)
	if len(fields) == 0 {
		return ""
	}
	name := filepath.Base(strings.ToLower(fields[0]))
	name = strings.TrimSuffix(name, ".exe")
	name = strings.TrimSuffix(name, ".cmd")
	name = strings.TrimSuffix(name, ".bat")
	return name
}

// viteConfigPort 从 vite.config.* 里读 server.port / preview.port。
// 先按块匹配，最后才退到「任意 port: N」，避免把 proxy target 里的端口当服务端口。
func viteConfigPort(p *dirProbe) (int, bool) {
	for _, name := range viteConfigFiles {
		text, ok := p.read(name)
		if !ok {
			continue
		}
		for _, re := range []*regexp.Regexp{viteServerPortRe, vitePreviewPortRe, viteAnyPortRe} {
			if m := re.FindStringSubmatch(text); m != nil {
				if n, err := strconv.Atoi(m[1]); err == nil && n > 0 && n <= 65535 {
					return n, true
				}
			}
		}
	}
	return 0, false
}

// pythonSourcePort 从 Python 源码里读 port=N（uvicorn.run(app, port=8085) 这类）。
func pythonSourcePort(text string) (int, bool) {
	if m := pyPortRe.FindStringSubmatch(text); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 && n <= 65535 {
			return n, true
		}
	}
	return 0, false
}

// probePortInUse 在本机回环上试着监听一次，用来预检端口是否被占用。
//
// 刻意不 import internal/runner（SPEC A16）：detect 要保持纯标准库、可单测，
// 不让 runner 的生命周期渗进来。Windows 上 Go 的监听 socket 带
// SO_EXCLUSIVEADDRUSE，所以「能绑上」就等价于「没人占用」。
func probePortInUse(port int) bool {
	if port <= 0 || port > 65535 {
		return false
	}
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return true
	}
	_ = ln.Close()
	return false
}
