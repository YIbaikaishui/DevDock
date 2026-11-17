package winproc

import (
	"fmt"
	"syscall"
	"unsafe"
)

// inheritiableNul 打开 NUL 设备的可继承写句柄，用作子进程 stdin。
//
// 必须可继承：HANDLE_LIST 里列出的句柄会被要求是 inheritable 的，
// 否则 CreateProcess 返回 ERROR_INVALID_PARAMETER。
func inheritiableNul() (syscall.Handle, error) {
	name, err := syscall.UTF16PtrFromString("NUL")
	if err != nil {
		return 0, err
	}
	var sa syscall.SecurityAttributes
	sa.Length = uint32(unsafe.Sizeof(sa))
	sa.InheritHandle = 1
	h, _, e := procCreateFileW.Call(uintptr(unsafe.Pointer(name)),
		genericRead|genericWrite, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE,
		uintptr(unsafe.Pointer(&sa)), openExisting, 0, 0)
	if h == 0 || h == uintptr(syscall.InvalidHandle) {
		return 0, fmt.Errorf("打开 NUL 设备失败: %w", errnoErr(e))
	}
	return syscall.Handle(h), nil
}

// Spawn 在 job 内以 suspended 状态创建进程，并在恢复执行前完成全部设置。
//
// 顺序不可调换：CreateProcess(CREATE_SUSPENDED) 让子进程"存在但未执行"，
// 此时它已经在 job 内，因此不存在任何逃逸窗口；随后才 ResumeThread。
func Spawn(job *Job, opts SpawnOptions) (SpawnResult, error) {
	if job == nil || job.Handle() == 0 {
		return SpawnResult{}, fmt.Errorf("job 无效，拒绝启动")
	}
	if opts.CommandLine == "" {
		return SpawnResult{}, fmt.Errorf("启动命令为空")
	}

	// 通过 cmd.exe /d /s /c 执行，让用户可以用 set FOO=1 && npm run dev 这类写法。
	// /d 跳过 AutoRun，/s 简化引号处理。
	cmdLine := `cmd.exe /d /s /c "` + opts.CommandLine + `"`
	argv, err := syscall.UTF16PtrFromString(cmdLine)
	if err != nil {
		return SpawnResult{}, fmt.Errorf("命令行包含非法字符: %w", err)
	}

	// --- 管道：stdout 与 stderr 合并到同一写端，读端只读一次 ---
	var sa syscall.SecurityAttributes
	sa.Length = uint32(unsafe.Sizeof(sa))
	sa.InheritHandle = 1
	var readEnd, writeEnd syscall.Handle
	r, _, e := procCreatePipe.Call(uintptr(unsafe.Pointer(&readEnd)),
		uintptr(unsafe.Pointer(&writeEnd)), uintptr(unsafe.Pointer(&sa)), 0)
	if r == 0 {
		return SpawnResult{}, fmt.Errorf("CreatePipe 失败: %w", errnoErr(e))
	}
	// 读端自己不继承，否则子进程会持有读端，导致写端关闭后我们仍读不到 EOF。
	procSetHandleInformation.Call(uintptr(readEnd), handleFlagInherit, 0)

	cleanup := func() {
		procCloseHandle.Call(uintptr(readEnd))
		procCloseHandle.Call(uintptr(writeEnd))
	}

	// --- stderr 用 writeEnd 的独立副本 ---
	// 关键：HANDLE_LIST 中若出现重复句柄，CreateProcess 会返回 ERROR_INVALID_PARAMETER(87)。
	// 用 DuplicateHandle 得到一个句柄值不同的副本，两者指向同一个管道，输出天然合并。
	self, _, _ := procGetCurrentProcess.Call()
	var errEnd syscall.Handle
	r, _, e = procDuplicateHandle.Call(self, uintptr(writeEnd), self,
		uintptr(unsafe.Pointer(&errEnd)), 0, 1, duplicateSameAccess)
	if r == 0 {
		cleanup()
		return SpawnResult{}, fmt.Errorf("DuplicateHandle(stderr) 失败: %w", errnoErr(e))
	}

	nul, err := inheritiableNul()
	if err != nil {
		procCloseHandle.Call(uintptr(errEnd))
		cleanup()
		return SpawnResult{}, err
	}

	// 关掉父进程持有的写端副本，保证读端能读到 EOF。
	closeWriteEnds := func() {
		procCloseHandle.Call(uintptr(writeEnd))
		procCloseHandle.Call(uintptr(errEnd))
	}

	// --- 属性列表：JOB_LIST + HANDLE_LIST ---
	var attrSize uintptr
	procInitializeProcThreadAttrList.Call(0, 2, 0, uintptr(unsafe.Pointer(&attrSize)))
	if attrSize == 0 {
		procCloseHandle.Call(uintptr(nul))
		closeWriteEnds()
		cleanup()
		return SpawnResult{}, fmt.Errorf("InitializeProcThreadAttributeList 未返回所需大小")
	}
	attrBuf := alignedBuffer(attrSize)
	// 保留 *byte 引用而不是 uintptr：uintptr 不被 GC 视为活引用，
	// 纯 uintptr 往返会同时在 go vet 报 unsafe.Pointer misuse，且缓冲可能被回收。
	attrPtr := &attrBuf[0]
	attrBase := uintptr(unsafe.Pointer(attrPtr))
	r, _, e = procInitializeProcThreadAttrList.Call(attrBase, 2, 0,
		uintptr(unsafe.Pointer(&attrSize)))
	if r == 0 {
		procCloseHandle.Call(uintptr(nul))
		closeWriteEnds()
		cleanup()
		return SpawnResult{}, fmt.Errorf("InitializeProcThreadAttributeList 失败: %w", errnoErr(e))
	}
	defer procDeleteProcThreadAttrList.Call(attrBase)

	jobHandle := job.Handle()
	r, _, e = procUpdateProcThreadAttribute.Call(attrBase, 0, procThreadAttributeJobList,
		uintptr(unsafe.Pointer(&jobHandle)), unsafe.Sizeof(jobHandle), 0, 0)
	if r == 0 {
		procCloseHandle.Call(uintptr(nul))
		closeWriteEnds()
		cleanup()
		return SpawnResult{}, fmt.Errorf("设置 JOB_LIST 属性失败: %w", errnoErr(e))
	}

	handles := []syscall.Handle{nul, writeEnd, errEnd} // 三者互不相同
	r, _, e = procUpdateProcThreadAttribute.Call(attrBase, 0, procThreadAttributeHandleList,
		uintptr(unsafe.Pointer(&handles[0])),
		unsafe.Sizeof(handles[0])*uintptr(len(handles)), 0, 0)
	if r == 0 {
		procCloseHandle.Call(uintptr(nul))
		closeWriteEnds()
		cleanup()
		return SpawnResult{}, fmt.Errorf("设置 HANDLE_LIST 属性失败: %w", errnoErr(e))
	}

	envBlock, err := buildEnvBlock(opts.Env)
	if err != nil {
		procCloseHandle.Call(uintptr(nul))
		closeWriteEnds()
		cleanup()
		return SpawnResult{}, err
	}

	var cwdPtr *uint16
	if opts.Cwd != "" {
		cwdPtr, err = syscall.UTF16PtrFromString(opts.Cwd)
		if err != nil {
			procCloseHandle.Call(uintptr(nul))
			closeWriteEnds()
			cleanup()
			return SpawnResult{}, fmt.Errorf("工作目录包含非法字符: %w", err)
		}
	}

	var si StartupInfoEx
	si.StartupInfo.Cb = uint32(unsafe.Sizeof(si))
	si.StartupInfo.Flags = startfUseStdHandles
	si.StartupInfo.StdInput = nul
	si.StartupInfo.StdOutput = writeEnd
	si.StartupInfo.StdErr = errEnd
	si.AttributeList = attrPtr

	var pi ProcessInformation
	r, _, e = procCreateProcessW.Call(
		0,
		uintptr(unsafe.Pointer(argv)),
		0, 0,
		1, // bInheritHandles：HANDLE_LIST 生效的前提
		createSuspended|createNoWindow|createUnicodeEnvironment|extendedStartupinfoPresent,
		uintptr(unsafe.Pointer(envBlock)),
		uintptr(unsafe.Pointer(cwdPtr)),
		uintptr(unsafe.Pointer(&si)),
		uintptr(unsafe.Pointer(&pi)),
	)
	if r == 0 {
		procCloseHandle.Call(uintptr(nul))
		closeWriteEnds()
		cleanup()
		return SpawnResult{}, fmt.Errorf("CreateProcess 失败: %w", errnoErr(e))
	}

	// 子进程此刻已存在且已在 job 内，但未执行任何指令。先抓身份令牌。
	identity, idErr := CaptureIdentity(pi.ProcessId)

	// 恢复到正常执行。
	procResumeThread.Call(uintptr(pi.Thread))
	procCloseHandle.Call(uintptr(pi.Thread))
	// 注意：进程句柄必须保留到项目结束。
	// 一旦关闭，进程对象被内核销毁，之后再也读不到真实退出码
	// （GetExitCodeProcess 只会失败，我们会误判成"退出码 0"，
	// 于是"启动即崩"永远检测不出来）。顺带的好处是：句柄在，
	// PID 就不会被系统复用，身份令牌更稳。
	procCloseHandle.Call(uintptr(nul))
	// 父进程必须放弃写端，否则读端永远等不到 EOF。
	closeWriteEnds()

	if idErr != nil {
		// 拿不到身份就无法安全停止，立即整树清理并报错。
		procCloseHandle.Call(uintptr(pi.Process))
		job.Terminate()
		cleanup()
		return SpawnResult{}, fmt.Errorf("无法获取子进程身份: %w", idErr)
	}

	return SpawnResult{Identity: identity, Output: &OutputPipe{h: readEnd}, Process: syscall.Handle(pi.Process)}, nil
}

