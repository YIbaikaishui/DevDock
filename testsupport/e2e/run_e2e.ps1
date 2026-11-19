<#
.SYNOPSIS
    DevDock 端到端验收脚本。

.DESCRIPTION
    用 testsupport/fixture 起一个真实受管项目，验证完整链路：
        启动 -> 进程真的起来 -> HTTP 业务就绪 -> 日志可读 -> 停止 -> 整棵进程树消失

    进程层级刻意做成三层，只有三层都消失才算"整树清理"通过：
        cmd.exe（DevDock 的根进程） -> fixture.exe（业务进程） -> fixture.exe -child（孙进程）

    全程使用隔离的 APPDATA，不会读写真实 %APPDATA%\DevDock，
    也不影响机器上其它正在运行的实例。

.NOTES
    需要 Go 1.25+。退出码 0 表示全部通过，1 表示有断言失败。
#>

$ErrorActionPreference = 'Stop'

$WorkRoot = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
if (-not (Test-Path (Join-Path $WorkRoot 'go.mod'))) {
    $WorkRoot = 'D:\Desktop\DevDock\work'
}

$E2E = Join-Path $WorkRoot '_e2e'
$Bin = Join-Path $E2E 'bin'
$AppData = Join-Path $E2E 'appdata'
$DevDockExe = Join-Path $Bin 'DevDock.exe'
$FixtureExe = Join-Path $Bin 'fixture.exe'

$Failures = New-Object System.Collections.Generic.List[string]
$ServerProc = $null
$IdleWorkingSetMiB = 0

function Step($msg) { Write-Host "`n=== $msg ===" -ForegroundColor Cyan }
function Info($msg) { Write-Host "    $msg" }
function Pass($msg) { Write-Host "  PASS  $msg" -ForegroundColor Green }
function Fail($msg) {
    Write-Host "  FAIL  $msg" -ForegroundColor Red
    $script:Failures.Add($msg)
}

# Get-FreePort 让系统分配一个空闲端口，避免和机器上其它服务撞车。
function Get-FreePort {
    $l = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
    $l.Start()
    $p = $l.LocalEndpoint.Port
    $l.Stop()
    return $p
}

# Test-PortOpen 判断端口上是否有人监听。
function Test-PortOpen([int]$Port) {
    $client = New-Object System.Net.Sockets.TcpClient
    try {
        $iar = $client.BeginConnect('127.0.0.1', $Port, $null, $null)
        if (-not $iar.AsyncWaitHandle.WaitOne(500)) { return $false }
        $client.EndConnect($iar)
        return $true
    } catch {
        return $false
    } finally {
        $client.Close()
    }
}

# Test-PidAlive 判断进程是否还活着。
function Test-PidAlive([int]$ProcessId) {
    if ($ProcessId -le 0) { return $false }
    $p = Get-Process -Id $ProcessId -ErrorAction SilentlyContinue
    return ($null -ne $p)
}

# Invoke-DevDock 调用 CLI，返回 @{ Code; Output }。
#
# 保留 `& $exe @CliArgs 2>&1` 的数组 splatting：Start-Process -ArgumentList
# 会把数组重新拼成一行命令行，带空格的 --command 值会被拆裂（实测）。
# 只在调用期间把 $ErrorActionPreference 临时降为 Continue：否则原生命令写的
# stderr 会被 PowerShell 包成 ErrorRecord 并抛异常，`remove` 一个不存在的项目
# 就会直接终止整个脚本。
function Invoke-DevDock {
    param([string[]]$CliArgs)
    $prev = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        $out = & $DevDockExe @CliArgs 2>&1
        $code = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $prev
    }
    return @{ Code = $code; Output = ($out | Out-String) }
}

# Stop-PriorServer 结束上一次运行遗留的服务进程。
#
# 只匹配本脚本自己编译出的那个 exe（$E2E\bin\DevDock.exe），
# 不会影响用户其它位置安装的 DevDock，也不会碰真实 %APPDATA% 下的实例。
# 不做这一步的话，上轮残留的进程会占着单实例互斥量，
# 导致本轮新起的实例直接把自己当成"第二个实例"而退出。
function Stop-PriorServer {
    $stale = @(Get-CimInstance Win32_Process -Filter "Name='DevDock.exe'" -ErrorAction SilentlyContinue |
        Where-Object { $_.ExecutablePath -ieq $DevDockExe })
    foreach ($p in $stale) {
        Write-Host "    清理上轮遗留实例 PID $($p.ProcessId)" -ForegroundColor Yellow
        Stop-Process -Id $p.ProcessId -Force -ErrorAction SilentlyContinue
    }
    if ($stale.Count -gt 0) { Start-Sleep -Milliseconds 500 }
}

