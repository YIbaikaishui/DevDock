# DevDock 验证记录

当前构建：DevDock.exe 版本 **0.3.0（待发布）**，10789376 bytes，SHA-256 `5EEF5B8E0A92310F43B186A21C4261D33B9D76D97D87C68F07908155B626F7BD`。0.3.0 的最后一次实测见下一节；本文件下半部分是 0.2.0 / 0.1.0 的历史记录。

本文件只写**实际跑过并留有输出**的内容。没有跑过的写在「未验证」一节，不并入结论。

## 0.3.0：界面重构（待发布）

起因是用户反馈：「我感觉现在不好用也不好看，不是让你去调研其他这种类似的工具是怎么做的嘛」。流程为：只读调研 → 主 agent 拍板规格 → 开发 → 主 agent 独立验收。

- 调研：[`docs/UX_RESEARCH.md`](UX_RESEARCH.md)（含参考实现 devwebui 的布局骨架与视觉 token，以及 Laravel Herd / DDEV / Lando / pm2 / lazydocker / Portainer / Orb Stack / Rancher Desktop / Homarr / Warp / VS Code 终端的联网调研表）。
- 规格：[`docs/UI_REWORK_SPEC.md`](UI_REWORK_SPEC.md)（R1–R20 裁决 + 视觉 token + 验收清单 + 偏差记录）。

### 调研要点

- 同类工具的共同做法是「一个项目一张卡 + 右侧抽屉看细节」，不是「左侧树 + 大表格」；顶栏承担全局操作与搜索。
- 状态用**颜色 + 文案**双通道表达，而不是只有一颗彩色圆点。
- 日志一律独立面板，带过滤、跟随、时间戳/编码开关。
- 危险操作二次确认，且产品自带确认框，不依赖浏览器原生 `confirm()`。

### 重构后的界面

- 顶栏：品牌 + 实时汇总（N/M 运行中）+ 居中搜索 + 新建 / 扫描本机 / 刷新。
- 主列：居中 800px 单列，一个项目一张卡（状态点 + 名称 + 状态胶囊 + 命令 / 端口 / 运行时长 / 就绪延迟 + 操作）。
- 右侧全高抽屉：日志、详情、扫描、新建/编辑，**互斥打开**（同时只有一个）。
- 自绘 `alertdialog` 确认弹窗，替换全部原生 `confirm()`。
- 窄屏（< 640px）规则：顶栏换行、卡片堆叠、抽屉整宽。

### 构建与静态检查（实测）

```text
go build ./...                                                                            BUILD_EXIT=0
go vet ./...                                                                              VET_EXIT=0
go build -trimpath -ldflags "-X main.version=0.3.0" -o ..\outputs\DevDock.exe .            BUILD_EXIT=0
```

产物：`outputs/DevDock.exe`，10789376 bytes，SHA-256 `5EEF5B8E0A92310F43B186A21C4261D33B9D76D97D87C68F07908155B626F7BD`。

### 自动化测试（实测）

```text
go test -count=1 ./...
?   devdock                         [no test files]
?   devdock/internal/cli            [no test files]
ok  devdock/internal/client         5.053s
?   devdock/internal/config         [no test files]
ok  devdock/internal/detect         7.962s
ok  devdock/internal/httpapi        5.387s
ok  devdock/internal/logs           2.587s
ok  devdock/internal/mcpserver      4.353s
?   devdock/internal/model          [no test files]
?   devdock/internal/ready          [no test files]
ok  devdock/internal/runner         6.170s
ok  devdock/internal/store          2.259s
ok  devdock/internal/winproc        2.665s
?   devdock/testsupport/fixture     [no test files]
?   devdock/web                     [no test files]
TEST_EXIT=0
```

### 端到端（实测）

`testsupport/e2e/run_e2e.ps1`：

```text
E2E_RESULT=PASS
E2E_IDLE_WORKINGSET_MIB=11.6
E2E_BOOT_MS=403
E2E_START_API_MEDIAN_MS=38.3
```

