## Codebase

Go module。后端核心是 `internal/library` 中的 `library` 包 —— 完整的进程内技能库(Library,见 ADR 0008),供本项目的 Go 调用方使用。SQLite 数据库是唯一权威数据源,磁盘上各 Agent 目录里已安装的技能文件夹由它生成。

- `internal/library/doc.go` — 包说明。**阅读入口,先读这个**;方法签名用 `go doc -all ./internal/library` 查看。
- `internal/library/library.go` — `Library` 结构体与实例生命周期:Open、Close、共享状态与操作锁;`Open` 里登记各来源(`source.Kinds`)与读取上限;`SetGitHubToken` 把调用方传入的 GitHub 令牌交给 GitHub 来源;`applyChange` 把 `store.InTx` 与 `agentdir.Change` 合成一次提交:暂存 → 开事务写记录 → `Commit` 移到位 → 提交事务 → `Done`,失败时 defer 倒序撤销。
- `internal/library/errors.go` — 公开错误类型 `Error` / `ErrorCode` / `ErrorDetails`,以及唯一的翻译点 `typedError` 和参数校验。
- 以下每个文件按用户流程放一步的公开类型、公开方法与其私有辅助函数:
  - `fetch.go` — 搜索、入库预览、审计、入库;`previewTarget` 把入库预览请求里给出的那个字段交给对应来源的 `Locate`(`Collection` 不是一种来源,从库里取该集合入库时的定位信息),入库预览、入库、更新与检查更新都只经 `source.Kinds` 打开的 `Snapshot`。
  - `skills.go` — 来源与技能的公开类型、技能查询 / 移除,以及 store 记录到公开类型的转换(来源的展示字段由各来源的 `Describe` 生成)。
  - `collections.go` — 按来源聚合的 Collection 与集合的身份 `CollectionID`(值就是来源那一行的主键)。
  - `update.go` — 按集合检查更新(结论随技能保存)与「集合 + 勾选的技能」的更新(库里全成或全不成)。
  - `versions.go` — 版本历史与差异,以及文件的公开类型与按版本 ID 读取文件(技能详情页看当前文件也走这里)。
  - `groups.go` — Group(带成员的技能 ID)与成员管理。
  - `agents.go` — Agent 的公开类型与列出(含是否检测到)、自定义 Agent 的添加 / 修改(改目录时迁移或去掉安装记录)/ 删除;按 Agent 实际算已装的各处(含共用目录推算的)处理,其他 Agent 还在读的文件夹不动、安装记录转给它们。
  - `projects.go` — 项目登记 / 改位置 / 注销、Scope 与 `ScopeDirectory`。
  - `install.go` — 安装计划、直接安装与组安装、卸载、安装记录列表(按 `SkillScopeFilter` 筛选;共用目录的各 Agent 各列一处)。
  - `reinstall.go` — `SkillScopeFilter`、SkillScope 巡检(每一处的状态与和磁盘一致的版本)与按处重新安装(`ReinstallRequest` 逐处列出、逐处同意覆盖)。
  - `sync.go` — 整库与 S3 的推拉同步:调用方传入的同步设置(`SetSyncSettings`,整组取用,没传入才读环境变量)、远端身份比对、同步计划里 S3 上那份库的信息(推送时间与机器、拉回前的摘要与不能拉回的原因);快照、拉回库校验与替换由 `store` 完成。
