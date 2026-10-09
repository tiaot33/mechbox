# Skills Manager

管理 Claude Code 和 Codex 的 Agent Skills。当前仓库已实现后端库 `Library`；桌面端（Wails v3，见 [ADR 0008](docs/adr/0008-desktop-is-wails-v3-in-process.md)）尚在开发，现在只有一个演示入口。

Skill 是以 `SKILL.md` 为入口、供 Agent 按需加载的完整文件夹。用户先对一个来源做入库预览，挑选候选入库；再把库里的技能安装到某个作用域，也就是某个 Agent 的全局，或某个 Agent 加一个已登记项目，库会把技能文件夹写进该 Agent 在这里读取的目录。术语见 [CONTEXT.md](CONTEXT.md)，Agent 如何发现、加载技能见 [生态背景](docs/agents/agent-skills-background.md)。

## 当前能力

- 对本地目录、GitHub 仓库或 ZIP URL 做入库预览，挑选候选入库，之后更新。
- 保存技能的完整文件（含相对路径和可执行权限）与全部版本历史，可比较版本差异；相同内容的文件只存一份。
- 按来源自动形成集合，用户可跨来源手动建组。
- 登记项目，把单个技能或整个组安装到 Claude Code、Codex 的全局或项目作用域；可巡检已安装的技能是否被手动改过、落后于库里的版本或已缺失，并重新安装。
- 把整个库推送到 S3，或从 S3 拉回替换本地库；拉回后把不在这台机器上的已登记项目改到本机位置。
- 通过稳定的 `ErrorCode` 向调用方返回可判断的错误；`library.ErrorOf(err)` 把任意 error 规范成可 JSON 编码的 `*Error`（非 `*Error` 按库内同一套规则归类，无可识别原因的记为 `internal`）。

## 仓库地图

| 位置 | 作用 |
| --- | --- |
| [`docs/agents/agent-skills-background.md`](docs/agents/agent-skills-background.md) | Agent Skills 格式、Claude Code/Codex 发现与调用规则、资料覆盖边界 |
| [`CONTEXT.md`](CONTEXT.md) | 产品术语及统一语言 |
| [`docs/adr/`](docs/adr/) | 现行的设计决策，按主题索引见其中的 README |
| [`cmd/demo/main.go`](cmd/demo/main.go) | 可直接运行的入口示例：导入、打开、调用并关闭 Library |
| [`internal/library/doc.go`](internal/library/doc.go) | 包说明：按用户流程列出每一步用到的方法和所在文件，阅读入口 |
| [`internal/library/library.go`](internal/library/library.go) | `Library` 结构体、`Open` / `Close`、操作锁，以及把数据库事务和磁盘改动合成一次提交 |
| [`internal/library/errors.go`](internal/library/errors.go) | 公开错误类型与错误码，以及把各子包错误翻译成错误码的唯一入口 |
| [`internal/library/fetch.go`](internal/library/fetch.go) | 市场搜索、入库预览、审计与入库 |
| [`internal/library/skills.go`](internal/library/skills.go)、[`collections.go`](internal/library/collections.go)、[`versions.go`](internal/library/versions.go) | 技能查询/移除、Collection、版本历史与差异 |
| [`internal/library/update.go`](internal/library/update.go) | 按集合检查更新与更新 |
| [`internal/library/groups.go`](internal/library/groups.go)、[`projects.go`](internal/library/projects.go) | Group 成员、Agent、项目注册与 Scope |
| [`internal/library/install.go`](internal/library/install.go)、[`reinstall.go`](internal/library/reinstall.go) | 直接安装与组安装、安装记录；巡检与重新安装 |
| [`internal/library/sync.go`](internal/library/sync.go) | 整库推拉同步与项目路径对应 |
| [`internal/library/internal/`](internal/library/internal/) | 实现子包，只有 `library` 能导入：`store`（SQLite）、`agentdir`（Agent 目录里的技能文件夹）、`source/…`（GitHub、URL、本地目录等来源）、`market`（skills.sh）、`s3`、`skillfile` |
| [`internal/library/internal/store/schema.go`](internal/library/internal/store/schema.go)、[`schema.sql`](internal/library/internal/store/schema.sql) | 库身份、校验及唯一 DDL 来源 |
| [`test/library/`](test/library/) | 技能库的集成测试：按行为组织，只经公开 API 断言；共享辅助函数在 `test_helpers_test.go` |

建议依次阅读 `CONTEXT.md`、相关 ADR、`internal/library/doc.go`（方法签名与说明用 `go doc -all ./internal/library` 查看）、测试，最后再进入具体实现。

桌面端放在 `cmd/desktop/`，见 [ADR 0008](docs/adr/0008-desktop-is-wails-v3-in-process.md)。应用依赖 Library，Library 不依赖应用；默认共用根 module。

## 验证和查看接口

需要 Go 1.26、CGO 和本机 C 编译工具链。

```sh
go test -race ./...
go vet ./...
go doc ./internal/library.Library
go test -list . ./test/library
```

可运行的入口示例见 [`cmd/demo/main.go`](cmd/demo/main.go)。在仓库根目录执行：

