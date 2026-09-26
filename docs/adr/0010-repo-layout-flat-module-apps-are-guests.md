# 仓库布局：单一 Go module，应用内部实现放在 internal

根 Go module 为 `github.com/tiaot33/skills-tui`。后端与任何应用的内部实现都放在 `internal/`，可执行入口放在 `cmd/` 下。共用一个 module 意味着技能库（Library）的变化可以在同一次编译里验证到全部调用方。

当前实际存在的只有 `internal/library/`（SQLite 技能库（Library）及其进程内 Go 接口，见 ADR 0004）与 `cmd/demo/`（用内存数据库演示如何导入、调用并关闭 Library）。其他目录等到出现实际代码时再创建并在此登记，本 ADR 不预先规划尚不存在的目录。

## 规则

- **应用是客人。** Library 是主体，应用依赖它，它不依赖任何应用。新增应用不得要求技能库为某个界面形态让步。
- **内部实现放 `internal/`，入口放 `cmd/`。** 应用内部包遵循 [Go 的 internal 导入规则](https://pkg.go.dev/cmd/go#hdr-Internal_packages)：调用方包路径须为 `github.com/tiaot33/skills-tui` 或其子路径。
- **默认共用根 module。** 只有当某个应用有独立的依赖或发布需求时，才为它另建 Go module；只有需要多个 Go module 联调时才引入 `go.work`，它不改变 `internal` 的可见性规则。
- **非 Go 的客户端沿用各自语言的工具链**，放在仓库顶层各自的目录下，不塞进 `internal/`。
- **例外：被 Go 进程嵌入的前端跟着嵌入它的入口走。** `go:embed` 不能引用包目录之外的文件，所以 Wails 这类应用的前端放在 `cmd/<app>/frontend/`，与 Go 入口同处一个目录，仍沿用自己的工具链（ADR 0013）。
