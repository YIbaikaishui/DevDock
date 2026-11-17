package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"devdock/internal/logs"
	"devdock/internal/model"
)

// heartbeatInterval 是 SSE 心跳间隔。定时发注释行可及早发现死连接。
const heartbeatInterval = 15 * time.Second

// logPayload 是 SSE 日志事件的负载：日志行 + 所属项目 id。
type logPayload struct {
	ID string `json:"id"`
	model.LogLine
}

// sseWriter 封装 SSE 输出。
type sseWriter struct {
	w  http.ResponseWriter
	fl http.Flusher
}

// newSSEWriter 设置 SSE 响应头并返回写入器。
func newSSEWriter(w http.ResponseWriter) (*sseWriter, bool) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, CodeInternal, "当前连接不支持流式输出")
		return nil, false
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	// 反转代理若存在也不缓冲 —— 本机直连，主要是保险。
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fl.Flush()
	return &sseWriter{w: w, fl: fl}, true
}

// event 输出一条 SSE 事件。
func (s *sseWriter) event(name string, payload any) error {
	var data []byte
	var err error
	if payload != nil {
		data, err = json.Marshal(payload)
		if err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", name, data); err != nil {
		return err
	}
	s.fl.Flush()
	return nil
}

// comment 输出 SSE 注释行，用作心跳。
func (s *sseWriter) comment(text string) error {
	if _, err := fmt.Fprintf(s.w, ": %s\n\n", text); err != nil {
		return err
	}
	s.fl.Flush()
	return nil
}

// handleEvents 是 GET /api/projects/{id}/events 与 /api/events 的实现。
// projectID 为空表示全局事件流。
func (a *API) handleEvents(w http.ResponseWriter, r *http.Request, projectID string) {
	sw, ok := newSSEWriter(w)
	if !ok {
		return
	}

	hub := a.runner.Hub()

	// 断线重连时按 since 补发，避免前端漏日志。
	// 若 since 已早于 ring 中最旧的序号，先发 gap 让客户端知道有缺口。
	if projectID != "" {
		if sinceStr := r.URL.Query().Get("since"); sinceStr != "" {
			var since uint64
			if _, err := fmt.Sscanf(sinceStr, "%d", &since); err == nil {
				ring := a.runner.Ring(projectID)
				lines, gap := ring.Since(since)
				if gap {
					_ = sw.event("gap", map[string]any{
						"id":     projectID,
						"fromSeq": ring.FirstSeq(),
						"toSeq":  ring.NextSeq(),
					})
				}
				for _, l := range lines {
					if err := sw.event("log", l); err != nil {
						return
					}
				}
			}
		}
		// 先发一次当前状态，UI 无需额外轮询即可正确渲染。
		st := a.runner.State(projectID)
		if err := sw.event("state", st); err != nil {
			return
		}
	}

	sub := hub.Subscribe()
	defer hub.Unsubscribe(sub)

	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			// 浏览器关闭标签后这里会立刻触发，订阅者随之注销，不会泄漏。
			return

		case ev, open := <-sub.C:
			if !open {
				return
			}
			// 慢客户端导致的缺口：在下次发送前先告诉它。
			if sub.TakeGap() {
				if err := sw.event("gap", map[string]any{"id": projectID, "reason": "subscriber_overflow"}); err != nil {
					return
				}
			}
			if err := dispatch(sw, ev, projectID); err != nil {
				return
			}

		case <-ticker.C:
			if err := sw.comment("ping"); err != nil {
				return
			}
		}
	}
}

// dispatch 把内部事件写成一个 SSE 事件。
// 单项目流（filterProject 非空）只写属于该项目的日志与状态。
func dispatch(sw *sseWriter, ev logs.Event, filterProject string) error {
	if filterProject != "" && ev.ProjectID != "" && ev.ProjectID != filterProject {
		return nil
	}
	switch ev.Type {
	case "log":
		// 全局流里前端需要知道这条日志属于哪个项目，因此附带 id。
		return sw.event("log", logPayload{ID: ev.ProjectID, LogLine: ev.Log})
	case "state":
		return sw.event("state", ev.State)
	case "ready":
		return sw.event("ready", ev.Data)
	case "gap":
		return sw.event("gap", ev.Data)
	default:
		return nil
	}
}
