# 自动识别项目 —— 开发规格（主 agent 拍板版）

> 输入：`docs/AUTO_DETECT_REQUIREMENTS.md`（调研 agent 产出，含实读 `file:line` 与本机实测数字）。
> 本文是**唯一施工依据**：与需求文档冲突时以本文为准，未在本文出现的细节按需求文档执行。
> 本文只做「裁决 + 落点 + 验收」，不重复调研过程。实现者：开发 agent。验收者：主 agent。

---

## A. 对需求文档第 10 节 13 个问题的裁决

| # | 问题 | 裁决 | 相对调研建议的改动 |
|---|---|---|---|
| A1 | 默认根目录 | 采纳：白名单（`%USERPROFILE%` 下一级常见目录 + 每个**固定本地盘**根下同名子目录），去重后取前 8 个；不扫盘根、不扫 `%USERPROFILE%` 整层 | 无 |
| A2 | 建议 vs 入库 | 采纳：`scan` 只读、只产出建议；导入必须显式（UI 勾选导入 / `add --auto`） | 无 |
| A3 | 批量导入 | 采纳：有，默认只勾 `high`+`medium`，单批 ≤ 20，二次确认（`confirm()`），逐条 `POST /api/projects`，失败逐条保留并显示 message，不中断 | 无 |
| A4 | 端口推断不出 | 采纳：`port=0`（留空），绝不猜 3000/8080 | 无 |
| A5 | 端口被占用 | 采纳：只标 `portInUse=true` + 文案，不阻止导入、不自动改端口 | 无 |
| A6 | Metro/Remotion/Docker | Metro、Remotion、Cargo **产出但 `confidence=low` + warning「非网页服务/默认端口」**；`docker compose`、`pom.xml`、JVM **v1 一律不产出**（本机零验证样本，宁缺勿滥）；`build.gradle` 只用于**排除**（`android/` 或含 `com.android.application`） | 比调研建议更保守：JVM/docker 整类下线 |
| A7 | 锁文件指向未安装的包管理器 | 采纳：`exec.LookPath` 校验，不可用回退 `npm run` + warning | 无 |
| A8 | monorepo 多 `package.json` | 采纳：全列出，按 `Group`（最近 `.git` 祖先）分组；UI 组内默认展开 5 条并显示「还有 N 条」 | 无 |
| A9 | 启动时自动扫描 | **不扫**。启动路径一行扫描代码都不加（老板第一要求是启动快） | 无 |
| A10 | 扫描根持久化 | **不做**。取消需求文档 6.5 的 `internal/config` 改动 | **改动**：现有没有 settings 写接口（`httpapi/api.go:50-59` 只有 health/meta/projects/events），为省重复输入新增一整条配置写链路不值；根目录按请求传入，UI 输入框留空即用默认 |
| A11 | UI 自动验收 | e2e 脚本只断言「`scan-mask` 等 DOM 存在 + 静态资源 200」；真实浏览器点击（开→扫→Esc 关）由**主 agent** 用 `paw-browser` 做并写进 `docs/VERIFICATION.md` | 浏览器自动化工具在本机不稳定（ref 点击落空、daemon 偶发断连），不塞进 CI 脚本 |
| A12 | MCP 加 `devdock_scan` | **不加**（`mcp.go:159` 既有约束，agent 可用 CLI） | 无 |
| A13 | Go `cmd/` 多入口 | 采纳：不产出（G4） | 无 |

### A 之外主 agent 追加的裁决

