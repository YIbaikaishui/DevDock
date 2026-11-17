# DevDock

DevDock 是一个面向 Windows 的本地开发项目控制台。一个独立 EXE 管理项目进程、状态和日志；不需要为 DevDock 另外安装 Bun 或 Node.js。

## 下载与启动

下载 [DevDock.exe](https://github.com/YIbaikaishui/DevDock/releases/latest)，双击后会打开本机浏览器界面。也可以在终端运行：

```powershell
.\DevDock.exe --help
.\DevDock.exe list --json
```

DevDock 监听 `127.0.0.1`，配置保存在 `%APPDATA%\DevDock`。已被管理的项目仍需要它们自己的运行环境，例如 Node、Go、Java、Python 或 Docker。

## 通过 CLI 管理

```powershell
# 新增项目
.\DevDock.exe add "我的前端" --cwd "D:\projects\frontend" --command "pnpm dev" --port 5173 --readiness /

# 查看状态、启动并等待就绪、查看日志
.\DevDock.exe list --json
.\DevDock.exe start "我的前端" --wait-ready --timeout 60s
.\DevDock.exe logs "我的前端" --tail 100

# 停止或重启
.\DevDock.exe stop "我的前端"
.\DevDock.exe restart "我的前端"
```

CLI 是自动化和 coding agent 的首选接口。JSON 模式便于脚本消费，退出码区分常见错误。MCP 可通过显式的 `DevDock.exe mcp` 命令按需使用；不用 MCP 的客户端无需接入或加载它的工具定义。

## 核心行为

- 生命周期与 HTTP readiness 分开报告：进程启动不等于业务已就绪。
- 停止项目时使用 Windows Job Object 清理进程树。
- 日志保存在有界内存缓冲区；磁盘日志默认关闭。
- UI 通过本机 HTTP 服务提供，包含状态推送。
- 自动化接口只绑定 loopback，并校验本机请求。

## 从源码构建

需要 Go 1.25 或更新版本：

```powershell
cd work
$env:CGO_ENABLED = "0"
go test ./...
go vet ./...
go build -trimpath -ldflags "-H=windowsgui" -o ..\outputs\DevDock.exe .
```

完整验证记录见 [`docs/VERIFICATION.md`](docs/VERIFICATION.md)。当前项目仍在早期阶段；性能数字以该文档的机器实测为准，不作为所有机器的保证。

## 路线

本项目优先保持单机、轻量和可脚本化。CLI 是稳定的基础入口。托盘体验、云同步和自动发现不属于首版范围。

## 许可

MIT，见 [`LICENSE`](LICENSE)。