try {
    # ---------- 准备 ----------
    Step '准备：编译 DevDock 与夹具'
    New-Item -ItemType Directory -Force -Path $Bin, $AppData | Out-Null
    Push-Location $WorkRoot
    try {
        & go build -trimpath -ldflags '-X main.version=0.3.0' -o $DevDockExe .
        if ($LASTEXITCODE -ne 0) { throw 'DevDock 编译失败' }
        & go build -o $FixtureExe ./testsupport/fixture
        if ($LASTEXITCODE -ne 0) { throw '夹具编译失败' }
    } finally {
        Pop-Location
    }
    Info "DevDock.exe  = $DevDockExe"
    Info "fixture.exe  = $FixtureExe"
    Info "隔离 APPDATA = $AppData"

    # ---------- 启动服务 ----------
    Step '启动：隔离环境下起一个 DevDock 服务'
    $env:APPDATA = $AppData
    $ServerLog = Join-Path $E2E 'server.out.log'
    $RuntimePath = Join-Path $AppData 'DevDock\runtime.json'

    # 清掉上一次运行可能残留的 runtime.json，避免误把陈旧文件当成"已就绪"。
    Remove-Item $RuntimePath -ErrorAction SilentlyContinue
    Stop-PriorServer

    # 注意：这里的就绪轮询直接看 runtime.json + /api/health，不走 CLI。
    # 因为 CLI 在服务写好 runtime.json 之前会自己再拉一个 serve，
    # 两个实例抢互斥量，可能导致我们刚起的那个反而退出，测到的 PID 就失真。
    $swBoot = [Diagnostics.Stopwatch]::StartNew()
    $ServerProc = Start-Process -FilePath $DevDockExe `
        -ArgumentList 'serve', '--no-open', '--quiet' `
        -RedirectStandardOutput $ServerLog -RedirectStandardError (Join-Path $E2E 'server.err.log') `
        -WindowStyle Hidden -PassThru

    $rt = $null
    for ($i = 0; $i -lt 400; $i++) {
        if (Test-Path $RuntimePath) {
            try {
                $candidate = Get-Content $RuntimePath -Raw | ConvertFrom-Json
                $resp = Invoke-WebRequest -Uri "http://127.0.0.1:$($candidate.port)/api/health" -UseBasicParsing -TimeoutSec 2
                if ($resp.StatusCode -eq 200) { $rt = $candidate; break }
            } catch {
                # 端口刚写好但还没开始监听，继续等。
            }
        }
        Start-Sleep -Milliseconds 25
    }
    $swBoot.Stop()

    if ($null -eq $rt) { throw 'DevDock 服务在 10 秒内没有可用' }
    $ServerPid = [int]$rt.pid
    Pass ("服务可用，启动到可响应耗时 {0:N0} ms（PID {1}，端口 {2}）" -f $swBoot.Elapsed.TotalMilliseconds, $ServerPid, $rt.port)

    # ---------- 新增并启动项目 ----------
    Step '启动项目：连接真实 HTTP 服务并等待业务就绪'

    # 脚本必须可重复执行：先清掉上一次运行残留的同名项目。
    Invoke-DevDock @('remove', 'fixture-e2e', '--stop') | Out-Null

    $Port = Get-FreePort
    Info "为夹具分配端口 $Port"

    $addCmd = '"{0}" -port {1} -logs 20 -log-interval 25 -grandchild' -f $FixtureExe, $Port
    $r = Invoke-DevDock @('add', 'fixture-e2e', '--cwd', $E2E, '--command', $addCmd, '--port', "$Port", '--readiness', '/health', '--ready-timeout', '20')
    if ($r.Code -ne 0) { throw "add 失败（退出码 $($r.Code)）：$($r.Output)" }
    Pass '项目已创建'

    $swStart = [Diagnostics.Stopwatch]::StartNew()
    $r = Invoke-DevDock @('start', 'fixture-e2e', '--wait-ready', '--timeout', '20s', '--json')
    $swStart.Stop()
    if ($r.Code -ne 0) { throw "start --wait-ready 失败（退出码 $($r.Code)）：$($r.Output)" }

    $st = $r.Output | ConvertFrom-Json
    Info ("lifecycle={0} readiness={1} pid={2} readyLatencyMs={3}" -f $st.lifecycle, $st.readiness, $st.pid, $st.readyLatencyMs)

    if ($st.lifecycle -eq 'running') { Pass 'lifecycle = running' } else { Fail "lifecycle 应为 running，实际 $($st.lifecycle)" }
    if ($st.readiness -eq 'ready') { Pass 'readiness = ready（业务真的可用，不只是进程活着）' } else { Fail "readiness 应为 ready，实际 $($st.readiness)" }

    # ---------- 真实 HTTP 校验 ----------
    Step '校验：业务端口真的在提供响应'
    try {
        $resp = Invoke-WebRequest -Uri "http://127.0.0.1:$Port/health" -UseBasicParsing -TimeoutSec 5
        Info ("GET /health -> {0} body={1}" -f $resp.StatusCode, $resp.Content.Trim())
        if ($resp.StatusCode -eq 200) { Pass '夹具 /health 返回 200' } else { Fail "夹具 /health 返回 $($resp.StatusCode)" }
    } catch {
        Fail "无法访问夹具端口：$($_.Exception.Message)"
    }

    # ---------- 日志 ----------
    Step '校验：日志能被读到'
    $r = Invoke-DevDock @('logs', 'fixture-e2e', '--tail', '200', '--json')
    if ($r.Code -ne 0) { throw "logs 失败（退出码 $($r.Code)）：$($r.Output)" }
    $logs = $r.Output | ConvertFrom-Json
    $texts = @($logs.lines | ForEach-Object { $_.text })
    Info ("日志行数：{0}" -f $texts.Count)
    $texts | Select-Object -First 5 | ForEach-Object { Info "  | $_" }

    if ($texts -match 'fixture log line') { Pass '日志中包含业务输出' } else { Fail '日志中没有 fixture log line' }

    # ---------- 抓取三层 PID ----------
    Step '校验：进程层级（根 / 业务 / 孙进程）'
    $fixturePid = 0
    $grandchildPid = 0
    foreach ($t in $texts) {
        if ($t -match 'FIXTURE_PID=(\d+)') { $fixturePid = [int]$Matches[1] }
        if ($t -match 'FIXTURE_SPAWNED_GRANDCHILD=(\d+)') { $grandchildPid = [int]$Matches[1] }
    }
    $rootPid = [int]$st.pid

    Info "根进程(cmd.exe) PID = $rootPid"
    Info "业务进程(fixture) PID = $fixturePid"
    Info "孙进程(fixture -child) PID = $grandchildPid"

    if (Test-PidAlive $rootPid) { Pass '根进程存活' } else { Fail "根进程 $rootPid 不在" }
    if (Test-PidAlive $fixturePid) { Pass '业务进程存活' } else { Fail "业务进程 $fixturePid 不在" }
    if (Test-PidAlive $grandchildPid) { Pass '孙进程存活' } else { Fail "孙进程 $grandchildPid 不在（无法验证整树清理）" }
    if (Test-PortOpen $Port) { Pass "端口 $Port 处于监听状态" } else { Fail "端口 $Port 无人监听" }

    # ---------- 空闲资源占用 ----------
    Step '测量：服务空闲时的 working set'
    Start-Sleep -Seconds 2
    $srv = Get-Process -Id $ServerPid -ErrorAction SilentlyContinue
    if ($null -ne $srv) {
        $wsMiB = [math]::Round($srv.WorkingSet64 / 1MB, 1)
        $privMiB = [math]::Round($srv.PrivateMemorySize64 / 1MB, 1)
        Info ("workingSet = {0} MiB, privateBytes = {1} MiB" -f $wsMiB, $privMiB)
        $IdleWorkingSetMiB = $wsMiB
    } else {
        Fail '服务进程意外退出'
    }

    # ---------- 停止 + 整树清理 ----------
    Step '停止：验证整棵进程树被清理'
    $r = Invoke-DevDock @('stop', 'fixture-e2e', '--json')
    if ($r.Code -ne 0) { throw "stop 失败（退出码 $($r.Code)）：$($r.Output)" }
    Pass 'stop 退出码 0'

    Start-Sleep -Milliseconds 800

    if (Test-PidAlive $rootPid) { Fail "根进程 $rootPid 仍存活" } else { Pass "根进程 $rootPid 已消失" }
    if (Test-PidAlive $fixturePid) { Fail "业务进程 $fixturePid 仍存活" } else { Pass "业务进程 $fixturePid 已消失" }
    if (Test-PidAlive $grandchildPid) { Fail "孙进程 $grandchildPid 仍存活（进程树清理失败）" } else { Pass "孙进程 $grandchildPid 已消失（进程树整树清理成功）" }
    if (Test-PortOpen $Port) { Fail "端口 $Port 仍被占用" } else { Pass "端口 $Port 已释放" }

    $r = Invoke-DevDock @('list', '--json')
    $after = $r.Output | ConvertFrom-Json
    $one = @($after.projects | Where-Object { $_.name -eq 'fixture-e2e' })[0]
    if ($null -ne $one) {
        Info ("停止后 lifecycle={0} readiness={1}" -f $one.state.lifecycle, $one.state.readiness)
        if ($one.state.lifecycle -eq 'stopped') { Pass '状态回到 stopped' } else { Fail "状态应为 stopped，实际 $($one.state.lifecycle)" }
    } else {
        Fail '停止后列表里找不到项目'
    }

    # ---------- start API 延迟 ----------
    # 直连 HTTP 而不是走 CLI：CLI 每次都要先做一次进程发现，会把命令行启动开销
    # 算进去，测不出「start 接口返回时进程已经 spawn」这件事本身有多快。
    Step '测量：start API 从调用到进程已 spawn'
    $hdr = @{ 'X-DevDock-Token' = $rt.token }
    $projectId = $one.id
    $startSamples = @()
    for ($k = 0; $k -lt 3; $k++) {
        $swOne = [Diagnostics.Stopwatch]::StartNew()
        Invoke-WebRequest -Method POST -Uri "http://127.0.0.1:$($rt.port)/api/projects/$projectId/start" `
            -Headers $hdr -UseBasicParsing -TimeoutSec 20 | Out-Null
        $swOne.Stop()
        $startSamples += [math]::Round($swOne.Elapsed.TotalMilliseconds, 1)

        Start-Sleep -Milliseconds 300
        # 对已停止的项目再次 stop 会返回 409，这是预期的产品行为，这里忽略。
        try {
            Invoke-WebRequest -Method POST -Uri "http://127.0.0.1:$($rt.port)/api/projects/$projectId/stop" `
                -Headers $hdr -UseBasicParsing -TimeoutSec 20 | Out-Null
        } catch {
        }
        Start-Sleep -Milliseconds 300
    }
    $sorted = @($startSamples | Sort-Object)
    $median = $sorted[[int]($sorted.Count / 2)]
    Info ("start API 耗时（3 次）: " + ($startSamples -join ' ms, ') + ' ms')
    Info ("中位数 $median ms（含 cmd.exe 进程创建，不含业务自身启动）")
    $script:StartApiMedianMs = $median

    # 收尾：循环里已经停过，这里只等一会儿再确认没有留下孙进程。
    Start-Sleep -Milliseconds 800
    $leftover = @(Get-Process -Name 'fixture' -ErrorAction SilentlyContinue |
        Where-Object { $_.Path -ieq $FixtureExe })
    if ($leftover.Count -eq 0) { Pass '反复启动/停止后没有残留的夹具进程' } else { Fail "残留 $($leftover.Count) 个夹具进程" }
}
catch {
    Fail "异常终止：$($_.Exception.Message)"
}
finally {
    # ---------- 清理：只结束本脚本启动的服务，不碰别人的进程 ----------
    if ($null -ne $ServerProc -and -not $ServerProc.HasExited) {
        Stop-Process -Id $ServerProc.Id -Force -ErrorAction SilentlyContinue
    }
    if ($ServerPid -gt 0 -and (Test-PidAlive $ServerPid)) {
        Stop-Process -Id $ServerPid -Force -ErrorAction SilentlyContinue
    }
    # 兜底：夹具若因断言失败仍在跑，按 PID 精确清理。
    if ($fixturePid -gt 0 -and (Test-PidAlive $fixturePid)) {
        Stop-Process -Id $fixturePid -Force -ErrorAction SilentlyContinue
    }
    if ($grandchildPid -gt 0 -and (Test-PidAlive $grandchildPid)) {
        Stop-Process -Id $grandchildPid -Force -ErrorAction SilentlyContinue
    }
}

# ---------- 结论 ----------
Write-Host ''
if ($Failures.Count -eq 0) {
    Write-Host 'E2E_RESULT=PASS' -ForegroundColor Green
    Write-Host "E2E_IDLE_WORKINGSET_MIB=$IdleWorkingSetMiB"
    Write-Host "E2E_BOOT_MS=$([math]::Round($swBoot.Elapsed.TotalMilliseconds,0))"
    Write-Host "E2E_START_API_MEDIAN_MS=$StartApiMedianMs"
    exit 0
} else {
    Write-Host "E2E_RESULT=FAIL ($($Failures.Count) 项)" -ForegroundColor Red
    $Failures | ForEach-Object { Write-Host "  - $_" -ForegroundColor Red }
    exit 1
}
