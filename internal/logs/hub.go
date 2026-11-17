package logs

import (
	"sync"
	"time"

	"devdock/internal/model"
)

// SubscriberBuffer 是每个订阅者的事件通道容量。
// 满即丢弃并标记缺口 —— 慢客户端只能看到"日志过期"，而不是拖垮整个服务。
const SubscriberBuffer = 256

// Event 是广播给订阅者的事件。
type Event struct {
	// Type 取 "log" / "state" / "ready" / "gap"。
	Type string
	// ProjectID 标识事件属于哪个项目，供单项目订阅流过滤。
	ProjectID string
	// Log 仅在 Type == "log" 时有效。
	Log model.LogLine
	// State 仅在 Type == "state" 时有效。
	State model.ProjectState
	// Data 承载 ready / gap 等自由负载。
	Data any
}

// Subscriber 是一个事件订阅者。
type Subscriber struct {
	// C 是事件通道。生产者非阻塞发送，满则丢弃。
	C chan Event
	// gap 标记该订阅者已经错过事件。
	mu  sync.Mutex
	gap bool
	// closed 防止重复关闭通道。
	closed bool
}

// MarkGap 标记该订阅者存在缺口。
func (s *Subscriber) MarkGap() {
	s.mu.Lock()
	s.gap = true
	s.mu.Unlock()
}

// TakeGap 读取并清除缺口标记。
func (s *Subscriber) TakeGap() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.gap
	s.gap = false
	return g
}

// Hub 向多个订阅者广播事件。单个慢订阅者不会阻塞其他人。
type Hub struct {
	mu     sync.RWMutex
	subs   map[*Subscriber]struct{}
	closed bool
}

// NewHub 创建广播中心。
func NewHub() *Hub {
	return &Hub{subs: make(map[*Subscriber]struct{})}
}

// Subscribe 注册一个订阅者。调用方必须在不再使用时调用 Unsubscribe。
func (h *Hub) Subscribe() *Subscriber {
	s := &Subscriber{C: make(chan Event, SubscriberBuffer)}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		close(s.C)
		s.closed = true
		return s
	}
	h.subs[s] = struct{}{}
	h.mu.Unlock()
	return s
}

// Unsubscribe 注销订阅者并关闭其通道。多次调用安全。
func (h *Hub) Unsubscribe(s *Subscriber) {
	if s == nil {
		return
	}
	h.mu.Lock()
	if _, ok := h.subs[s]; ok {
		delete(h.subs, s)
		s.mu.Lock()
		if !s.closed {
			s.closed = true
			close(s.C)
		}
		s.mu.Unlock()
	}
	h.mu.Unlock()
}

// Count 返回当前订阅者数量，用于验证订阅者不泄漏。
func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs)
}

// Publish 非阻塞广播一个事件。
// 通道满时丢弃该事件并给该订阅者打缺口标记，绝不阻塞调用方。
func (h *Hub) Publish(ev Event) {
	h.mu.RLock()
	subs := make([]*Subscriber, 0, len(h.subs))
	for s := range h.subs {
		subs = append(subs, s)
	}
	h.mu.RUnlock()

	for _, s := range subs {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			continue
		}
		select {
		case s.C <- ev:
		default:
			s.gap = true
		}
		s.mu.Unlock()
	}
}

// Close 关闭 hub，注销全部订阅者。
func (h *Hub) Close() {
	h.mu.Lock()
	subs := make([]*Subscriber, 0, len(h.subs))
	for s := range h.subs {
		subs = append(subs, s)
	}
	h.subs = make(map[*Subscriber]struct{})
	h.closed = true
	h.mu.Unlock()

	for _, s := range subs {
		s.mu.Lock()
		if !s.closed {
			s.closed = true
			close(s.C)
		}
		s.mu.Unlock()
	}
}

// nowRFC3339Milli 返回带毫秒的 RFC3339 时间戳。
func nowRFC3339Milli() string {
	return time.Now().Format("2006-01-02T15:04:05.000Z07:00")
}
