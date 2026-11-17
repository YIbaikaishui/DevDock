// Package ready 实现 HTTP 就绪探测。
//
// 关键约定：探测目标固定使用 127.0.0.1 而不是 localhost。
// localhost 在部分 Windows 配置下优先解析到 ::1，而项目通常只监听 IPv4，
// 会造成"进程明明起来了却一直不就绪"的假故障。
package ready

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"
)

// Result 是一次探测的最终结果。
type Result struct {
	// Ready 为真表示探测成功。
	Ready bool
	// TimedOut 为真表示在超时时间内未就绪。
	TimedOut bool
	// StatusCode 是最后一次成功连上时的状态码。
	StatusCode int
	// Latency 是从开始探测到就绪的耗时。
	Latency time.Duration
	// Err 是最后一次错误，用于向用户解释为什么没就绪。
	Err error
}

// Options 描述一次探测任务。
type Options struct {
	Port         int
	Path         string
	ExpectStatus []int
	Timeout      time.Duration
	Interval     time.Duration
}

// url 构造探测地址。
func (o Options) url() string {
	return "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(o.Port)) + o.Path
}

// accepts 判断状态码是否在期望范围内。
func (o Options) accepts(code int) bool {
	if len(o.ExpectStatus) == 0 {
		return code >= 200 && code <= 399
	}
	// ExpectStatus 形如 [200, 399] 表示闭区间。
	if len(o.ExpectStatus) == 2 {
		lo, hi := o.ExpectStatus[0], o.ExpectStatus[1]
		if lo > hi {
			lo, hi = hi, lo
		}
		return code >= lo && code <= hi
	}
	for _, want := range o.ExpectStatus {
		if code == want {
			return true
		}
	}
	return false
}

// client 是复用的 HTTP 客户端。连接复用避免每 200ms 建一次 TCP 造成端口堆积。
func newClient() *http.Client {
	return &http.Client{
		Timeout: time.Second,
		Transport: &http.Transport{
			Proxy:              nil, // 不走系统代理，避免公司代理拦截本机请求
			DisableKeepAlives:  false,
			MaxIdleConns:       4,
			IdleConnTimeout:    30 * time.Second,
			DisableCompression: true,
		},
		// 探测不跟随重定向：我们关心的是这个端口有没有响应。
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// Wait 在 ctx 结束或超时前反复探测，直到就绪。
//
// 注意：超时不是错误状态 —— 进程仍在运行，只是业务没准备好。
// 上层必须把它映射为 readiness=timeout 而保持 lifecycle=running。
func Wait(ctx context.Context, opts Options) Result {
	start := time.Now()
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Second
	}
	if opts.Interval <= 0 {
		opts.Interval = 200 * time.Millisecond
	}
	if opts.Port <= 0 {
		return Result{Err: fmt.Errorf("未声明端口，无法探测就绪")}
	}
	if opts.Path == "" {
		opts.Path = "/"
	}

	target := opts.url()
	client := newClient()
	defer client.CloseIdleConnections()

	deadline := time.Now().Add(opts.Timeout)
	var lastErr error
	var lastCode int

	for {
		if ctx.Err() != nil {
			return Result{Err: ctx.Err(), StatusCode: lastCode, Latency: time.Since(start)}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return Result{Err: err, Latency: time.Since(start)}
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
		} else {
			lastCode = resp.StatusCode
			resp.Body.Close()
			if opts.accepts(lastCode) {
				return Result{Ready: true, StatusCode: lastCode, Latency: time.Since(start)}
			}
			lastErr = fmt.Errorf("状态码 %d 不在期望范围内", lastCode)
		}

		if time.Now().After(deadline) {
			return Result{
				TimedOut:   true,
				StatusCode: lastCode,
				Latency:    time.Since(start),
				Err:        lastErr,
			}
		}

		select {
		case <-ctx.Done():
			return Result{Err: ctx.Err(), StatusCode: lastCode, Latency: time.Since(start)}
		case <-time.After(opts.Interval):
		}
	}
}
