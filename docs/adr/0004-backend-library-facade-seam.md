# Backend is an in-process Go facade, shaped to be serialization-ready

The backend is a single in-process Go package exposing one facade interface — `Library` — that callers use directly; there is **no separate service or transport layer**. We chose in-process over a daemon/RPC backend because the tool is single-user and local (one SQLite Library behind it), so paying the cross-process tax now buys nothing. Because a caller in another language may need one later, the interface is deliberately designed so a thin transport adapter can wrap it **without reshaping the core**. Every decision below picks the option that avoids the cross-process tax today while avoiding serialization debt tomorrow.

## The decisions

- **One facade interface, `Library`.** All operations are methods on a single type. UI depends on one type, mocks one type, and a future RPC layer exposes one service. Splitting into sub-interfaces later is a cheap, compiler-guided refactor in Go (implicit interface satisfaction — the concrete implementation never changes); it only gets expensive after the facade has been frozen as an RPC service with external clients, which the deferred-transport stance avoids.
- **Synchronous `(result, error)`, `ctx` first.** No progress/streaming model. Long operations (Preview, Fetch, Sync) are wrapped into a background task by the caller. `ctx context.Context` as the first parameter costs nothing and reserves timeout/cancellation without changing signatures.
- **The interface speaks in domain DTOs, never DB rows.** Methods return plain value structs matching the glossary (`Skill`, `Collection`, `Group`, `Scope`, `Source`), mapped from SQLite rows inside the backend by a thin row→DTO layer. This is the single coordination point with the separate DB-schema effort: the schema can change freely — indexes, blob layout, columns — and the UI never moves. It is the natural extension of ADR-0001 (SQLite is an opaque store; the folders in agent dirs are copies of it): the interface keeps SQLite hidden behind it.
- **Typed error with a minimal `ErrorCode` enum.** Errors carry a small, stable code (e.g. `NotFound`, `AlreadyExists`, `SourceUnreachable`, `Permission`, `Internal`) plus a message, so the UI can branch on the code to render distinct messages and the code survives a future serialization boundary intact. Codes are kept minimal — only the kinds the UI actually branches on; everything else is `Internal`.

## Error codes, Details, and the "minimal set" boundary

The original "minimal `ErrorCode`" decision still holds: codes exist so the UI can branch, not so every implementation failure gets a unique label. #67 tightened what "minimal" means.

**Codes follow UI actions, not failure taxonomy.** The set is the smallest collection that lets a client pick one of: 改输入 / 改状态 / 重试 / 收起 / 报 bug. That is why three codes were added rather than a larger catalog:

- `canceled` — ctx 取消或超时。动作:静默收起。`Error.Err` 保留原始 context 错误,`errors.Is` 仍成立,JSON-RPC 的 cancel 通知不必改。
- `failed_precondition` — 请求合法、对象都在,但当前库状态不允许。动作:说明原因,不提供覆盖或重试。
- `source_invalid` — 来源可达但内容不可用。动作:展示原因、换来源;不要提供"重试"。
- `busy` — 另一进程正持有库的写锁,等待超过上限(ADR 0012)。动作:提示稍后重试;不当 bug,也不与 `source_unreachable` 混为网络故障。

`RateLimited` / `Unauthenticated` 会再增加分支(限流等待 / 重新配置凭证),但要等配置 API 落地才对调用方有意义,见 #103。

**`Details` 是类型化结构,不是 `map[string]string`。** Go 侧字段有类型,TS 可直接生成,不必再维护一套键名常量或把 ID 当字符串解析。需要新字段(例如 E2 的 retry-after)时追加即可。JSON 只在有细节时输出 `details`,且永不输出内部 `Err`。

**描述调用方错误的 error 必须是 `*Error`。** `fmt.Errorf` 经 `typedError` 会变成 `internal`。校验、冲突、前置条件、来源内容问题都在触发 SQLite 约束之前显式返回带 `Details` 的 `*Error`。`typedError` 的兜底(UNIQUE / PRIMARY KEY → `already_exists`,FOREIGN KEY → `not_found`,`SQLITE_BUSY` / `SQLITE_LOCKED` → `busy`,context → `canceled`)只接漏网,不带 `Details`。

## Consequences

- The caller is responsible for running long calls off its main thread and for spinner/wait UX, since the interface offers no progress channel.
- Adding an out-of-process client is: implement a transport adapter over `Library` — not a core rewrite.
- A row→DTO mapping layer must be maintained; it is deliberately thin.
- UI 按 `ErrorCode` 选动作、按 `Details` 填字段,不要解析英文 `Message`。`internal` 只表示 bug 或真正的 I/O 故障。
