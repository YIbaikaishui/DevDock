# DevDock 自动识别本机开发项目 —— 需求文档

> 状态：调研完成，待主 agent 拍板（决策项见第 10 节），拍板后由开发 agent 实现。
> 本文只描述**要做什么、按什么规则做、怎么验收**，不含生产代码改动。
> 文中每条结论都标注了依据：代码给 `文件:行号`，勘察给实测数字。凡未实跑过的内容单独列在第 12 节「未验证」。

---

## 0. 一页结论

| 项 | 结论 |
|---|---|
| 痛点在哪个环节 | 只有 `devdock add` 一个创建入口，强制手填 `--cwd/--command/--port`（`internal/cli/cli.go:467-528`） |
| 识别结果如何落地 | **只产出「建议」，不自动入库**；导入必须由用户/脚本显式触发（理由见第 8 节） |
| 规则覆盖优先级 | ① Node（package.json scripts，本机 177 个）→ ② Go（9 个 go.mod）→ ③ Python（有服务框架依赖才算，25 个 pyproject / 19 个 requirements）→ ④ Rust/JVM/Docker 先给「能判定则给」的兜底，默认不产出建议 |
| 端口策略 | 显式 `--port`/`-p` > 配置文件（vite/pyproject）> 框架默认表 > **留空**（不猜） |
| 就绪路径 | **一律留空**，不猜 `/health`（现有探针默认只认 200–399，根路径常返 404 会误判超时，`internal/model/model.go:41-51`） |
| 扫描范围 | 默认**不全盘**：白名单常见开发目录（最多 8 个根）+ depth 3 + 1.5s 硬预算，可 `--root` 覆盖 |
| 性能预算 | depth 3 目标 ≤ 300ms（热）/ ≤ 2s（冷）；硬上限 1.5s 必到（超时返回 `timedOut:true`，绝不挂住） |
| 新增接口面 | CLI `devdock scan` + `devdock add --auto`；HTTP `POST /api/scan`；UI 一个扫描抽屉；**不加 MCP 工具**（v1） |
| 新增代码面 | 新包 `internal/detect`（纯标准库），改 `httpapi` / `client` / `cli` / `config` / `web` / 文档 |

---

## 1. 现有架构与新增功能的落点

### 1.1 包结构与依赖方向（实读）

| 包 | 职责 | 与本次相关的内容 |
|---|---|---|
| `internal/model` | 纯数据类型，无副作用（`model.go:1-3`） | `Project`（`model.go:54-65`）、`ReadinessConfig`（`33-38`）、`View`（`98-101`）、`Defaults()`（`41-51`）、`ReadyURL()`（`105-110`） |
| `internal/config` | `%APPDATA%\DevDock\config.json` 读写，原子替换（`config.go:6-8`、`214-248`） | `SchemaVersion=1`（`26`）、`Settings`（`43-47`）、`File`（`50-56`）、`Load` 容忍损坏（`125-168`） |
| `internal/store` | 项目定义内存索引 + 持久化，运行态不落盘（`store.go:1-3`） | `Create` 自动 slug（`95-130`）、`validate`（`226-262`）、`slugify`（`266-289`）、`ErrDuplicate`（`26`） |
| `internal/runner` | 进程 Job、就绪探测、端口检测 | `CheckPortAvailable`（`runner.go:215-223`）、端口冲突在 `Start` 里先检（`275-279`）、注入 `DEVDOCK_PORT`（`239-240`）、就绪探测仅当 `readiness.path != "" && port > 0`（`328`） |
| `internal/httpapi` | 本机 HTTP API + 安全中间件 + SSE | 路由表（`api.go:47-62`）、子路径分发（`158-191`）、严格 JSON（`484-491`）、错误码词表（`respond.go:23-33`） |
| `internal/client` | CLI/MCP 共用 HTTP 客户端，不含业务逻辑（`client.go:1-2`） | `Discover` 自动拉起服务（`66-85`）、`Create` 只发服务端认识的字段（`255-279`） |
| `internal/cli` | 子命令分发与退出码 | 退出码常量（`cli.go:22-32`）、`Run` 分发（`42-73`）、`parseInterspersed`（`129-143`）、`emitJSON`（`604-613`） |
| `internal/mcpserver` | 4 个 MCP 工具 | `toolDefs`（`mcp.go:159-228`），注释明确「只有 4 个工具，不随意扩张」（`159`） |
| `web` | 静态前端，全部 embed 进 exe | `//go:embed index.html style.css app.js favicon.svg`（`embed.go:11`）——**文件名写死，新增文件必须同步改这里** |

### 1.2 必须遵守的既定约定（违反会被现有测试或安全模型打回）

| # | 约定 | 依据 | 对新功能的具体要求 |
|---|---|---|---|
| 1 | 仅标准库，无第三方依赖 | `go.mod` 只有 `module devdock` + `go 1.25`；`README.md:44-52` | 识别器只能用 `os`/`path/filepath`/`encoding/json`/`regexp`/`bufio`；**不得引入 glob 库**（devwebui 用了 tinyglobby/yaml，我们不能跟） |
| 2 | HTTP 写接口用 `DisallowUnknownFields` 严格解析，请求体上限 1MB | `api.go:484-491` | `POST /api/scan` 的请求体类型必须与客户端发送字段**逐字一致**；客户端不得直接 `json.Marshal(model.Project)`（`client.go:250-279` 已踩过 `createdAt` 的坑） |
| 3 | 客户端只发服务端认识的字段 | `client.go:250-254`、`docs/VERIFICATION.md:175` | 新增 `Scan` 客户端方法同样手写请求体结构体 |
| 4 | CLI 位置参数可与 flag 交错 | `cli.go:129-143` | `devdock scan D:\Desktop --json` 与 `devdock scan --root D:\Desktop --json` 都应能用 → 用 `parseInterspersed` |
| 5 | `--json` 时 stdout 只输出 JSON，提示一律走 stderr | `cli.go:1-2`、`604-613` | `scan --json` 不得混入「扫描中…」等文字 |
| 6 | 稳定退出码 0–7 | `cli.go:22-32`、`146-161` | 复用既有码，不新增数字（见 7.1） |
| 7 | 所有 `/api/*`（除 `/api/health`）必须带 token | `security.go:70-77` | `POST /api/scan` 自动受保护，UI 侧沿用 `api()` 封装（`app.js:54-76`） |
| 8 | CSP 禁内联脚本、禁外链 | `security.go:125-127` | 扫描 UI 的 JS 必须写在 `app.js`，样式写在 `style.css`，不得内联 `<script>`/`<style>` |
| 9 | 前端零构建链 | `web/embed.go:1-11`、`style.css:1-3` | 不加打包器、不加框架；新文件必须加进 embed 指令 |
| 10 | 重启后不信任陈旧运行态 | `store.go:1-3` | 扫描缓存只放内存，不落盘 |
| 11 | `add` 的既有用法不能退化 | `cli.go:483-490`、`docs/VERIFICATION.md:176` | `devdock add 名称 --cwd X --command C` 行为完全不变 |

### 1.3 可直接复用的现成能力

| 能力 | 位置 | 复用方式 |
|---|---|---|
| `CheckPortAvailable(port)` 试绑回环端口 | `runner.go:215-223` | 建议里预检端口占用，`portInUse` 字段直接用它 |
| `slugify(name)` 生成 ID | `store.go:266-289` | 只保留 ASCII；中文名会退化成 `project-<纳秒>`（`282-284`），所以**扫描层要自己生成稳定 ID 再显式传入** |
| `store.Create` 的重复校验 | `store.go:108-110` | 导入时按 `ErrDuplicate` 提示，不要自己再判一次 |
| `printTable` 中文对齐表格 | `cli.go:616-661` | `scan` 的表格输出直接复用 |
| `emitJSON` | `cli.go:604-613` | 同上 |
| 隔离 APPDATA 的测试手法 | `store_test.go:13-26`（`t.Setenv("APPDATA", t.TempDir())`） | 单测构造临时项目树时同样隔离 |
| 真实进程夹具 `testsupport/fixture` | `testsupport/fixture/main.go:25-119` | 端到端可用它验证「扫描不启动任何进程」 |

---

## 2. 参考项目 devwebui 的做法（只读调研，未做任何修改）

**结论：它有完整的自动识别能力，而且做得相当成熟。** 我们的规则可以借鉴其判定思路，代码不照抄（它是 Bun + 第三方库，我们只能标准库）。

| 能力 | 位置 | 做法要点 |
|---|---|---|
| 单项目识别 | `server/src/detect.ts:307-354`（`detectProject`） | 读 `package.json` → 找 dev 脚本 → 推断框架/端口/名称 → 返回 `Detection`（**不落盘**） |
| 脚本键名判定 | `detect.ts:17` | `^(dev|start|serve|preview)(:[\w.-]+)?$/i` |
| 命令体判定 | `detect.ts:21`（`SERVER_CMD_RE`） | 白名单正则：vite（排除 `vite build`）、`next dev/start`、`react-scripts start`、`nuxt`、`astro dev`、`webpack serve`、`ng serve` 等 |
| 非服务命令黑名单 | `detect.ts:25`（`NOT_SERVER_RE`） | `build|--check|eslint|prettier|tsc|typecheck|vitest|jest|playwright|cypress|test|lint|audit|codegen|generate` |
| 框架默认端口表 | `detect.ts:28-41` | Astro 4321 / Next 3000 / Nuxt 3000 / Remix 3000 / CRA 3000 / webpack 8080 / Angular 4200 / Vue CLI 8080 / SvelteKit 5173 / Parcel 1234 / Rsbuild 3000 / Vite 5173 |
| 显式端口解析 | `detect.ts:86-89` | `(?:--port[ =]|-p[ =]|\bPORT[ =])(\d{2,5})` |
| vite 配置端口 | `detect.ts:96-118` | 依次尝试 `vite.config.{ts,js,mjs,mts,cjs,cts}`，匹配 `server:{...port:N}`，失败再退到任意 `port: N` |
| 包管理器判定 | `detect.ts:65-84` | **只看锁文件**（`package-lock.json`/`pnpm-lock.yaml`/`yarn.lock`/`bun.lock`），映射到 `npm run` / `pnpm` / `yarn` / `bun run` 前缀 |
| 命名 | `detect.ts:131-139` | 脚本键 → `dev:web` 变 "Web"；项目名取 `package.json#name` 去 npm scope 再 TitleCase |
| 同 ID 去重 | `detect.ts:164-170` | 冲突时 `-2`、`-3` 递增 |
| monorepo 展开 | `detect.ts:245-304` | 解析 `workspaces` 字段与 `pnpm-workspace.yaml`，最多 `MAX_WORKSPACE_DIRS=60` 个包；单项目最多 `MAX_PROCESSES=12` 个进程 |
| 全盘扫描 | `server/src/scan.ts:256-420` | BFS + 工作池；剪枝 `node_modules/dist/build/out/coverage/vendor/target/bin/obj/__pycache__/venv/.venv/go` 与全部点目录（`22-48`、`281`）；不跟随符号链接（`299`） |
| 扫描预算档位 | `scan.ts:163-171` | `quick: depth 3 / 6000ms / 500 条`；`deep: depth 16 / 30000ms / 5000 条`；`scoped`、`startup` 各一档 |
| 取消与看门狗 | `scan.ts:370-387` | 独立 `setTimeout(budget+100ms)` 兜底，防止所有 `readdir` 都卡住时不返回；HTTP 请求 signal 中断即停 |
| 并发 | `scan.ts:185-218` | 全守护进程最多 1 个扫描在跑，串行 baton；相同参数的并发请求合并 |
| 默认根目录 | `scan.ts:123-134` | `os.homedir()` + Windows 上除系统盘外的所有固定盘根目录 |
| 结果去向 | `server/src/projects/load-target.ts:49-56`、`server/src/http/project-routes.ts:194-217` | 返回 **proposal / 预览**，用户确认后才写 `.devwebui`；`scaffoldDevWebUIFile` 明确「never overwrites」（`load-target.ts:66-75`） |