启动到可响应 403 ms、空闲 working set 11.6 MiB、`start` API 到进程已 spawn 中位数 38.3 ms，三项都在目标内（< 1000 ms / < 50 MiB / < 100 ms）。三层进程树（`cmd.exe → fixture.exe → fixture.exe -child`）停止后全部消失、端口释放。

`testsupport/e2e/run_scan_e2e.ps1`：

```text
SCAN_E2E_RESULT=PASS
SCAN_E2E_MS=2721
SCAN_E2E_SUGGESTIONS=3
SCAN_E2E_SCAN_ONLY_MS=28
```

两个脚本里的 `-X main.version` 已从 0.2.0 同步改为 0.3.0。

### 发布前复测（2026-11-19，同一构建）

提交前在本机又跑了一遍，**功能全绿，但性能数字明显高于上表**，如实记录如下，不要读成代码回退：

```text
go test -count=1 -vet=off -p 1 ./...（拆两批串行）    EXIT=0
  client 3.136s / detect 5.489s / logs 1.418s / runner 4.580s
  store 1.946s / winproc 2.596s / mcpserver 2.982s / httpapi 3.582s

run_e2e.ps1  第 1 次   E2E_RESULT=PASS  BOOT_MS=2966   START_API_MEDIAN_MS=2000.1
run_e2e.ps1  第 2 次   E2E_RESULT=PASS  BOOT_MS=869    START_API_MEDIAN_MS=608.6
run_scan_e2e.ps1        SCAN_E2E_RESULT=PASS  SCAN_E2E_MS=21534  SCAN_ONLY_MS=243
```

**为什么判断是环境噪声而不是回退**（同一份源码、同一台机器、同一天）：

- 本次会话里 `go build ./...` 从不足 2 秒变成 **128 秒**；
- 并发 `go test ./...` 直接**卡死超过 10 分钟**（`-p 1` 串行后 3–6 秒/包），与 `GATES.md` §3.2 记录的「杀毒抢锁」现象一致；
- `run_scan_e2e.ps1` 里与界面无关的 `scan` 本身从 **28 ms 变成 243 ms**（约 8 倍）。

按同一倍率折算，早前的 `start` API 38.3 ms 对应本次约 300–600 ms，与实测 608.6 ms 吻合。**功能判据（进程树整树清理、端口释放、只读性、不新建进程）全部 PASS，未受影响。** 上表的 403 ms / 38.3 ms 仍是本构建在本机最快的一次实测，保留不删。

### 真实浏览器走查（实测）

用 `paw browser-action` 在真实 Chrome 里逐项走完（隔离实例，APPDATA=`_e2e\appdata-ui`）：

```text
主界面     项目卡显示 命令/端口/运行时长/就绪延迟/状态；fixture-e2e 启动后
           状态胶囊=就绪，名称变成 <a href="http://127.0.0.1:53045">
日志抽屉   24 行、可滚动（scrollHeight 1897 / clientHeight 553）
           上滚到底之外 -> hintHidden=false, followChecked=false
           回到底部       -> hintHidden=true,  followChecked=true
           过滤 FIXTURE_PID -> 1 行 + 1 个 <mark> 高亮
           过滤 zzz-no-match -> 0 行 + 「没有匹配「zzz-no-match」的日志」
详情抽屉   命令 / cwd / 端口 / PID / 运行时长 / 就绪（1210 ms · 已就绪）/
           自动启动 / 创建时间 / 更新时间 / 环境变量 全部渲染
扫描抽屉   分组标题=末段 work，title=完整路径（修 U15）
           3 条建议、默认勾选 2 条；web-a 带「已存在」徽章且不勾
           忽略 -> 行加 .ignored、「已忽略」徽章、勾选禁用、计数 2 -> 1
           撤销 -> 徽章移除、勾选恢复可用
           填入表单 -> 关闭扫描抽屉、打开新建表单并填好
                    （web-b / D:\...\scanroot\web-b / npm run dev / 3001），焦点在名称
更多菜单   编辑 / 打开目录 / 删除
           删除 -> 自绘 alertdialog（标题/描述/取消/删除），
                 取消后焦点落回「删除」，项目仍在（2 张卡）
搜索       web  -> 只剩 web-a；zzz -> 空态 +「清除搜索」；
           点清除 -> 恢复 2 张卡且输入框清空
焦点圈     扫描抽屉打开时 Tab 从最后一个控件回到第一个，Shift+Tab 反向
主题       亮色（:root）与暗色（prefers-color-scheme: dark）都正常渲染
```

