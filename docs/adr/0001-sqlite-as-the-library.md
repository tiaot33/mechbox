# SQLite is the Library

The Library — the tool's single source of truth — is a single SQLite database. It holds both skill **contents** (as blobs) and all **metadata**: collections, groups, activation state, and registered projects. Agent directories are projections of it, not inputs.

We chose one opaque store over skill folders on disk because it keeps the source of truth in exactly one place, gives transactional integrity across an operation that touches many skills at once, and makes S3 Sync a clean "export the database, push it" step. Sync is non-realtime, so SQLite's coarse, whole-blob nature costs nothing: a periodic export from SQLite is the sync artifact.

## Consequences

- **Install** writes a fetched skill's files into SQLite as blobs.
- **Activation** expands a skill out of SQLite into the target agent directory; **deactivation** removes that directory.
- **Self-developed skills** are edited through the tool rather than by editing files in place — content lives in the database, so authoring needs an explicit export/edit/re-import path or an in-tool editor (to be specified).
- **Sync** exports the database and pushes it to S3 (and pulls to restore), reproducing a full setup on another machine.

## Agent Skills 生态背景与集成边界

2026-09-14 补充，依据 [Agent Skills 背景](../agents/agent-skills-background.md) 中的 2026-09-12 调研。以下说明既有 SQLite 与目录投影决策在该生态中的含义。

Agent 消费的是含 `SKILL.md` 的目录，并按“元数据发现 → 正文加载 → 按需读取资源”使用技能。数据库只供 Library 管理内容；Activation 将文件还原到 Agent 的发现目录，Agent 因而无需读取 SQLite。这个边界既保留了 Library 的事务、历史和 Sync 能力，也适配客户端原有的文件发现机制。

- **保存整个 Skill 的文件集合。** `scripts/`、`references/`、`assets/` 等资源属于技能的一部分。渐进披露约束的是 Agent 何时读取内容，不意味着 Library 只保存 `SKILL.md`；相对路径与可执行位随版本保存，见 [ADR 0005](0005-content-addressed-skill-history.md)。客户端扩展字段及配套配置作为文件内容保留，其调用语义由客户端解释。
- **区分入库、投影和运行时加载。** 生态文档的“安装”通常指把文件夹放到发现目录；本项目的 Install 指入 Library，Activation 指投影。规范所说的 Activation 是加载正文，不等于本项目的 Activation。投影成功或存在 Activation 记录，只能说明本工具已提供文件，不能证明 Agent 已发现、选用或执行它。
- **Scope 表示 Agent 与管理层级。** 项目当前管理个人全局和 Registered project 两类层级；“全局”指当前用户跨项目，不代表企业、ADMIN 或 SYSTEM。目录名称来自 Agent 的发现约定，具体路径是 Scope 的落地位置。客户端从 CWD 向上扫描、嵌套加载、附加目录及插件启用等规则由客户端负责；登记一个项目不等于已复现这些规则。
- **区分投影冲突与客户端同名选择。** `ConflictPolicy` 处理写入同一目标目录时的冲突；客户端跨目录的同名优先级是另一件事。Claude Code 在企业、个人、项目之间覆盖，但嵌套与插件可并存；Codex 文档允许同名技能并存，未给出完整优先级。Library 的投影记录不表示某个同名技能在会话中胜出，也不据此自动移除其他 Scope 的投影。

这延续了以目录投影作为集成方式的选择：Library 管理技能内容与投影状态，Agent 负责发现、选择和执行。完整路径与客户端规则集中在生态背景文档，避免把随客户端变化的行为固化为 Library 的通用规则。

### 当前实现差异

以下是文档与现有实现的对照，不代表已完成兼容性修复：

- **Codex 发现路径已改为现行 `.agents/skills`，旧目录不迁移。** Agent 布局由 Library 的 `agents` 表持有并以身份 Scope 解析（见 [ADR 0011](0011-scope-is-identity-not-path.md)）；Codex 种子为 `$HOME/.agents/skills` 与项目 `.agents/skills`。打开旧库时以 `.codex/skills` 记录的 Activation 会被认成对应身份，但此前投到 `.codex/skills` 里的文件夹不会被搬迁或删除，需用户重新 Activate 才落到现行目录。旧路径是否仍被目标 Codex 版本发现尚未确认。
- **格式说明不等于完整规范校验。** [source_fetch.go](../../library/source_fetch.go) 的候选解析检查 YAML 与安全名称，按 #59 要求 `description` 为必填字符串，未要求名称等于父目录名；本地 Install 的行为也不等同于严格 frontmatter 验证。背景文档记录的必填项、长度与命名约束是开放规范要求，不能据此宣称 Library 已完整执行这些校验。

本次补充记录生态背景与已有设计边界；路径迁移和校验策略的实现需另行处理。
