# Go for the tool, Charm's Bubble Tea picked for the terminal UI

The tool is written in Go, with `go-sqlite3` for the Library. If and when a terminal UI is built, it uses the Charm stack — Bubble Tea for the runtime, Bubbles/Lipgloss for components and styling.

We picked this over Rust + ratatui, TypeScript + Ink, and Python + Textual for the maturity of the Charm TUI ecosystem, single-binary distribution (no runtime to ship alongside the tool), and straightforward SQLite access. Rust was heavier to write for the same result; Ink and Textual both drag a language runtime into distribution.

> **2026-09-25:TUI 已搁置。** v1 唯一的客户端是桌面端(ADR 0013);本 ADR 保留为将来做 TUI 时的既定选择。

Only the Go and `go-sqlite3` halves are in effect — no Charm package is a dependency yet. The rest of this ADR is a recorded choice, so the comparison does not have to be re-run later.
