# 发布说明

## v0.2.0

这一版只做一件事：**把「自动识别本机项目」做出来**（用户原话：「这个项目不能自动识别吗？你这样很鸡肋」），外加把 0.1.1 里已定位但未发布的缺陷修复一起收口。

产物：`DevDock.exe`，10752000 bytes，SHA-256 `FE9B83C0A9241CF9CFD8BC2D4612CE8E5496156C13738176BA665F52592AC71F`。

### 新增：自动识别本机项目

以前只能 `devdock add <名称> --cwd X --command C` 手填三样东西。现在三个面都能用：

- **CLI**：`devdock scan`（可选 `--root` / `--depth` / `--timeout` / `--limit` / `--all` / `--json`），以及 `devdock add --auto --cwd <目录>`（识别完直接建，识别结论打到 stderr，不污染 `--json`）。
- **HTTP**：`POST /api/scan`（仍需 token；扫描中重复请求返回 409 `scan_in_progress`）。
- **UI**：顶栏「扫描本机」→ 抽屉。按仓库分组、显示框架/置信度/端口来源、端口占用预检、单条「填 入表单」、勾完批量导入（单批 ≤ 20、导入前二次确认、失败逐条报错不中断）。

识别范围与约束（本机实测）：

- **默认只扫白名单根目录**（用户主目录下常见开发目录 + 各固定本地盘同名子目录，最多 8 个），不做全盘扫描。
- **三重上限**：深度 ≤ 6、目录数 ≤ 5000、单次 ≤ 2 s（看门狗强停，绝不挂住）。`node_modules`、`.git`、`dist`、`build` 等一律剪枝。
- **端口推断四档**：命令行 flag → `vite.config` → 框架默认 → 留空。推断不出来就留空，不猜。
- **只读**：不写文件、不改配置、不启动任何进程。端到端有断言（扫描前后项目数与 `config.json` 的修改时间不变）。
- 覆盖 Node（Vite/Next/Nuxt/Metro/Remotion…）、Go、Python（Django/FastAPI/Flask…）、Rust；`pom.xml`/`*.csproj`/`docker compose` 本机零样本，本轮不产出。

### 已修复（含 0.1.1 的五项）

1. **`go test ./...` 全量退出码为 1**。根因是 `client.spawnService()` 在测试二进制里用 `os.Executable()` 拉起「自己」，形成指数级自我复制的进程树并锁住测试 exe。已加测试进程守卫 + 可注入的服务发现，并补回归测试。
2. **`devdock add` 从未成功过**。请求体带了服务端不接受的字段（`createdAt` 等），被严格解析拒绝。已改为只发服务端接受的字段。
3. **CLI 位置参数写法全部失效**。标准库 `flag` 遇到第一个非 flag 参数即停止解析。已支持位置参数与 flag 交错。
4. **README 构建命令有误**。`-H=windowsgui` 会让 CLI 不再向终端输出。已移除并写明原因。
5. **「新建项目」抽屉关不掉**。作者样式 `display:flex` 盖住了浏览器默认的 `[hidden]{display:none}`。已补 `.drawer-mask[hidden]`。
6. **`tsx ./src/index.ts` 被当成开发服务器**。裸 `tsx`/`ts-node` 跑完即退出（本机 `open-design\tools\*` 四例），现在必须带 `watch`/`serve` 才算服务。
7. **默认勾选会勾到 CLI 工具**。原来只看置信度，会把「启动即退出」的条目替用户勾上；现在要求「有端口 / 有框架 / high」才默认勾。
8. **扫描列表溢出盖住底部导入栏**。改为列表自身滚动。

### 已验证（附实测数据）

完整记录见 [`docs/VERIFICATION.md`](../docs/VERIFICATION.md)。

- `go build ./...`、`go vet ./...` 退出码 0。
- `go test -count=1 ./...` **退出码 0**（含新增 `internal/detect` 的 28 个用例）。
- 端到端 `run_e2e.ps1`：**PASS**。启动到 HTTP 可响应 281 ms；空闲 working set 11.7 MiB；`start` API 到进程已 spawn 中位数 16.8 ms；三层进程树（`cmd.exe → fixture.exe → fixture.exe -child`）停止后全部消失、端口释放。
- 扫描端到端 `run_scan_e2e.ps1`：**PASS**（含只读性与「不新建进程」断言）。
- 扫描性能（`D:\Desktop`，depth 3，5 次）：热跑墙钟中位 **146 ms**，`scannedDirs` 1575，41 条建议；depth 6 边界 1555 ms / 8654 目录，未触及 2.2 s 预算。
- 真机识别质量：4 条指定项目全部命中且端口正确（3100/3001/3013/3000），4 类误判（`node_modules`、`vite build --watch`、electron、纯静态页）全部不出现。
- 真实浏览器（Chrome）走通：打开扫描抽屉 → 扫描出 41 条 → Escape 关闭 → 点遮罩关闭。

### 未验证

- **MCP 与真实 MCP host 对接**（只有协议层单测）。
- **浏览器端其它交互**：首屏渲染、SSE 日志推送、托盘未在真实环境验证（扫描抽屉已验证）。
- **长时间稳定性**、**多项目并发**。
- **真冷缓存扫描**（未重启机器/未清 FS 缓存复测）。
- **异常目录**：网络盘、OneDrive 占位文件、超长路径。
- **`pom.xml` / `*.csproj` / `docker compose` 规则**：本机零样本，本轮未上线。

### 不在本版范围

托盘体验、云同步、全盘扫描（只做了白名单有界扫描）。