```sh
go run ./cmd/demo
```

示例使用内存数据库，列出默认 Agent 后关闭；需要持久化时，把 `library.Open(":memory:")` 的参数改成数据库文件路径。入口使用 `package main`，与后端共用根 `go.mod`；导入路径为 `github.com/tiaot33/skills-tui/internal/library`，代码中仍通过 `library.Open` 等名字调用。资源在 `run` 中打开并通过 `defer` 关闭，`main` 负责报告错误和设置退出码。

对本地目录做入库预览、把其中的 skill 入库并安装到 Codex 全局的最小调用方式：

```go
ctx := context.Background()

lib, err := library.Open("skills.db")
if err != nil {
	return err
}
defer lib.Close()

preview, err := lib.PreviewFetch(ctx, library.PreviewFetchRequest{Local: "/path/to/skills"})
if err != nil {
	return err
}
var names []string
for _, candidate := range preview.Candidates {
	if candidate.Fetched == 0 { // 跳过已入库的候选
		names = append(names, candidate.Name)
	}
}
skills, err := lib.Fetch(ctx, preview.ID, names)
if err != nil {
	return err
}
err = lib.Install(ctx, skills[0].ID, library.GlobalScope(library.AgentCodex), library.InstallOptions{})
```

`*library.Library` 是供桌面端直接调用的进程内 Go API，位于 `internal/library`，导入路径为 `github.com/tiaot33/skills-tui/internal/library`；Go 调用方的包路径须为 `github.com/tiaot33/skills-tui` 或其子路径。当前没有网络监听入口；以后若有别的进程要用库，届时再定通信方式，见 [ADR 0008](docs/adr/0008-desktop-is-wails-v3-in-process.md)。

## 数据库

[`internal/library/internal/store/schema.sql`](internal/library/internal/store/schema.sql) 通过 Go `embed` 直接用于新库初始化，因此代码与可查看的 DDL 不会形成两份事实来源。当前包含 11 张表：

- 来源和技能：`sources`、`skills`
- 版本和内容：`skill_versions`、`blobs`、`version_files`
- 分组：`groups`、`skill_groups`
- Agent：`agents`（稳定 `code`、展示 `name`，以及全局与项目下各自读取技能的目录规则）
- 项目、安装记录与组安装：`projects`、`skill_scopes`、`group_installs`

新数据库由 `library.Open` 自动初始化，写入应用标识和 schema 版本。发布前不迁移旧库：应用标识或 schema 版本不符时 `Open` 报错且不改文件，见 [ADR 0003](docs/adr/0003-no-migrations-before-release.md)。应用内置的 Agent 以应用版本为准：`Open` 时与同步拉回后补上缺失的行，并以应用为准覆盖已有行的名称和目录规则；库里有、本版应用不认识的 Agent（例如从装了更新版本应用的机器同步来的）保持原样。库没有增删改 Agent 的方法。

作用域只记录身份（`GlobalScope(agent)` 或 `ProjectScope(agent, projectID)`），不含路径；目录由只读的 `ScopeDirectory` 按该 Agent 的目录规则算出，全局相对用户主目录，项目相对已登记项目的路径。Agent 换了读取目录后只认新目录，旧目录里的文件夹不搬、不删。同步拉库后只需重新对应已登记项目的本机路径（`RelocateProject`），安装记录不变。见 [ADR 0005](docs/adr/0005-scope-is-agent-and-level-not-path.md)。

库文件以 WAL 模式运行，同一目录下会出现 `library.db-wal` 与 `library.db-shm`；最后一个连接正常关闭时 SQLite 合并并删除 `-wal`，`-shm` 可能保留，无害。**有进程打开库时不要只复制 `library.db` 来备份**，尚未合并的写入还在 `-wal` 里；请用同步推送（推送的是一致快照），或确认没有进程打开后再复制。

## 配置

| 环境变量 | 用途 | 默认值 |
| --- | --- | --- |
| `SKILLS_S3_BUCKET` | S3 bucket | 无；同步时必填 |
| `SKILLS_S3_KEY` | S3 object key | `library.db` |
| `SKILLS_S3_ENDPOINT` | S3 兼容服务地址 | AWS S3 地址 |
| `AWS_REGION` | S3 region | `us-east-1` |
| `AWS_ACCESS_KEY_ID` | S3 access key | 无；同步时必填 |
| `AWS_SECRET_ACCESS_KEY` | S3 secret key | 无；同步时必填 |
| `AWS_SESSION_TOKEN` | 临时凭证 token | 无 |

GitHub Source 优先走 GitHub API，不可用时通过本机 `git` 命令浅克隆；URL Source 返回 ZIP 文件；本地目录在入库预览时读入内存作为快照（候选文件累计上限 1000 个、64 MiB）。

## 尚未实现

- 桌面端（开发中）；终端界面已搁置。当前 `cmd/demo` 仅演示后端调用。
- 对外的网络或进程间接口（HTTP、JSON-RPC 等）；目前没有计划。
- 回收不再被任何版本引用的文件内容、版本回退、更新后自动重新安装（现需手动重新安装）。
