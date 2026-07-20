# rpc/ — 预留:Library → JSON-RPC over stdio(服务端)

**当前为空,这是刻意的。** RPC sidecar 只服务**非 Go** 的 GUI(Electron / AppKit);TUI 是进程内直连 `library` 的,永远不碰 RPC(ADR 0008)。GUI 未决(ADR 0004),此层暂无消费者,故延后(ADR 0010)。

真做时,按 ADR 0008 实现:`skills serve` 把 `Library` 门面暴露成 stdio 上的 JSON-RPC 2.0(`sourcegraph/jsonrpc2`),conformance fixture 放 `rpc/testdata/`,与 `clients/` 里的客户端桩对齐。
