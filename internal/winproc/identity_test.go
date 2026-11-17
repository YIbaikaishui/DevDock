package winproc

import (
	"os"
	"testing"
)

// TestCaptureIdentitySelf 验证能拿到自身身份令牌。
func TestCaptureIdentitySelf(t *testing.T) {
	id, err := CaptureIdentity(uint32(os.Getpid()))
	if err != nil {
		t.Fatalf("抓取自身身份失败: %v", err)
	}
	if !id.Valid() {
		t.Fatalf("自身身份令牌无效: %+v", id)
	}
	if id.PID != uint32(os.Getpid()) {
		t.Fatalf("PID 不匹配: %d != %d", id.PID, os.Getpid())
	}
}

// TestVerifyIdentitySelf 验证正确令牌被接受、被篡改的令牌被拒绝。
// 这是"防 PID 复用误杀"的正向与负向两面。
func TestVerifyIdentitySelf(t *testing.T) {
	id, err := CaptureIdentity(uint32(os.Getpid()))
	if err != nil {
		t.Fatalf("抓取自身身份失败: %v", err)
	}
	if !VerifyIdentity(id) {
		t.Fatal("自身令牌应当校验通过")
	}

	// 负向：篡改创建时间。等价于"同 PID 但已是另一个进程"。
	forged := id
	forged.CreationTime = id.CreationTime + 1
	if VerifyIdentity(forged) {
		t.Fatal("创建时间不符的令牌被错误接受 —— 会导致 PID 复用误杀")
	}
	if IsAlive(forged) {
		t.Fatal("创建时间不符的令牌被 IsAlive 判为存活")
	}

	// 负向：创建时间为 0 表示无效令牌，必须一律拒绝。
	zero := Identity{PID: id.PID}
	if VerifyIdentity(zero) {
		t.Fatal("无效令牌（CreationTime=0）被错误接受")
	}

	// 负向：不存在的 PID。
	gone := Identity{PID: 0xFFFFFFF0, CreationTime: id.CreationTime}
	if VerifyIdentity(gone) {
		t.Fatal("不存在的 PID 被错误接受")
	}
}

// TestIdentityValid 验证 Valid 的边界。
func TestIdentityValid(t *testing.T) {
	cases := []struct {
		id   Identity
		want bool
	}{
		{Identity{}, false},
		{Identity{PID: 1}, false},
		{Identity{CreationTime: 1}, false},
		{Identity{PID: 1, CreationTime: 1}, true},
	}
	for _, c := range cases {
		if got := c.id.Valid(); got != c.want {
			t.Fatalf("%+v.Valid() = %v，期望 %v", c.id, got, c.want)
		}
	}
}

// TestIsAliveSelf 验证存活判断。
func TestIsAliveSelf(t *testing.T) {
	id, err := CaptureIdentity(uint32(os.Getpid()))
	if err != nil {
		t.Fatalf("抓取自身身份失败: %v", err)
	}
	if !IsAlive(id) {
		t.Fatal("当前进程应当被判为存活")
	}
}

// TestExitCodeRunning 验证运行中进程的 ExitCode 返回 (0,false,nil)。
func TestExitCodeRunning(t *testing.T) {
	id, err := CaptureIdentity(uint32(os.Getpid()))
	if err != nil {
		t.Fatalf("抓取自身身份失败: %v", err)
	}
	_, exited, err := ExitCode(id)
	if err != nil {
		t.Fatalf("读取自身退出码失败: %v", err)
	}
	if exited {
		t.Fatal("当前进程不应被判为已退出")
	}
}