走查中修掉的缺陷（详见 `UI_REWORK_SPEC.md` §12）：

1. **项目名整条不显示**：`.p-name` 是 flex 子项且带 `overflow:hidden`，自动最小宽度为 0，被压成 0 宽。加 `flex:1 1 auto; min-width:0`。
2. **端口/时长被挤没**：`.p-meta` 换行后 `.p-cmd` 占满整行，端口被顶到下一行。改 `nowrap` + `.p-cmd` 改 `flex:0 1 auto`；再给 `.p-port/.p-up/.p-note` 加 `flex:0 0 auto; white-space:nowrap`，空间不足时只由命令省略。
3. **完全没有窄屏规则**：317px 视口下顶栏按钮被推出视口、卡片名称消失。新增 `@media (max-width: 640px)`。
4. **窄屏下抽屉只有 44vw**（317px → 139px），扫描行被挤成逐字竖排。media 内改为整宽。
5. **抽屉关闭后焦点掉回 body**：`renderList()` 整表重建把触发按钮移出文档，`restoreFocus()` 无处可还。改为按「卡片 id + data-act」重新定位（实测 Esc 后焦点回到 `data-act=logs` / `data-act=detail`）。
6. **删除确认弹窗取消后焦点掉回 body**：菜单先被 `closeMenu()` 隐藏，触发按钮进了隐藏层。改为弹窗期间菜单保持打开，确认时才关。
7. **Esc 关不掉「更多」菜单**。补上，并把焦点还给「更多」按钮。
8. **抽屉/弹窗没有焦点圈**，Tab 能跑到背后页面，`aria-modal` 形同虚设。补 Tab 圈。
9. **忽略态缺「已忽略」徽章**（规格 §6.2 要求，`.scan-badge.ignored` 样式早就写好但没用上）。

### 交付物冒烟（实测）

用 `outputs/DevDock.exe`（0.3.0）在隔离 APPDATA 下：

```text
HTTP=200 BODY={"configPath":"...\_smoke3\DevDock\config.json","ok":true,"pid":17156,"uptimeSec":0,"version":"0.3.0"}
PID=17156 PORT=56326
DevDock 0.3.0                                            VERSION_EXIT=0
UI_STATUS=200 UI_BYTES=11295
CLEANUP_REMAINING=0
```

### 截图

`_e2e/`（该目录在 `.gitignore` 里，只作本地归档）：`ui-main-live.png`、`ui-main-narrow.png`、`ui-log-open.png`、`ui-detail-open.png`、`ui-scan-open2.png`、`ui-drawer-open.png`。被它们取代的三张旧图（`ui-main.png`、`ui-drawer.png`、`ui-scan-open.png`）已删除。

**视口说明**：paw 浏览器的 WebContentsView 只有 317px CSS 宽，截图全部是在这个宽度下拍的；≥ 1000px 的桌面排版未截图。

**键盘投递说明**：会话早期真实按键可用（日志抽屉被真实 Esc 关闭过一次），后期 paw 无法把按键投递到页面（页面收到 0 次 keydown）。因此除那一次之外的 Esc 路径，都是用 `dispatchEvent(new KeyboardEvent('keydown', {key:'Escape'}))` 验证处理逻辑，**不能声称全部 Esc 路径都用过真实按键**。

## 0.2.0：构建与静态检查

```text
go build ./...                                            BUILD_EXIT=0
go vet ./...                                              VET_EXIT=0
go build -trimpath -ldflags "-X main.version=0.2.0" ...   BUILD_EXIT=0
```

