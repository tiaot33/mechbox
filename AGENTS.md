## Codebase

Go module。后端核心是 `internal/library` 中的 `library` 包 —— 完整的进程内技能库(Library,见 ADR 0008),供本项目的 Go 调用方使用。SQLite 数据库是唯一权威数据源,磁盘上各 Agent 目录里已安装的技能文件夹由它生成。

- `internal/library/doc.go` — 包说明。**阅读入口,先读这个**;方法签名用 `go doc -all ./internal/library` 查看。
- `internal/library/library.go` — `Library` 结构体与实例生命周期:Open、Close、共享状态与操作锁;`applyChange` 把 `store.InTx` 与 `agentdir.Change` 合成一次提交:暂存 → 开事务写记录 → `Commit` 移到位 → 提交事务 → `Done`,失败时 defer 倒序撤销。
- `internal/library/errors.go` — 公开错误类型 `Error` / `ErrorCode` / `ErrorDetails`,以及唯一的翻译点 `typedError` 和参数校验。
- 以下每个文件按用户流程放一步的公开类型、公开方法与其私有辅助函数:
  - `fetch.go` — 搜索、入库预览、审计、入库;`openSnapshot` 按来源类型打开快照,入库预览、入库、更新与批量检查都只经 `Snapshot`。
  - `skills.go` — 来源与技能的公开类型、技能查询 / 移除,以及 store 记录到公开类型的转换。
  - `collections.go` — 按来源聚合的 Collection。
  - `update.go` — 批量检查与单项更新。
  - `versions.go` — 版本历史与差异。
  - `groups.go` — Group 与成员管理。
  - `projects.go` — Agent、项目注册、Scope 与 `Resolve`。
  - `install.go` — 安装预览、直接安装与组安装、卸载、安装记录列表。
  - `reinstall.go` — SkillScope 巡检(可按 `SkillScopeFilter` 缩小范围)与重新安装。
  - `sync.go` — 整库与 S3 的推拉同步:远端身份比对、`SKILLS_PROJECT_PATH_MAP` 解析;快照、拉回库校验与替换由 `store` 完成。
- `internal/library/internal/skillfile` — 技能文件 `File` / `Mode`(`skills.go` 以类型别名公开)、文件集合比较、内容指纹,以及巡检用的 `Digest`。
- `internal/library/internal/source` — 来源的统一接口 `Snapshot`(钉住的一份来源内容:候选、按名取文件、检查更新)。`Check` 返回变了 / 没变 / 未知三态,未知时由 `update.go` 的 `checkSnapshot` 取文件比较;各来源自己的读取状态 `State` 存在 `skill_versions.source_state`,Library 只存取不解析。子包共用的哨兵错误 `ErrUnreachable` / `ErrPermission` / `ErrInvalid` 也在这里。
- `internal/library/internal/source/local` — 本地目录来源。
- `internal/library/internal/source/download` — URL 来源:下载 zip 压缩包,按 HTTP validator 发条件请求;validator 作为 `State` 记在各 Skill 的版本上。
- `internal/library/internal/source/github` — GitHub 来源:`input.go` 解析 GitHub 输入并跑 git 命令,`contents.go` 是 `Snapshot` 实现(commit / tree / blob、目录 tree SHA 与确认 commit 作为 `State`),`transfer.go` / `memo.go` 负责下载与对象缓存,`clone.go` 是 API 不可用时的浅克隆回退。`Client` 持有跨操作共享的认证降级状态与缓存,由 Library 持有、`Close` 时清空。
- `internal/library/internal/source/discovery` — 各来源共用的候选发现:目录扫描规则、插件清单、SKILL.md frontmatter 解析、读取上限。子包只返回自己的哨兵错误,由 `errors.go` 的 `typedError` 翻译成公开错误码。
- `internal/library/internal/store` — SQLite,Library 的全部数据库操作都在这里:打开库(建表、身份与 schema 校验、补 Agent 默认行)、`InTx` 事务、同步用的快照 / 拉回库(`Image`)/ 整库替换;`schema.go` + 嵌入的 `schema.sql` 是唯一 DDL 来源。方法按「一次有意义的数据改动」划分,读方法写在 `queries` 上、`*Store` 与 `*Tx` 共用,多语句写入只在 `*Tx` 上;组卸载等写在 SQL 里的规则随方法留在这里。用自己的记录类型(ID 为 `int64`、时间为 `time.Time`),由 Library 转换成公开类型;返回自己的哨兵错误 `ErrNotFound` / `ErrAlreadyExists` / `ErrBusy` / `ErrUnacceptable`,由 `typedError` 翻译。
- `internal/library/internal/agentdir` — Agent 目录里的技能文件夹:`Change` 负责暂存(`Stage`)、登记删除(`Remove`)、移到位(`Commit`)、撤销(`Rollback`)与事务提交后清理备份(`Done`);另有存在性检查、读取已安装文件夹的指纹、规范化目录路径。
- `internal/library/internal/market` — skills.sh 的搜索与审计接口:发请求、限制响应大小、解析结果;返回自己的哨兵错误 `ErrUnreachable` / `ErrPermission` / `ErrInvalidResponse`。哪些结果能转成入库预览、审计查不到时算哪种状态,由 Library 决定。
- `internal/library/internal/s3` — 同步用的 S3 对象读写:从环境变量读配置、SigV4 签名、`Head` / `Get` / 条件 `Put`;返回自己的哨兵错误,由 `typedError` 翻译。
- `internal/library/*_test.go` — 按行为组织的技能库公开规格(`package library_test`);共享辅助函数在 `test_helpers_test.go`。
- `cmd/demo/main.go` — 可运行的入口示例,用内存数据库演示 Library 的导入、调用和关闭;运行 `go run ./cmd/demo`。

用户流程的编排留在 `package library`,公开方法与其私有辅助函数按用户流程放在同一文件;每个外部系统(SQLite、Agent 目录、各来源、skills.sh、S3)一个子包,放在 `internal/library/internal/` 下,只有 `library` 能导入。子包不知道公开错误码,只返回自己的哨兵错误或错误类型。新增一种来源时,在 `source` 下加子包,并在 `fetch.go` 的 `openSnapshot` / `validateSource` 里接上。

新增任何应用时:应用依赖 Library,Library 不依赖应用;默认共用根 module,入口放 `cmd/<app>/`。桌面端的位置见 [ADR 0008](docs/adr/0008-desktop-is-wails-v3-in-process.md)。

**注释:`library` 包的导出标识符(类型、函数、方法、常量)都写 doc comment;`schema.sql`,以及以类型别名公开的内部类型(如 `skillfile.File`)也写注释。其他非测试代码(未导出的实现、各子包)不写注释**(`//go:build`、`//go:embed` 等编译指令除外)。注释一律使用中文;标识符、技术术语保留英文。

## Agent skills

### Ecosystem background

涉及 Skill 格式、Agent 发现/调用、Agent 发现目录或同名行为时，先读 [Agent Skills 背景](docs/agents/agent-skills-background.md)；不熟悉生态时也从这里开始。本项目与生态的对应关系、尚未确认的差异见该文末节。

### Issue tracker

Issues live in this repo's GitHub Issues (via the `gh` CLI). See `docs/agents/issue-tracker.md`.

### Triage labels

The five canonical triage roles, each label string equal to its role name. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: one `CONTEXT.md` + `docs/adr/` at the repo root. See `docs/agents/domain.md`.