| # | 事项 | 裁决 | 理由 |
|---|---|---|---|
| A14 | 扫描结果缓存（需求 5.2 的 TTL 10s） | **不做**。只做「单飞」：扫描中再来请求直接 `409 scan_in_progress` | 缓存会让「刚导入的项目」在 10s 内仍被判重/状态陈旧；少一层状态，确定性测试（T27）也简单 |
| A15 | `depth` 下界 | API 允许 `0–6`（0 = 只判定根目录自身）；CLI 用户面仍是 `1–6` | `add --auto --cwd X` 需要「只看 X 本身」，不扫子目录，避免选错 |
| A16 | 端口占用预检的实现 | `internal/detect` 内用 `net.Listen("tcp","127.0.0.1:N")` 自己探，**不 import `internal/runner`** | 保持 detect 纯标准库、可单测，不让 runner 的生命周期渗进来 |
| A17 | `.env` 读取 | **完全不读 `.env`**（需求 4.8 的 `.env PORT=` 那一档删掉；命令行内 `PORT=8080 node x.js` 前缀仍要认） | 实测 22 个 `.env` 里 0 个含 `PORT=`，但 4 个含 `DB_PORT`；读它收益为零、隐私面为负 |
| A18 | 单次预算默认值 | 统一 `2000ms`（API 的 `budgetMs` 默认值与 CLI `--timeout` 默认值一致），范围 100–10000 | 需求文档里 1500/2s 两个默认值并存，会造成「CLI 不传参」和「API 不传参」结果不同 |

---

## B. 文件级任务清单

> 顺序即施工顺序。**不许新增第三方依赖**（`go.mod` 保持只有 `module devdock` + `go 1.25`）；不许新增 web 静态文件（`web/embed.go:11` 是显式文件名列表）。

### B1. 新包 `internal/detect`（纯标准库，只 import `model` + stdlib）

| 文件 | 内容 |
|---|---|
| `types.go` | `Suggestion`（字段见需求 4.1，一字不改）、`Result`、`Options`、`DefaultOptions()`、`(Options) normalize()`（补默认值 + 越界返回 error）、`var ErrBusy = errors.New("scan_in_progress")` |
| `scan.go` | `Scan(ctx, Options) (*Result, error)`：有界 BFS + 8 worker + `time.AfterFunc` 看门狗 + 剪枝（需求 4.2 S1–S7）+ 聚合排序（4.7 排序键：Group↑ → Port>0 优先 → Confidence high→low → Cwd↑）+ ID 去重（`-2`/`-3`）+ `map[lower(cwd)]` 跨根去重。**根目录本身也要判定**（depth 0）。`Scanner`（`sync.Mutex` + running 标志，`ErrBusy`）单飞靠 httpapi 层持有 |
| `node.go` | 需求 4.3 N1–N8：脚本键优先序、`isServer()` 分段判定（拆 `&&`/`&`、剥 `VAR=`/`chcp N`、`npm run X` 递归 ≤2 层）、`SERVER_CMD_RE`/`NOT_SERVER_RE`、runner 判定 + `LookPath` 回退、`vite.config.*` 端口、框架表 |
| `golang.go` | 需求 4.4 G1–G5（根 main → `go run .`；`cmd/` 唯一子目录 → `go run ./cmd/X`；多子目录仅名字唯一命中 → medium；否则不产出） |
| `python.go` | 需求 4.5 P1–P5（**依赖闸门优先**：没有服务框架依赖直接不产出）、入口文件 + `__main__`/`uvicorn.run`/`app.run` 判定、源码 `port=` 提取 |
| `other.go` | `Cargo.toml`+`src/main.rs` → `cargo run`（low）；`build.gradle(.kts)` 含 `com.android.application` 或目录名 `android` → **不产出**；只有 `index.html` → 不产出；`pom.xml`/`docker-compose.yml` → 不产出（A6） |
| `ports.go` | 统一端口优先级（需求 4.8，删掉 `.env` 档）、`--port N`/`--port=N` 通用、`-p N` 仅限白名单命令、`PORT=N` 前缀、框架默认表、`probePortInUse(port)`（A16） |
| `naming.go` | Name（`package.json#name` 去 scope → 目录名 → 泛名前拼父目录）、ID（ASCII slug，空则 `p-`+FNV-1a(cwd) 低 32 位 hex，≤64 字符）、Group（向上 ≤6 层找 `.git`，找不到取父目录） |
| `*_test.go` | 见 D 节清单 |