**我们照搬什么：** 判定分层（键名 → 命令体白名单 → 黑名单）、框架默认端口表、显式端口正则、vite 配置端口、`-2` 去重、预算档位、串行 + 看门狗、结果只做预览。
**我们不照搬什么：** ① `package-manager-detector`/`tinyglobby`/`yaml` 等第三方依赖；② 默认扫 home + 全盘（太慢，见 5.1）；③ 只认 `package.json`（本机还有 9 个 Go、13 个 Python 服务型项目）；④ workspace glob 展开（标准库实现 glob 成本高，v1 改为「扫到哪个 package.json 就认哪个」+ 按仓库分组）。

---

## 3. 本机真实项目勘察（这是规则阈值的依据）

### 3.1 方法（可复现、有边界）

- 根目录 `D:\Desktop`，BFS，最大深度 6，单目录最多读 5000 条目，总用时预算 150s，不跟随符号链接。
- 跳过 `node_modules / dist / build / out / coverage / vendor / target / bin / obj / __pycache__ / venv / .venv / env / .next / .nuxt / .output / .cache / Pods` 以及**所有以 `.` 开头的目录**。
- 「候选项目根」= 目录内出现以下任一：`package.json`、`go.mod`、`pyproject.toml`、`requirements.txt`、`manage.py`、`Cargo.toml`、`pom.xml`、`build.gradle(.kts)`、`*.csproj`、`docker-compose.yml`、`Gemfile`、`composer.json`。
- 实测规模：**访问 8613 个目录**（depth 0–6 分别 1/42/286/1246/2558/2855/1610），**候选项目根 236 个**，其中能进一步判定出「可启动服务」的 JS/TS 根 **48 个**。

> 对照：纯 Python 单线程参考实现走完这 8613 个目录，**热缓存 0.7–2.2s**；同一台机器上第一次（冷缓存）走 5000 个目录用了 **11.9s**。这两个数字直接决定了第 5 节的预算：**冷缓存是不可忽略的成本**。

### 3.2 信号文件出现频次（按「含该文件的目录数」）

| 文件 | 次数 | 文件 | 次数 |
|---|---|---|---|
| `index.html` | 237 | `go.mod` | 9 |
| `package.json` | 177 | `main.py` | 9 |
| `tsconfig.json` | 138 | `uv.lock` | 8 |
| `package-lock.json` | 42 | `bun.lock` | 8 |
| `pyproject.toml` | 25 | `app.py` | 8 |
| `vite.config.ts` | 24 | `Makefile` | 5 |
| `.env` | 22 | `yarn.lock` | 5 |
| `requirements.txt` | 19 | `next.config.mjs` | 5 |
| `pnpm-lock.yaml` | 14 | `settings.py` | 4 |
| `docker-compose.yml` | 13 | `build.gradle` | 4 |
| `next.config.js` | 3 | `next.config.ts` | 2 |
| `server.py` | 3 | `go.sum` | 2 |
| `docker-compose.yaml` | 2 | `Gemfile` | 2 |
| `manage.py` | 2 | `build.gradle.kts` | 2 |
| `Cargo.toml` | 1 | `vite.config.js` | 1 |
| `nuxt.config.ts` | 1 | **`pom.xml`** | **0** |
| **`*.csproj`** | **0** | | |

**结论：** 规则要覆盖 Node（压倒性主力）、Go、Python；Rust/JVM/Docker 只能写「能判定则给」的兜底，本机几乎没有样本（`Cargo.toml` 唯一一例是 devwebui 的 Rust 托盘程序 `misc/tray-host-native`，不在 `D:\Desktop` 顶层项目里）。Maven/.NET 规则在本机**零样本，属未验证规则**。

### 3.3 package.json 的 scripts 实际情况

177 个 `package.json` 中，68 个含 `dev/start/serve/preview` 之一。键名频次（前 20）：

| 键 | 次数 | 键 | 次数 |
|---|---|---|---|
| `build` | 114 | `preview` | 14 |
| `typecheck` | 87 | `clean` | 13 |
| `test` | 65 | `test:watch` | 12 |
| **`dev`** | **64** | `postinstall` | 10 |
| `lint` | 27 | `prepublishOnly` | 8 |
| **`start`** | **21** | `format` | 8 |

真实命令样本（全部来自本机，摘录关键类别）：

| 目录 | 键 | 命令 | 识别结论 |
|---|---|---|---|
| `aihaoji-workspace\aihaoji-app-module-detail` | dev | `vite` | ✅ Vite / 5173 |
| `aihaoji-workspace\frontend_pc_web_mobile` | dev | `next dev -p 3001` | ✅ Next / **3001（显式）** |
| `huobao-drama\frontend` | dev | `nuxt dev --port 3013` | ✅ Nuxt / **3013** |
| `open-ai-canvas\web` | dev | `vite --host 0.0.0.0 --port 3000` | ✅ Vite / **3000** |
| `AiToEarn\project\aitoearn-web` | dev | `next dev -p 6061` | ✅ Next / **6061** |
| `huobao-drama\desktop` | dev | `npm run build:backend && npm run build:main && electron .` | ❌ 桌面应用 → 排除 |
| `Toonflow-app\packages\ext\image` 等 10 个 | dev | `vite build --config viteConfig.ts --watch` | ❌ 库构建 → 排除（**最容易误判的一类**） |
| `pi\packages\orchestrator` | dev | `tsgo -p tsconfig.build.json --watch ...` | ❌ 类型检查 → 排除 |
| `AiToEarn\project\aitoearn-electron` | dev | `chcp 65001 && vite` | ✅ 需剥离 `chcp` 前缀后判定为 Vite |
| `qiaomu-clipper` | dev | `npm run dev:chrome` | ⚠️ 间接调用，需**递归解析 1 层**才能命中 |
| `aihaoji-workspace\frontend_pc_web` | dev / start | `next dev` / `NODE_OPTIONS=... next start -p 3001` | ✅ 取 dev，端口 3000 |
| `aihaoji-workspace\noteai-app` | start | `react-native start` | ✅ 判定为 Metro / 8081（是否值得导入见 10.6） |
| `open-design\skills\...\remotion-template` | start | `remotion studio` | ✅ Remotion / 3000 |
| `Toonflow-app`（根） | dev | `bun run --filter '@toonflow/web' --filter '@toonflow/server' dev` | 依赖 bun → 可给建议但必须 warn |

**关键发现：**
1. **`vite build --watch` 必须排除**，本机有 10 例；不排除会一次性刷出 10 条无效建议。
2. **`dev` 优先于 `start`** 是对的：`start` 常是 `next start`（需先 build）或带 `NODE_OPTIONS` 的产物入口。
3. **`&&` 串联、`chcp 65001 &&`、`VAR=x cmd`、`npm run X` 间接调用**四种形态真实存在，命令体解析必须按「段」处理而不是整串匹配。
4. **端口两种来源都要读**：48 条里只有 **5 条**命令带 `-p`/`--port`（`aihaoji-workspace\frontend_pc_web_mobile` 3001、`huobao-drama\frontend` 3013、`infinite-canvas\web` 3000、`open-ai-canvas\web` 3000、`AiToEarn\project\aitoearn-web` 6061），其余靠框架默认；而 25 个 `vite.config.*` 里有 **9 个**写了 `port`（3.5），其中 6 个**不等于 5173**（`workshop-admin` 3200、`workshop-web` 3100、`xianyu.../frontend` 9000、`devwebui/web` 4010、`EverOS\frontend` 3000、`handout-agent` 5173）。→ 命令 flag 与 vite 配置必须都解析，只靠默认值会错。

### 3.4 锁文件分布（决定启动命令前缀）

| 锁文件 | 数量 | 本机对应可执行文件 |
|---|---|---|
| `package-lock.json` | 42 | `npm` ✅ 存在 |
| `pnpm-lock.yaml` | 14 | `pnpm` ✅ 存在（`C:\nvm4w\nodejs\pnpm`） |
| `bun.lock` | 8 | `bun` ✅ 存在，但**老板明确不要求装 Bun** |
| `yarn.lock` | 5 | `yarn` ❌ **本机未安装** |

其他工具链实测：`node`/`npm`/`go`/`py`/`uv`/`cargo`/`java`/`mvn` 存在；`gradle`、`docker` **不存在**。

**结论：** 不能像 devwebui 那样「锁文件说什么就用什么」——`yarn.lock` 项目在这台机器上直接跑 `yarn dev` 会失败。规则须为：**锁文件推断 + `exec.LookPath` 可用性校验，不可用回退 `npm run` 并在建议里 warn**。

### 3.5 端口与 `.env` 的真实价值

- `vite.config.*` 共 25 个，其中 **9 个显式写了 `port`**（解析成功率 9/25，其余走默认）。
- `.env` 共 22 个，**没有任何一个含 `PORT=`**。
- 但 `.env` 里有 **`DB_PORT`（4 次）、`REDISPORT`（3 次）** —— 这是陷阱：任何「在 .env 里找 port」的模糊匹配都会把数据库端口当成服务端口。

**结论：** `.env` 的 `PORT=` 解析「保留但优先级最低」，且**只认键名严格等于 `PORT`**，绝不允许子串匹配 `DB_PORT`/`REDISPORT`。这条实测数据（0/22）也说明它基本用不上，不要为它写复杂逻辑。