产物：`outputs/DevDock.exe`，10752000 bytes，SHA-256 `FE9B83C0A9241CF9CFD8BC2D4612CE8E5496156C13738176BA665F52592AC71F`。

## 0.2.0：自动化测试

0.1.0 时全量命令的退出码是 1，此前记录为「本机临时测试 exe 删除权限问题，未定位」。现在已定位到根因并修复，不再是环境问题。

### 根因

`internal/client` 的 `spawnService()` 用 `os.Executable()` 取自身路径，再以 `serve --no-open --quiet` 拉起服务。在测试二进制里，`os.Executable()` 返回的就是测试程序自己（`mcpserver.test.exe`），于是：

1. `mcpserver` 的测试调用 `runTool` → `client.Discover(true)` → 找不到运行实例 → `spawnService()` 把**测试二进制本身**当服务拉了起来；
2. Go 测试框架会忽略不认识的位置参数，所以被拉起的子进程又跑了一遍完整测试套件；
3. 每一代子进程再拉起下一代 —— 形成指数级自我复制的进程树；
4. 每一代都持有 `mcpserver.test.exe` 的镜像文件句柄，`go test` 收尾 unlink 时失败，报 `Access is denied`，整体退出码 1。

复现证据：对单个用例 `-test.run TestToolsCallMissingID` 运行，轮询到 40+ 个链式 PID，命令行全部是 `_dbg_mcpserver.exe serve --no-open --quiet`，父子链为 `pid=1540 ← 16052`、`pid=16052 ← 16980` 这样层层向下。

同一个原因也解释了当时 `mcpserver` 包测试要跑 13.8 秒：每次 `Discover` 失败后都要 `Sleep(100ms)` 轮询满 5 秒。

### 修复

- `internal/mcpserver`：`Server` 增加可注入的 `discover` 字段，默认仍是 `client.Discover`；测试注入桩实现，不再触碰真实服务发现。
- `internal/client`：`spawnService()` 增加 `isTestProcess()` 守卫（检查 `test.v` / `test.run` 标志位），在测试进程中直接返回 `ErrRefuseSelfSpawn`，任何调用方都不可能再把自身当服务拉起。
- 新增回归测试 `TestSpawnServiceRefusesInTestProcess`，断言测试进程里调用 `spawnService()` 必须被拒绝。

### 当前结果

```text
go test -count=1 ./...
?   devdock                         [no test files]
?   devdock/internal/cli            [no test files]
ok  devdock/internal/client         5.289s
?   devdock/internal/config         [no test files]
ok  devdock/internal/detect         6.324s
ok  devdock/internal/httpapi        3.611s
ok  devdock/internal/logs           1.899s
ok  devdock/internal/mcpserver      2.670s
?   devdock/internal/model          [no test files]
?   devdock/internal/ready          [no test files]
ok  devdock/internal/runner         6.474s
ok  devdock/internal/store          2.888s
ok  devdock/internal/winproc        3.158s
?   devdock/testsupport/fixture     [no test files]
?   devdock/web                     [no test files]
TEST_EXIT=0
```

`internal/detect` 是 0.2.0 新增的包（自动识别），含 T1–T28 共 28 个用例，其中 T25/T28 按规格允许在条件不满足时 `t.Skip`。

连续多次运行均为 `TEST_EXIT=0`，`unlinkat` 报错不再出现。**全量测试命令现在通过。**

## 0.2.0：端到端验收（真实受管项目）

0.1.0 曾尝试用 `.cmd` 批处理做夹具，遇到 `ERROR: Input redirection is not supported`，那次不算成功。现在改用 `testsupport/fixture`（Go 程序），脚本为 `testsupport/e2e/run_e2e.ps1`，全程使用隔离 APPDATA。

进程层级刻意做成三层，只有三层都消失才算整树清理通过：

```text
cmd.exe（DevDock 根进程） -> fixture.exe（业务进程） -> fixture.exe -child（孙进程）
```

单次运行的原样输出（为可读性只去掉了 ANSI 颜色；本机绝对路径替换为 `<work>` 占位）：