// ListProcesses 返回 pid -> parentPID 的快照，用于诊断与验收脚本。
func ListProcesses() (map[uint32]uint32, error) {
	snap, _, e := procCreateToolhelp32Snapshot.Call(th32csSnapProcess, 0)
	if snap == 0 || snap == uintptr(syscall.InvalidHandle) {
		return nil, fmt.Errorf("CreateToolhelp32Snapshot 失败: %w", errnoErr(e))
	}
	defer procCloseHandle.Call(snap)

	type processEntry32 struct {
		Size              uint32
		CntUsage          uint32
		ProcessID         uint32
		DefaultHeapID     uintptr
		ModuleID          uint32
		CntThreads        uint32
		ParentProcessID   uint32
		PriClassBase      int32
		Flags             uint32
		ExeFile           [maxPath]uint16
	}

	out := map[uint32]uint32{}
	var pe processEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	r, _, _ := procProcess32FirstW.Call(snap, uintptr(unsafe.Pointer(&pe)))
	for r != 0 {
		out[pe.ProcessID] = pe.ParentProcessID
		pe.Size = uint32(unsafe.Sizeof(pe))
		r, _, _ = procProcess32NextW.Call(snap, uintptr(unsafe.Pointer(&pe)))
	}
	return out, nil
}

// Descendants 返回 root 的全部后代 PID（含多层）。
// 仅用于诊断与验收；真正的清理依赖 job，不依赖父子关系遍历。
func Descendants(root uint32) []uint32 {
	parent, err := ListProcesses()
	if err != nil {
		return nil
	}
	seen := map[uint32]bool{root: true}
	var out []uint32
	frontier := []uint32{root}
	for len(frontier) > 0 {
		var next []uint32
		for _, p := range frontier {
			for pid, pp := range parent {
				if pp == p && !seen[pid] {
					seen[pid] = true
					out = append(out, pid)
					next = append(next, pid)
				}
			}
		}
		frontier = next
	}
	return out
}