### 3.6 Python 候选根的真实成色

34 个 Python 候选根里，只有 13 个能读到服务框架依赖，**10 个完全没有服务信号**：

| 有服务依赖（可判定） | 无任何服务信号（必须排除） |
|---|---|
| `EverOS`（fastapi,uvicorn）、`MediaCrawler`（fastapi+`main.py`+`api/main.py`）、`aihaoji-workspace\backend`（aiohttp,fastapi,starlette,uvicorn + `src/main.py`）、`backend_global`、`Cairn\cairn`、`deer-flow\backend`、`workshop\workshop-mcp`（starlette,uvicorn）、`workshop\workshop-service`（fastapi）、`xianyu...\{backend-web,scheduler,websocket}`（+`main.py`）、`workshop-templates\...\apps\server`（django + `manage.py` + `run.py`）、（flask 模板 + `run.py`） | `AutoResearch`、`Interface_auto_pro`、`open_deep_research`、`PageIndex`、`superbakup`、`ai-services\LuxTTS`、`workshop\packages\contracts`、`sdk\memory-core\python`、`zimeiti\_downloads\AgnesAI-Models`、`workshop\workshop-harness` |
| `TrendRadar\docker`（`manage.py`）、`ai-services`（`run.py`）、`urlfinder`（`main.py`，但无框架依赖） | |

**结论：** 「有 `pyproject.toml`/`requirements.txt` 就当项目」会产出 10 条以上噪音。Python 必须**先看依赖**（fastapi/starlette/uvicorn/aiohttp/flask/django/sanic/litestar），再看入口文件。

### 3.7 Go / JVM 的真实形态

| 目录 | go.mod module | 入口 | 端口线索 |
|---|---|---|---|
| `DevDock\work` | `devdock` | 根目录 `package main` + `func main` | 无（`0` 是默认值） |
| `open-code-review` | `github.com/alibaba/open-code-review` | `cmd/opencodereview`（**cmd 下仅 1 个子目录**） | 源码里出现 1234/3000（文档站） |
| `open-ai-canvas\backend` | `infinite-canvas/backend` | `cmd/` 下 **4 个**子目录（`host-updater`、`migrate-*`），全是工具 | `:8080` |
| `open-code-review\pages` | 注释说明「仅为让文档站不并入根模块」 | 无 main | — |
| `DevDock\archive\spikes\spike{,2,3,4}` | 各自 module | 根目录 main | — |

**结论：** Go 的入口判定顺序应为「根目录有 main → `go run .`；否则 `cmd/` 恰好 1 个子目录 → `go run ./cmd/X`；`cmd/` 多个子目录且有名字命中 `server|api|web|app|gateway` → 用它；否则**不产出建议**」。端口一律留空（真实值散落在源码常量里，靠正则猜会错）。

`build.gradle` 4 例**全部位于 React Native 项目的 `android/` 子目录**（`aihaoji-workspace\aihaoji-frondend-app\android`、`noteai-app\android` 等），是移动端构建脚本，**不能当成 JVM 后端项目**。`pom.xml` 本机 0 例。

### 3.8 仓库边界（分组依据）

抽查 13 个多项目顶层目录，**12 个本身是 git 仓库**（只有 `TencentDB-Agent-Memory-feat-server_team` 没有 `.git`）。而建议集中在少数仓库里：`aihaoji-workspace` 11 条、`workshop` 6 条、`open-design` 6 条、`Toonflow-app` 4 条、`TencentDB-...` 4 条 —— **48 条建议只落在 15 个顶层目录里**。

**结论：** 用「最近的存在 `.git` 的祖先目录」做分组键，可以把 48 条建议折叠成 ~15 组，直接解决 monorepo 噪音。

### 3.9 建议的深度分布（决定 depth 默认值）

48 条 JS 建议相对扫描根的深度分布：

| 深度 | 1 | 2 | 3 | 4 | 5 | 6 |
|---|---|---|---|---|---|---|
| 条数 | 1 | 25 | 13 | 4 | 1 | 4 |
| 累计 | 1 | **26** | **39 (81%)** | **43 (90%)** | 44 | 48 |

对应目录规模：depth ≤ 3 共 **1575** 个目录；≤ 4 共 **4133** 个；≤ 6 共 **8598** 个。

**结论：depth 3 能覆盖 81% 的可识别项目，且目录量只有 depth 6 的 18%。默认 3，上限 6。**

---

## 4. 识别规则（文档核心，开发 agent 按表实现）

### 4.1 数据结构

建议放在新包 `internal/detect`，只依赖 `devdock/internal/model`（不 import store/httpapi，避免环）。

```go
// Suggestion 是一条「可导入的项目建议」。它是纯数据，不含运行态。
type Suggestion struct {
    Cwd         string   `json:"cwd"`                 // 绝对路径，已 Clean，无反斜杠混用
    Name        string   `json:"name"`                // 建议显示名，允许中文
    ID          string   `json:"id"`                  // 建议 ID，slug 合法（^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$）
    Kind        string   `json:"kind"`                // node|go|python|rust|jvm|docker
    Framework   string   `json:"framework,omitempty"` // vite|next|nuxt|astro|django|fastapi|...
    Command     string   `json:"command"`             // 完整启动命令；判定不出时为空串
    CommandFrom string   `json:"commandFrom,omitempty"` // 证据："package.json#scripts.dev" / "manage.py"
    Port        int      `json:"port"`                // 0 = 未能推断
    PortFrom    string   `json:"portFrom,omitempty"`  // flag|vite.config|framework-default|django-default
    Confidence  string   `json:"confidence"`          // high|medium|low
    Warnings    []string `json:"warnings,omitempty"`  // 「脚本依赖 bun，本机未安装」等
    PortInUse   bool     `json:"portInUse,omitempty"`
    Duplicate   bool     `json:"duplicate,omitempty"` // 已有项目占用同一 cwd
    ExistingID  string   `json:"existingId,omitempty"`
    Group       string   `json:"group,omitempty"`     // 最近 .git 祖先；无则取 cwd 的父目录
}

// Result 是一次扫描的完整结果。
type Result struct {
    Roots       []string     `json:"roots"`
    Suggestions []Suggestion `json:"suggestions"`
    ScannedDirs int          `json:"scannedDirs"`
    ElapsedMs   int64        `json:"elapsedMs"`
    Truncated   bool         `json:"truncated"`  // 命中 limit / maxDirs
    TimedOut    bool         `json:"timedOut"`   // 命中时间预算
    Depth       int          `json:"depth"`
}
```

`Suggestion` 刻意**不内嵌 `model.Project`**：建议里 `command` 可能为空（Python 判不出入口时），而 `store.validate` 会拒绝空命令（`store.go:227-230`）；导入时再走 `POST /api/projects` 的正常校验。

### 4.2 通用扫描规则（先剪枝，再判定）

| # | 规则 | 值 | 依据 |
|---|---|---|---|
| S1 | 不跟随符号链接/junction | `Dirent.IsDir()` 且 `Mode()&os.ModeSymlink == 0` | devwebui `scan.ts:299`；Windows junction 会造成环 |
| S2 | 跳过所有以 `.` 开头的目录 | 直接剪枝 | devwebui `scan.ts:281`；本机 `.git/.next/.venv` 数量巨大 |
| S3 | 剪枝目录名（不区分大小写） | `node_modules`、`dist`、`build`、`out`、`coverage`、`vendor`、`target`、`bin`、`obj`、`__pycache__`、`venv`、`.venv`、`env`、`Pods`、`DerivedData`、`$RECYCLE.BIN`、`System Volume Information`、`Windows`、`Program Files`、`Program Files (x86)`、`AppData`、`WindowsApps`、`steamapps`、`SteamLibrary` | devwebui `scan.ts:22-48`；本机 `D:\Desktop` 下这些目录确实存在且体量最大 |
| S4 | 深度上限 | 默认 3，参数可调 1–6，超限不进入 | 3.9 的实测分布 |
| S5 | 单目录条目上限 | 2000；超出时按名字排序取前 2000 并置 `Truncated`（本机无目录超过 5000 条目） | 3.1 |
| S6 | 总目录数上限 | 20000（默认根下实测只有 1575–8613） | 3.1 |
| S7 | 单次总时长硬上限 | 默认 1500ms，上限 10000ms；到点立即返回已收集结果并置 `TimedOut` | 第 5 节 |
| S8 | 并发 | 8 个 worker（I/O 密集，标准库 `readdir` 用 goroutine 池即可）；全局同时只允许 1 次扫描，相同参数 10s 内复用结果 | devwebui `scan.ts:185-218` 的思路 |
| S9 | 取消 | 扫描函数签名带 `context.Context`；HTTP 层传 `r.Context()`，CLI 传自己的 timeout ctx；`ctx.Done()` 时返回已收集结果 | 需求硬约束 |
| S10 | 只读 | 扫描过程**不写任何文件**、不 spawn 任何进程、不读 `.env` 的值以外的敏感内容 | 见 11.4 的验收测试 |
| S11 | 排除已管理项目所在目录 | 结果里标 `Duplicate/ExistingID`，不隐藏（用户可能想改配置） | 便于 UI 灰显 |

### 4.3 Node / TypeScript 规则（本机主力，实测 48 条命中）

**判定顺序（按表从上到下，命中即停）：**

| # | 条件 | 产出 |
|---|---|---|
| N1 | 目录内有 `package.json` 且是合法 JSON（解析失败 → 不产出，除非有 `go.mod` 等其它信号） | 进入 N2 |
| N2 | 按 `dev` → `develop` → `start` → `serve` → `preview` 顺序取脚本；**对每个候选先做 N3 的「服务性判定」，不通过则取下一个键**；全部不通过 → 不产出建议 | — |
| N3 | 服务性判定 `isServer(cmd)`：① 按 `&&`、`&` 拆段；② 每段剥掉 `VAR=value` 前缀与 `chcp \d+`；③ 任一段命中 `SERVER_CMD_RE` 且不命中 `NOT_SERVER_RE` → 真；④ 段形如 `npm run X`/`pnpm X`/`yarn X`/`bun run X` → 递归解析 `scripts[X]`（**深度 ≤ 2**，防环） | 真/假 |
| N4 | 启动命令 = `<runner> run <key>`（`runner` 见 N5）；`CommandFrom = "package.json#scripts." + key` | `npm run dev` 等 |
| N5 | runner 判定：锁文件 → `pnpm⊃yarn⊃bun⊃npm` 优先级取**唯一**一个；再 `exec.LookPath(runner)`，不存在则回退 `npm run` 并加 warn「检测到 pnpm 锁文件但本机无 pnpm，已用 npm 运行」 | `npm`/`pnpm`/`yarn`/`bun` |
| N6 | 命令体里出现 `bun ` 且 `exec.LookPath("bun")` 失败 → warn「该脚本依赖 bun，本机未安装」并降级 `confidence=low` | warn |
| N7 | 端口：见 4.8 的统一端口优先级 | `Port`/`PortFrom` |
| N8 | 框架：命中 `FRAMEWORK_TABLE` 的第一个 | `Framework` |

