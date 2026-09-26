# 桌面端：Wails v3,与 Library 同进程

v1 唯一的客户端是桌面端。它是一个 Wails v3 应用:Go 进程直接持有 `Library`(ADR 0004),React 前端经 Wails 绑定调用技能库，不另起 sidecar,也不建 RPC 层。取代 ADR 0008。理由与 ADR 0004 相同：工具单用户、本地，跨进程的成本换不来任何东西;同一个 module 编译即可验证桌面端这个调用方(ADR 0010)。

## 决定

- **宿主形态**:Wails v3,与 Library 同进程。绑定方法沿用技能库的 `ctx` 优先、`(T, error)` 签名;参数与返回值经 JSON 往返，所以技能库类型必须能跨 JSON 往返(ADR 0004 的 serialization-ready 在这里第一次兑现)。
- **Wails 版本**:v3 在选定时仍是 beta(`v3.0.0-beta.25`),钉住具体版本。选 v3 而不是稳定的 v2,是因为:绑定方法的 `ctx` 随前端 `promise.cancel()` 按次取消，长操作(Fetch、InstallFetch、Update、Sync)的取消不用接入层自管;错误以 JSON 放在前端错误的 `cause` 里,`*library.Error` 的码与 Details 直达前端;具名字符串类型生成 TS `enum`。**退回 v2 的条件**:v3 的 beta 变更阻塞开发且短期跟不上。退回时错误经 `ErrorFormatter` 编码成 JSON 字符串由前端解析，取消由接入层自管。
- **平台**:macOS 一等支持;Windows / Linux 只保证能编译。v3 在 Linux 默认需要 WebKitGTK 6.0。
- **单实例**:开启 Wails 单实例锁。TUI 已搁置(ADR 0002),也没有 CLI,库的写入者只有这一个进程，所以不做跨进程的库修订号或变更通知，前端在自己的写操作之后重读。ADR 0012 的多进程共存底线保留不动。
- **前端**:React + TypeScript + Vite + shadcn/ui + Tailwind CSS。TS 类型由 Wails 绑定生成器从技能库类型生成，不手写。
- **位置**:`cmd/desktop/`,内含 Go 入口、`frontend/` 与构建配置，使用根 module(ADR 0010)。
- **接入边界归属**:让技能库类型跨 JSON 往返是 Library 的责任——`Scope` 改为带显式 `Kind` 的单一结构体(ADR 0011),`library.ErrorOf(err)` 把任意 error 规范成 `*Error`(非 `*Error` 按技能库同一套规则归类，无可识别原因的记为 `internal`),桌面端的错误编码只调它，映射规则只在 Library 里写一份。Wails 专属的东西(绑定服务、事件、窗口)留在 `cmd/desktop/`,Library 不依赖 Wails。

## 考虑过的选项

- **Electron + Go sidecar(ADR 0008 的 stdio JSON-RPC)**:各平台渲染一致，生态最大;但接界面之前要先建完整的 RPC 层与 TS 类型生成，发布要带 Chromium 与 Node。平台目标以 macOS 为主，渲染一致性买不到什么。
- **Tauri + Go sidecar**:包体小，同样要 RPC 层，另多一条 Rust 工具链。
- **Wails v2**:稳定，但没有按次取消，错误只能以字符串传出，接口类型的参数无法生成绑定。

## 后果

- **运行时配置成为桌面端可用的前提。** 从 Finder / Dock 启动的 macOS 应用读不到 shell 环境变量,`PATH` 里也没有 Homebrew;Library 目前全部靠环境变量(`GITHUB_TOKEN`、`SKILLS_S3_*`、`SKILLS_STORE_URL` 等)与 `gh` 回退拿配置和凭证，在打包后的桌面端上等于不存在。
- 路线图里"RPC 边界与通知"一步取消:`Scope` 与错误编码并入 #88,变更通知与库修订号不做。进度报告仍然需要:Library 经 `ctx` 带进度回调，桌面端转成 Wails 事件。
- 以后若出现进程外调用方(TUI 恢复、CLI、多实例),跨进程变更信号与传输方式届时重新决定,ADR 0008 的推理可作参考。
