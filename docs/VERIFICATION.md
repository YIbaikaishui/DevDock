# DevDock 验证记录

当前构建：DevDock.exe 版本 0.1.0，10566656 bytes，SHA-256 961847CA2036A6F1299E7D771D4954D13301966D9EC3116A0AB56FEA9DD739F6。

## 构建与静态检查

```text
go build ./...                                          exit 0
go vet ./...                                            exit 0
go build -trimpath -ldflags '-X main.version=0.1.0' ... exit 0
DevDock 0.1.0
```

## 自动化测试

以下独立包测试命令实际退出码为 0：

```text
go test -count=1 ./internal/httpapi ./internal/logs ./internal/runner ./internal/store ./internal/winproc
ok devdock/internal/httpapi 4.037s
ok devdock/internal/logs 1.987s
ok devdock/internal/runner 4.948s
ok devdock/internal/store 2.649s
ok devdock/internal/winproc 2.622s
PACKAGE_TEST_EXIT=0
```

MCP 测试二进制单独构建并运行，10 项均通过，MCP_TEST_EXIT=0。进程管理测试包括 cmd.exe 多层后代，关闭 Job Object 后逐个确认后代退出。

完整 `go test -count=1 ./...` 的各包都打印 `ok`，但 Go 在结束清理时返回非零：

```text
go: unlinkat ...\mcpserver.test.exe: Access is denied.
```

因此不宣称全量测试命令通过。此主机上的临时测试 exe 删除权限问题尚未定位。

## 当前 exe 冒烟

隔离 APPDATA 下启动当前 0.1.0 exe 后，实际请求输出：

```text
HTTP=200 BYTES=6416 PID=25296 PORT=63866
CLI_LIST_EXIT=0 JSON={ "projects": [] }
```

随后尝试对命令行管理的临时项目做运行日志端到端检查时，测试夹具的 Windows shell 输入重定向失败；该次结果不作为成功证据。UI 启动和空项目 CLI 查询已通过，真实受管项目端到端验收仍需补做。

未测冷启动耗时或空闲 working set，因此目前不发布性能数字。MCP 已有协议单测，尚未对接具体 MCP host 实测。