### B2. 接口层

| 文件 | 改动 |
|---|---|
| `internal/httpapi/api.go` | `API` 加 `scanner *detect.Scanner` 字段（在 `New` 里初始化）；`Handler` 注册 `mux.HandleFunc("/api/scan", a.handleScan)`；新增 `handleScan` + `scanRequest`（`DisallowUnknownFields` + 1MB 上限，仿 `api.go:452-491`）；响应前读 `store.List()` 标 `Duplicate/ExistingID` |
| `internal/httpapi/respond.go` | 词表加 `CodeScanInProgress = "scan_in_progress"` |
| `internal/client/client.go` | 加 `CodeScanInProgress`；新增 `func (c *Client) Scan(req ScanRequest) (*detect.Result, error)`（手写请求体，仿 `client.go:250-279`，**不许直接 Marshal 业务结构体**） |
| `internal/cli/cli.go` | `Run` 加 `case "scan"`；新增 `cmdScan`（用 `parseInterspersed`，位置参数 = `--root`）；`cmdAdd` 加 `--auto` 分支（识别 → 填 `name/command/port`，显式 flag 覆盖；识别失败退出码 1 + 中文原因；成功后打印识别结论到 **stderr**）；`printHelp` 补两行 |

### B3. 前端（零构建链、CSP 禁内联）

| 文件 | 改动 |
|---|---|
| `web/index.html` | `.topactions` 加 `<button class="btn ghost" id="btn-scan">扫描本机</button>`；新增 `<div class="drawer-mask" id="scan-mask" hidden>` + `<aside class="drawer scan-drawer">`，内部 id：`scan-roots`、`scan-start`、`scan-cancel`、`scan-list`、`scan-summary`、`scan-import` |
| `web/app.js` | `openScan/closeScan/runScan/importSelected/fillFromSuggestion`；`fetch('/api/scan',{signal})`；Escape 与遮罩点击复用文档级监听（`app.js:542-544`）；全部用 `el()`/`textContent`，**禁止 `innerHTML`** |
| `web/style.css` | 只加 `.scan-drawer`/`.scan-row`/`.scan-badge` 等；**任何新增类名若带 `display:flex/grid`，必须同时写 `.<类名>[hidden]{display:none}`**（上次抽屉关不掉的根因，见 `style.css:363-368`） |

### B4. 测试与文档

| 文件 | 改动 |
|---|---|
| `testsupport/e2e/run_scan_e2e.ps1` | 新增，按需求 9.2 的六步；输出 `SCAN_E2E_RESULT=PASS` / `SCAN_E2E_MS=<n>` |
| `README.md` | 66 行「自动发现不属于首版范围」改写；补 `scan` / `add --auto` 用法与「默认只扫白名单根目录」 |
| `docs/AUTO_DETECT_SPEC.md` | 本文件，末尾追加「实现偏差记录」小节（开发 agent 若偏离本文必须在此逐条写明原因） |
| **不改** | `internal/config/config.go`（A10）、`internal/mcpserver/*`（A12）、`docs/VERIFICATION.md` 与 `docs/RELEASE_NOTES.md`（由主 agent 验收后统一写数字） |

---

## C. 不可越界的红线

1. 扫描过程**零副作用**：不写文件、不 spawn 进程、不改 `store`/`config`。
2. `devdock add <名称> --cwd X --command C` 的旧行为**逐字节不变**；`--auto` 是新增分支。
3. 任何 `/api/*` 仍需 token；`/api/scan` 不特殊放行。
4. 退出码复用 0–7，不新增数字（`scan` 0 条建议 = 0）。
5. `--json` 时 stdout 只有 JSON，提示走 stderr。
6. 不扫非固定盘（`GetDriveType != DRIVE_FIXED` 跳过）、不跟随符号链接/junction、不进入以 `.` 开头的目录。
7. 单次扫描总时长**必须**在 `budget + 200ms` 内返回（`timedOut=true` 也算成功返回），任何情况下不挂住。
8. 新增代码不得 import `internal/runner`（A16）。