`SERVER_CMD_RE`（Go regexp，`(?i)`）：

```
\b(vite|next\s+(dev|start)|nuxt|nuxi\s+dev|astro\s+(dev|preview)|react-scripts\s+start|
react-app-rewired\s+start|vue-cli-service\s+serve|ng\s+serve|svelte-kit\s+dev|
webpack(-dev-server|\s+serve)|parcel|rsbuild\s+dev|nest\s+start|remotion\s+studio|
react-native\s+start|tsx|ts-node|nodemon|node|bun|deno|python|uvicorn|gunicorn|flask|
manage\.py|serve)\b
```

`NOT_SERVER_RE`（命中即排除）：

```
\b(build|tsc|tsgo|typecheck|type-check|eslint|prettier|vitest|jest|mocha|playwright|
cypress|lint|audit|codegen|generate|electron|remotion\s+render)\b
```

> 注意：**不要把 `watch` 放进黑名单**——`tsx watch src/index.ts`（本机 3 例）是合法的服务启动命令；而 `tsgo ... --watch`、`vite build --watch` 已经被 `tsgo`、`build` 排除。
> 注意：`electron` 必须在黑名单里（`huobao-drama\desktop` 的 `dev` 是 `... && electron .`），否则一键建议出一个永远没有端口的桌面应用。

### 4.4 Go 规则（本机 9 个 go.mod）

| # | 条件 | 命令 | 端口 | confidence |
|---|---|---|---|---|
| G1 | 根目录存在 `go.mod`，且根目录某 `.go` 文件同时含 `^package main` 与 `func main(` | `go run .` | 0（留空） | high |
| G2 | 否则 `cmd/` 下**恰好 1 个**子目录 | `go run ./cmd/<name>` | 0 | high |
| G3 | `cmd/` 下多个子目录，且其中有名字匹配 `(?i)(server|api|web|app|gateway|cmd)` 的**恰好 1 个** | `go run ./cmd/<name>` | 0 | medium |
| G4 | `cmd/` 下多个子目录且无上述唯一命中 | **不产出建议**（本机 `open-ai-canvas\backend` 的 4 个 cmd 全是迁移工具） | — | — |
| G5 | 只有 `go.mod` 没有任何 main 包（库/工具模块，如 `open-code-review\pages`） | 不产出 | — | — |

端口补充：不解析源码常量。实测 `:8080` 之类字面量确实存在，但「哪一行是服务端口」无法稳定判定（`DevDock\work` 解析出的 `0` 就是误命中），**一律留空**。

### 4.5 Python 规则（必须先过依赖闸门）

| # | 条件 | 命令 | 端口 | confidence |
|---|---|---|---|---|
| P1 | 目录有 `manage.py`，且 `pyproject.toml`/`requirements.txt` 含 `django` | `python manage.py runserver 127.0.0.1:8000` | 8000（django 默认） | high |
| P2 | 依赖含 `fastapi`/`starlette`/`uvicorn`/`aiohttp`/`flask`/`sanic`/`litestar`/`hypercorn`/`quart`/`tornado`，且目录内有 `main.py`/`app.py`/`server.py`/`run.py` 之一，且文件内含 `__main__` 或 `uvicorn.run(`/`app.run(` | `python <file>` | 从文件里 `port\s*=\s*(\d{4,5})` 取；无则 flask→5000、其它→0 | high/medium |
| P3 | 同 P2 依赖，但入口是包目录（存在 `<pkg>/__main__.py`，如 `TrendRadar` 的 `trendarar/__main__.py`） | `python -m <pkg>` | 0 | medium |
| P4 | 同 P2 依赖，有 `pyproject.toml` 但**找不到任何入口文件** | 空命令 + warn「检测到 Python 服务框架但找不到入口，请填写启动命令」 | 0 | low |
| P5 | 有 `pyproject.toml`/`requirements.txt` 但**无任何服务框架依赖** | **不产出**（本机 10 例纯库/脚本项目） | — | — |

依赖读取范围：`pyproject.toml` 的 `[project] dependencies` / `[tool.poetry.dependencies]` / `[tool.uv]` 段落，以及 `requirements*.txt`、`Pipfile`。**用子串+词边界匹配即可，不要写 TOML 解析器**（标准库没有；`pyproject.toml` 里 grep `fastapi` 已经足够，本机 13/13 命中正确）。

### 4.6 Rust / JVM / Docker（兜底，本机几乎零样本）

| # | 条件 | 命令 | 端口 | confidence | 备注 |
|---|---|---|---|---|---|
| R1 | `Cargo.toml` + `src/main.rs` 存在 | `cargo run` | 0 | low | 本机唯一 `Cargo.toml` 在 devwebui 的托盘程序里，**未验证** |
| J1 | `pom.xml` 且含 `spring-boot` | `mvn spring-boot:run` | 8080 | low | 本机 **0 例，未验证** |
| J2 | `build.gradle(.kts)` 且含 `org.springframework.boot` | `gradle bootRun` | 8080 | low | 本机 4 例全是 Android（含 `com.android.application`）→ **必须排除** |
| J3 | 目录名 `android/` 或 gradle 文件含 `com.android.application` | **不产出** | — | — | `aihaoji-workspace\*\android`、`noteai-app\android` |
| D1 | `docker-compose.yml`/`compose.yml` 且 `exec.LookPath("docker")` 成功 | `docker compose up` | 从 `ports:` 的 `"8080:80"` 取宿主端口 | low | 本机 **docker 未安装**，13 个 compose 文件；建议 v1 **默认不产出**，见 10.7 |
| H1 | 只有 `index.html`（无 `package.json`/`go.mod` 等） | **不产出** | — | — | 本机 237 个 `index.html`，绝大多数是模板/产物/静态页，产出即噪音 |

### 4.7 名称与 ID

| 项 | 规则 | 依据 |
|---|---|---|
| Name | ① `package.json#name`（去掉 `@scope/` 前缀，保留原大小写）；② 无则用目录名（**保留中文**，如 `28高三`）；③ 目录名形如 `frontend`/`web`/`apps` 等泛名时，前面拼上父目录名（`aihaoji-app-module-detail`） | 本机 48 条里有 11 条叫 `frontend`/`web`/`admin`/`client`，不加父名无法区分 |
| ID | 对 Name 做 ASCII slug（复用 `store.slugify` 的规则但**在 detect 层自己实现**，因为要保证稳定）；结果为空（纯中文名）时用 `p-` + FNV-1a(cwd) 的低 32 位十六进制；同批次冲突追加 `-2`、`-3` | `store.go:266-289`：纯中文会退化成时间戳，不可接受（每次扫描 ID 都变） |
| ID 长度 | ≤ 64 字符（`store.go:105-107` 的 `slugPattern`） | 同上 |
| Group | 从 `cwd` 向上最多 6 层找第一个含 `.git` 的目录；找不到则用父目录 | 3.8 实测 12/13 |
| 排序 | `Group` 升序 → `Port>0` 优先 → `Confidence(hight→low)` → `Cwd` 升序。**必须完全确定**，便于测试断言 | 现有 `SortViews`（`cli.go:711-713`）同理 |

### 4.8 端口推断（统一优先级，所有生态共用）

| 优先级 | 来源 | 适用 | 示例 | `PortFrom` | confidence 影响 |
|---|---|---|---|---|---|
| 1 | 命令里的 `--port N` / `--port=N` | 全部 | `next dev -p 3001`、`nuxt dev --port 3013` | `flag` | 保持原判 |
| 2 | 命令里的 `-p N` | **仅当该段以** `vite`/`next`/`nuxt`/`serve`/`astro`/`remix`/`webpack`/`rsbuild`/`http-server` 开头 | `next start -p 3001` | `flag` | 同上 |
| 3 | 命令行内 `PORT=N` 环境前缀 | 全部 | `PORT=8080 node server.js` | `flag` | 同上 |
| 4 | `vite.config.*` 的 `server.port` / `preview.port` | 仅 vite | `workshop-web` → 3100 | `vite.config` | high |
| 5 | Python 源码里 `port=N` / `--port` | Python | `uvicorn.run(app, port=8085)` | `source` | high |
| 6 | 框架默认值（下表） | 已识别框架 | `vite` → 5173 | `framework-default` | **降为 medium** |
| 7 | 都没有 | — | `tsx src/index.ts` | — | `Port=0`，UI 显示「未推断」 |

框架默认端口表（与 devwebui `detect.ts:28-41` 对齐，并补 Python/Metro）：

| 框架 | 默认端口 | 框架 | 默认端口 |
|---|---|---|---|
| Vite | 5173 | Next.js | 3000 |
| Nuxt | 3000 | Astro | 4321 |
| CRA (`react-scripts start`) | 3000 | Vue CLI | 8080 |
| Angular (`ng serve`) | 4200 | SvelteKit | 5173 |
| webpack-dev-server | 8080 | Parcel | 1234 |
| Rsbuild | 3000 | Nest | 3000 |
| Remix | 3000 | Metro (`react-native start`) | 8081 |
| Remotion studio | 3000 | Django `runserver` | 8000 |
| Flask | 5000 | Go / Rust / JVM 其它 | 留空 |

**「推断不出来时怎么办」= 留空（`port=0`）**，绝不猜 3000。
依据：48 条建议里 18 条（37%）命令里没有任何端口线索（`tsx`/`node`/`bun` 类脚本）；`Port=0` 在现有模型里是合法值（`store.go:250`、`model.go:105-110`），UI 历史文案是「未声明」（`app.js:172`），语义自洽。猜一个错端口会让用户「启动成功但打不开」，比留空更糟。

### 4.9 就绪路径

**统一留空（`Readiness=nil`）。** 理由：
1. 现有探针 `expectStatus` 默认 `[200,399]`（`model.go:48-50`），而本机绝大多数项目根路径返回 302（重定向到登录/语言前缀）或 404 → 探测会被判「超时」，用户看到「就绪超时」比「未配置就绪检查」更困惑。
2. `validate` 规定「有 readiness.path 必须有 port」（`store.go:253-259`），而 37% 的建议没有端口，配了就校验失败。
3. 未发现通行的 `/health` 约定（**未验证**：没有对全库做 `/health` 路由的穷举 grep；仅确认 `testsupport/fixture` 自带 `/health`）。
4. `ReadinessConfig.TimeoutSec/IntervalMs` 有 `Defaults()` 兜底（`model.go:41-51`），导入时给 `nil` 最省事。

