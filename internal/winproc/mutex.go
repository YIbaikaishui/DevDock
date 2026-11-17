package winproc

import (
	"fmt"
	"syscall"
	"unsafe"
)

// Mutex 是一个命名互斥量，用于单实例判定。
type Mutex struct {
	h syscall.Handle
}

// AcquireMutex 尝试获取命名互斥量。
//
// 返回 (mutex, true, nil) 表示本次是唯一实例，调用方需在退出时 Close。
// 返回 (nil, false, nil) 表示已有实例在运行。
//
// 注意：不能直接用 syscall 里对 CreateMutex 的封装来判断"已存在"，
// 因为已存在时它会把 ERROR_ALREADY_EXISTS 当成错误返回，导致判断逻辑写错。
// 这里直接读 last error，语义明确。
func AcquireMutex(name string) (*Mutex, bool, error) {
	n, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return nil, false, fmt.Errorf("互斥量名非法: %w", err)
	}
	h, _, e := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(n)))
	if h == 0 {
		return nil, false, fmt.Errorf("CreateMutexW 失败: %w", errnoErr(e))
	}
	if errnoErr(e) != nil {
		if errno, ok := e.(syscall.Errno); ok && errno == errorAlreadyExists {
			// 句柄有效但实例已存在：释放自己的句柄并报告非唯一。
			procCloseHandle.Call(h)
			return nil, false, nil
		}
		procCloseHandle.Call(h)
		return nil, false, fmt.Errorf("CreateMutexW 失败: %w", e)
	}
	return &Mutex{h: syscall.Handle(h)}, true, nil
}

// Close 释放互斥量句柄。
func (m *Mutex) Close() {
	if m == nil || m.h == 0 {
		return
	}
	procCloseHandle.Call(uintptr(m.h))
	m.h = 0
}