```text
=== 准备：编译 DevDock 与夹具 ===
    DevDock.exe  = <work>\_e2e\bin\DevDock.exe
    fixture.exe  = <work>\_e2e\bin\fixture.exe
    隔离 APPDATA = <work>\_e2e\appdata

=== 启动：隔离环境下起一个 DevDock 服务 ===
  PASS  服务可用，启动到可响应耗时 489 ms（PID 13492，端口 65303）

=== 启动项目：连接真实 HTTP 服务并等待业务就绪 ===
    为夹具分配端口 65306
  PASS  项目已创建
    lifecycle=running readiness=ready pid=14996 readyLatencyMs=204
  PASS  lifecycle = running
  PASS  readiness = ready（业务真的可用，不只是进程活着）

=== 校验：业务端口真的在提供响应 ===
    GET /health -> 200 body=ok
  PASS  夹具 /health 返回 200

=== 校验：日志能被读到 ===
    日志行数：18
      | FIXTURE_SPAWNED_GRANDCHILD=25964
      | fixture log line 1
      | FIXTURE_PID=8088
      | FIXTURE_PORT=65306
      | FIXTURE_GRANDCHILD_PID=25964
  PASS  日志中包含业务输出

=== 校验：进程层级（根 / 业务 / 孙进程）===
    根进程(cmd.exe) PID = 14996
    业务进程(fixture) PID = 8088
    孙进程(fixture -child) PID = 25964
  PASS  根进程存活
  PASS  业务进程存活
  PASS  孙进程存活
  PASS  端口 65306 处于监听状态

=== 测量：服务空闲时的 working set ===
    workingSet = 11.4 MiB, privateBytes = 15.7 MiB

=== 停止：验证整棵进程树被清理 ===
  PASS  stop 退出码 0
  PASS  根进程 14996 已消失
  PASS  业务进程 8088 已消失
  PASS  孙进程 25964 已消失（进程树整树清理成功）
  PASS  端口 65306 已释放
    停止后 lifecycle=stopped readiness=none
  PASS  状态回到 stopped

=== 测量：start API 从调用到进程已 spawn ===
    start API 耗时（3 次）: 49.1 ms, 68.6 ms, 57 ms
    中位数 68.6 ms（含 cmd.exe 进程创建，不含业务自身启动）
  PASS  反复启动/停止后没有残留的夹具进程

E2E_RESULT=PASS
E2E_IDLE_WORKINGSET_MIB=11.4
E2E_BOOT_MS=489
E2E_START_API_MEDIAN_MS=68.6
```

连续 5 次运行结果均为 `E2E_RESULT=PASS`（前四次在抽屉修复前，第五次在修复后）。

0.2.0 构建（含自动识别）的最后一次原样输出：

```text
=== 启动：隔离环境下起一个 DevDock 服务 ===
  PASS  服务可用，启动到可响应耗时 281 ms（PID 39064，端口 57107）
=== 启动项目：连接真实 HTTP 服务并等待业务就绪 ===
    lifecycle=running readiness=ready pid=31216 readyLatencyMs=202
=== 校验：进程层级（根 / 业务 / 孙进程） ===
    根进程(cmd.exe) PID = 31216
    业务进程(fixture) PID = 17232
    孙进程(fixture -child) PID = 16928
  PASS  根进程存活
  PASS  业务进程存活
  PASS  孙进程存活
  PASS  端口 57110 处于监听状态
=== 测量：服务空闲时的 working set ===
    workingSet = 11.7 MiB, privateBytes = 16 MiB
=== 停止：验证整棵进程树被清理 ===
  PASS  stop 退出码 0
  PASS  根进程 31216 已消失
  PASS  业务进程 17232 已消失
  PASS  孙进程 16928 已消失（进程树整树清理成功）
  PASS  端口 57110 已释放
=== 测量：start API 从调用到进程已 spawn ===
    start API 耗时（3 次）: 16.6 ms, 16.8 ms, 14.7 ms
    中位数 16.8 ms（含 cmd.exe 进程创建，不含业务自身启动）

E2E_RESULT=PASS
E2E_IDLE_WORKINGSET_MIB=11.7
E2E_BOOT_MS=281
E2E_START_API_MEDIAN_MS=16.8
```

