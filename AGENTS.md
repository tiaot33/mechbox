## Codebase

Go module。后端核心是 `internal/library` 中的 `library` 包 —— 完整的进程内技能库(Library,见 ADR 0008),供本项目的 Go 调用方使用。SQLite 数据库是唯一权威数据源,磁盘上各 Agent 目录里已安装的技能文件夹由它生成。

- `internal/library/api.go` — 全部领域类型 + `Library` 接口,带中文注释。**阅读入口,先读这个。**
- `internal/library/library.go` — 实例生命周期:Open、Close、共享状态与操作锁。
- `internal/library/schema.go` + `schema.sql` — 库身份与 schema 校验;建表定义统一来自嵌入的 `schema.sql`。
- `internal/library/skills.go` + `versions.go` — 入库、技能查询/移除、Collection、版本历史与差异。
- `internal/library/preview.go` — 入库预览(PreviewFetch)与入库(Fetch);`source_read.go` 的 `openSnapshot` 按来源类型打开快照,入库预览、入库、更新与批量检查都只经 `Snapshot`。GitHub 来源暂在本包:`github_contents.go` 是它的 `Snapshot` 实现(commit / tree / blob、克隆回退,读取记录为目录 tree SHA 与确认 commit),`github_transfer.go` / `github_memo.go` 负责下载与缓存,`source_input.go` 负责解析 GitHub 输入与 git 命令。
- `internal/library/update.go` + `batch_update.go` — 单项与批量更新检查;`search.go` 负责市场搜索。
- `internal/library/groups.go` + `projects.go` — Group 成员管理、项目注册与 Scope。
- `internal/library/install.go` — 直接安装与组安装、SkillScope 记录、磁盘暂存与回滚;SkillScope 巡检(可按 `SkillScopeFilter` 缩小范围)与重新安装共用的筛选。
- `internal/library/reinstall.go` — 重新安装:把已安装的技能文件夹带回当前版本,不改留着它的直接安装与组安装。
- `internal/library/sync.go` — 整库与 S3 的推拉同步(SigV4 签名、快照、远端身份与项目路径重映射)。
- `internal/library/files.go` + `errors.go` — 共享文件操作与内部错误转换。
- `internal/library/internal/skillfile` — 技能文件 `File` / `Mode`(`api.go` 以类型别名公开)、文件集合比较、内容指纹。
- `internal/library/internal/source` — 来源的统一接口 `Snapshot`(钉住的一份来源内容:候选、按名取文件、检查更新)。`Check` 返回变了 / 没变 / 未知三态,未知时由 `update.go` 的 `checkSnapshot` 取文件比较;各来源自己的读取状态 `State` 存在 `skill_versions.source_state`,Library 只存取不解析。子包共用的哨兵错误 `ErrUnreachable` / `ErrPermission` / `ErrInvalid` 也在这里。
- `internal/library/internal/source/local` — 本地目录来源。
- `internal/library/internal/source/download` — URL 来源(也用于 store):下载 zip 压缩包,按 HTTP validator 发条件请求;validator 作为 `State` 记在各 Skill 的版本上。
- `internal/library/internal/source/discovery` — 各来源共用的候选发现:目录扫描规则、插件清单、SKILL.md frontmatter 解析、读取上限。子包只返回自己的哨兵错误,由 `errors.go` 的 `typedError` 翻译成公开错误码。
- `internal/library/*_test.go` — 按行为组织的技能库公开规格(`package library_test`);共享辅助函数在 `test_helpers_test.go`。
- `cmd/demo/main.go` — 可运行的入口示例,用内存数据库演示 Library 的导入、调用和关闭;运行 `go run ./cmd/demo`。

实现保持同一 `package library`;公开方法与其私有辅助函数按职责放在一起。

新增任何应用时:应用依赖 Library,Library 不依赖应用;默认共用根 module,入口放 `cmd/<app>/`。桌面端的位置见 [ADR 0008](docs/adr/0008-desktop-is-wails-v3-in-process.md)。

**注释:只有 `internal/library/api.go`、`schema.sql`,以及 `api.go` 用类型别名公开的内部类型写注释,其他非测试代码不写注释**(`//go:build`、`//go:embed` 等编译指令除外)。注释一律使用中文;标识符、技术术语保留英文。

## Agent skills

### Ecosystem background

涉及 Skill 格式、Agent 发现/调用、Agent 发现目录或同名行为时，先读 [Agent Skills 背景](docs/agents/agent-skills-background.md)；不熟悉生态时也从这里开始。本项目与生态的对应关系、尚未确认的差异见该文末节。

### Issue tracker

Issues live in this repo's GitHub Issues (via the `gh` CLI). See `docs/agents/issue-tracker.md`.

### Triage labels

The five canonical triage roles, each label string equal to its role name. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: one `CONTEXT.md` + `docs/adr/` at the repo root. See `docs/agents/domain.md`.