- `internal/library/internal/skillfile` — 技能文件 `File` / `Mode`(`library` 另有同形的公开类型 `File` / `FileMode`,由 `versions.go` 在出口转换)、文件集合比较、内容指纹,以及巡检与版本差异共用的 `Digest`。
- `internal/library/internal/source` — 每种来源实现的 `Kind`(按定位信息打开 `Snapshot`、生成展示名称 / Ref / 查看链接)与按来源类型分派的 `Kinds`;`Snapshot` 是钉住的一份来源内容:候选、按名取文件、检查更新。定位信息 `Locator` 对 Library 不透明,由各来源的 `Locate` 从用户输入生成。`Check` 返回变了 / 没变 / 未知三态,未知时由 `update.go` 的 `checkSnapshot` 取文件比较;各来源自己的读取状态 `State` 存在 `skill_versions.source_state`,Library 只存取不解析。子包共用的错误也在这里:哨兵错误 `ErrUnreachable` / `ErrPermission` / `ErrInvalid` / `ErrUnsupported`,以及输入不合法的 `InputError`、ref / 子路径 / 技能不存在的 `NotFoundError`(按 `What` 区分,检查更新据此把「来源里没有这个技能」记为来源里已删除);各来源不另定义错误。
- `internal/library/internal/source/local` — 本地目录来源。
- `internal/library/internal/source/download` — URL 来源:下载 zip 压缩包,按 HTTP validator 发条件请求;validator 作为 `State` 记在各 Skill 的版本上。
- `internal/library/internal/source/github` — GitHub 来源:`input.go` 解析 GitHub 输入并跑 git 命令,`contents.go` 是 `Snapshot` 实现(commit / tree / blob、目录 tree SHA 与确认 commit 作为 `State`),`transfer.go` / `memo.go` 负责下载与对象缓存,`clone.go` 是 API 不可用时的浅克隆回退。`Client` 持有跨操作共享的认证降级状态、调用方传入的令牌(只用在匿名之后、gh 之前的令牌那一步,没传入才读环境变量;降级状态随令牌换新,换令牌前发出的请求和已有的入库预览都不再沿用旧结论)与缓存,由 Library 持有、`Close` 时清空。
- `internal/library/internal/source/discovery` — 各来源共用的候选发现:目录扫描规则、插件清单、SKILL.md frontmatter 解析、读取上限。子包只返回自己的哨兵错误,由 `errors.go` 的 `typedError` 翻译成公开错误码。
- `internal/library/internal/store` — SQLite,Library 的全部数据库操作都在这里:打开库(建表、身份与 schema 校验、补 Agent 默认行)、`InTx` 事务、同步用的快照 / 拉回库(`Image`)/ 整库替换;`schema.go` + 嵌入的 `schema.sql` 是唯一 DDL 来源。方法按「一次有意义的数据改动」划分,读方法写在 `queries` 上、`*Store` 与 `*Tx` 共用,多语句写入只在 `*Tx` 上;组卸载等写在 SQL 里的规则随方法留在这里。用自己的记录类型(ID 为 `int64`、时间为 `time.Time`),由 Library 转换成公开类型;返回自己的哨兵错误 `ErrNotFound` / `ErrAlreadyExists` / `ErrBusy` / `ErrUnacceptable`,由 `typedError` 翻译。
- `internal/library/internal/agentdir` — Agent 目录里的技能文件夹:`Change` 负责暂存(`Stage`)、登记删除(`Remove`)、移到位(`Commit`)、撤销(`Rollback`)与事务提交后清理备份(`Done`);另有存在性检查、读取已安装文件夹的指纹、规范化目录路径。
- `internal/library/internal/market` — skills.sh 的搜索与审计接口:发请求、限制响应大小、解析结果;返回自己的哨兵错误 `ErrUnreachable` / `ErrPermission` / `ErrInvalidResponse`。哪些结果能转成入库预览、审计查不到时算哪种状态,由 Library 决定。
- `internal/library/internal/s3` — 同步用的 S3 对象读写:用 Library 传入的设置,没有时从环境变量读配置、SigV4 签名、`Head` / `Get` / 条件 `Put`;返回自己的哨兵错误,由 `typedError` 翻译。
- `test/library/` — 技能库的集成测试(`package library_test`),按行为组织,只经公开 API 断言,用真实的 SQLite 文件、git 仓库与 `httptest` 服务;共享辅助函数在 `test_helpers_test.go`。运行 `go test ./test/library`;统计 Library 的覆盖率加 `-coverpkg=./internal/library/...`。文件名不按流程一一对应,找某个方法的测试按方法名 grep(如 `lib.Install(`)或用 `go test -run`;整理某个流程的 API 时,顺手把该流程的测试按流程合并、改名。
- `cmd/demo/main.go` — 可运行的入口示例,用内存数据库演示 Library 的导入、调用和关闭;运行 `go run ./cmd/demo`。

用户流程的编排留在 `package library`,公开方法与其私有辅助函数按用户流程放在同一文件;每个外部系统(SQLite、Agent 目录、各来源、skills.sh、S3)一个子包,放在 `internal/library/internal/` 下,只有 `library` 能导入。子包不知道公开错误码,只返回自己的哨兵错误或错误类型。新增一种来源时,在 `source` 下加子包(导出 `Type`、`Locate` 和实现 `source.Kind` 的类型),在 `library.go` 的 `Open` 里登记,并在 `PreviewFetchRequest` 加一个输入字段、在 `fetch.go` 的 `previewTarget` 里接上;更新、检查更新、展示与错误翻译都不用改。

测试放置:只经公开 API 的集成测试放在顶层 `test/` 下按被测对象分目录(如 `test/library/`);针对某个文件内部实现的单元测试放在该文件旁边。

新增任何应用时:应用依赖 Library,Library 不依赖应用;默认共用根 module,入口放 `cmd/<app>/`。桌面端的位置见 [ADR 0008](docs/adr/0008-desktop-is-wails-v3-in-process.md)。

**注释:`library` 包的导出标识符(类型、函数、方法、常量)都写 doc comment;`schema.sql` 也写注释。其他非测试代码(未导出的实现、各子包)不写注释**(`//go:build`、`//go:embed` 等编译指令除外)。注释一律使用中文;标识符、技术术语保留英文。

## Agent skills

### Ecosystem background

涉及 Skill 格式、Agent 发现/调用、Agent 发现目录或同名行为时，先读 [Agent Skills 背景](docs/agents/agent-skills-background.md)；不熟悉生态时也从这里开始。本项目与生态的对应关系、尚未确认的差异见该文末节。

### Product

设计桌面端交互，或判断一个功能该不该做、该做成什么样时，先读 [产品定位](docs/product/positioning.md)。涉及检查更新、更新、回滚时，再读 [更新的产品逻辑](docs/product/update.md)。任何功能都要守住的承诺见 [ADR 0010](docs/adr/0010-product-promises.md)。

### Issue tracker

Issues live in this repo's GitHub Issues (via the `gh` CLI). See `docs/agents/issue-tracker.md`.

### Triage labels

The five canonical triage roles, each label string equal to its role name. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: one `CONTEXT.md` + `docs/adr/` at the repo root. See `docs/agents/domain.md`.
