// Package winproc 是 DevDock 唯一允许直接调用 Win32 的包。
//
// 设计要点（均由本机实测验证，不是推测）：
//
//  1. 用标准库 syscall.NewLazyDLL 直接调 Win32，不引入 golang.org/x/sys，
//     因此整个项目零外部依赖、可完全离线构建。
//  2. 子进程用 CREATE_SUSPENDED + PROC_THREAD_ATTRIBUTE_JOB_LIST 启动，
//     即"一出生就在 job 内"，没有事后 Assign 的逃逸窗口。
//  3. HANDLE_LIST 中的句柄必须互不相同 —— 重复句柄会让 CreateProcess 直接
//     返回 ERROR_INVALID_PARAMETER(87)。所以 stderr 用 DuplicateHandle 得到的
//     独立副本，而 stdout/stderr 仍指向同一管道写端，读端天然合并两路输出。
//  4. 进程身份 = PID + 创建时间（FILETIME）。任何 kill / 存活判断都先校验身份，
//     时间戳不符即认为目标已消失，绝不误杀被系统复用了 PID 的无关进程。
package winproc

import (
	"errors"
	"fmt"
	"io"
	"syscall"
	"unsafe"
)

var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procCreateJobObjectW             = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject      = kernel32.NewProc("SetInformationJobObject")
	procQueryInformationJobObject    = kernel32.NewProc("QueryInformationJobObject")
	procTerminateJobObject           = kernel32.NewProc("TerminateJobObject")
	procCreateProcessW               = kernel32.NewProc("CreateProcessW")
	procInitializeProcThreadAttrList = kernel32.NewProc("InitializeProcThreadAttributeList")
	procUpdateProcThreadAttribute    = kernel32.NewProc("UpdateProcThreadAttribute")
	procDeleteProcThreadAttrList     = kernel32.NewProc("DeleteProcThreadAttributeList")
	procResumeThread                 = kernel32.NewProc("ResumeThread")
	procCreatePipe                   = kernel32.NewProc("CreatePipe")
	procSetHandleInformation         = kernel32.NewProc("SetHandleInformation")
	procDuplicateHandle              = kernel32.NewProc("DuplicateHandle")
	procGetCurrentProcess            = kernel32.NewProc("GetCurrentProcess")
	procCreateFileW                  = kernel32.NewProc("CreateFileW")
	procOpenProcess                  = kernel32.NewProc("OpenProcess")
	procGetExitCodeProcess           = kernel32.NewProc("GetExitCodeProcess")
	procWaitForSingleObject          = kernel32.NewProc("WaitForSingleObject")
	procGetProcessTimes              = kernel32.NewProc("GetProcessTimes")
	procReadFile                     = kernel32.NewProc("ReadFile")
	procCloseHandle                  = kernel32.NewProc("CloseHandle")
	procCreateMutexW                 = kernel32.NewProc("CreateMutexW")
	procCreateToolhelp32Snapshot     = kernel32.NewProc("CreateToolhelp32Snapshot")
	procProcess32FirstW              = kernel32.NewProc("Process32FirstW")
	procProcess32NextW               = kernel32.NewProc("Process32NextW")
	procAssignProcessToJobObject     = kernel32.NewProc("AssignProcessToJobObject")
)

// Win32 常量。
const (
	createSuspended            = 0x00000004
	createNoWindow             = 0x08000000
	createUnicodeEnvironment   = 0x00000400
	extendedStartupinfoPresent = 0x00080000

	procThreadAttributeJobList    = 0x0002000D
	procThreadAttributeHandleList = 0x00020002

	// jobObjectExtendedLimitInformationClass 是 JOBOBJECTINFOCLASS 枚举值 9。
	jobObjectExtendedLimitInformationClass = 9
	jobObjectLimitKillOnJobClose           = 0x00002000

	handleFlagInherit   = 0x00000001
	startfUseStdHandles = 0x00000100

	processQueryLimitedInformation = 0x1000
	processTerminate               = 0x0001
	processSetQuota                = 0x0100

	stillActive        = 259
	errorAlreadyExists = 183

	// WaitForSingleObject 的等待参数与返回值。
	waitInfinite = 0xFFFFFFFF
	waitObject0  = 0

	duplicateSameAccess = 0x00000002
	genericRead         = 0x80000000
	genericWrite        = 0x40000000
	createAlways        = 2
	openExisting        = 3
	fileAttributeNormal = 0x00000080

	th32csSnapProcess = 0x00000002
	maxPath           = 260
)

