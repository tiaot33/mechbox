# RPC transport choice: a stdio sidecar speaking JSON-RPC 2.0

> **状态：已被 [ADR 0013](0013-desktop-is-wails-v3-in-process.md) 取代(2026-09-25)。** 桌面端与 Library 同进程，经 Wails 绑定调用;目前没有计划中的进程外调用方。下文保留为当时的推理，将来出现进程外调用方时可作参考。

ADR 0004 shaped the `Library` facade to be serialization-ready and deferred the transport. This ADR records the transport that was picked for the day a non-Go caller needs one — nothing here is built yet. It is a technology choice with rejected alternatives, not a design for the adapter; the adapter's shape is decided when it is written.

The criterion: pick the option that costs the least while the tool is single-user and local, without foreclosing a daemon-based, multi-client topology later — **"A now, B-ready"**.

## The choices

- **Sidecar child process, not a daemon.** The caller spawns the backend as its own child process and it dies with the caller. No port, no autostart, no discovery, no cross-process lifecycle. This assumes one caller at a time (see the coexistence note below).
- **Transport is stdio.** The parent writes requests to the child's stdin and reads responses from stdout; the channel is private (no auth) and lifecycle-bound. Two constraints follow and are not negotiable once stdio is chosen: **all diagnostics go to stderr — nothing but protocol JSON may touch stdout**, or the stream corrupts (the classic LSP/MCP footgun); and the RPC server cannot share a process with anything else that owns the terminal's stdin/stdout.
- **JSON-RPC 2.0 via `sourcegraph/jsonrpc2`.** The library gives us framing, request-ID multiplexing, notifications (cancellation, and change notification later), and a stream abstraction over the transport — exactly the A-now/B-ready seam. Only the method dispatch table would be ours to write.

## Considered and rejected

- **Daemon + Unix socket (topology "B").** The right shape *once several callers must coexist and stay consistent* — one process owns the single SQLite connection, pushes change notifications, no write contention. Rejected now because it adds daemon lifecycle and forces every in-process caller onto RPC. Kept cheap to adopt later: the JSON-RPC contract is unchanged and the transport is already a swappable stream.
- **gRPC / Connect.** Strong multi-language codegen, but HTTP/2-based — they do not ride raw stdio and would force a socket/TCP immediately. Off the table while transport is stdio.
- **Hand-rolled JSON-RPC.** Zero dependency, but re-implements framing, concurrency, notifications, and transport abstraction that the library already provides and tests.

## Consequences

- Coexistence gap accepted: two processes may open one SQLite file (`SQLITE_BUSY`, stale views). Tolerated because the tool is manual and non-realtime; the real fix is topology B. ADR 0012 later set the floor under topology A — WAL, immediate transactions and a retryable `busy` — so the remaining gap is stale views and cross-process install atomicity, not lock failures.
- No progress channel rides this transport unless ADR 0004 gains one first; the caller owns spinner/wait UX.
