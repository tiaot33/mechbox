# The RPC layer is a stdio sidecar speaking JSON-RPC 2.0

ADR 0004 shaped the `Library` facade to be serialization-ready and deferred the transport. This is that transport, realized for non-Go desktop frontends (Electron, AppKit): a thin adapter that wraps `Library` and exposes it as JSON-RPC 2.0 over the sidecar process's stdin/stdout. We pick the option that costs the least today (single-user, local) while keeping a later daemon-based, multi-client topology a wrapper swap rather than a rewrite — **"A now, B-ready"**.

## The decisions

- **Sidecar child process, not a daemon.** Each desktop app spawns the backend as its own child process and it dies with the app. No port, no autostart, no discovery, no cross-process lifecycle. This assumes one frontend is in use at a time (see the coexistence note below).
- **Transport is stdio.** The parent writes requests to the child's stdin and reads responses from stdout; the channel is private (no auth) and lifecycle-bound. **All diagnostics go to stderr — in `serve` mode nothing but protocol JSON may touch stdout**, or the stream corrupts (the classic LSP/MCP footgun).
- **One binary, two mutually exclusive modes.** `skills serve` runs the RPC server over stdio and never starts the TUI; running with no subcommand runs the Bubble Tea TUI, which still calls `Library` in-process (ADR 0004 unchanged). TUI and RPC never share one process — Bubble Tea owns the terminal's stdin/stdout, which would collide with stdio framing.
- **JSON-RPC 2.0 via `sourcegraph/jsonrpc2`.** The library gives us framing, request-ID multiplexing, notifications (used for cancellation now, change-notification later), and a stream abstraction over the transport — exactly the A-now/B-ready seam. We write only the method dispatch table.
- **Expose `Library` 1:1, minus `Close` and `InstallFetched`.** `Close` is server lifecycle, not a domain method. Fetching happens only in the backend (`InstallRemote`), so the client-fetch path (`InstallFetched`) is not surfaced. DTOs cross the wire unchanged; `File.Content []byte` rides as base64 (Go's default JSON encoding).
- **Errors: domain vs protocol.** Domain errors serialize as JSON-RPC code `-32000` carrying the stable string `ErrorCode` in `error.data.code` (so ADR 0004's code survives the boundary intact); malformed requests / unknown methods use standard JSON-RPC codes.
- **Per-request goroutine + ctx cancellation.** The server dispatches each request concurrently (`Library` is already goroutine-safe; the single SQLite connection serializes DB I/O). A client `cancel` notification cancels that request's `ctx`, wired to the four network/S3 operations (`InstallRemote`, `Update`, `SyncPush`, `SyncPull`). No progress model in v1 (ADR 0004).
- **Go is the one source of truth across three languages.** The TS client is generated from the Go interface; the Swift client is hand-written; all three are pinned by a shared conformance fixture (request→response JSON) so drift fails a test.

## Considered and rejected (for now)

- **Daemon + Unix socket (topology "B").** The right shape *once the TUI and a desktop app must coexist and stay consistent* — one process owns the single SQLite connection, pushes change notifications, no write contention. Rejected now because it adds daemon lifecycle and forces the TUI off in-process `Library` onto RPC. Kept cheap to adopt later: the JSON-RPC contract is unchanged, the transport is already a swappable stream, and the TUI depends on the `Library` interface, so a `Library`-shaped RPC client stub slots in.
- **gRPC / Connect.** Strong TS *and* Swift codegen, but HTTP/2-based — they do not ride raw stdio and would force us onto a socket/TCP immediately. Off the table while transport is stdio.
- **Hand-rolled JSON-RPC.** Zero dependency, but re-implements framing, concurrency, notifications, and transport abstraction that the library already provides and tests.

## Consequences

- Coexistence gap accepted: if a user runs the TUI and a desktop app at once, two processes open one SQLite file (`SQLITE_BUSY`, stale views). Tolerated because the tool is manual and non-realtime; the real fix is topology B (daemon + change notifications). ADR 0012 later set the floor under topology A — WAL, immediate transactions and a retryable `busy` — so the remaining gap is stale views and cross-process projection atomicity, not lock failures.
- The frontend owns spinner/wait UX; the API offers no progress channel.
- A `Library`-shaped RPC client stub is the unit of future reuse — it is what both the desktop clients need conceptually and what the TUI would adopt under topology B.
- New layout: `cmd/skills` (subcommand dispatch), `rpc/` (transport-agnostic adapter), `rpc/testdata/` (conformance fixture + test), `clients/ts` (generated), `clients/swift` (hand-written).
