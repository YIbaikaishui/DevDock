//go:build windows

package detect

import (
	"syscall"
	"unsafe"
)

// DRIVE_FIXED 是 GetDriveTypeW 对「本地固定盘」的返回值。
// 其余取值（可移动/网络/光驱/无根目录）一律跳过：扫描绝不能挂在网络盘上。
const driveFixed = 3

// fixedDriveRoots 返回所有存在且类型为 DRIVE_FIXED 的盘根，如 `C:\`、`D:\`。
//
// 用标准库 syscall 直接调 Win32，和 internal/winproc 的做法一致，
// 不引入 golang.org/x/sys。
func fixedDriveRoots() []string {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getDriveType := kernel32.NewProc("GetDriveTypeW")

	out := make([]string, 0, 4)
	for c := 'A'; c <= 'Z'; c++ {
		root := string(c) + `:\`
		ptr, err := syscall.UTF16PtrFromString(root)
		if err != nil {
			continue
		}
		r, _, _ := getDriveType.Call(uintptr(unsafe.Pointer(ptr)))
		if r == driveFixed {
			out = append(out, root)
		}
	}
	return out
}
