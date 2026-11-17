// Package logs 提供有界日志缓冲与订阅广播。
//
// 三种"日志泄漏"必须同时防住，本包对应前两种（第三种由 disk.go 负责）：
//   - 内存泄漏：固定行的环形缓冲，超出即丢弃最旧并累加 Dropped
//   - 订阅者泄漏：订阅通道满即丢弃并标记缺口，绝不阻塞生产者
package logs

import (
	"sync"

	"devdock/internal/model"
)

// Ring 是固定容量的日志环形缓冲。
// 采用"按行数"而非按字节的容量口径：行数上限更容易推理，
// 配合 read.go 里的单行截断（64 KiB）即可保证内存上界为 行数上限 × 单行上限。
type Ring struct {
	mu       sync.RWMutex
	capacity int
	lines    []model.LogLine
	start    int // 最旧元素下标
	count    int
	nextSeq  uint64
	dropped  uint64
}

// NewRing 创建容量为 capacity 行的环形缓冲。capacity <= 0 时使用默认值。
func NewRing(capacity int) *Ring {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	return &Ring{
		capacity: capacity,
		lines:    make([]model.LogLine, capacity),
	}
}

// DefaultCapacity 是每项目默认保留的日志行数。
const DefaultCapacity = 4000

// Append 追加一行，返回分配到的序号。
func (r *Ring) Append(stream, text string) model.LogLine {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.nextSeq++
	line := model.LogLine{
		Seq:    r.nextSeq,
		Ts:     nowRFC3339Milli(),
		Stream: stream,
		Text:   text,
	}

	idx := (r.start + r.count) % r.capacity
	if r.count == r.capacity {
		// 缓冲满：覆盖最旧一条，并推进起点。
		r.start = (r.start + 1) % r.capacity
		r.dropped++
	} else {
		r.count++
	}
	r.lines[idx] = line
	return line
}

// Dropped 返回因溢出被丢弃的行数。
func (r *Ring) Dropped() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.dropped
}

// NextSeq 返回下一个将被分配的序号。
func (r *Ring) NextSeq() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.nextSeq + 1
}

// FirstSeq 返回当前缓冲中最早一行的序号；缓冲为空时返回 NextSeq。
func (r *Ring) FirstSeq() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.count == 0 {
		return r.nextSeq + 1
	}
	return r.lines[r.start].Seq
}

// Tail 返回最后 n 行。
func (r *Ring) Tail(n int) []model.LogLine {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if n <= 0 || n > r.count {
		n = r.count
	}
	out := make([]model.LogLine, 0, n)
	from := r.count - n
	for i := from; i < r.count; i++ {
		out = append(out, r.lines[(r.start+i)%r.capacity])
	}
	return out
}

// Since 返回序号 > seq 的所有行。
// 若 seq 已早于缓冲中最旧的序号，则 gap 为 true，调用方应提示部分日志已过期。
func (r *Ring) Since(seq uint64) (lines []model.LogLine, gap bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.count == 0 {
		return nil, false
	}
	first := r.lines[r.start].Seq
	if seq+1 < first {
		gap = true
	}
	out := make([]model.LogLine, 0, r.count)
	for i := 0; i < r.count; i++ {
		l := r.lines[(r.start+i)%r.capacity]
		if l.Seq > seq {
			out = append(out, l)
		}
	}
	return out, gap
}

// Len 返回当前缓冲行数。
func (r *Ring) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.count
}