可选的未来增强（不在本期）：导入后第一次 `start --wait-ready` 成功时，把命中 `/` 的路径写回配置。**需要主 agent 拍板，默认不做。**

### 4.10 去重与「已管理」判定

| 场景 | 处理 |
|---|---|
| 两个目录产生同一个 ID | 后者追加 `-2`、`-3`（递增，必收敛） |
| ID 与 `store` 里已有项目冲突 | 保留建议 ID 不变，但标 `Duplicate`（导入时改名为 `-2`） |
| `cwd` 与已有项目相同（`filepath.Clean` 后 `strings.EqualFold` 比较，Windows 大小写不敏感） | 标 `Duplicate=true` + `ExistingID`，UI 灰显且默认不勾选 |
| 同一个目录被多个根覆盖（如 `--root D:\Desktop --root D:\Desktop\foo`） | 用 `map[lower(cwd)]` 去重，只留第一条 |

---

## 5. 扫描范围与性能预算

### 5.1 默认根目录（不全盘）

**原则：默认只扫「这个人把代码放在哪儿」的白名单目录，最多 8 个根，不扫盘根、不扫 `%USERPROFILE%` 整层。**

| 优先级 | 来源 | 说明 |
|---|---|---|
| 1 | CLI/UI 显式传入的 `roots` | 最高优先，给几个扫几个（≤ 8） |
| 2 | `config.json` 的 `settings.scanRoots` | 用户上次确认过的根，持久化（新增字段，见 6.5） |
| 3 | 自动候选 | 见下 |

自动候选（**只保留真实存在的目录**，取前 8 个）：

| 类别 | 候选路径 |
|---|---|
| 用户目录下的一级子目录 | `%USERPROFILE%\Desktop`、`Documents`、`Downloads`、`projects`、`code`、`dev`、`src`、`workspace`、`work`、`repos`、`git`、`开发`、`项目` |
| 各 IDE 的默认工程位置 | `%USERPROFILE%\source\repos`（VS 默认）、`%USERPROFILE%\IdeaProjects`、`%USERPROFILE%\PycharmProjects` |
| 每个**固定本地盘**根下的同名子目录 | `X:\Desktop`、`X:\projects`、`X:\code`、`X:\dev`、`X:\src`、`X:\workspace`、`X:\work`、`X:\repos` |

为什么这样定（三条实测理由）：
1. 本机项目**几乎都不在 `%USERPROFILE%` 下**，而是 `D:\Desktop\<项目>`；只扫 home 会「一个都识别不到」。实测：`%USERPROFILE%\source\repos` 存在但**为空**；`Documents` 下的 `Codex`、`Qoder` 是 agent 会话日志目录（按日期分子目录）而非工程；`Desktop` 只有零星文件，无项目目录。所以 home 下一级只能当「低成本附带」，主战场是「每个盘的同名子目录」（5.1 第二行，覆盖 `D:\Desktop`）。
2. devwebui 的默认值（`os.homedir()` + 除系统盘外所有盘根，`scan.ts:123-134`）在这台机器上等于扫 `D:\` 整盘——按 3.1 的密度（`D:\Desktop` 前 4 层就有 4133 个目录）推算，全盘是分钟级，直接违反「启动快」。
3. 白名单命中 `D:\Desktop`，depth 3 只走 1575 个目录（3.9），热缓存亚秒级。

**明确不做：** 不扫盘根；不扫 `C:\Windows`、`Program Files`、`AppData`、`$Recycle.Bin`；不扫网络盘/可移动盘（`GetDriveType` 非 `DRIVE_FIXED` 一律跳过）；不扫 `%USERPROFILE%` 本身（除非用户显式 `--root`）。

### 5.2 预算表

| 参数 | 默认 | 允许范围 | 依据 |
|---|---|---|---|
| 根目录数 | 自动候选（≤8） | 1–8 | 5.1 |
| `depth` | 3 | 1–6 | 3.9：depth 3 覆盖 81%，目录量仅 depth 6 的 18% |
| `maxDirs` | 20000 | 100–100000 | 本机默认根下实际 1575–8613 |
| `maxEntriesPerDir` | 2000 | 100–10000 | 本机无超 5000 条目目录（S5） |
| `limit`（返回建议上限） | 100 | 1–500 | 48 条实测；上限防刷屏 |
| `budgetMs`（硬超时） | 1500 | 100–10000 | 见 5.3 |
| 并发 worker | 8 | 1–32 | I/O 密集 |
| 结果缓存 TTL | 10s（同参数） | 固定 | 防 UI 连点 |

### 5.3 预期耗时与验证方法

| 场景 | 目标 | 说明 |
|---|---|---|
| 默认（3 个根，depth 3） | 热 ≤ 300ms，冷 ≤ 2s | 1575 目录/根的实测密度 |
| 全 `D:\Desktop`，depth 3 | 热 ≤ 400ms | 3.1 实测：8613 目录 0.7–2.2s（全深度） |
| 全 `D:\Desktop`，depth 6 | 热 ≤ 2s，冷 ≤ 15s | 冷缓存实测 5000 目录 11.9s（3.1） |
| **任何情况** | 必须在 `budgetMs + 200ms` 内返回，且 `timedOut` 正确 | 看门狗，参考 devwebui `scan.ts:370-373` |

**开发 agent 必须按以下方法实测并把数字写进 `docs/VERIFICATION.md`：**

```powershell
# 1) 连续 5 次，第 1 次视为冷启动，报「第1次」与「中位数」
1..5 | ForEach-Object {
  $t = Measure-Command { .\DevDock.exe scan --root "D:\Desktop" --depth 3 --json | Out-Null }
  "run $_ = {0:N0} ms" -f $t.TotalMilliseconds
}

# 2) 压边界：depth 6 + limit 500，必须仍在 budget+200ms 内返回
$t = Measure-Command { .\DevDock.exe scan --root "D:\Desktop" --depth 6 --limit 500 --timeout 2s --json | Out-Null }
"edge = {0:N0} ms" -f $t.TotalMilliseconds

# 3) 从返回里核对自报耗时与目录数
.\DevDock.exe scan --root "D:\Desktop" --depth 3 --json | ConvertFrom-Json |
  Select-Object scannedDirs, elapsedMs, truncated, timedOut, depth
```

判定标准：`elapsedMs`（服务端自报）与 `Measure-Command`（端到端）差值应 ≤ 200ms（差值过大说明 CLI 启动/服务发现开销占了主导，需在报告里说明）。

### 5.4 取消与不卡死

| 要求 | 实现要点 |
|---|---|
| CLI 可取消 | `cmdScan` 用 `context.WithTimeout`；Ctrl+C 走 `signal.NotifyContext`（同 `cli.go:433` 的写法） |
| HTTP 可取消 | `handler` 里 `ctx := r.Context()` 直接传下去；浏览器端 `AbortController` 取消 fetch 后，服务端 `readdir` 循环在下一个检查点退出 |
| 全局只有一个扫描 | `detect.Scanner` 内部 `sync.Mutex` + `inflight` map；第二个并发请求立刻返回 `409 scan_in_progress`（不排队，避免 CLI 卡死） |
| 硬超时 | 独立 `time.AfterFunc(budgetMs+100ms)` 置位 `timedOut` 并 `close(done)`；主循环每处理一个目录检查一次 `done`（对应 devwebui 的看门狗注释 `scan.ts:367-373`） |
| 不阻塞启动 | **默认启动时不做任何扫描**（不做「首启动自动扫描」），只在用户点按钮/敲命令时扫 |

---

## 6. 接口设计

### 6.1 CLI

```
devdock scan [<目录>...] [--root <目录>]... [--depth N] [--timeout 2s] [--limit N] [--json] [--all]
devdock add --auto --cwd <目录> [--name <名称>] [--id <ID>] [--command <命令>] [--port N] [选项同现有 add]
```

| 命令/参数 | 语义 | 备注 |
|---|---|---|
| `scan` | 只读扫描并打印建议 | **不创建任何项目**（第 8 节） |
| 位置参数 `<目录>...` | 等价于逐个 `--root` | 用 `parseInterspersed`；`devdock scan D:\Desktop --json` 必须能用 |
| `--depth N` | 1–6，默认 3 | 越界 → 退出码 2 |
| `--timeout D` | 默认 `2s`，`time.Duration`（与 `start --timeout` 一致，`cli.go:260`） | 传给服务端 `budgetMs`（封顶 10000） |
| `--limit N` | 默认 100，上限 500 | |
| `--all` | 连 `confidence=low` 与 `Duplicate` 一起输出 | 默认过滤掉 low |
| `--json` | stdout 只输出 JSON | 结构 = `detect.Result`（4.1） |
| `add --auto` | 从 `--cwd` 识别并填 `name/command/port` | 显式 flag 覆盖自动值；识别失败 → 退出码 1 + 中文原因 |

`scan --json` 输出形状（**直接序列化 `detect.Result`**，不要包一层）：

```json
{
  "roots": ["D:\\Desktop"],
  "suggestions": [
    {
      "cwd": "D:\\Desktop\\workshop\\workshop-web",
      "name": "workshop-web",
      "id": "workshop-web",
      "kind": "node",
      "framework": "vite",
      "command": "npm run dev",
      "commandFrom": "package.json#scripts.dev",
      "port": 3100,
      "portFrom": "vite.config",
      "confidence": "high",
      "group": "D:\\Desktop\\workshop"
    }
  ],
  "scannedDirs": 1575,
  "elapsedMs": 214,
  "truncated": false,
  "timedOut": false,
  "depth": 3
}
```

人类可读输出（非 `--json`）表格列：`名称 | 类型 | 框架 | 端口 | 命令 | 目录`，其中目录截断为相对根的路径；末尾附一行汇总 `共 N 条建议（扫描 1575 个目录，用时 214ms）`，若有 `truncated/timedOut` 再追加提示（提示走 **stdout 的表格区或 stderr**——按约定「`--json` 时提示走 stderr」，非 JSON 模式写 stdout 汇总行即可）。

帮助文本需同步更新 `cli.go:90-111`（现有 102 行 `add`、105 行 `mcp`）：

```
  devdock scan [<目录>...] [--depth 3] [--timeout 2s] [--json]
                                           扫描本机可识别的开发项目（只读）
  devdock add <名称> --cwd <目录> --command <命令> [--port N] ...
  devdock add --auto --cwd <目录>          自动识别并创建项目
