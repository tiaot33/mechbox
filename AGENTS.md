## Codebase

Go module。后端核心是 `library` 包 —— 未来 TUI 之下的完整进程内门面(见 ADR 0004)。SQLite 数据库是唯一权威数据源,磁盘上的 skills 目录只是它的投影。

- `library/api.go` — 全部领域类型 + `Library` 接口,带中文注释。**阅读入口,先读这个。**
- `library/schema.sql` — 当前 SQLite 表结构(字段有中文注释),由 `library.Open` 嵌入执行。
- `library/library.go` — 全部业务逻辑:安装、版本历史、远程抓取、激活投影、分组、项目注册。
- `library/sync.go` — 整库与 S3 的推拉同步(SigV4 签名、schema 校验、项目路径重映射)。
- `library/*_test.go` — 行为规格;测试名即行为描述,理解语义时对照着读。

**注释语言:代码注释一律使用中文**;标识符、技术术语保留英文。

## Agent skills

### Ecosystem background (read first if unfamiliar)

If you do not already know the **Agent Skills** ecosystem (`SKILL.md` folders, how agents load them, common on-disk paths), read **`docs/agents/agent-skills-background.md`**. That file is ecosystem facts only — not this product’s design.

### Issue tracker

Issues live in this repo's GitHub Issues (via the `gh` CLI). See `docs/agents/issue-tracker.md`.

### Triage labels

The five canonical triage roles, each label string equal to its role name. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: one `CONTEXT.md` + `docs/adr/` at the repo root. See `docs/agents/domain.md`.
