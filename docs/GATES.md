# DevDock 门禁与验证手册

> 本文所有数字均为 2026-11-18 在 DESKTOP-0KFECJ1 实测。
> 复现命令逐条给出，不写「应该通过」。

## 1. 环境事实

| 项 | 实测值 |
|---|---|
| Go | `go1.25.1 windows/amd64`（`D:\go\bin\go.exe`） |
| 唯一外部依赖 | `golang.org/x/sys`，**锁 `v0.47.0`** |
| 工作目录 | `D:\Desktop\DevDock\work`（git 仓库，origin 是 GitHub `YIbaikaishui/DevDock`） |
| 构建产物 | `D:\Desktop\DevDock\outputs\DevDock.exe` |

**依赖版本约束不要动**：`x/sys v0.48.0+` 要求 `go >= 1.26.0`，用 `@latest` 会直接构建失败。

## 2. 静态门禁

```powershell
cd D:\Desktop\DevDock\work
go build ./...
go vet ./...
```

**2026-11-18 实测：**
```
go build ./...   → EXIT=0（无输出）
go vet ./...     → EXIT=0（无输出）
```

## 3. 自动化测试

### 3.1 分包含跑（推荐，稳定）

```powershell
go test -count=1 ./internal/httpapi ./internal/logs ./internal/runner `
                 ./internal/store ./internal/winproc ./internal/mcpserver
```

**2026-11-18 实测（分开跑）：**
```
./internal/httpapi   -> exit=0 | ok  2.124s
./internal/logs      -> exit=0 | ok  1.026s
./internal/runner    -> exit=0 | ok  3.591s
./internal/store     -> exit=0 | ok  1.238s
./internal/winproc   -> exit=0 | ok  1.624s
./internal/mcpserver -> exit=0 | ok  2.220s
```

### 3.2 全量 `go test ./...` —— 会偶发失败，但不是测试的问题

**2026-11-18 实测三次，结果不一致：**

| 次数 | 结果 |
|---|---|
| 第一次（并发跑 6 包） | 各包打出 `ok`，但整体 **EXIT=1** |
| 第二次（`./...`） | **EXIT=0**，全绿 |
| 第三次（`-p 1` 串行） | **EXIT=0**，全绿 |

第一次的报错原文：
```
go: unlinkat C:\Users\zhang\.config\alma\scratch\go-build1699149293\b224\mcpserver.test.exe: Access is denied.
```

**根因判断：不是代码问题，是杀毒软件（MsMpEng）在 Go 删除临时测试 exe 时抢了文件锁。**
证据：同一个 `go test` 命令重跑就过；单独跑 `./internal/mcpserver` 也过
（`14.304s` 和 `2.220s` 两次都 exit=0）。

**因此：**
- **不要宣称「全量 `go test ./...` 通过」。** 它是偶发的，不能作为门禁。
- **门禁用 3.1 的分包命令。** 六个包全 exit=0 是可靠判据。
- 若一定要全量跑，加 `-p 1` 降低并发，或把 Go 的 tmp 目录排除出杀毒扫描。

### 3.3 测试覆盖了什么

| 包 | 覆盖的行为 |
|---|---|
| `httpapi` | REST 路由、SSE、loopback 安全中间件、Host/Origin 校验 |
| `logs` | ring buffer 有界性、订阅 fan-out、磁盘轮转、读截断 |
| `runner` | 生命周期状态机、1s 早退检测、readiness 与 lifecycle 正交 |
| `store` | 内存索引、原子落盘、schemaVersion 迁移、损坏降级 |
| `winproc` | Job Object 整树终止、嵌套 job、身份令牌防 PID 复用、`CreateMutexW` 单实例 |
| `mcpserver` | stdio JSON-RPC 协议、工具定义、错误码 |

**`runner` 与 `winproc` 的测试包含真实进程操作**（`cmd.exe` 多层后代），
不是 mock。这是这个项目最有价值的部分——**进程树收尾是它的核心承诺**。

## 4. 构建可交付产物

```powershell
cd D:\Desktop\DevDock\work
go build -trimpath -ldflags "-H=windowsgui -X main.version=0.1.0" -o ..\outputs\DevDock.exe .
```

**验收判据（三条都要）：**
1. `outputs\DevDock.exe` 存在且大小在 8–12 MiB
2. `DevDock.exe --version` 输出 `0.1.0`
3. 双击能打开浏览器界面（或 `DevDock.exe` 无参 = serve）

**⚠️ 出包判据不是「包能跑」，是「包的版本号 == 源码版本」。**
版本号通过 `-X main.version=` 注入，不注入就是 `dev`。构建时忘记加 ldflags，
包能跑但版本是旧的——这是「产物存在」和「产物是当前版本」的区别。

## 5. 冒烟测试（真实 HTTP）

```powershell
# 隔离 APPDATA 启动，避免污染真实配置
$env:APPDATA = "D:\temp\devdock-smoke"
.\outputs\DevDock.exe
# 浏览器打开提示的 127.0.0.1:<port>/?t=<token>
```

**2026-11-15 实测：**
```
HTTP=200 BYTES=6416
CLI_LIST_EXIT=0 JSON={ "projects": [] }
```

## 6. CLI 冒烟

```powershell
.\outputs\DevDock.exe list --json
.\outputs\DevDock.exe add "测试" --cwd "D:\temp\proj" --command "cmd /c echo hi" --port 9999
.\outputs\DevDock.exe start "测试" --wait-ready --timeout 30s
.\outputs\DevDock.exe logs "测试" --tail 20
.\outputs\DevDock.exe stop "测试"
```

**注意：CLI 是 automation / coding agent 的首选接口，JSON 模式便于脚本消费。**
退出码区分常见错误（见 `internal/cli` 实现）。

## 7. 已知缺口（如实记录，不粉饰）

| 缺口 | 状态 |
|---|---|
| 真实受管项目的端到端验收 | **未完成**。2026-11-15 试时测试夹具的 Windows shell 输入重定向失败，那次结果不作为成功证据 |
| MCP 对接真实 host | **未完成**。只做了协议单测（10 项通过），没接具体 MCP 客户端实测 |
| 冷启动耗时 / 空闲 working set | **未测**。PLAN §1 里的 10.6 MiB 是设计参考，不是本机实测 |
| `go test ./...` 稳定性 | **偶发失败**（杀毒抢锁），见 3.2 |

## 8. 不要做的事

- **不要用 `taskkill /T /F` 清进程。** 它按父 PID 遍历，有 PID 复用竞态。用 Job Object（已实现）。
- **不要在 `web/` 之外写 HTML/CSS。** 前端零构建，不许有 `package.json`。
- **不要在 `winproc/` 之外 `import "unsafe"`。**
- **不要改 x/sys 版本到 v0.48+。**