## Codebase

Go module。后端核心是 `library` 包 —— 未来 TUI 之下的完整进程内门面(见 ADR 0004)。SQLite 数据库是唯一权威数据源,磁盘上的 skills 目录只是它的投影。

- `library/api.go` — 全部领域类型 + `Library` 接口,带中文注释。**阅读入口,先读这个。**
- `library/library.go` — 实例生命周期:Open、Close、共享状态与操作锁。
- `library/schema.go` + `schema.sql` — 库身份、迁移与 schema 校验;建表定义统一来自嵌入的 `schema.sql`。
- `library/skills.go` + `versions.go` — 安装入库、技能查询/移除、Collection、版本历史与差异。
- `library/fetch.go` — Fetch 会话、候选选择与 InstallFetch;`source_input.go` / `source_fetch.go` / `github_contents.go` 负责来源解析与内容获取。
- `library/update.go` + `batch_update.go` — 单项与批量更新检查;`search.go` 负责市场搜索。
- `library/groups.go` + `projects.go` — Group 成员管理、项目注册与 Scope。
- `library/activation.go` — 独立及分组激活、投影记录、磁盘暂存与回滚。
- `library/sync.go` — 整库与 S3 的推拉同步(SigV4 签名、快照、远端身份与项目路径重映射)。
- `library/files.go` + `errors.go` — 共享文件操作与内部错误转换。
- `library/*_test.go` — 按行为组织的公开门面规格(`package library_test`);共享辅助函数在 `test_helpers_test.go`。

实现保持同一 `package library`;公开方法与其私有辅助函数按职责放在一起。

**注释语言:代码注释一律使用中文**;标识符、技术术语保留英文。

## Agent skills

### Ecosystem background

涉及 Skill 格式、Agent 发现/调用、Scope 路径或同名行为时，先读 [Agent Skills 背景](docs/agents/agent-skills-background.md)；不熟悉生态时也从这里开始。产品集成边界与实现差异见 [ADR 0001](docs/adr/0001-sqlite-as-the-library.md)。

### Issue tracker

Issues live in this repo's GitHub Issues (via the `gh` CLI). See `docs/agents/issue-tracker.md`.

### Triage labels

The five canonical triage roles, each label string equal to its role name. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: one `CONTEXT.md` + `docs/adr/` at the repo root. See `docs/agents/domain.md`.
