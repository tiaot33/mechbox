# Skills Manager

用于管理 Claude Code 和 Codex Agent Skills 的后端库。当前仓库已经实现 SQLite `Library` 及其行为，还没有可运行的 TUI，也没有 HTTP、RPC 或 OpenAPI 接口。

Skill 是以 `SKILL.md` 为入口、供 Agent 按需加载的完整文件夹。本项目的 Install 表示安装入 Library，Activation 表示投影到 Agent 的 Scope；Agent 的发现、加载与调用规则见 [生态背景](docs/agents/agent-skills-background.md)。Scope 是 Agent 的全局或 Agent＋已登记项目两种身份，目录由 Library 持有的 Agent 布局算出（Codex 现行为 `.agents/skills`），调用方不传路径，见 [ADR 0011](docs/adr/0011-scope-is-identity-not-path.md)；旧 `.codex/skills` 目录里的投影不自动迁移，详见 [ADR 0001 的实现差异](docs/adr/0001-sqlite-as-the-library.md#当前实现差异)。

## 当前能力

- 从本地目录、GitHub 仓库、ZIP URL 或技能商店安装和更新技能。
- 保存完整文件集合、相对路径、可执行权限、内容寻址版本历史和版本差异。
- 按 Source 自动生成 Collection，手动维护跨 Source 的 Group。
- 登记项目，并将单个技能或整个 Group 激活到 Claude Code、Codex 的全局或项目 Scope。
- 将整个 SQLite Library 推送到 S3，或从 S3 全量替换并按机器重映射项目路径。
- 通过稳定的 `ErrorCode` 向调用方返回可判断的错误。

## 仓库地图

| 位置 | 作用 |
| --- | --- |
| [`docs/agents/agent-skills-background.md`](docs/agents/agent-skills-background.md) | Agent Skills 格式、Claude Code/Codex 发现与调用规则、资料覆盖边界 |
| [`CONTEXT.md`](CONTEXT.md) | 产品术语及统一语言 |
| [`docs/adr/`](docs/adr/) | 已做出的架构和数据模型决策 |
| [`library/api.go`](library/api.go) | 进程内 Go 公共接口、DTO、选项和错误契约 |
| [`library/library.go`](library/library.go) | 实例生命周期、共享状态和操作锁 |
| [`library/schema.go`](library/schema.go)、[`schema.sql`](library/schema.sql) | 库身份、迁移、校验及唯一 DDL 来源 |
| [`library/skills.go`](library/skills.go)、[`versions.go`](library/versions.go) | 安装入库、查询/移除、Collection、版本历史与差异 |
| [`library/fetch.go`](library/fetch.go) | Fetch 会话、候选选择与 InstallFetch |
| [`library/source_input.go`](library/source_input.go)、[`source_fetch.go`](library/source_fetch.go)、[`github_contents.go`](library/github_contents.go) | 来源解析、校验与内容获取 |
| [`library/update.go`](library/update.go)、[`batch_update.go`](library/batch_update.go)、[`search.go`](library/search.go) | 更新、批量检查与市场搜索 |
| [`library/groups.go`](library/groups.go)、[`projects.go`](library/projects.go) | Group 成员、项目注册与 Scope |
| [`library/activation.go`](library/activation.go) | 独立及分组激活、投影记录、磁盘暂存与回滚 |
| [`library/sync.go`](library/sync.go) | 快照、全库替换、路径重映射和 S3 同步 |
| [`library/files.go`](library/files.go)、[`errors.go`](library/errors.go) | 共享文件操作与内部错误转换 |
| `library/*_test.go` | 按行为组织的公开门面测试;共享辅助函数在 `test_helpers_test.go` |

建议依次阅读 `CONTEXT.md`、相关 ADR、`library/api.go`、测试，最后再进入具体实现。

## 验证和查看接口

需要 Go 1.26、CGO 和本机 C 编译工具链。

```sh
go test -race ./...
go vet ./...
go doc ./library.Library
go test -list . ./library
```

最小调用方式：

```go
ctx := context.Background()

lib, err := library.Open("skills.db")
if err != nil {
	return err
}
defer lib.Close()

skill, err := lib.Install(ctx, library.LocalSource{Path: "/path/to/skill"})
```

`Library` 是供 TUI 直接调用的进程内 Go 接口。当前没有网络监听入口；若以后需要外部客户端，应按 [ADR-0004](docs/adr/0004-backend-library-facade-seam.md) 在它外层增加传输适配器。

## 数据库

[`library/schema.sql`](library/schema.sql) 通过 Go `embed` 直接用于新库初始化，因此代码与可查看的 DDL 不会形成两份事实来源。当前包含 11 张表：

- 来源和技能：`sources`、`skills`
- 版本和内容：`skill_versions`、`blobs`、`version_files`
- 分组：`groups`、`skill_groups`
- Agent 配置：`agents`（稳定 `code`、展示 `name`、两段布局规则）
- 项目和激活：`projects`、`activations`、`activation_groups`

新数据库由 `library.Open` 自动初始化并写入应用标识和 schema version 5。已有 v1–v4 Library 在一个事务内迁移到当前版本：v1 补上历史版本的 `git_tree_sha`，v3 补上 Agent 名称，所有旧版的 Activation 都改为只存 Scope 身份（Skill＋Agent 代码＋可选已登记项目）；提交前校验身份、表与索引指纹和外键。旧库里以绝对路径记录的 Activation，若路径等于该 Agent 现行布局（或本产品此前写过的 `.codex/skills`）在当前用户主目录或某已登记项目下算出的目录，就映射成对应身份，映射到同一身份的多行合并保留全部依据；无法识别的行（未知 Agent、任意目录、另一台机器的路径）被丢弃，磁盘上的文件夹一律不动。Open 只补缺失的 Agent 种子，不覆盖已有名称或布局。`ListAgents` 按库内 `agents` 表列出 Agent，返回可扩展的 `library.Agent{Code}` 结构体（不含布局规则与绝对目录）。`Scope` 是密封接口，实现只有 `GlobalScope{Agent}` 与 `ProjectScope{Agent, ProjectID}`，不含路径；只读的 `Resolve` 按 `agents` 表中该 Agent 的两段规则算出目录——全局相对当前用户主目录，项目相对 Registered project 根——不写库、不写磁盘，未知 Agent 或未登记项目返回 `NotFound`。`Activate` / `Deactivate` / `ActivateGroup` / `DeactivateGroup` 只接受 `Scope` 身份，目录由 `Resolve` 算出、记录按身份落库；`ListActivations` 每条给出 Scope 身份与按现行布局派生的 `Directory`，只供对照磁盘，不能再当作 Activate 的输入。没有列举「全部带路径的 Scope」的方法，可选目标是 `ListAgents` ×（全局 | `ListRegisteredProjects`）。厂商改布局后只认现行目录，旧目录里上次投下的文件夹不搬迁、不删除。Sync 拉库只重映射 Registered project 的本机路径，Activation 目录按本机 `$HOME` 与映射后的项目根重新派生。门面上没有增删改 Agent 的方法。迁移及共用 schema 校验集中在 `library/schema.go`。

## 配置

| 环境变量 | 用途 | 默认值 |
| --- | --- | --- |
| `SKILLS_STORE_URL` | 技能商店基础 URL | 无；使用 store Source 时必填 |
| `SKILLS_S3_BUCKET` | S3 bucket | 无；同步时必填 |
| `SKILLS_S3_KEY` | S3 object key | `library.db` |
| `SKILLS_S3_ENDPOINT` | S3 兼容服务地址 | AWS S3 地址 |
| `AWS_REGION` | S3 region | `us-east-1` |
| `AWS_ACCESS_KEY_ID` | S3 access key | 无；同步时必填 |
| `AWS_SECRET_ACCESS_KEY` | S3 secret key | 无；同步时必填 |
| `AWS_SESSION_TOKEN` | 临时凭证 token | 无 |
| `SKILLS_PROJECT_PATH_MAP` | Pull 时旧项目路径到本机路径的 JSON 对象；每个项目只按拉回库里的原路径匹配一次，不链式改写；目标路径仍被其他项目占用或映射成环时 pull 以 `InvalidArgument` 失败、本地库不变 | `{}` |

GitHub Source 通过本机 `git` 命令拉取；URL 和 store Source 返回 ZIP 文件。

## 尚未实现

- Bubble Tea TUI、命令行入口和可执行程序。
- HTTP、JSON-RPC、OpenAPI 等外部服务接口。
- Blob 垃圾回收、版本回滚（后端操作及 UI）、激活副本自动跟随更新。
