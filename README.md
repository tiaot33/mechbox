# Skills Manager

用于管理 Claude Code 和 Codex Agent Skills 的后端库。当前仓库已经实现 SQLite `Library` 及其行为，还没有可运行的 TUI，也没有 HTTP、RPC 或 OpenAPI 接口。

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
| [`CONTEXT.md`](CONTEXT.md) | 产品术语及统一语言 |
| [`docs/adr/`](docs/adr/) | 已做出的架构和数据模型决策 |
| [`library/api.go`](library/api.go) | 进程内 Go 公共接口、DTO、选项和错误契约 |
| [`library/schema.sql`](library/schema.sql) | SQLite v1 表结构的唯一 DDL 来源 |
| [`library/library.go`](library/library.go) | 安装、版本、组织、Scope 和激活实现 |
| [`library/sync.go`](library/sync.go) | SQLite 校验、全库替换和 S3 同步实现 |
| `library/*_test.go` | 从 `Library` 接口验证外部行为的集成测试 |

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

[`library/schema.sql`](library/schema.sql) 通过 Go `embed` 直接用于新库初始化，因此代码与可查看的 DDL 不会形成两份事实来源。当前包含 10 张表：

- 来源和技能：`sources`、`skills`
- 版本和内容：`skill_versions`、`blobs`、`version_files`
- 分组：`groups`、`skill_groups`
- 项目和激活：`projects`、`activations`、`activation_groups`

新数据库由 `library.Open` 自动初始化并写入应用标识和 schema version 1。已有数据库会校验身份、表结构指纹和外键；当前没有迁移机制，表结构变化时需要先实现对应迁移。

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
| `SKILLS_PROJECT_PATH_MAP` | Pull 时旧项目路径到本机路径的 JSON 对象 | `{}` |

GitHub Source 通过本机 `git` 命令拉取；URL 和 store Source 返回 ZIP 文件。

## 尚未实现

- Bubble Tea TUI、命令行入口和可执行程序。
- HTTP、JSON-RPC、OpenAPI 等外部服务接口。
- SQLite schema migration。
- Blob 垃圾回收、版本回滚（后端操作及 UI）、激活副本自动跟随更新。
