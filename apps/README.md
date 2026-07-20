# apps/ — 预留:未来的桌面 GUI 壳

**当前为空,这是刻意的。** v1 只交付 TUI(见 ADR 0002/0003),GUI 三选一(Wails / Electron / AppKit)尚未决定(ADR 0004)。

未来某个桌面 GUI 落地时,放在这里,规则如下(ADR 0010):

- 每个壳是**独立 module**,有自己的 `go.mod`;根目录届时引入 `go.work` 把它和核心 `library` module 链起来。
- **Wails**(Go):自带 `wails.json` + `frontend/`,进程内直接 `import library`,不走 RPC(ADR 0004)。
- **Electron / AppKit**(非 Go):通过 stdio JSON-RPC 走 `rpc/` sidecar,消费 `clients/` 里对应语言的客户端桩(ADR 0008)。

TUI 不在这里 —— 它是 Go 进程内前端,属于根 module 的 `cmd/skills`,不是 `apps/` 意义上的"app"(ADR 0010)。