## 0.2.0：性能实测

同一台机器、同一构建、隔离 APPDATA。四次连续运行：

| 指标 | 1 | 2 | 3 | 4 | 目标 | 结论 |
|---|---|---|---|---|---|---|
| 进程启动 → HTTP 可响应 | 325 ms | 463 ms | 433 ms | 489 ms | < 1000 ms | 达标 |
| 空闲 working set | 11.5 MiB | 11.6 MiB | 11.3 MiB | 11.4 MiB | < 50 MiB | 达标 |
| start API → 进程已 spawn（中位数） | 60.4 ms | 79.7 ms | 46 ms | 68.6 ms | < 100 ms | 达标 |

范围汇总：启动 325–489 ms，working set 11.3–11.6 MiB，start API 中位数 46–79.7 ms。

抽屉 CSS 修复后补测一次：启动 545 ms、空闲 working set 11.3 MiB、start API 中位数 50.1 ms，仍在目标内。

补充说明，避免误读这些数字：

- **启动耗时**是「exe 进程创建 → `/api/health` 返回 200」的实测值，轮询粒度 25 ms。它代表界面可用时间下界；浏览器端首次渲染的耗时未单独测量。
- 早前探索性运行中出现过 185 ms、239 ms、272 ms、338 ms、686 ms，最大一次 1886 ms 是测量脚本自身的缺陷（用 CLI 轮询导致额外拉起实例）造成的，修正后不再出现。
- **start API 耗时**是直连 HTTP、只计 `POST /api/projects/{id}/start` 往返，其中包含 `cmd.exe` 进程创建。各次运行的单次样本区间为 37.2–79.7 ms。不含被启动业务自身的启动时间。
- **working set** 是服务进程在项目已停止、无请求两秒后读取的 `WorkingSet64`；同时记录到的 `PrivateMemorySize64` 为 15.7–16.1 MiB。
- 以上均为本机数字，不作为其它机器的保证。

### 扫描性能（0.2.0 新增，`devdock scan`，产物 `outputs/DevDock.exe`）

命令 `scan --root D:\Desktop --depth 3 --json` 连续 5 次（第 1 次为冷启动）：

| 运行 | 墙钟 | `elapsedMs` | `scannedDirs` | 条数 |
|---|---|---|---|---|
| 1（冷） | 251 ms | 121 ms | 1575 | 41 |
| 2 | 135 ms | 120 ms | 1575 | 41 |
| 3 | 144 ms | 129 ms | 1575 | 41 |
| 4 | 146 ms | 131 ms | 1575 | 41 |
| 5 | 148 ms | 132 ms | 1575 | 41 |

热跑墙钟中位数 146 ms（`elapsedMs` 中位 131 ms），墙钟与 `elapsedMs` 差值最大 33 ms。

深度边界 `scan --root D:\Desktop --depth 6 --limit 500 --timeout 2s --json`：墙钟 1555 ms、`elapsedMs` 1538 ms、`scannedDirs` 8654、`timedOut=false`、58 条，未触及 2.2 s 硬预算。（系统繁忙时同一命令出现过 `elapsedMs=2003 / scannedDirs=4840 / timedOut=true`，墙钟 2030 ms，仍在预算内返回——看门狗生效，不挂住。）

作为对照，调研阶段用 Python 参考实现跑同一棵树：同进程内首次 11.9 s，后续 0.7–2.2 s。Go 版把「冷缓存 + 5000 目录」的最坏情况压进了 2 s 预算内。

## 0.2.0：交付物冒烟

用构建出的 `outputs/DevDock.exe`（不是测试目录里的副本），在隔离 APPDATA 下：

