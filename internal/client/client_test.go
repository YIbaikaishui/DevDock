package client

import (
	"errors"
	"testing"
)

// TestSpawnServiceRefusesInTestProcess 是针对"进程树自我复制"的回归测试。
//
// spawnService 用 os.Executable() 取自身路径再以 serve 参数拉起服务。
// 在测试进程里，os.Executable() 指向的就是测试二进制本身，而测试二进制
// 会忽略不认识的位置参数、把整套测试再跑一遍 —— 于是每一代都再生一代，
// 并且一直持有自身 exe 的镜像句柄，导致 go test 收尾删除失败
// （表现为 `go: unlinkat ...mcpserver.test.exe: Access is denied`）。
//
// 这个测试断言：在测试进程里必须直接拒绝，而不是真的去启动子进程。
func TestSpawnServiceRefusesInTestProcess(t *testing.T) {
	if !isTestProcess() {
		t.Fatal("测试进程应该被识别为测试进程，否则防护不会生效")
	}

	err := spawnService()
	if err == nil {
		t.Fatal("测试进程中 spawnService 必须拒绝启动，否则会自我复制成进程树")
	}
	if !errors.Is(err, ErrRefuseSelfSpawn) {
		t.Fatalf("应返回 ErrRefuseSelfSpawn，实际: %v", err)
	}
}

// TestDiscoverAutoStartFailsSafely 验证自动启动路径在测试环境里
// 返回可读错误而不是把测试进程变成服务。
//
// 用 t.Setenv 把 APPDATA 指到临时目录，让探测一定落空，这样用例不依赖
// 「本机此刻有没有 DevDock 在跑」，可以每次真跑（而不是条件跳过）。
func TestDiscoverAutoStartFailsSafely(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())

	c, err := Discover(true)
	if err == nil {
		t.Fatalf("不应发现实例，实际拿到: %+v", c)
	}
	if !errors.Is(err, ErrServiceUnavailable) {
		t.Fatalf("应返回 ErrServiceUnavailable，实际: %v", err)
	}
}
