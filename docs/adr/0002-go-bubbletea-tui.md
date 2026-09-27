# 工具使用 Go，终端界面选用 Charm 的 Bubble Tea

工具使用 Go 编写，Library 使用 `go-sqlite3`。如果以后构建终端界面，将采用 Charm 技术栈：Bubble Tea 提供运行时，Bubbles/Lipgloss 提供组件和样式。

相比 Rust + ratatui、TypeScript + Ink 和 Python + Textual，我们选择这个方案，是因为 Charm 的 TUI 生态成熟、可以单个二进制文件分发（无需随工具附带运行时），且访问 SQLite 简单直接。实现相同效果时，Rust 的编写成本更高；Ink 和 Textual 都需要在分发时附带语言运行时。

> **2026-09-25:TUI 已搁置。** v1 唯一的客户端是桌面端(ADR 0013);本 ADR 保留为将来做 TUI 时的既定选择。

目前只有 Go 和 `go-sqlite3` 两项已生效，尚未引入任何 Charm 包依赖。本 ADR 的其余部分保留已作出的选择，避免以后重复比较。