```text
HTTP=200 BODY={"configPath":"...\_smoke2\DevDock\config.json","ok":true,"pid":40736,"uptimeSec":2,"version":"0.2.0"}
PID=40736 PORT=53000
DevDock 0.2.0                                            VERSION_EXIT=0
{   "projects": [] }                                     LIST_EXIT=0
UI_STATUS=200 UI_BYTES=7907
CLEANUP_REMAINING=0
```

## 0.2.0：本轮修复的其它缺陷

除上述 fork bomb 外，端到端与界面走查过程中还发现并修复了三个缺陷（前两个会让 README 里的示例直接不可用）：

1. **`add` 从未成功过**：客户端把整个 `model.Project` 编码进请求体，服务端用 `DisallowUnknownFields` 严格解析，于是 `createdAt` / `updatedAt` 被判为未知字段，返回 `请求格式不正确：json: unknown field "createdAt"`。已改为只发送服务端接受的字段。
2. **位置参数写法全部失效**：标准库 `flag` 遇到第一个非 flag 参数即停止解析，所以 `devdock add 名称 --cwd ...` 里 `--cwd`、`--command` 根本没被读取，只报一句「必须同时提供 --cwd 与 --command」；`devdock start 名称 --wait-ready` 则直接报用法错误并退出码 2。已加入 `parseInterspersed`，位置参数与 flag 前置两种写法都可用。
3. **「新建项目」抽屉关不掉**：`web/style.css` 里 `.drawer-mask { display: flex }` 属于作者样式，优先级高于浏览器默认的 `[hidden] { display: none }`，因此 JS 把 `hidden` 设为 `true` 后抽屉依旧可见，遮罩还挡住了整个页面；关闭、取消、Escape、点遮罩四种关法全部失效。补一条 `.drawer-mask[hidden] { display: none }` 后恢复正常。

## 自动识别本机项目（0.2.0 新增）

需求与裁决：[`docs/AUTO_DETECT_REQUIREMENTS.md`](AUTO_DETECT_REQUIREMENTS.md)（调研）、[`docs/AUTO_DETECT_SPEC.md`](AUTO_DETECT_SPEC.md)（主 agent 拍板的规格）。

### 三个面

- CLI：`devdock scan [dirs...] --root ... --depth --timeout --limit --all --json`；`devdock add --auto --cwd <dir>`。
- HTTP：`POST /api/scan`（需 token；扫描中再来请求返回 409 `scan_in_progress`）。
- UI：顶栏「扫描本机」→ 抽屉（分组、置信度、端口来源、勾选导入、单条「填 入表单」）。

### 只读性（关键保证）

`testsupport/e2e/run_scan_e2e.ps1` 断言：扫描前后 `list` 的项目数不变、`config.json` 的 `LastWriteTimeUtc` 不变、`APPDATA\DevDock` 下不新增文件、扫描过程不新建任何子进程。实测：

```text
PASS  list 数量不变（1 -> 1）
PASS  config.json LastWriteTimeUtc 未变
PASS  APPDATA\DevDock 下没有新增文件
PASS  DevDock 服务没有为扫描新建任何子进程
PASS  机器上没有新增 node/go/fixture 进程
```

### 扫描端到端

```text
SCAN_E2E_RESULT=PASS
SCAN_E2E_MS=2245
SCAN_E2E_SUGGESTIONS=3
SCAN_E2E_SCAN_ONLY_MS=26
```

同一脚本还断言了 UI 结构与红线：`#btn-scan`/`#scan-mask`/`#scan-roots`/`#scan-start`/`#scan-cancel`/`#scan-list`/`#scan-summary`/`#scan-import` 存在、`scan-mask` 复用 `.drawer-mask`、`style.css` 保留 `.drawer-mask[hidden]`、`app.js` 未使用 `innerHTML`、默认勾选条件符合 A3、单批导入 ≤ 20、导入前二次确认、组内折叠 + 「还有 N 条」。

### 真机识别质量（`D:\Desktop`，depth 3，41 条）

必现且端口正确（4/4）：

| 项目 | 端口 | 来源 |
|---|---|---|
| `workshop\workshop-web` | 3100 | `vite.config` |
| `aihaoji-workspace\frontend_pc_web_mobile` | 3001 | flag |
| `huobao-drama\frontend` | 3013 | flag |
| `open-ai-canvas\web` | 3000 | flag |

