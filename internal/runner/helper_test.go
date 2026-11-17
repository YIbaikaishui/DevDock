package runner

import (
	"net"
	"os"
	"strconv"
	"testing"
	"time"
)

// TestHelperBlockingProcess 不是真正的测试，而是供其他测试作为子进程启动的入口。
//
// 用法：以 -test.run=TestHelperBlockingProcess 启动测试二进制，并置
// DEVDOCK_TEST_HELPER=1。此时本函数会永久阻塞，模拟一个长驻的开发服务。
// 未置该环境变量时它立即返回，不会干扰正常测试。
func TestHelperBlockingProcess(t *testing.T) {
	if os.Getenv("DEVDOCK_TEST_HELPER") != "1" {
		return
	}
	// 打印一行日志，供日志采集测试使用。
	os.Stdout.WriteString("helper process started\n")
	os.Stdout.Sync()
	// 远长于任何测试的等待上限，实际由 DevDock 的 job 清理终止。
	time.Sleep(10 * time.Minute)
}

// freePort 申请一个当前空闲的端口并立刻释放，返回端口号。
// 存在极小的竞争窗口，但仅用于测试中构造"非冲突端口"。
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("申请空闲端口失败: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	if _, err := strconv.Atoi(strconv.Itoa(port)); err != nil {
		t.Fatalf("端口转换失败: %v", err)
	}
	return port
}