---

## D. 验收标准（主 agent 会逐条复跑）

### D1. 静态与单测

```powershell
cd D:\Desktop\DevDock\work
go build ./...    # 0
go vet ./...      # 0
go test -count=1 ./...   # 0，且现有 7 个包不退化
```

`internal/detect` 必须有下列用例（T 编号沿用需求 9.1）：T1、T2、T3、T4、T5、T6、T7、T8、T10、T11、T12、T13、T14、T15、T16、T17、T18、T19、T21、T22、T23、T24、T26、T27。其中：
- T9（锁文件回退）用二分断言：`yarn` 存在则 `yarn run dev`，否则 `npm run dev` + warning；
- T20 改为「`.env` 里有 `DB_PORT=3306` 且无 `PORT=` → `port==0`」（A17 后 `.env` 根本不读，此用例用于防回归）；
- T25（不可读目录）、T28（端口占用，需真占 5173）允许 `t.Skip` 并写明原因。

### D2. 端到端

```powershell
powershell -ExecutionPolicy Bypass -File .\testsupport\e2e\run_e2e.ps1        # E2E_RESULT=PASS（既有，不得退化）
powershell -ExecutionPolicy Bypass -File .\testsupport\e2e\run_scan_e2e.ps1   # SCAN_E2E_RESULT=PASS
```

### D3. 真机扫描性能（必须实测并把原始输出交给主 agent，不许估）

```powershell
.\DevDock.exe scan --root "D:\Desktop" --depth 3 --json     # 连续 5 次，第 1 次=冷
.\DevDock.exe scan --root "D:\Desktop" --depth 6 --limit 500 --timeout 2s --json
```

通过线：depth 3 热跑中位数 ≤ 400ms；depth 6 边界在 `2000+200ms` 内返回且 `timedOut=true`；`elapsedMs` 与 `Measure-Command` 差值 ≤ 200ms（差值超标要在报告里解释）；`scannedDirs` 量级与调研的 1575 / 8613 同量级（±50% 内可接受，偏差更大说明剪枝逻辑不同，必须解释）。

### D4. 识别质量（本机真实数据，主 agent 会抽查）

对 `D:\Desktop` depth 3 的扫描结果：
- 建议条数落在 **25–45** 区间（调研 depth≤3 命中 39 条）；
- 必须出现且端口正确：`workshop-web`→3100（vite.config）、`frontend_pc_web_mobile`→3001（flag）、`huobao-drama\frontend`→3013（flag）、`open-ai-canvas\web`→3000（flag）；
- **必须不出现**：任何 `node_modules` 下的条目、`Toonflow-app\packages\ext\image`（`vite build --watch`）、`huobao-drama\desktop`（electron）、`*_downloads` 纯 `index.html` 目录；
- `add --auto --cwd D:\Desktop\DevDock\work` → `go run .`，`port=0`。

---

## E. 交付格式（开发 agent 回给主 agent）

1. 改动文件清单（新增/修改，一行一个）；
2. `go build` / `go vet` / `go test -count=1 ./...` 的真实退出码与末尾输出；
3. `run_e2e.ps1` 与 `run_scan_e2e.ps1` 的 `*_RESULT=` 行与耗时；
4. D3 的 5 次原始耗时 + depth 6 边界耗时 + `scannedDirs/elapsedMs/timedOut` 实际值；
5. D4 的抽查结论（命中/误伤逐条）；
6. 与本规格的偏差（没有就写「无」）。

**不许**在回话里写「测试通过」而没有上面的原始输出。

---

## 实现偏差记录

由开发 agent 于实现完成后追加，逐条对应上文裁决/验收条目。

