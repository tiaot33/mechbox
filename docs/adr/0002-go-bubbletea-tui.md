# Go + Bubble Tea for the TUI

The tool is built in Go using the Charm stack — Bubble Tea for the TUI runtime, Bubbles/Lipgloss for components and styling — with `go-sqlite3` for the Library.

We picked this over Rust + ratatui, TypeScript + Ink, and Python + Textual for the maturity of the Charm TUI ecosystem, single-binary distribution (no runtime to ship alongside the tool), and straightforward SQLite access. Rust was heavier to write for the same result; Ink and Textual both drag a language runtime into distribution.