```

### 6.2 退出码

| 情况 | 退出码 | 依据 |
|---|---|---|
| 扫描成功（**含 0 条建议**） | 0 | 与 `list` 空结果一致（`cli.go:187-190`） |
| 参数非法（depth 越界、root 不存在、limit 越界） | 2 `ExitUsage` | 现有用法错误约定（`cli.go:26`） |
| 服务无法连接/拉起 | 7 `ExitServiceAbsent` | `cli.go:31`、`118-119` |
| 已有扫描在进行中（409 `scan_in_progress`） | 6 `ExitConflict` | `cli.go:152-153` 已把 `conflict` 映射为 6 |
| `add --auto` 识别失败 / 创建失败 | 1 `ExitError` | 与 `add` 现有行为一致（`cli.go:518-521`） |
| `add --auto` 与已有项目重复 | 1（服务端返回 400 `config_invalid`） | `store.ErrDuplicate` → `CodeConfigInvalid`（`respond.go:57-58`） |

> **不新增退出码数字。** `scan` 找到 0 条不是错误——脚本要能区分「没扫到」和「扫失败」，前者靠 `suggestions.length == 0`。

### 6.3 HTTP API

```
POST /api/scan
Content-Type: application/json
X-DevDock-Token: <token>
```

请求体（**严格解析，字段名逐字对齐**，参考 `api.go:452-461` 的 `projectRequest` 写法）：

```json
{
  "roots": ["D:\\Desktop"],
  "depth": 3,
  "budgetMs": 2000,
  "limit": 100,
  "includeLow": false
}
```

| 字段 | 类型 | 必填 | 默认 | 校验 |
|---|---|---|---|---|
| `roots` | `[]string` | 否 | 服务端自动候选 | ≤ 8 个；每个必须存在且是目录，否则 400 `config_invalid` |
| `depth` | `int` | 否 | 3 | 1–6 |
| `budgetMs` | `int` | 否 | 1500 | 100–10000 |
| `limit` | `int` | 否 | 100 | 1–500 |
| `includeLow` | `bool` | 否 | false | — |

响应 `200 OK`：`detect.Result`（4.1）。

错误：

| 状态 | code | 触发 |
|---|---|---|
| 400 | `config_invalid` | 请求体格式错、未知字段（`DisallowUnknownFields`）、root 不存在、depth/limit 越界 |
| 409 | `scan_in_progress` | 已有一次扫描在跑 |
| 403 | `unauthorized` | 缺 token（中间件自动处理，`security.go:70-77`） |
| 500 | `internal` | 内部错误 |

新增稳定错误码：`CodeScanInProgress = "scan_in_progress"`，加在 `respond.go:23-33` 的词表里；`client.CodeScanInProgress` 同步加在 `client.go:23-31`。**注意 token 校验对 `/api/` 全覆盖，`/api/scan` 自动受保护，无需改中间件。**

导入复用 `POST /api/projects`（`api.go:133-150`），**不新增导入接口**。

### 6.4 服务端实现要点

| 项 | 要求 |
|---|---|
| 扫描位置 | **在 serve 进程内执行**（不是 CLI 进程）。理由：CLI 与 UI 共用一份缓存；CLI 进程本来就只做 HTTP 调用（`client.go:1-2` 的架构约定） |
| 与其他包的关系 | `internal/detect` 只 import `model` + 标准库；`httpapi` import `detect`；`client` import `detect`（仅为复用类型，无环） |
| 并发控制 | `detect.Scanner` 单例挂在 `API` 上（`api.go:24-32` 加字段），内部互斥 + inflight 复用 |
| 缓存 | `map[cacheKey]{result, at}`，key = `roots|depth|limit|includeLow`，TTL 10s；**缓存命中不刷新 `elapsedMs` 语义**——命中时返回 `cached:true`？（可选，主 agent 拍板；默认不加字段，直接返回上次结果） |
| 只读 | 扫描不触碰 `store`，只在 `httpapi` 层读 `store.List()` 来标 `Duplicate`（保持 detect 包可单测） |

### 6.5 配置变更（向后兼容）

在 `config.Settings`（`config.go:43-47`）里追加两个字段：

```go
type Settings struct {
    LogToDisk   bool     `json:"logToDisk"`
    ScanRoots   []string `json:"scanRoots,omitempty"`   // 用户确认过的扫描根
    ScanExclude []string `json:"scanExclude,omitempty"` // 额外排除的目录名（v1 可只在 CLI 用）
}
```

- `SchemaVersion` 保持 1：`config.Load` 用非严格 `json.Unmarshal`（`config.go:148-157`），旧文件缺这两个字段会得到 nil，天然兼容。
- 写入仍走 `SetProjects`/`Update` + 原子替换（`config.go:196-248`），不要新写一套持久化。
- `scan` **本身不写配置**；只有 UI 上「记住这个根目录」或 `scan --save-root`（若主 agent 采纳）才写。

### 6.6 UI（零构建链）

| 项 | 方案 |
|---|---|
| 入口 | 顶栏 `.topactions`（`index.html:22-24`）加 `<button class="btn ghost" id="btn-scan">扫描本机</button>`，放在「刷新」左边 |
| 弹层 | **复用 `.drawer-mask` 类**：`<div class="drawer-mask" id="scan-mask" hidden><aside class="drawer scan-drawer" role="dialog" aria-modal="true">…</aside></div>`。复用同一个类名可自动享受已有的 `.drawer-mask[hidden]{display:none}`（`style.css:363-368`），**这是本次必须遵守的硬约束**（`docs/VERIFICATION.md:177` 记着上次抽屉关不掉的根因） |
| 新增 CSS | 只加 `.scan-drawer { width: 560px }`、`.scan-row`、`.scan-badge` 等；**若引入任何新类名且带 `display:flex/grid`，必须同时写 `.新类名[hidden]{display:none}`** |
| 面板内容 | ① 顶部：根目录输入框（分号/换行分隔，留空=用默认）+「开始扫描」+「取消」；② 中部：按 `group` 分组的建议列表，每行 = 复选框 + 名称 + 框架徽标 + `command`（等宽）+ 端口 + 警告徽标 + 「填入表单」；③ 底部：「导入选中 (n)」+ 汇总文案（`扫描 1575 个目录 · 214ms · 9 条建议`） |
| 交互 | `fetch('/api/scan', {method:'POST', …, signal: controller.signal})`；扫描中按钮禁用 + 文案「扫描中…」；点取消 → `controller.abort()` 并提示「已取消」；`Duplicate` 行灰显且默认不勾；`portInUse` 行显示「端口 5173 当前被占用」 |
| 「填入表单」 | 复用 `openDrawer(null)`（`app.js:368-393`）后把 `name/cwd/command/port` 填进 `#project-form`，用户确认后走现有 `submitForm`（`app.js:412-459`）。**不直接调用创建接口**，这样用户能改 |
| 批量导入 | 逐条 `POST /api/projects`（顺序执行，串行），失败的条目保留在列表里并显示服务端 message；全部成功后才 `closeDrawer()` + `loadProjects()` |
| 转义 | 只用 `textContent` / `el()`（`app.js:38-43`），禁止 `innerHTML` 拼串（CSP 禁内联，且命令里可能有 `<`） |
| Escape/遮罩关闭 | 复用文档级 `keydown` 监听（`app.js:542-544`），判断 `!$('#scan-mask').hidden` |
| 空结果 | 显示「没有发现可识别的项目」+ 提示「可以指定一个根目录再试」 |

### 6.7 MCP

**v1 不新增 `devdock_scan` 工具。** 依据：`mcpserver/mcp.go:159` 明确写着「只有 4 个工具，不随意扩张」，且 agent 完全可以用 CLI `scan --json`。若主 agent 要加，需同步 `toolDefs()`（`mcp.go:160-228`）、`runTool`（`258-302`）与 `mcp_test.go`。

---

## 7. 实现清单（给开发 agent 的落点）

| # | 文件 | 动作 | 内容 |
|---|---|---|---|
| 1 | `internal/detect/types.go` | 新增 | `Suggestion`/`Result`/`Options`（4.1） |
| 2 | `internal/detect/scan.go` | 新增 | 有界 BFS + worker 池 + 看门狗 + 剪枝（4.2）+ 分组/去重/排序（4.7、4.10） |
| 3 | `internal/detect/node.go` | 新增 | N1–N8（4.3）+ 端口（4.8）+ vite 配置解析 |
| 4 | `internal/detect/golang.go` | 新增 | G1–G5（4.4） |
| 5 | `internal/detect/python.go` | 新增 | P1–P5（4.5） |
| 6 | `internal/detect/other.go` | 新增 | R1/J1–J3/D1/H1（4.6） |
| 7 | `internal/detect/scan_test.go`、`node_test.go`、`python_test.go` | 新增 | 第 9 节单测清单 |
| 8 | `internal/config/config.go` | 改 | `Settings` 加 `ScanRoots`/`ScanExclude`（6.5），行 43-47 |
| 9 | `internal/httpapi/api.go` | 改 | `API` 加 `scanner *detect.Scanner` 字段（24-32）；`Handler` 注册 `/api/scan`（47-62）；新增 `handleScan` + `scanRequest`（严格解析，仿 452-491） |
| 10 | `internal/httpapi/respond.go` | 改 | 加 `CodeScanInProgress`（23-33） |
| 11 | `internal/client/client.go` | 改 | 加 `CodeScanInProgress`（23-31）与 `func (c *Client) Scan(req detect.ScanRequest) (*detect.Result, error)`（仿 255-279 的手写 body） |
| 12 | `internal/cli/cli.go` | 改 | 分发加 `case "scan"`（42-73）；新增 `cmdScan`；`cmdAdd` 加 `--auto`（467-528）；`printHelp` 更新（90-111） |
| 13 | `web/index.html` | 改 | 顶栏按钮（22-24）+ 扫描抽屉（放在现有抽屉后，93-145 之后） |
| 14 | `web/app.js` | 改 | `openScan()/closeScan()/runScan()/importSelected()/fillFromSuggestion()`；`bind()` 里挂事件（462-545） |
| 15 | `web/style.css` | 改 | `.scan-drawer` 等；若新增带 display 的类，补 `[hidden]` 规则（参照 363-368） |
| 16 | `web/embed.go` | 改（仅当新增静态文件） | embed 指令是**显式文件名**（`embed.go:11`）；建议不新增文件，全部写进现有 3 个文件 |
| 17 | `README.md` | 改 | 66 行「自动发现不属于首版范围」需改写；补 `scan` / `add --auto` 用法 |
| 18 | `docs/VERIFICATION.md` | 改 | 188-190 行「全盘自动扫描未实现」需改写；新增扫描性能实测表与验收输出 |
| 19 | `testsupport/e2e/run_scan_e2e.ps1` | 新增 | 第 9.2 节的端到端脚本 |

