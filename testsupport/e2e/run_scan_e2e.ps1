<#
.SYNOPSIS
    DevDock「自动识别本机开发项目」端到端验收脚本。

.DESCRIPTION
    按 docs/AUTO_DETECT_REQUIREMENTS.md 9.2 的六步执行：
        1) 造一棵可预测的临时树（真实文件，非 mock）
        2) 隔离 APPDATA 起服务
        3) 只读性：扫描前后 list 数量与 config.json 完全不变
        4) 内容断言：建议条数、名称集合、端口来源、node_modules 诱饵被剪
        5) 导入路径：add --auto 复用识别结果，重复添加被拒
        6) 扫描不启动任何进程

    另外按 SPEC A11 断言 UI 侧的 DOM 与静态资源（不做真实浏览器点击）：
        index.html 含 scan 抽屉所需的全部 id，style.css / app.js / favicon.svg 均 200。

    全程使用隔离的 APPDATA，不读写真实 %APPDATA%\DevDock；
    收尾只按精确可执行文件路径结束本脚本启动的进程。

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

$Failures = New-Object System.Collections.Generic.List[string]
$ServerProc = $null
$ServerPid = 0
$rt = $null

function Step($msg) { Write-Host "`n=== $msg ===" -ForegroundColor Cyan }
function Info($msg) { Write-Host "    $msg" }
function Pass($msg) { Write-Host "  PASS  $msg" -ForegroundColor Green }
function Fail($msg) {
    Write-Host "  FAIL  $msg" -ForegroundColor Red
    $script:Failures.Add($msg)
}

# Invoke-DevDock 调用 CLI，返回 @{ Code; StdOut; StdErr }。
# stdout 与 stderr 分开捕获，才能验证「--json 时 stdout 只有 JSON」。
#
# 保留 `& $exe @CliArgs` 的数组 splatting：Start-Process -ArgumentList 会把数组
# 重新拼成一行命令行，带空格的参数值会被拆裂（实测）。
# 调用期间把 $ErrorActionPreference 临时降为 Continue：否则原生命令写的 stderr
# 会被 PowerShell 包成 ErrorRecord 并抛异常（实测），add --auto 打印的
# 「识别结论」会被误判成失败。
function Invoke-DevDock {
    param([string[]]$CliArgs)
    $errFile = Join-Path $E2E ("cli.err.{0}.tmp" -f [guid]::NewGuid().ToString('N'))
    $prev = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        $out = & $DevDockExe @CliArgs 2>$errFile
        $code = $LASTEXITCODE
        $errText = ''
        if (Test-Path $errFile) { $errText = (Get-Content $errFile -Raw) }
        return @{
            Code   = $code
            StdOut = (@($out) -join "`n")
            StdErr = $errText
        }
    } finally {
        $ErrorActionPreference = $prev
        Remove-Item $errFile -ErrorAction SilentlyContinue
    }
}

# Stop-PriorServer 只结束本脚本自己编译的那个 exe，避免碰用户其它实例。
function Stop-PriorServer {
    $stale = @(Get-CimInstance Win32_Process -Filter "Name='DevDock.exe'" -ErrorAction SilentlyContinue |
        Where-Object { $_.ExecutablePath -ieq $DevDockExe })
    foreach ($p in $stale) {
        Write-Host "    清理上轮遗留实例 PID $($p.ProcessId)" -ForegroundColor Yellow
        Stop-Process -Id $p.ProcessId -Force -ErrorAction SilentlyContinue
    }
    if ($stale.Count -gt 0) { Start-Sleep -Milliseconds 500 }
}

function Test-PidAlive([int]$ProcessId) {
    if ($ProcessId -le 0) { return $false }
    return ($null -ne (Get-Process -Id $ProcessId -ErrorAction SilentlyContinue))
}

$swTotal = [Diagnostics.Stopwatch]::StartNew()

