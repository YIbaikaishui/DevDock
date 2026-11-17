package logs

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestRingBounded 验证环形缓冲在超量写入后只保留容量内的行，并正确累计丢弃数。
// 这是"内存不泄漏"的核心保证。
func TestRingBounded(t *testing.T) {
	const capacity = 50
	r := NewRing(capacity)
	const total = 500

	for i := 0; i < total; i++ {
		r.Append("out", fmt.Sprintf("line-%d", i))
	}

	if got := r.Len(); got != capacity {
		t.Fatalf("容量未生效：Len=%d，期望 %d", got, capacity)
	}
	// 丢弃数必须精确等于总写入数减去容量。
	if got, want := r.Dropped(), uint64(total-capacity); got != want {
		t.Fatalf("Dropped=%d，期望 %d", got, want)
	}
	if got := len(r.Tail(capacity * 10)); got != capacity {
		t.Fatalf("Tail 超出容量时未截断：得到 %d 行", got)
	}
	// 保留的必须是最新的那段。
	tail := r.Tail(1)
	if len(tail) != 1 || tail[0].Text != "line-499" {
		t.Fatalf("最新行不正确：%+v", tail)
	}
	// 最旧的必须是第 total-capacity 行。
	first := r.Tail(capacity)[0]
	if want := "line-450"; first.Text != want {
		t.Fatalf("最旧行应为 %s，实际 %s", want, first.Text)
	}
}

// TestRingSeqMonotonic 验证序号单调递增，断线补拉依赖这一点。
func TestRingSeqMonotonic(t *testing.T) {
	r := NewRing(10)
	var prev uint64
	for i := 0; i < 20; i++ {
		line := r.Append("out", "x")
		if line.Seq <= prev {
			t.Fatalf("序号未递增：%d 之后出现 %d", prev, line.Seq)
		}
		prev = line.Seq
	}
}

// TestRingSinceGap 验证序号已过期时返回 gap，让前端知道有缺口。
func TestRingSinceGap(t *testing.T) {
	r := NewRing(10)
	for i := 0; i < 100; i++ {
		r.Append("out", fmt.Sprintf("l%d", i))
	}

	// 问一个远早于缓冲起点的序号，必须报告缺口。
	lines, gap := r.Since(1)
	if !gap {
		t.Fatal("序号 1 早已过期，应当报告 gap")
	}
	if len(lines) != 10 {
		t.Fatalf("应返回全部 10 行，实际 %d", len(lines))
	}

	// 问当前最新的序号，不应有缺口也不应有数据。
	lines, gap = r.Since(r.NextSeq() - 1)
	if gap {
		t.Fatal("最新序号不应报告 gap")
	}
	if len(lines) != 0 {
		t.Fatalf("最新序号之后不应有行，实际 %d", len(lines))
	}
}

// TestRingConcurrent 用 -race 验证读写并发安全。
func TestRingConcurrent(t *testing.T) {
	r := NewRing(100)
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				r.Append("out", "x")
			}
		}()
	}
	for rd := 0; rd < 4; rd++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				r.Tail(10)
				r.FirstSeq()
				r.NextSeq()
				r.Dropped()
			}
		}()
	}
	wg.Wait()
}

// TestHubSlowSubscriberDoesNotBlock 是"订阅者泄漏"防护的核心测试：
// 订阅者通道填满后，生产者必须立刻返回而不是卡住。
func TestHubSlowSubscriberDoesNotBlock(t *testing.T) {
	h := NewHub()
	defer h.Close()

	slow := h.Subscribe()
	defer h.Unsubscribe(slow)

	// 塞满通道。
	for i := 0; i < SubscriberBuffer; i++ {
		slow.C <- Event{Type: "log"}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		// 再多发 1000 条，若实现会阻塞这里就永远不会关闭 done。
		for i := 0; i < 1000; i++ {
			h.Publish(Event{Type: "log"})
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("订阅者通道已满时 Publish 被阻塞，存在泄漏风险")
	}

	// 溢出必须被记为缺口，否则前端会静默丢日志。
	if !slow.TakeGap() {
		t.Fatal("通道溢出后应当标记 gap")
	}
}

// TestHubUnsubscribeNoLeak 验证注销后订阅者数量归零。
func TestHubUnsubscribeNoLeak(t *testing.T) {
	h := NewHub()
	defer h.Close()

	subs := make([]*Subscriber, 20)
	for i := range subs {
		subs[i] = h.Subscribe()
	}
	if got := h.Count(); got != 20 {
		t.Fatalf("订阅者计数错误：%d", got)
	}
	for _, s := range subs {
		h.Unsubscribe(s)
	}
	if got := h.Count(); got != 0 {
		t.Fatalf("注销后仍有 %d 个订阅者，存在泄漏", got)
	}
}

// TestHubPublishAfterClose 验证关闭后发布与注销都不 panic。
func TestHubPublishAfterClose(t *testing.T) {
	h := NewHub()
	s := h.Subscribe()
	h.Close()
	h.Publish(Event{Type: "log"})
	h.Unsubscribe(s)
}

// TestReadLinesTruncatesLongLine 验证超长行被截断而不是中断读取。
// 用 bufio.Scanner 的常见错误就是在这里丢日志。
func TestReadLinesTruncatesLongLine(t *testing.T) {
	long := strings.Repeat("A", MaxLineBytes+5000)
	input := long + "\n" + "after\n"

	var got []string
	err := ReadLines(strings.NewReader(input), func(text string) {
		got = append(got, text)
	})
	if err != nil {
		t.Fatalf("ReadLines 返回错误: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("应读到 2 行，实际 %d 行", len(got))
	}
	if !strings.HasSuffix(got[0], truncatedMarker) {
		t.Fatal("超长行应带截断标记")
	}
	// 关键：超长行之后的行必须仍然被读到。
	if got[1] != "after" {
		t.Fatalf("超长行之后的正常行丢失：%q", got[1])
	}
}

// TestReadLinesStripsCRLF 验证 Windows 换行被清理。
func TestReadLinesStripsCRLF(t *testing.T) {
	var got []string
	if err := ReadLines(strings.NewReader("a\r\nb\r\n"), func(s string) { got = append(got, s) }); err != nil {
		t.Fatalf("ReadLines 返回错误: %v", err)
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("换行未正确清理：%q", got)
	}
}