必不现（4/4，无误伤）：`node_modules` 下的任何条目、`Toonflow-app\packages\ext\image`（`vite build --watch`）、`huobao-drama\desktop`（electron）、只含 `index.html` 的 `_downloads` 目录。

`add --auto --cwd D:\Desktop\DevDock\work` → `go run .`、`port=0`，识别结论打印到 stderr（不污染 `--json` 的 stdout），重复添加被拒。

### 真实浏览器验收

用 `paw browser-action` 在真实 Chrome 里走了一遍（不是静态检查）：

```text
navigate http://127.0.0.1:60244/?t=<token>
点击 #btn-scan        -> #scan-mask.hidden = false
点击 #scan-start      -> 5s 后 41 条建议、25 个分组、默认勾选 26 条
按 Escape            -> #scan-mask.hidden = true
点击遮罩             -> #scan-mask.hidden = true
```

截图：`_e2e/ui-scan-open2.png`（抽屉展开，可见分组、徽章、端口、底部「导入选中 (26)」与「扫描 1706 个目录 · 113ms · 41 条建议」）。

### 实现期发现并修掉的三个问题

1. **`tsx ./src/index.ts` 被当成服务**：白名单里的裸 `tsx`/`ts-node` 跑完即退出（本机 `open-design\tools\{dev,pack,release,serve}` 四例，调研 3.3 已点名）。已改为「不带 `watch`/`serve` 的一次性转译器不算服务」，并补 T08b/T08c 两条用例锁住。建议数因此从 46 降到 41。
2. **默认勾选会勾到 CLI 工具**：原来的默认勾选只看 `confidence !== 'low'`，会把 `node dist/cli.js --no-open`、`node serve.cjs` 这类条目勾上，一键导入就是一键导入一堆启动即退出的项目。已改为「非 low 且有端口 / 有框架 / high」才默认勾（Go 服务没有端口与框架线索，靠 high 兜住）；默认勾选数 24 → 26（Go 的两条回归勾选），无端口无框架的 15 条仍列出但不勾。
3. **扫描列表溢出盖住底部导入栏**：`.scan-list` 被 flex 压窄后内容溢出，视觉上叠在「导入选中」按钮下面。已改为列表自身滚动（`flex:1 1 auto; min-height:0; overflow-y:auto`），浏览器复验正常。

## 未验证（截至 0.3.0）

以下内容没有跑过，不并入上面的结论：

- **MCP 与真实 MCP host 对接**。目前只有协议层单测（握手、tools/list、错误码、stdout 洁净性等），未在 Claude Desktop、Cursor 等真实客户端中加载验证。
- **浏览器端交互体验的覆盖范围**。0.3.0 已在真实 Chrome 里走通主界面、日志/详情/扫描/表单四个抽屉、更多菜单、自绘确认框、搜索、焦点圈与两套主题（见上一节）。仍未验证的：SSE 状态推送（断线重连）、托盘交互、≥ 1000px 桌面宽度下的排版截图。
- **长时间稳定性**。没有做数小时/数天的持续运行、日志环形缓冲长期溢出、SSE 长时间连接断线重连的压测。
- **多项目并发**。端到端只验证了单项目；同时启动多个项目、端口冲突场景下的表现为未验证。
- **冷缓存扫描**。只测到「同一进程内首次 121 ms」；没有重启机器或清 FS 缓存后复测真冷缓存。
- **异常目录**。网络盘、OneDrive 占位文件（云端未下载）、超长路径（>260 字符）下的扫描行为未构造。
- **零样本生态规则**。`pom.xml`（Maven/Spring Boot）、`*.csproj`（.NET）、`docker compose` 在本机零样本，本轮**未上线**，不要写入结论。

## 不在首版范围

托盘体验、云同步未实现。全盘扫描未做，只做白名单根目录 + 深度/目录数/时延三重上限的有界扫描（见「自动识别本机项目」一节）。