try {
    # ---------- 1) 造树 ----------
    Step '1) 造一棵可预测的临时树'
    $scanRoot = Join-Path $E2E 'scanroot'
    if (Test-Path $scanRoot) { Remove-Item $scanRoot -Recurse -Force }
    New-Item -ItemType Directory -Force -Path $Bin, $AppData | Out-Null
    New-Item -ItemType Directory -Force -Path `
        "$scanRoot\web-a", "$scanRoot\web-b", "$scanRoot\go-a", "$scanRoot\node_modules\x" | Out-Null

    '{"name":"web-a","scripts":{"dev":"vite"}}'             | Set-Content "$scanRoot\web-a\package.json" -Encoding utf8
    '{"name":"web-b","scripts":{"dev":"next dev -p 3001"}}'  | Set-Content "$scanRoot\web-b\package.json" -Encoding utf8
    "module go-a`ngo 1.25`n"                          | Set-Content "$scanRoot\go-a\go.mod" -Encoding utf8
    "package main`n`nfunc main() {}"                     | Set-Content "$scanRoot\go-a\main.go" -Encoding utf8
    '{"name":"decoy","scripts":{"dev":"vite"}}'             | Set-Content "$scanRoot\node_modules\x\package.json" -Encoding utf8
    Info "扫描根：$scanRoot"

    # ---------- 编译 ----------
    Step '2) 编译并启动隔离服务'
    Push-Location $WorkRoot
    try {
        & go build -trimpath -ldflags '-X main.version=0.2.0' -o $DevDockExe .
        if ($LASTEXITCODE -ne 0) { throw 'DevDock 编译失败' }
    } finally {
        Pop-Location
    }
    Info "DevDock.exe  = $DevDockExe"
    Info "隔离 APPDATA = $AppData"

    $env:APPDATA = $AppData
    $ServerLog = Join-Path $E2E 'scan-server.out.log'
    $RuntimePath = Join-Path $AppData 'DevDock\runtime.json'
    $cfgPath = Join-Path $AppData 'DevDock\config.json'

    Remove-Item $RuntimePath -ErrorAction SilentlyContinue
    Stop-PriorServer

    $ServerProc = Start-Process -FilePath $DevDockExe `
        -ArgumentList 'serve', '--no-open', '--quiet' `
        -RedirectStandardOutput $ServerLog -RedirectStandardError (Join-Path $E2E 'scan-server.err.log') `
        -WindowStyle Hidden -PassThru

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
    if ($null -eq $rt) { throw 'DevDock 服务在 10 秒内没有可用' }
    $ServerPid = [int]$rt.pid
    Pass ("服务可用（PID {0}，端口 {1}）" -f $ServerPid, $rt.port)

    # 脚本必须可重复执行：只清掉上一次运行残留下来的、cwd 位于本脚本扫描根内的项目。
    # 刻意不清空整个隔离 APPDATA —— run_e2e.ps1 共用同一个目录，不能互相破坏。
    $pre = Invoke-DevDock @('list', '--json')
    if ($pre.Code -eq 0) {
        $preProjects = ($pre.StdOut | ConvertFrom-Json).projects
        if ($null -eq $preProjects) { $preProjects = @() }
        $stale = @($preProjects | Where-Object { $_.cwd -like "$scanRoot*" })
        foreach ($p in $stale) {
            Invoke-DevDock @('remove', $p.name, '--stop') | Out-Null
        }
        if ($stale.Count -gt 0) { Info "已清理上次运行残留的 $($stale.Count) 个项目" }
    }

    # ---------- A11：UI DOM 与静态资源 ----------
    Step '3) UI：DOM 存在 + 静态资源 200'
    $base = "http://127.0.0.1:$($rt.port)"
    foreach ($asset in @('/', '/index.html', '/style.css', '/app.js', '/favicon.svg')) {
        try {
            $resp = Invoke-WebRequest -Uri ($base + $asset) -UseBasicParsing -TimeoutSec 5
            if ($resp.StatusCode -eq 200) { Pass ("GET $asset -> 200") }
            else { Fail "GET $asset -> $($resp.StatusCode)" }
        } catch {
            Fail "GET $asset 失败：$($_.Exception.Message)"
        }
    }

    $html = (Invoke-WebRequest -Uri ($base + '/index.html') -UseBasicParsing -TimeoutSec 5).Content
    foreach ($id in @('btn-scan', 'scan-mask', 'scan-roots', 'scan-start', 'scan-cancel',
                      'scan-list', 'scan-summary', 'scan-import')) {
        if ($html -match ('id="' + $id + '"')) { Pass "index.html 含 #$id" }
        else { Fail "index.html 缺少 #$id" }
    }
    # 硬约束：扫描抽屉必须复用 .drawer-mask，才能自动享受 [hidden]{display:none}。
    if ($html -match 'id="scan-mask"[^>]*class="[^"]*drawer-mask' -or
        $html -match 'class="drawer-mask"[^>]*id="scan-mask"') {
        Pass 'scan-mask 复用 .drawer-mask 类'
    } else {
        Fail 'scan-mask 未复用 .drawer-mask 类（会导致抽屉关不掉）'
    }

    $css = (Invoke-WebRequest -Uri ($base + '/style.css') -UseBasicParsing -TimeoutSec 5).Content
    if ($css -match '\.drawer-mask\[hidden\]') { Pass 'style.css 保留 .drawer-mask[hidden]' }
    else { Fail 'style.css 缺少 .drawer-mask[hidden]' }

    $js = (Invoke-WebRequest -Uri ($base + '/app.js') -UseBasicParsing -TimeoutSec 5).Content
    foreach ($fn in @('openScan', 'closeScan', 'runScan', 'importSelected', 'fillFromSuggestion')) {
        if ($js -match ('function\s+' + $fn + '\s*\(')) { Pass "app.js 含 $fn()" }
        else { Fail "app.js 缺少 $fn()" }
    }
    if ($js -match 'innerHTML') { Fail 'app.js 出现 innerHTML（CSP 与转义红线）' }
    else { Pass 'app.js 未使用 innerHTML' }
# A3：默认只勾「非 low，且有端口/框架/high」、单批 ≤ 20、导入前二次确认。
if ($js -match "s\.confidence\s*!==\s*'low'" -and $js -match "s\.port\s*>\s*0\s*\|\|\s*!!s\.framework\s*\|\|\s*s\.confidence\s*===\s*'high'") { Pass 'app.js 默认勾选带端口/框架/high 的条目（A3）' }
else { Fail 'app.js 默认勾选条件不符合 A3（应排除 low，且要求 port>0 或 framework 或 high）' }
    if ($js -match 'picks\.length\s*>\s*20') { Pass 'app.js 单批导入 ≤ 20（A3）' }
    else { Fail 'app.js 缺少单批 20 条上限（A3）' }
    if ($js -match 'confirm\(') { Pass 'app.js 导入前二次确认（A3）' }
    else { Fail 'app.js 缺少导入二次确认（A3）' }
    # A8：组内默认展开 5 条 + 「还有 N 条」。
    if ($js -match 'scan-more' -and $js -match 'items\.slice\(0,\s*5\)') { Pass 'app.js 组内折叠 + 还有 N 条（A8）' }
    else { Fail 'app.js 缺少组内折叠展开（A8）' }

    # ---------- 3) 只读性 ----------
    Step '4) 只读性：扫描前后 list 与 config.json 完全不变'
    $before = Invoke-DevDock @('list', '--json')
    if ($before.Code -ne 0) { throw "list 失败（退出码 $($before.Code)）：$($before.StdOut)$($before.StdErr)" }
    $beforeList = $before.StdOut | ConvertFrom-Json
    $beforeCount = @($beforeList.projects).Count

    $cfgExistedBefore = Test-Path $cfgPath
    $cfgMtimeBefore = $null
    if ($cfgExistedBefore) { $cfgMtimeBefore = (Get-Item $cfgPath).LastWriteTimeUtc }
    $appDirBefore = @(Get-ChildItem (Join-Path $AppData 'DevDock') -Recurse -File -ErrorAction SilentlyContinue |
        ForEach-Object { $_.FullName })

    $swScan = [Diagnostics.Stopwatch]::StartNew()
    $r = Invoke-DevDock @('scan', '--root', $scanRoot, '--json')
    $swScan.Stop()
    Info ("scan 退出码 {0}，Measure-Command 耗时 {1:N0} ms" -f $r.Code, $swScan.Elapsed.TotalMilliseconds)
    $ScanElapsedMs = [math]::Round($swScan.Elapsed.TotalMilliseconds, 0)

    if ($r.Code -ne 0) { Fail "scan 退出码应为 0，实际 $($r.Code)：$($r.StdOut)$($r.StdErr)" }
    else { Pass 'scan 退出码 0' }

    if ($swScan.Elapsed.TotalMilliseconds -lt 2000) { Pass '扫描在 2000ms 内返回' }
    else { Fail ("扫描耗时 {0:N0} ms，超过 2000ms" -f $swScan.Elapsed.TotalMilliseconds) }

    # 红线 5：--json 时 stdout 只有 JSON。
    try {
        $parsed = $r.StdOut | ConvertFrom-Json
        Pass '--json 的 stdout 可被解析为 JSON'
    } catch {
        Fail "--json 的 stdout 混入了非 JSON 内容：$($r.StdOut)"
        $parsed = $null
    }

    $after = Invoke-DevDock @('list', '--json')
    $afterList = $after.StdOut | ConvertFrom-Json
    $afterCount = @($afterList.projects).Count
    if ($afterCount -eq $beforeCount) { Pass "list 数量不变（$beforeCount -> $afterCount）" }
    else { Fail "list 数量变化：$beforeCount -> $afterCount（扫描不允许写库）" }

    if (-not $cfgExistedBefore) {
        if (Test-Path $cfgPath) { Fail '扫描前 config.json 不存在，扫描后出现了（扫描不允许写配置）' }
        else { Pass 'config.json 未被创建（扫描零副作用）' }
    } else {
        $mtimeAfter = (Get-Item $cfgPath).LastWriteTimeUtc
        if ($mtimeAfter -eq $cfgMtimeBefore) { Pass 'config.json LastWriteTimeUtc 未变' }
        else { Fail 'config.json 被改写（扫描不允许写配置）' }
    }
    $appDirAfter = @(Get-ChildItem (Join-Path $AppData 'DevDock') -Recurse -File -ErrorAction SilentlyContinue |
        ForEach-Object { $_.FullName })
    $newFiles = @($appDirAfter | Where-Object { $_ -notin $appDirBefore })
    if ($newFiles.Count -eq 0) { Pass 'APPDATA\DevDock 下没有新增文件' }
    else { Fail "扫描后新增文件：$($newFiles -join ', ')" }

    # ---------- 4) 内容断言 ----------
    Step '5) 内容：建议条数、名称集合、端口来源、诱饵被剪'
    if ($null -eq $parsed) { throw 'scan 结果不可解析，后续断言无法进行' }
    $sugs = @($parsed.suggestions)
    Info ("建议条数 {0}，scannedDirs {1}，elapsedMs {2}，timedOut {3}，truncated {4}" -f `
        $sugs.Count, $parsed.scannedDirs, $parsed.elapsedMs, $parsed.timedOut, $parsed.truncated)
    $script:ScanSuggestions = $sugs.Count

    if ($sugs.Count -eq 3) { Pass '建议条数 = 3' }
    else { Fail "建议条数应为 3，实际 $($sugs.Count)" }

    $names = @($sugs | ForEach-Object { $_.name } | Sort-Object)
    $want = @('go-a', 'web-a', 'web-b' | Sort-Object)
    if (($names -join ',') -eq ($want -join ',')) { Pass "名称集合恰为 $($want -join '/')" }
    else { Fail "名称集合应为 $($want -join '/')，实际 $($names -join '/')" }

    $decoy = @($sugs | Where-Object { $_.cwd -match 'node_modules' })
    if ($decoy.Count -eq 0) { Pass 'node_modules 诱饵被剪枝' }
    else { Fail "node_modules 下的诱饵出现在结果里：$($decoy[0].cwd)" }

    $webA = @($sugs | Where-Object { $_.name -eq 'web-a' })[0]
    if ($null -ne $webA -and $webA.port -eq 5173 -and $webA.portFrom -eq 'framework-default') {
        Pass 'web-a: port=5173, portFrom=framework-default'
    } else { Fail "web-a 端口推断错误：port=$($webA.port) portFrom=$($webA.portFrom)" }

    $webB = @($sugs | Where-Object { $_.name -eq 'web-b' })[0]
    if ($null -ne $webB -and $webB.port -eq 3001 -and $webB.portFrom -eq 'flag') {
        Pass 'web-b: port=3001, portFrom=flag'
    } else { Fail "web-b 端口推断错误：port=$($webB.port) portFrom=$($webB.portFrom)" }

    $goA = @($sugs | Where-Object { $_.name -eq 'go-a' })[0]
    if ($null -ne $goA -and $goA.command -eq 'go run .') { Pass 'go-a: command="go run ."' }
    else { Fail "go-a 命令错误：$($goA.command)" }
    if ($null -ne $goA -and $goA.port -eq 0) { Pass 'go-a: port=0（未推断，不猜）' }
    else { Fail "go-a port 应为 0，实际 $($goA.port)" }

    # ---------- 5) 导入路径 ----------
    Step '6) 导入：add --auto 复用识别结果，重复添加被拒'
    $a1 = Invoke-DevDock @('add', '--auto', '--cwd', "$scanRoot\web-a", '--json')
    if ($a1.Code -ne 0) { Fail "add --auto 退出码应为 0，实际 $($a1.Code)：$($a1.StdOut)$($a1.StdErr)" }
    else { Pass 'add --auto 退出码 0' }

    try { $created = $a1.StdOut | ConvertFrom-Json } catch { $created = $null }
    if ($null -ne $created) {
        if ($created.command -eq 'npm run dev') { Pass 'add --auto 采用识别命令 npm run dev' }
        else { Fail "add --auto 命令应为 npm run dev，实际 $($created.command)" }
        if ($created.port -eq 5173) { Pass 'add --auto 采用识别端口 5173' }
        else { Fail "add --auto 端口应为 5173，实际 $($created.port)" }
        if ($created.cwd -ieq "$scanRoot\web-a") { Pass 'add --auto cwd 正确' }
        else { Fail "add --auto cwd 错误：$($created.cwd)" }
    } else {
        Fail "add --auto 的 stdout 不是 JSON：$($a1.StdOut)"
    }
    if ($a1.StdErr -match '识别结论') { Pass '识别结论打印到 stderr（不污染 --json 的 stdout）' }
    else { Fail "add --auto 未在 stderr 打印识别结论：$($a1.StdErr)" }

    $a2 = Invoke-DevDock @('add', '--auto', '--cwd', "$scanRoot\web-a", '--json')
    if ($a2.Code -ne 0) { Pass "重复 add --auto 被拒（退出码 $($a2.Code)）" }
    else { Fail '重复 add --auto 竟然成功（同一 cwd 应被拒）' }

    $afterImport = Invoke-DevDock @('list', '--json')
    $importList = $afterImport.StdOut | ConvertFrom-Json
    $imported = @($importList.projects | Where-Object { $_.name -eq 'web-a' })
    if ($imported.Count -eq 1) { Pass 'list 里 web-a 只有一条' }
    else { Fail "list 里 web-a 有 $($imported.Count) 条" }

    # ---------- 6) 不启动任何进程 ----------
    Step '7) 零副作用：扫描不启动任何进程'
    $kidsBefore = @(Get-CimInstance Win32_Process -ErrorAction SilentlyContinue |
        Where-Object { $_.ParentProcessId -eq $ServerPid } |
        ForEach-Object { "$($_.ProcessId)|$($_.Name)" })
    $procsBefore = @(Get-Process -Name 'node', 'go', 'fixture' -ErrorAction SilentlyContinue |
        ForEach-Object { $_.Id })

    Invoke-DevDock @('scan', '--root', $scanRoot, '--json') | Out-Null

    $kidsAfter = @(Get-CimInstance Win32_Process -ErrorAction SilentlyContinue |
        Where-Object { $_.ParentProcessId -eq $ServerPid } |
        ForEach-Object { "$($_.ProcessId)|$($_.Name)" })
    $procsAfter = @(Get-Process -Name 'node', 'go', 'fixture' -ErrorAction SilentlyContinue |
        ForEach-Object { $_.Id })

    $newKids = @($kidsAfter | Where-Object { $_ -notin $kidsBefore })
    if ($newKids.Count -eq 0) { Pass 'DevDock 服务没有为扫描新建任何子进程' }
    else { Fail "扫描期间新增子进程：$($newKids -join ', ')" }

    $newProcs = @($procsAfter | Where-Object { $_ -notin $procsBefore })
    if ($newProcs.Count -eq 0) { Pass '机器上没有新增 node/go/fixture 进程' }
    else { Fail "扫描期间新增 node/go/fixture 进程 PID：$($newProcs -join ', ')" }
}
catch {
    Fail "异常终止：$($_.Exception.Message)"
}
finally {
    $swTotal.Stop()
    if ($null -ne $ServerProc -and -not $ServerProc.HasExited) {
        Stop-Process -Id $ServerProc.Id -Force -ErrorAction SilentlyContinue
    }
    if ($ServerPid -gt 0 -and (Test-PidAlive $ServerPid)) {
        Stop-Process -Id $ServerPid -Force -ErrorAction SilentlyContinue
    }
    # 兜底：只按精确可执行文件路径清理，不用 Stop-Process -Name DevDock。
    $ours = @(Get-CimInstance Win32_Process -Filter "Name='DevDock.exe'" -ErrorAction SilentlyContinue |
        Where-Object { $_.ExecutablePath -ieq $DevDockExe })
    foreach ($p in $ours) {
        Stop-Process -Id $p.ProcessId -Force -ErrorAction SilentlyContinue
    }
}

# ---------- 结论 ----------
Write-Host ''
if ($Failures.Count -eq 0) {
    Write-Host 'SCAN_E2E_RESULT=PASS' -ForegroundColor Green
    Write-Host "SCAN_E2E_MS=$([math]::Round($swTotal.Elapsed.TotalMilliseconds,0))"
    Write-Host "SCAN_E2E_SUGGESTIONS=$script:ScanSuggestions"
    Write-Host "SCAN_E2E_SCAN_ONLY_MS=$ScanElapsedMs"
    exit 0
} else {
    Write-Host "SCAN_E2E_RESULT=FAIL ($($Failures.Count) 项)" -ForegroundColor Red
    $Failures | ForEach-Object { Write-Host "  - $_" -ForegroundColor Red }
    Write-Host "SCAN_E2E_MS=$([math]::Round($swTotal.Elapsed.TotalMilliseconds,0))"
    exit 1
}
