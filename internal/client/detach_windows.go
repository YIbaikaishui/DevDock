package client

import (
	"os/exec"
	"syscall"
)

// detach 让子进程脱离当前控制台，使 CLI 退出时后台服务不被一起终止。
//
// CREATE_NEW_PROCESS_GROUP 让服务不接收当前控制台的 Ctrl+C，
// DETACHED_PROCESS 让它不持有控制台窗口。
func detach(cmd *exec.Cmd) {
	const (
		createNewProcessGroup = 0x00000200
		detachedProcess       = 0x00000008
	)
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags = createNewProcessGroup | detachedProcess
}