实现顺序建议：1→2→3→4→5→6→7（先把 detect 跑通、单测全绿）→ 8→9→10→11→12（接口层）→ 13→14→15（UI）→ 16→17→18→19（文档与验收）。

---

## 8. 建议 vs 直接入库

**推荐：只产出建议，导入必须显式触发（单条填入表单 / 多选批量导入）。**

| 理由 | 实测/代码依据 |
|---|---|
| 误判率不为零，且误判代价高 | 48 条建议里，`open-design\tools\{dev,pack,release,serve}` 是 CLI 工具（`tsx ./src/index.ts` 可能执行完就退出）、`Toonflow-app` 根脚本依赖 `bun`、`react-native start` 是 Metro 而不是可浏览页面。直接入库会让侧边栏塞进一堆「启动即退出/打不开」的项目 |
| 命令不完整时会污染配置 | Python P4 分支 `command` 为空；`store.validate` 会拒绝空命令（`store.go:227-230`），自动入库整批失败或需要静默丢弃 |
| 同一仓库会产生多条 | `aihaoji-workspace` 11 条、`workshop` 6 条、`open-design` 6 条（3.8）。自动入库等于替用户决定「这个仓库要 11 个启动配方」 |
| 端口冲突需要人判断 | 读 `vite.config` 后仍有约 10 条 Vite 建议落在 5173、6 条 Next 落在 3000（3.5 实测）；自动入库后启动必然大面积 `port_in_use`（`runner.go:275-279`） |
| 与参考项目一致 | devwebui 也是「预览 proposal → 用户确认 → 才写 `.devwebui`」，且 `scaffoldDevWebUIFile` 明确 never overwrites（`load-target.ts:66-75`） |
| 保留「一键」体验 | UI 的「导入选中 (n)」+ CLI `add --auto` 已经能做到「扫完点两下就建好」，不会退回手填 |

**唯一例外建议**：`add --auto` 是「单条、显式、带 cwd」的，等价于「用户已经指着这个目录说就要它」，可以直接创建（并在创建前把识别结果打印出来）。

---

## 9. 测试方案

### 9.1 单元测试（必须真实运行，`go test ./internal/detect/...`）

写法与现有风格一致：`t.TempDir()` 造树、`t.Setenv("APPDATA", t.TempDir())` 隔离（参照 `store_test.go:13-26`），不需要外部进程。

| # | 用例 | 构造 | 断言 |
|---|---|---|---|
| T1 | Vite 默认端口 | `package.json` `{"scripts":{"dev":"vite"}}` | `command=="npm run dev"`, `framework=="vite"`, `port==5173`, `portFrom=="framework-default"`, `confidence=="medium"` |
| T2 | 构建脚本不算服务 | `{"scripts":{"dev":"vite build --config x.ts --watch"}}` | **0 条建议** |
| T3 | `&&` + `chcp` 前缀 | `{"scripts":{"dev":"chcp 65001 && vite"}}` | 命中 vite |
| T4 | 显式端口优先 | `{"scripts":{"dev":"next dev -p 3001"}}` | `port==3001`, `portFrom=="flag"`, `confidence=="high"` |
| T5 | vite.config 端口 | `vite.config.ts` 含 `server:{ port: 3100 }` + `dev:"vite --host"` | `port==3100`, `portFrom=="vite.config"` |
| T6 | dev 优先于 start | `{"scripts":{"dev":"next dev","start":"next start -p 3001"}}` | `command=="npm run dev"`, `port==3000` |
| T7 | 递归解析 `npm run X` | `{"scripts":{"dev":"npm run dev:web","dev:web":"vite"}}` | 命中 vite |
| T8 | electron 排除 | `{"scripts":{"dev":"npm run build && electron ."}}` | 0 条 |
| T9 | 锁文件回退 | 目录内 `yarn.lock`（测试机可能无 yarn） | `command` 为 `npm run dev` 且 `warnings` 含 yarn 关键字；**不要断言死 runner**，用「要么 yarn 存在则 yarn，否则 npm+warning」的二分断言 |
| T10 | ID 去重 | 两个目录 `a/web`、`b/web` 且 `package.json` 都叫 `web` | ID 分别为 `web`、`web-2` |
| T11 | 中文目录名 | 目录 `28高三/handout-agent` | `name` 含中文时 `ID` 仍匹配 `^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`（由 FNV 兜底） |
| T12 | Go 根 main | `go.mod` + `main.go`(`package main`/`func main`) | `command=="go run ."`, `port==0` |
| T13 | Go cmd 单入口 | `go.mod` + `cmd/svc/main.go` | `command=="go run ./cmd/svc"` |
| T14 | Go cmd 多入口无命中 | `cmd/migrate`、`cmd/host-updater` | 0 条 |
| T15 | Django | `manage.py` + `requirements.txt` 含 `django` | `command=="python manage.py runserver 127.0.0.1:8000"`, `port==8000` |
| T16 | Python 无框架依赖 | 仅 `pyproject.toml` 含 `requests` | 0 条 |
| T17 | FastAPI + 端口 | `requirements.txt` 含 `fastapi`，`main.py` 含 `uvicorn.run(app, port=8085)` | `port==8085`, `command=="python main.py"` |
| T18 | Python 无入口 | 依赖 fastapi，无 `main/app/server/run.py` | 1 条，`command==""`, `confidence=="low"`, 有 warn |
| T19 | 静态页不产出 | 只有 `index.html` | 0 条 |
| T20 | `.env` 陷阱 | `.env` 含 `DB_PORT=3306`，无 `PORT=` | `port==0`（不得出现 3306） |
| T21 | 剪枝 | `node_modules/x/package.json`、`.git/`、`dist/` 里放诱饵 `package.json` | 诱饵都不出现在结果里 |
| T22 | 深度上限 | 根下 5 层处放 `package.json`，`depth=3` | 不出现在结果里；`depth=6` 时出现 |
| T23 | 超时 | 造 2000 个空目录 + `budgetMs=1` | 返回 `TimedOut==true` 且**在 200ms 内返回**（不 panic、不挂） |
| T24 | 目录数上限 | 造 300 个目录 + `maxDirs=10` | `Truncated==true`，`ScannedDirs<=10`（如果暴露该字段） |
| T25 | 不可读目录不崩 | 用 `os.Chmod` 造不可读目录（Windows 上改用「扫描中途删除目录」模拟） | 不 panic，返回剩余结果 |
| T26 | 已管理项目标重 | store 里已有 `cwd=树内某目录` 的项目 | 该条 `Duplicate==true`、`ExistingID` 正确 |
| T27 | 确定性 | 同一棵树扫两次 | 两次 `Suggestion` 切片（除 `ElapsedMs` 外）逐字段相等 |
| T28 | 端口占用预检 | 测试内 `net.Listen("127.0.0.1:0")` 占一个端口，把建议端口设成它（可用 `port==5173` 的方式构造：先占 5173，跳过失败则 `t.Skip`） | `PortInUse==true` |

覆盖要求：`go test -count=1 ./...` 全绿，`go vet ./...` 退出码 0。

### 9.2 端到端（新增 `testsupport/e2e/run_scan_e2e.ps1`，参照 `run_e2e.ps1` 的隔离与清理手法）

```powershell
# 1) 造一棵可预测的临时树（真实文件，非 mock）
$scanRoot = Join-Path $E2E 'scanroot'
New-Item -ItemType Directory -Force -Path "$scanRoot\web-a","$scanRoot\web-b","$scanRoot\go-a","$scanRoot\node_modules\x" | Out-Null
'{"name":"web-a","scripts":{"dev":"vite"}}'            | Set-Content "$scanRoot\web-a\package.json" -Encoding utf8
'{"name":"web-b","scripts":{"dev":"next dev -p 3001"}}'| Set-Content "$scanRoot\web-b\package.json" -Encoding utf8
"module go-a`n`ngo 1.25`n"                             | Set-Content "$scanRoot\go-a\go.mod" -Encoding utf8
"package main`n`nfunc main() {}"                       | Set-Content "$scanRoot\go-a\main.go" -Encoding utf8
'{"name":"decoy","scripts":{"dev":"vite"}}'            | Set-Content "$scanRoot\node_modules\x\package.json" -Encoding utf8

# 2) 隔离 APPDATA 起服务（复用 run_e2e.ps1 第 116-151 行的启动+轮询逻辑）
# 3) 只读性：扫描前后对比 list 数量与 config.json 的 LastWriteTime
$before = (Invoke-DevDock @('list','--json')).Output | ConvertFrom-Json
$cfgBefore = (Get-Item "$AppData\DevDock\config.json").LastWriteTimeUtc
$t = Measure-Command { $r = Invoke-DevDock @('scan','--root',$scanRoot,'--json') }
$after = (Invoke-DevDock @('list','--json')).Output | ConvertFrom-Json
#   断言：$r.Code -eq 0；$after.projects.Count -eq $before.projects.Count
#         (Get-Item ...).LastWriteTimeUtc -eq $cfgBefore
#         $t.TotalMilliseconds -lt 2000

# 4) 断言内容
$s = $r.Output | ConvertFrom-Json
#   suggestions.Count -eq 3            （web-a、go-a、web-b 都在 root 下一层，depth=2 全覆盖）
#   名称集合恰为 web-a/web-b/go-a，且不含 decoy（node_modules 被剪）
#   web-a.port -eq 5173 且 portFrom -eq 'framework-default'
#   web-b.port -eq 3001 且 portFrom -eq 'flag'
#   go-a.command -eq 'go run .'

# 5) 导入路径：add --auto 复用识别结果
Invoke-DevDock @('add','--auto','--cwd',"$scanRoot\web-a",'--json')   # 断言 Code 0、command 'npm run dev'、port 5173
Invoke-DevDock @('add','--auto','--cwd',"$scanRoot\web-a",'--json')   # 断言 Code -ne 0（重复），list 里仍只有 1 条

