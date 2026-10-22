## Codebase

Go module。后端核心是 `internal/library` 中的 `library` 包 —— 完整的进程内技能库(Library,见 ADR 0008),供本项目的 Go 调用方使用。SQLite 数据库是唯一权威数据源,磁盘上各 Agent 目录里已安装的技能文件夹由它生成。

**先读 `internal/library/doc.go`**(包说明:按用户流程列出每一步的方法与所在文件,以及跨文件的实现机制),方法签名与各类型的说明用 `go doc -all ./internal/library` 查看。本节只写目录职责与约定,按文件的细节以 doc.go 和代码的 doc comment 为准。

### 目录

- `internal/library/` — `package library`:公开类型、公开方法与用户流程的编排。公开方法与其私有辅助函数按用户流程放在同一个文件里(`fetch.go`、`update.go`、`install.go` 等,对应关系见 doc.go);`library.go` 是实例生命周期与共享状态,`errors.go` 是公开错误类型与唯一的错误翻译点。
- `internal/library/internal/` — 实现子包,每个外部系统一个:
  - `store` — SQLite:建表与校验、事务、同步用的快照与整库替换;`schema.sql` 是唯一的 DDL 来源。
  - `agentdir` — Agent 目录与已登记项目的全部路径与文件操作:用户主目录、由相对路径拼出作用域目录与技能文件夹、路径的规范化与比较、校验用户填的目录与项目路径，以及技能文件夹的暂存、移到位、撤销与读取。`library` 不直接用 `os` / `filepath` 处理这些路径。
  - `source` — 来源的公共接口(`Kind`、`Snapshot`)、按类型分派与子包共用的错误;每种来源一个子包:`local`(本地目录)、`github`、`git`(GitLab 与其他 Git 服务)、`download`(直接下载)、`wellknown`(well-known 索引);`gitcmd` 是 Git 类来源共用的 git 命令,`discovery` 是各来源共用的候选发现规则与读取上限。
  - `market` — skills.sh 的搜索与审计接口。
  - `s3` — 同步用的 S3 对象读写。
  - `skilllock` — 解析 `npx skills` 留下的来源记录，给未纳管技能认出来源(不是一种来源);记录文件由 `agentdir` 读出。
  - `skillfile` — 技能文件、文件集合比较与内容指纹,`library` 与各子包共用。
- `test/library/` — 技能库的集成测试。
- `cmd/desktop/` — 桌面端(Wails v3,与 Library 同进程,ADR 0008):绑定服务开放 Library 的全部方法,`frontend/` 是 React + TS 前端,`frontend/bindings/` 由 `wails3 generate bindings` 生成、不手改。开发、构建、端到端验证与约定见 `cmd/desktop/README.md`。

### 依赖方向

- 应用依赖 Library,Library 不依赖应用。新增应用时默认共用根 module,入口放 `cmd/<app>/`;桌面端的位置见 [ADR 0008](docs/adr/0008-desktop-is-wails-v3-in-process.md)。
- 子包放在 `internal/library/internal/` 下,只有 `library` 能导入;用户流程的编排留在 `package library`。
- 每个外部系统(SQLite、Agent 目录、各来源、skills.sh、S3)一个子包;子包用自己的记录类型(如 store 的记录),由 `library` 转换成公开类型。

### 错误

子包不知道公开错误码,只返回自己的哨兵错误(如 store 的 `ErrNotFound`、source 的 `ErrUnreachable`)或错误类型(如 source 的 `NotFoundError`),由 `errors.go` 的 `typedError` 统一翻译成公开的 `Error` / `ErrorCode`。新增错误时在子包里定义哨兵或类型,在 `typedError` 里加翻译。

### 新增一种来源

1. 在 `internal/library/internal/source/` 下加子包,导出 `Type`、识别输入的 `Matches` / `Locate`(要联网才认得出的给 `Probe`)和实现 `source.Kind` 的类型。
2. 在 `library.go` 的 `Open` 里登记。
3. 在 `fetch.go` 的 `previewTarget`(或 `probeLink`)识别顺序里放到合适的位置。
4. 在 `test/library/library_sources_test.go` 的 `sourceKinds` 里加一行，让它和其他来源走同一遍入库、检查更新、更新、新技能、删除。

`PreviewFetchRequest` 只有一个输入,不加字段。更新、检查更新、展示与错误翻译都不用改。

### 测试

- 只经公开 API 的集成测试放在顶层 `test/` 下按被测对象分目录(如 `test/library/`,`package library_test`,共享辅助函数在 `test_helpers_test.go`),用真实的 SQLite 文件、git 仓库与 `httptest` 服务。运行 `go test ./test/library`;统计 Library 的覆盖率加 `-coverpkg=./internal/library/...`。
- 针对某个文件内部实现的单元测试放在该文件旁边。
- `test/library/` 的测试文件按被测方法所在的源文件命名:文件名前缀就是那个源文件,后面可加方面(`PreviewFetch` 在 `fetch.go`,它的测试在 `fetch_preview_test.go`);某种来源特有的行为放 `source_<来源>_test.go`(如 `source_github_test.go`);Library 整体的性质(并发、多进程、建表、错误、JSON 契约)放 `library_<方面>_test.go`。一个测试用到几个方法时按它主要验证的那个归属。找某个方法的测试也可以按方法名 grep(如 `lib.Install(`)或用 `go test -run`。

### 注释

**`library` 包的导出标识符(类型、函数、方法、常量)都写 doc comment;`schema.sql` 也写注释。其他非测试代码(未导出的实现、各子包)不写注释**(`//go:build`、`//go:embed` 等编译指令除外)。注释一律使用中文;标识符、技术术语保留英文。

## Agent skills

### Ecosystem background

涉及 Skill 格式、Agent 发现/调用、Agent 发现目录或同名行为时，先读 [Agent Skills 背景](docs/agents/agent-skills-background.md)；不熟悉生态时也从这里开始。本项目与生态的对应关系、尚未确认的差异见该文末节。

### Product

设计桌面端交互，或判断一个功能该不该做、该做成什么样时，先读 [产品定位](docs/product/positioning.md)。涉及检查更新、更新、回滚时，再读 [更新的产品逻辑](docs/product/update.md)。任何功能都要守住的承诺见 [ADR 0010](docs/adr/0010-product-promises.md)。

### Issue tracker

Issues live in this repo's GitHub Issues (via the `gh` CLI). See `docs/agents/issue-tracker.md`.

### Issue labels

Creating or labelling an issue: one type, one or more areas, one triage state (the five canonical roles, each label string equal to its role name). See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: one `CONTEXT.md` + `docs/adr/` at the repo root. See `docs/agents/domain.md`.
