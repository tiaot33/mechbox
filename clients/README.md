# clients/ — 预留:非 Go 前端的 RPC 客户端桩(SDK)

**当前为空,这是刻意的。** 客户端桩是替非 Go 前端说 stdio JSON-RPC 的那层薄 SDK,与 `rpc/`(服务端)是同一条契约的两端。GUI 未决(ADR 0004),暂无消费者,故延后(ADR 0010)。

真做时(ADR 0008):

- `clients/ts` —— **从 Go 的 `Library` 接口生成**的 TypeScript 客户端。
- `clients/swift` —— **手写**的 Swift 客户端。
- Go 是三种语言里唯一的事实源;三者用 `rpc/testdata/` 的共享 fixture 钉住,任何一方漂移即测试失败。

放在这里(而非各自 app 目录内)是因为它是**生成的契约产物 + 潜在多消费者**,和 `apps/` 里手写的产品壳分开(ADR 0010)。Wails 用不到它 —— Wails 是 Go,进程内直连。