// Identity 是进程身份令牌。CreationTime 为 0 表示无效令牌。
type Identity struct {
	PID          uint32 `json:"pid"`
	CreationTime uint64 `json:"creationTime"`
}

// Valid 报告令牌是否可用于校验。
func (id Identity) Valid() bool { return id.PID != 0 && id.CreationTime != 0 }

// String 便于日志与审计输出。
func (id Identity) String() string {
	return fmt.Sprintf("pid=%d ct=%d", id.PID, id.CreationTime)
}

// StartupInfoEx 对应 Win32 STARTUPINFOEXW。
type StartupInfoEx struct {
	StartupInfo   syscall.StartupInfo
	AttributeList *byte
}

// ProcessInformation 对应 Win32 PROCESS_INFORMATION。
type ProcessInformation struct {
	Process   syscall.Handle
	Thread    syscall.Handle
	ProcessId uint32
	ThreadId  uint32
}

type jobObjectBasicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type ioCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

type jobObjectExtendedLimitInformation struct {
	BasicLimitInformation jobObjectBasicLimitInformation
	IoInfo                ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

// basicAccountingInformation 用于 QueryInformationJobObject 读取活跃进程数。
type jobObjectBasicAccountingInformation struct {
	TotalUserTime             int64
	TotalKernelTime           int64
	ThisPeriodTotalUserTime   int64
	ThisPeriodTotalKernelTime int64
	TotalPageFaultCount       uint32
	TotalProcesses            uint32
	ActiveProcesses           uint32
	TotalTerminatedProcesses  uint32
}

const jobObjectBasicAccountingInformationClass = 1

// errnoErr 把 LazyProc.Call 返回的 error 规范化为 nil（errno 0）。
func errnoErr(e error) error {
	if e == nil {
		return nil
	}
	if errno, ok := e.(syscall.Errno); ok && errno == 0 {
		return nil
	}
	return e
}

// alignedBuffer 分配至少 8 字节对齐的缓冲。
// PROC_THREAD_ATTRIBUTE_LIST 内部是带指针对齐要求的结构，
// Go 的 make([]byte) 不保证对齐，故显式补 padding。
func alignedBuffer(n uintptr) []byte {
	raw := make([]byte, n+8)
	pad := (8 - uintptr(unsafe.Pointer(&raw[0]))%8) % 8
	return raw[pad : pad+n]
}

// Job 封装一个 Windows Job Object。
// 它持有 KILL_ON_JOB_CLOSE 语义：一旦关闭句柄，内核会终止 job 内全部进程。
type Job struct {
	h syscall.Handle
}

// NewJob 创建带 KILL_ON_JOB_CLOSE 的 job。
//
// 不设置 BREAKAWAY_OK，因此子进程无法主动脱离 —— 这是"整树必被清理"的前提。
// 句柄不继承（CreateJobObjectW 传 NULL 安全属性），否则子进程持有句柄会让
// KILL_ON_JOB_CLOSE 在我们关闭句柄后仍然不触发。
func NewJob() (*Job, error) {
	hh, _, e := procCreateJobObjectW.Call(0, 0)
	if hh == 0 {
		return nil, fmt.Errorf("CreateJobObjectW 失败: %w", errnoErr(e))
	}
	info := jobObjectExtendedLimitInformation{}
	info.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnJobClose
	r, _, e := procSetInformationJobObject.Call(hh, jobObjectExtendedLimitInformationClass,
		uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info))
	if r == 0 {
		procCloseHandle.Call(hh)
		return nil, fmt.Errorf("SetInformationJobObject 失败: %w", errnoErr(e))
	}
	return &Job{h: syscall.Handle(hh)}, nil
}

// Handle 返回底层 job 句柄。调用方不应关闭它。
func (j *Job) Handle() syscall.Handle {
	if j == nil {
		return 0
	}
	return j.h
}

// ActiveProcesses 返回 job 内当前活跃进程数。
func (j *Job) ActiveProcesses() (uint32, error) {
	if j == nil || j.h == 0 {
		return 0, errors.New("job 已关闭")
	}
	var info jobObjectBasicAccountingInformation
	r, _, e := procQueryInformationJobObject.Call(uintptr(j.h),
		jobObjectBasicAccountingInformationClass,
		uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info), 0)
	if r == 0 {
		return 0, fmt.Errorf("QueryInformationJobObject 失败: %w", errnoErr(e))
	}
	return info.ActiveProcesses, nil
}

