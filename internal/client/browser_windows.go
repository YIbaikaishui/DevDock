package client

import (
	"fmt"
	"os/exec"
)

// openInBrowser 用系统默认浏览器打开 URL。
// 通过 rundll32 而不是 cmd start，避免经过 shell 解析带来的引号问题。
func openInBrowser(url string) error {
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("调用系统浏览器失败: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// OpenInBrowser 是导出包装，供 main 使用。
func OpenInBrowser(url string) error { return openInBrowser(url) }
