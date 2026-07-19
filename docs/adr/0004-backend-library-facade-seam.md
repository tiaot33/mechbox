# Backend is an in-process Go facade, shaped to be serialization-ready

The backend is a single in-process Go package exposing one facade interface — `Library` — that the TUI calls directly; there is **no separate service or transport layer in v1**. We chose in-process over a daemon/RPC backend because the tool is single-user and local (one SQLite Library behind it), so paying the cross-process tax now buys nothing. But because a future desktop app may be Wails (Go), AppKit, or Electron — and two of those are non-Go — the interface is deliberately designed so a thin transport adapter (HTTP / JSON-RPC / Wails binding) can wrap it later **without reshaping the core**. Every decision below picks the option that avoids the cross-process tax today while avoiding serialization debt tomorrow.

## The decisions

- **One facade interface, `Library`.** All operations are methods on a single type. UI depends on one type, mocks one type, and a future RPC layer exposes one service. Splitting into sub-interfaces later is a cheap, compiler-guided refactor in Go (implicit interface satisfaction — the concrete implementation never changes); it only gets expensive after the facade has been frozen as an RPC service with external clients, which the deferred-transport stance avoids.
- **Synchronous `(result, error)`, `ctx` first.** No progress/streaming model in v1. Long operations (Install, Sync) are wrapped into a background task by the frontend (in Bubble Tea, a `tea.Cmd`). `ctx context.Context` as the first parameter costs nothing and reserves timeout/cancellation without changing signatures.
- **The interface speaks in domain DTOs, never DB rows.** Methods return plain value structs matching the glossary (`Skill`, `Collection`, `Group`, `Scope`, `Source`), mapped from SQLite rows inside the backend by a thin row→DTO layer. This is the single coordination point with the separate DB-schema effort: the schema can change freely — indexes, blob layout, columns — and the UI never moves. It is the natural extension of ADR-0001 (SQLite is an opaque store; agent dirs are projections): the interface keeps SQLite hidden behind it.
- **Typed error with a minimal `ErrorCode` enum.** Errors carry a small, stable code (e.g. `NotFound`, `AlreadyExists`, `SourceUnreachable`, `Permission`, `Internal`) plus a message, so the UI can branch on the code to render distinct messages and the code survives a future serialization boundary intact. Codes are kept minimal — only the kinds the UI actually branches on; everything else is `Internal`.

## Consequences

- The frontend is responsible for running long calls off the UI thread (Bubble Tea `Cmd`) and for spinner/wait UX, since the interface offers no progress channel.
- Adding a desktop client is: implement a transport adapter over `Library` — not a core rewrite.
- A row→DTO mapping layer must be maintained; it is deliberately thin.