# 6) 扫描不启动任何进程（关键回归）
$procsBefore = @(Get-Process -Name 'fixture','node','go' -ErrorAction SilentlyContinue | Select-Object -Expand Id)
Invoke-DevDock @('scan','--root',$scanRoot,'--json') | Out-Null
$procsAfter  = @(Get-Process -Name 'fixture','node','go' -ErrorAction SilentlyContinue | Select-Object -Expand Id)
#   断言：两个集合相等（新增进程数 0）
```

最终输出统一格式（便于 CI 断言）：`SCAN_E2E_RESULT=PASS`、`SCAN_E2E_MS=<中位数>`、`SCAN_E2E_SUGGESTIONS=<n>`。

### 9.3 回归要求

| 项 | 命令 | 通过标准 |
|---|---|---|
| 编译 | `go build ./...` | 退出码 0 |
| 静态检查 | `go vet ./...` | 退出码 0 |
| 全量单测 | `go test -count=1 ./...` | 全绿（现有 7 个有测试的包不得退化） |
| 既有 E2E | `powershell -ExecutionPolicy Bypass -File .\testsupport\e2e\run_e2e.ps1` | `E2E_RESULT=PASS` |
| 新增 E2E | `powershell -ExecutionPolicy Bypass -File .\testsupport\e2e\run_scan_e2e.ps1` | `SCAN_E2E_RESULT=PASS` |
| 启动性能不退化 | 复用 `run_e2e.ps1` 的 `E2E_BOOT_MS` | 仍在 1000ms 以内（现有 325–489ms，`docs/VERIFICATION.md:142`） |
| UI 抽屉可关闭 | 人工可复现的自动断言：`run_e2e.ps1` 里已有 UI 200 检查；新增可用 `paw-browser` 打开页面点「扫描本机」再按 Escape，断言 `#scan-mask` 的 `hidden` 属性为 `true` | 见 10.11（人工验收 vs 浏览器自动化需主 agent 定） |

---

## 10. 需要主 agent 拍板的取舍

> 每条都给建议方案与理由，但不代替决定。

| # | 问题 | 我的建议 | 理由 |
|---|---|---|---|
| 10.1 | 默认根目录是否包含 `%USERPROFILE%` 整层？是否包含所有固定盘？ | **都不包含**。只用「白名单常见开发目录（home 下一级 + 每个固定盘的同名子目录）」，最多 8 个 | 整层 home 在本机会扫到 `AppData` 外的海量杂项目录；全盘更慢。白名单在实测里能命中 `D:\Desktop`（本机唯一的项目聚集地） |
| 10.2 | 识别结果直接入库，还是只给建议？ | **只给建议**（第 8 节） | 误判类别明确（库构建/Metro/CLI 工具/依赖缺失），自动入库会污染配置 |
| 10.3 | 是否提供「批量导入」？ | **提供**，但：仅 high+medium 默认勾选、单次 ≤ 20 条、必须二次确认弹窗、失败逐条报错不中断 | 48 条建议里 30 条有明确端口，用户确实想一次建好几个；但 20 条上限防误点全选 |
| 10.4 | 端口推断不出来：留空还是猜（3000/8080）？ | **留空**（`port=0`） | 37% 的建议无端口线索；猜错导致「启动成功但打不开」，比「未声明端口」更难排查。`port=0` 在现有模型里语义完整（`model.go:105-110`、`app.js:172`） |
| 10.5 | 端口已被占用时怎么提示？ | 建议里预检并标 `portInUse=true` + 文案「端口 5173 当前被占用」；**不阻止导入**、不自动改端口 | 16 个 Vite 都指 5173，是必然现象；自动改端口会让「用户以为的地址」和实际不符。是否要「自动分配空闲端口」按钮 → 请拍板（我倾向 v1 不做） |
| 10.6 | `react-native start`（Metro，8081）、`remotion studio`、`docker compose up` 是否算「开发项目」？ | Metro 与 remotion **保留但标 low 提示「非网页服务」**；docker **默认不产出** | 它们的端口确实可访问，但不是「网页预览」；本机 docker 未安装，13 个 compose 会把列表淹掉 |
| 10.7 | `yarn.lock` 项目但本机无 yarn，怎么办？ | 回退 `npm run` + warn；**不**因为锁文件就写死 `yarn` | 本机 `yarn` 未安装（3.4）；`npm run` 对 package.json 脚本总能工作 |
| 10.8 | monorepo 内多个 `package.json`：全列出还是只列 workspace 根？ | **全列出**，但按 `.git` 分组折叠，单组默认展开前 5 条、其余显示「还有 N 条」 | 3.8：48 条落在 15 个仓库，全列出才有用；只列根会漏掉 `aihaoji-workspace\frontend_pc_web` 这种真正想跑的子项目 |
| 10.9 | 是否在 DevDock 启动时自动扫一次？ | **不扫**。只在用户点按钮 / 敲 `scan` 时扫 | 老板第一要求是启动快；扫描是纯附加价值，不该进启动路径 |
| 10.10 | 扫描根是否持久化到 `config.json`？是否提供「忽略此目录」黑名单？ | `settings.scanRoots` **持久化**（用户填过的根）；`ignore` 黑名单 **v1 不做** | 持久化省重复输入；黑名单可以靠「项目已存在 → Duplicate 灰显」覆盖大部分诉求，少一个配置面 |
| 10.11 | UI 的自动验收怎么做？ | 用 `paw-browser` 做一次真实点击（打开 → 扫描 → Escape 关闭 → 断言 `#scan-mask[hidden]`），把截图与断言写进 `docs/VERIFICATION.md` | 上次抽屉 bug 就是纯静态检查没抓到；但引入浏览器自动化会动到验收脚本结构 → 请拍板 |
| 10.12 | 是否给 MCP 加 `devdock_scan`？ | **v1 不加** | `mcp.go:159` 的既有约束；agent 可用 CLI |
| 10.13 | Go 的 `cmd/` 多入口（本机 `open-ai-canvas\backend` 4 个）要不要让用户选？ | 不选，**直接不产出**（G4） | 这 4 个都是 `migrate-*` 工具；让用户从 4 个里挑只会增加噪音。需要的话用户自己 `add` |

---

## 11. 风险与边界（实现时必须守住）

| 风险 | 后果 | 对策 |
|---|---|---|
| 扫描卡住（网络盘、休眠盘、巨量目录） | UI 转圈、CLI 挂死 | 看门狗 + `context` + 硬预算（5.4）；不扫非固定盘 |
| 冷缓存下首次扫描很慢（实测 11.9s/5000 目录） | 用户以为卡死 | 硬预算兜底；UI 显示「扫描中…（最多 2 秒）」；不做启动自动扫描 |
| 误判产出大量无效建议 | 用户不再信任该功能 | 黑名单 + 依赖闸门（4.3/4.5）+ 分组折叠 + 只勾 high |
| 端口误判 | 启动即失败 | 端口留空策略（4.8）+ 预检标记（10.5） |
| 扫描读敏感文件 | 隐私问题 | 只读 `package.json`/`go.mod`/`pyproject.toml`/`requirements*.txt`/`vite.config.*`/`manage.py`/入口 `.py`/`Cargo.toml`/`*.gradle`/`pom.xml`/compose 文件；**不读 `.env` 的值**（只按需取 `PORT=` 一行且不入库、不落盘、不回传） |
| 与现有 `add` 行为冲突 | 破坏已验证的功能 | `add` 的旧用法完全保留（约定 11）；`--auto` 只是新增分支 |
| 新增类名又把 `hidden` 压掉 | 复现上次抽屉 bug | 复用 `.drawer-mask`；新类一律带 `[hidden]` 规则（6.6） |
| 新增静态文件忘改 embed | 运行时 404 | 优先不加文件；若加，改 `web/embed.go:11` |

---

## 12. 未验证清单（不要写进结论）

| # | 内容 | 原因 |
|---|---|---|
| 1 | `pom.xml`（Maven/Spring Boot）规则 | 本机 `D:\Desktop` 全树 **0 个 `pom.xml`** |
| 2 | `*.csproj`（.NET）规则 | 本机 **0 个** |
| 3 | `Cargo.toml` 规则 | 唯一一例在 `devwebui\misc\tray-host-native`（不在本项目勘察范围，且是 GUI 托盘程序） |
| 4 | Gradle/JVM 服务规则 | 本机 4 个 gradle 文件全在 React Native 的 `android/` 下，是移动端构建 |
| 5 | `docker compose` 建议 | 本机 `docker` 未安装，无法验证命令与端口解析 |
| 6 | 是否存在通行的 `/health` 约定 | 未对全库做路由穷举 grep；只确认 `testsupport/fixture` 自带 |
| 7 | 扫描在**真冷缓存**（重启后首次）的耗时 | 只测到「同一进程内首次 11.9s / 后续 0.7–2.2s」；没有重启机器或清 FS 缓存后复测 |
| 8 | 扫描性能的 Go 实现数字 | 本次是 Python 参考实现的实测；Go 版并发后的真实数字需开发 agent 按 5.3 实测 |
| 9 | UI 在真实浏览器里的表现 | 没有开浏览器验证扫描抽屉的交互与关闭（与 `docs/VERIFICATION.md:184` 同一类未验证） |
| 10 | 多根、网络盘、OneDrive 占位文件（云端未下载）下的扫描行为 | 未构造 |
| 11 | 长路径（>260 字符）项目目录 | 未构造；实现时应对 `readdir` 错误静默跳过 |

---

## 13. 附录：本次勘察的原始口径

- 勘察时间：本机当前会话；根 `D:\Desktop`；BFS；depth ≤ 6；单目录 ≤ 5000 条目；跳过点目录与 13 个产物目录；不跟随链接。
- 规模：8613 目录、236 个候选项目根、177 个 `package.json`、48 条 JS/TS 可启动建议（30 条有端口）。
- 48 条的分布：depth 1/2/3/4/5/6 = 1/25/13/4/1/4；仓库分组 15 个（`aihaoji-workspace` 11、`workshop` 6、`open-design` 6、`Toonflow-app` 4、`TencentDB-...` 4、`infinite-canvas`/`AiToEarn`/`EverOS` 各 3、`huobao-drama` 2、其余各 1）。
- 框架分布：Vite 16、Next 8、Metro 2、Nuxt 1、Webpack 1、Nest 1、Remotion 1、无框架 18。
- 端口可推断 30 / 留空 18；`vite.config` 显式端口 9/25；`.env` 含 `PORT=` **0/22**，含 `DB_PORT` 4/22。
- 锁文件：`package-lock` 42、`pnpm-lock` 14、`bun.lock` 8、`yarn.lock` 5。
- 工具链：`node`/`npm`/`pnpm`/`bun`/`go`/`py`/`uv`/`cargo`/`java`/`mvn` 存在；`yarn`/`gradle`/`docker` **不存在**。
- 参考项目只读检查：`devwebui` 有 `server/src/detect.ts`（354 行，单项目识别）与 `server/src/scan.ts`（420 行，有界全盘扫描），均**只读**，未做任何修改。
