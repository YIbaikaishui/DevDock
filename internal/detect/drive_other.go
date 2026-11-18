//go:build !windows

package detect

import "os"

// fixedDriveRoots 在非 Windows 上退化为「根下常见的挂载点」，仅用于让本包可编译。
// DevDock 只支持 Windows，真正的盘类型判定见 drive_windows.go。
func fixedDriveRoots() []string {
	out := make([]string, 0, 2)
	for _, p := range []string{"/home", "/mnt", "/Volumes"} {
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			out = append(out, p)
		}
	}
	return out
}