// Terminate 终止 job 内全部进程。
// exitCode 固定为 0：这是一个受控停止，不应被上层误判为崩溃。
func (j *Job) Terminate() error {
	if j == nil || j.h == 0 {
		return nil
	}
	r, _, e := procTerminateJobObject.Call(uintptr(j.h), 0)
	if r == 0 {
		return fmt.Errorf("TerminateJobObject 失败: %w", errnoErr(e))
	}
	return nil
}

// Close 关闭 job 句柄。由 KILL_ON_JOB_CLOSE，这本身即兜底终止整棵树。
// 多次调用安全。
func (j *Job) Close() {
	if j == nil || j.h == 0 {
		return
	}
	procCloseHandle.Call(uintptr(j.h))
	j.h = 0
}

// Assign 把已存在的进程加入 job。仅在无法用 JOB_LIST 启动时作为补偿手段。
func (j *Job) Assign(pid uint32) error {
	h, err := OpenProcess(processSetQuota|processTerminate, pid)
	if err != nil {
		return err
	}
	defer procCloseHandle.Call(uintptr(h))
	r, _, e := procAssignProcessToJobObject.Call(uintptr(j.h), uintptr(h))
	if r == 0 {
		return fmt.Errorf("AssignProcessToJobObject 失败: %w", errnoErr(e))
	}
	return nil
}

// OpenProcess 以给定访问权限打开进程。
func OpenProcess(access uint32, pid uint32) (syscall.Handle, error) {
	h, _, e := procOpenProcess.Call(uintptr(access), 0, uintptr(pid))
	if h == 0 {
		return 0, fmt.Errorf("OpenProcess(%d) 失败: %w", pid, errnoErr(e))
	}
	return syscall.Handle(h), nil
}

// CaptureIdentity 抓取进程身份令牌。
func CaptureIdentity(pid uint32) (Identity, error) {
	h, err := OpenProcess(processQueryLimitedInformation, pid)
	if err != nil {
		return Identity{}, err
	}
	defer procCloseHandle.Call(uintptr(h))
	var creation, exit, kernel, user syscall.Filetime
	r, _, e := procGetProcessTimes.Call(uintptr(h),
		uintptr(unsafe.Pointer(&creation)), uintptr(unsafe.Pointer(&exit)),
		uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user)))
	if r == 0 {
		return Identity{}, fmt.Errorf("GetProcessTimes(%d) 失败: %w", pid, errnoErr(e))
	}
	ct := uint64(creation.HighDateTime)<<32 | uint64(creation.LowDateTime)
	return Identity{PID: pid, CreationTime: ct}, nil
}

// VerifyIdentity 校验 pid 当前是否仍是令牌描述的那个进程。
//
// 这是防 PID 复用误杀的唯一依据：PID 相同但创建时间不同 => 目标已消失。
func VerifyIdentity(id Identity) bool {
	if !id.Valid() {
		return false
	}
	cur, err := CaptureIdentity(id.PID)
	if err != nil {
		return false
	}
	return cur.CreationTime == id.CreationTime
}

// IsAlive 判断令牌描述的进程是否仍存活且身份未变。
func IsAlive(id Identity) bool {
	if !VerifyIdentity(id) {
		return false
	}
	h, err := OpenProcess(processQueryLimitedInformation, id.PID)
	if err != nil {
		return false
	}
	defer procCloseHandle.Call(uintptr(h))
	var code uint32
	r, _, e := procGetExitCodeProcess.Call(uintptr(h), uintptr(unsafe.Pointer(&code)))
	if r == 0 {
		return false
	}
	_ = e
	return code == stillActive
}

// ExitCode 读取已退出进程的退出码。
// 若进程仍在运行，返回 (0, false, nil)。
func ExitCode(id Identity) (code uint32, exited bool, err error) {
	h, oerr := OpenProcess(processQueryLimitedInformation, id.PID)
	if oerr != nil {
		return 0, true, oerr
	}
	defer procCloseHandle.Call(uintptr(h))
	var c uint32
	r, _, e := procGetExitCodeProcess.Call(uintptr(h), uintptr(unsafe.Pointer(&c)))
	if r == 0 {
		return 0, true, fmt.Errorf("GetExitCodeProcess 失败: %w", errnoErr(e))
	}
	if c == stillActive {
		return 0, false, nil
	}
	return c, true, nil
}

