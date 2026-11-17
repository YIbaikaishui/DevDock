package runner

import (
	"fmt"
	"net"
	"strconv"
)

// netListen 在 127.0.0.1 的指定端口上试绑，用于端口占用检测。
// 固定 loopback，不碰外部接口。
func netListen(port int) (net.Listener, error) {
	return net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
}

// LoopbackListener 创建仅监听回环地址的监听器。
//
// 这是安全模型的第一道防线：DevDock 永远不允许监听 0.0.0.0 或 [::]。
// 返回前断言地址确实是回环，防止未来改动意外引入外网暴露。
func LoopbackListener(port int) (net.Listener, error) {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok || !addr.IP.IsLoopback() {
		ln.Close()
		return nil, fmt.Errorf("监听地址 %v 不是回环地址，已拒绝启动", ln.Addr())
	}
	return ln, nil
}
