# 后端采用进程内 Go 门面，并为序列化做好准备

后端是一个进程内 Go 包，只暴露一个供调用方直接使用的门面接口 `Library`，**没有独立的服务层或传输层**。我们选择进程内调用而不是守护进程／RPC 后端，是因为工具面向单用户、本地使用，背后只有一个 SQLite Library，现在承担跨进程成本没有收益。考虑到以后可能有其他语言的调用方需要传输层，接口特意设计为可由一个薄的传输适配器包装，**无需重塑核心**。以下每项决策都兼顾两点：现在避免跨进程成本，将来也不积累序列化债务。

## 决定

- **只有一个门面接口 `Library`。** 所有操作都是同一个类型上的方法。UI 依赖一个类型、模拟一个类型，未来的 RPC 层也只暴露一个服务。在 Go 中，以后拆成子接口是由编译器指导的低成本重构（接口是隐式实现的，具体实现无需改变）；只有当门面已经固化为供外部客户端使用的 RPC 服务时，拆分才会变得昂贵。延后传输层可以避免这一点。
- **同步返回 `(result, error)`，`ctx` 放在首位。** 不提供进度／流式模型。耗时操作（Preview、Fetch、Sync）由调用方包装成后台任务。将 `ctx context.Context` 作为第一个参数没有额外成本，还能预留超时／取消能力，无需以后改变签名。
- **接口使用领域 DTO，不暴露数据库行。** 方法返回与术语表对应的普通值结构体（`Skill`、`Collection`、`Group`、`Scope`、`Source`），由后端内部一个薄的行→DTO 映射层从 SQLite 行转换而来。这是与独立进行的数据库 schema 工作之间唯一需要协调的地方：索引、blob 布局、列等 schema 细节可以自由变化，UI 无需跟着改动。这自然延续了 ADR-0001 的决定（SQLite 是对外隐藏内部结构的存储，Agent 目录中的文件夹只是副本）：接口将 SQLite 隐藏在内部。
- **类型化错误与最小的 `ErrorCode` 枚举。** 错误携带一个简短、稳定的码（例如 `NotFound`、`AlreadyExists`、`SourceUnreachable`、`Permission`、`Internal`）和一条消息，UI 据此分支并显示不同提示；错误码也能完整跨越未来的序列化边界。错误码保持最小集合，只包含 UI 实际需要区分的类别，其余均为 `Internal`。

## 错误码、Details 与“最小集合”的边界

最初“最小 `ErrorCode`”的决策仍然成立：错误码用于让 UI 分支处理，而不是给每一种实现故障分配独有标签。#67 进一步明确了“最小”的含义。

**错误码按 UI 动作划分，不按故障分类体系划分。** 只需用最小集合让客户端能够选择以下动作之一：改输入 / 改状态 / 重试 / 收起 / 报 bug。因此只增加了三个错误码，而不是更庞大的目录：

- `canceled` — ctx 取消或超时。动作:静默收起。`Error.Err` 保留原始 context 错误,`errors.Is` 仍成立,JSON-RPC 的 cancel 通知不必改。
- `failed_precondition` — 请求合法、对象都在,但当前库状态不允许。动作:说明原因,不提供覆盖或重试。
- `source_invalid` — 来源可达但内容不可用。动作:展示原因、换来源;不要提供"重试"。
- `busy` — 另一进程正持有库的写锁,等待超过上限(ADR 0012)。动作:提示稍后重试;不当 bug,也不与 `source_unreachable` 混为网络故障。

`RateLimited` / `Unauthenticated` 会再增加分支(限流等待 / 重新配置凭证),但要等配置 API 落地才对调用方有意义,见 #103。

**`Details` 是类型化结构,不是 `map[string]string`。** Go 侧字段有类型,TS 可直接生成,不必再维护一套键名常量或把 ID 当字符串解析。需要新字段(例如 E2 的 retry-after)时追加即可。JSON 只在有细节时输出 `details`,且永不输出内部 `Err`。

**描述调用方错误的 error 必须是 `*Error`。** `fmt.Errorf` 经 `typedError` 会变成 `internal`。校验、冲突、前置条件、来源内容问题都在触发 SQLite 约束之前显式返回带 `Details` 的 `*Error`。`typedError` 的兜底(UNIQUE / PRIMARY KEY → `already_exists`,FOREIGN KEY → `not_found`,`SQLITE_BUSY` / `SQLITE_LOCKED` → `busy`,context → `canceled`)只接漏网,不带 `Details`。

## 后果

- 接口不提供进度通道，因此调用方负责在主线程之外执行耗时调用，并提供加载指示与等待体验。
- 增加进程外客户端时，只需在 `Library` 之上实现传输适配器，无需重写核心。
- 需要维护一个行→DTO 映射层，该层有意保持精简。
- UI 按 `ErrorCode` 选动作、按 `Details` 填字段,不要解析英文 `Message`。`internal` 只表示 bug 或真正的 I/O 故障。