// SpawnOptions 描述一次启动请求。
type SpawnOptions struct {
	// CommandLine 交给 cmd.exe /d /s /c 执行，因此支持 "npm run dev" 这类写法。
	CommandLine string
	// Cwd 为工作目录，必须已存在。
	Cwd string
	// Env 是完整的环境变量块（调用方负责基于 os.Environ() 合并覆盖）。
	Env []string
}

// SpawnResult 是一次成功启动的结果。
type SpawnResult struct {
	Identity Identity
	// Output 是合并后的 stdout+stderr 读端。调用方负责关闭。
	Output *OutputPipe
	// Process 是子进程句柄，必须保留到项目结束再关闭。
	// 保留它才能在其退出后读到真实退出码（否则进程对象已被销毁）。
	Process syscall.Handle
}

// WaitExitCode 等待进程退出并返回其退出码。
//
// 必须在持有 Process 句柄时调用：句柄关闭后进程对象随之销毁，
// 退出码再也读不到。仅当我们主动终止（job 清理）导致句柄失效时才返回错误。
func (r SpawnResult) WaitExitCode() (uint32, error) {
	if r.Process == 0 {
		return 0, errors.New("进程句柄已关闭，无法读取退出码")
	}
	r0, _, e := procWaitForSingleObject.Call(uintptr(r.Process), uintptr(waitInfinite))
	if r0 != waitObject0 {
		return 0, fmt.Errorf("WaitForSingleObject 失败: %w", errnoErr(e))
	}
	var code uint32
	rc, _, re := procGetExitCodeProcess.Call(uintptr(r.Process), uintptr(unsafe.Pointer(&code)))
	if rc == 0 {
		return 0, fmt.Errorf("GetExitCodeProcess 失败: %w", errnoErr(re))
	}
	return code, nil
}

// Close 释放子进程句柄。多次调用安全。
func (r SpawnResult) CloseProcess() {
	if r.Process != 0 {
		procCloseHandle.Call(uintptr(r.Process))
	}
}

// OutputPipe 包装管道读端。
type OutputPipe struct {
	h syscall.Handle
}

// Handle 返回底层管道句柄。
func (o *OutputPipe) Handle() syscall.Handle {
	if o == nil {
		return 0
	}
	return o.h
}

// Close 关闭读端。
func (o *OutputPipe) Close() error {
	if o == nil || o.h == 0 {
		return nil
	}
	procCloseHandle.Call(uintptr(o.h))
	o.h = 0
	return nil
}

// Read 实现 io.Reader。
//
// 直接 syscall.Read 在匿名管道上当写端仍打开时会立即返回 0（非阻塞语义），
// 导致日志循环空转。用 ReadFile 才是阻塞读 —— 它会一直等到有数据或写端全部关闭。
func (o *OutputPipe) Read(p []byte) (int, error) {
	if o == nil || o.h == 0 {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	var n uint32
	r, _, e := procReadFile.Call(uintptr(o.h), uintptr(unsafe.Pointer(&p[0])),
		uintptr(len(p)), uintptr(unsafe.Pointer(&n)), 0)
	if r == 0 {
		// 写端全部关闭后 ReadFile 返回 ERROR_BROKEN_PIPE，这是正常的流结束。
		if errno, ok := e.(syscall.Errno); ok {
			if errno == 109 /* ERROR_BROKEN_PIPE */ || errno == 232 /* ERROR_NO_DATA */ {
				return int(n), io.EOF
			}
		}
		return int(n), errnoErr(e)
	}
	if n == 0 {
		return 0, io.EOF
	}
	return int(n), nil
}

// buildEnvBlock 生成 CreateProcessW 需要的双 NUL 结尾 UTF-16 环境块。
func buildEnvBlock(env []string) (*uint16, error) {
	if len(env) == 0 {
		return nil, nil
	}
	var units []uint16
	for _, kv := range env {
		if kv == "" {
			continue
		}
		u, err := syscall.UTF16FromString(kv)
		if err != nil {
			// 环境变量里含 NUL 时跳过而不是整体失败。
			continue
		}
		units = append(units, u...) // UTF16FromString 已含结尾 NUL
	}
	units = append(units, 0) // 整个块的双 NUL 结尾
	return &units[0], nil
}

// lastError 便于在上层构造中文错误时保留原始 errno。
func lastError(e error) error { return errnoErr(e) }

var _ = lastError

