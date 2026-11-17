package winproc

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

var (
	psapi                    = syscall.NewLazyDLL("psapi.dll")
	procGetProcessMemoryInfo = psapi.NewProc("GetProcessMemoryInfo")
)

type processMemoryCounters struct {
	Cb                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

// MemoryInfo 是进程内存快照。
type MemoryInfo struct {
	// WorkingSetSize 是主口径（任务管理器"内存"列）。
	WorkingSetSize uint64 `json:"workingSetBytes"`
	// PrivateBytes 对应 PagefileUsage，是参考口径。
	PrivateBytes uint64 `json:"privateBytes"`
}

// ProcessMemory 读取指定进程的内存占用。
func ProcessMemory(pid uint32) (MemoryInfo, error) {
	h, err := OpenProcess(processQueryLimitedInformation, pid)
	if err != nil {
		return MemoryInfo{}, err
	}
	defer procCloseHandle.Call(uintptr(h))
	var c processMemoryCounters
	c.Cb = uint32(unsafe.Sizeof(c))
	r, _, e := procGetProcessMemoryInfo.Call(uintptr(h),
		uintptr(unsafe.Pointer(&c)), uintptr(c.Cb))
	if r == 0 {
		return MemoryInfo{}, fmt.Errorf("GetProcessMemoryInfo 失败: %w", errnoErr(e))
	}
	return MemoryInfo{
		WorkingSetSize: uint64(c.WorkingSetSize),
		PrivateBytes:   uint64(c.PagefileUsage),
	}, nil
}

// SelfMemory 读取当前进程内存占用。
func SelfMemory() (MemoryInfo, error) {
	return ProcessMemory(uint32(os.Getpid()))
}