| # | 条目 | 偏差 | 原因与证据 |
|---|---|---|---|
| 1 | D4「建议条数落在 25–45 区间」 | **超出上界 1 条**：`D:\Desktop --depth 3` 实测 **46** 条（`scannedDirs=1575`） | 该区间的依据是调研 3.9 的 **39 条 JS 命中**（仅 Node/TS）。本实现按需求 4.3 的 `SERVER_CMD_RE`（显式包含 `tsx`/`ts-node`/`nodemon`/`node`/`bun`/`deno`）以及 4.4/4.5 的 Go、Python 规则产出，多出的 7 条是：`open-design\tools\{dev,pack,release,serve}`（`dev = tsx ./src/index.ts`，4 条）、`open-design\apps\daemon`（`pnpm run build && node dist/cli.js --no-open`）、`infinite-canvas\canvas-agent`（`tsx src/index.ts`）、以及 `open-ai-canvas\backend`（Go `cmd/server`，G3 唯一命中）。这些都是规则命中，不是实现错误；其中 `open-design\tools\*` 与调研 8 节列出的「CLI 工具」误判是同一批目录，但 `tsx` 在需求 4.3 的白名单里，按「SPEC 优先」不予剔除。D4 的 4 个必现端口与 4 个必不现项全部通过（见交付报告）。**如需严格落回 25–45，需要主 agent 裁决是否把 `tsx`/`node` 从 `SERVER_CMD_RE` 收紧。** |
| 2 | D3「depth 6 边界 …… `timedOut=true`」 | depth 6 实测**有时 `timedOut=false`**（系统空闲时 815–1748ms 跑完 8619–8657 个目录；系统繁忙时 2003–2004ms 命中预算、`timedOut=true`、4840 个目录） | 通过线是「在 `budget+200ms` 内返回」，两条路径都满足（wall 最大 2030ms < 2200ms）。`timedOut` 只是「是否撞上预算」的事实标记，更快时不该判失败。`scannedDirs` 两种情形为 4840 / 8619–8657，均在调研 8613 的 ±50% 内。 |
| 3 | B3 前端 | 无偏差（A3 的「默认只勾 high+medium」「单批 ≤ 20」「`confirm()` 二次确认」与 A8 的「组内默认展开 5 条 + 还有 N 条」**已实现**，并在 `run_scan_e2e.ps1` 里补了静态断言） | 初版漏实现，验收前已补齐。 |

其余裁决（A1–A18、B1–B4、C1–C8）与 D1/D2 无偏差。

### 主 agent 验收后的追加裁决（2026-11-18）

| # | 条目 | 裁决 | 证据 |
|---|---|---|---|
| 4 | 偏差 1 的「是否收紧 `tsx`」 | **收紧**：`tsx`/`ts-node` 不带 `watch`/`serve` 不算服务（A3 之外新增一条规则），建议数 46 → 41 | 本机 `open-design\tools\{dev,pack,release,serve}` 的 `dev` 确为 `tsx ./src/index.ts`（一次性 CLI）；`huobao-drama\backend` 的 `tsx watch src/index.ts` 仍保留。补 T08b/T08c 两条用例。 |
| 5 | A3「默认只勾 high+medium」 | **收紧**：默认勾选要求「非 low 且有端口 / 有框架 / high」 | 原规则会把 `node dist/cli.js --no-open`、`node serve.cjs` 这类跑完即退出的条目勾上（本机 46 条里 24 条默认勾，其中含上述 CLI 工具）。新规则默认勾 26 条（Go 服务靠 high 兜住），15 条无端口无框架的仍列出但不勾。 |
| 6 | B3 扫描抽屉 | 修一个布局缺陷：`.scan-list` 被 flex 压窄后内容溢出，视觉上叠住底部「导入选中」栏 | 改为 `flex:1 1 auto; min-height:0; overflow-y:auto`；真实浏览器复验（截图 `_e2e/ui-scan-open2.png`）。 |
