# 仓库布局：单一 Go module，应用内部实现放在 internal

根 Go module 为 `github.com/tiaot33/skills-tui`。Library、未来的 TUI 与 RPC 实现放在 `internal/`，正式应用入口放在 `cmd/skills/`。这些包共同构建，门面变化可以在同一个 module 内完成编译验证。当前已实现 `internal/library/` 与可运行的入口示例 `cmd/demo/`，其他目录在出现实际代码时创建，规划统一记录在本 ADR。

## 目录职责

| 位置 | 职责 | 创建时机 |
| --- | --- | --- |
| `internal/library/` | SQLite Library 及进程内 Go 门面，见 ADR 0004 | 已实现 |
| `cmd/demo/` | 使用内存数据库演示 Go 入口如何导入、调用并关闭 Library | 已实现 |
| `internal/tui/` | Bubble Tea 界面，进程内直接调用 Library，见 ADR 0009 | 实现 TUI 时 |
| `internal/rpc/` | Library 的 stdio JSON-RPC 服务端，见 ADR 0008 | 实现非 Go 客户端的 sidecar 时 |
| `cmd/skills/` | 组装依赖、分派启动模式；无子命令启动 TUI，`serve` 启动 RPC | 实现可执行程序时 |
| `apps/<client>/` | 桌面 GUI 及对应语言的构建配置 | 实现具体 GUI 时 |
| `clients/ts/`、`clients/swift/` | 非 Go 客户端可共享的 RPC SDK | 实现对应 SDK 时 |

## module 与调用关系

- **后端、TUI 与 RPC 共用根 module。** Library 保持同一 `package library`，实现与 `package library_test` 测试按职责放在同包目录。应用内部包遵循 [Go 的 internal 导入规则](https://pkg.go.dev/cmd/go#hdr-Internal_packages)：调用方包路径须为 `github.com/tiaot33/skills-tui` 或其子路径。
- **GUI 按实际构建需要组织。** 非 Go 客户端沿用各自语言的工具链；包含 Go 代码的客户端，根据独立依赖或发布需求决定是否另建 Go module。只有需要多个 Go module 联调时才引入 `go.work`，它不改变 `internal` 的可见性规则。
- **Wails 使用进程内 Library。** 配置与前端资源保留在具体 app 下，如 `wails.json`、`frontend/`。若采用独立 Go module，其路径使用 `github.com/tiaot33/skills-tui/apps/desktop-wails` 等子路径，以便导入 `github.com/tiaot33/skills-tui/internal/library`。
- **Electron / AppKit 使用 stdio sidecar。** GUI 启动 `skills serve`，通过 `internal/rpc/` 的实现访问 Library；TUI 继续进程内调用。服务端与 SDK 共用 `internal/rpc/testdata/` 中的协议一致性 fixture。
- **SDK 与 GUI 分开。** `clients/ts/` 从 Go 的 `Library` 接口生成，`clients/swift/` 手写，均通过共享 fixture 校验。SDK 可供多个 app 复用，产品界面留在 `apps/`。
