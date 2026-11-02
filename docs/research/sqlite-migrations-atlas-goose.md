# SQLite 数据库升级：Atlas 与 goose

**Date:** 2026-11-02  
**Question:** Atlas（重点 Community Edition）与 goose（`github.com/pressly/goose/v3`）能不能用来升级桌面 app 的 SQLite 数据库，具体怎么用。对照的设想方案：Atlas 只在开发机和 CI 上用，从 `schema.sql` 与上一版结构的差异生成 goose 格式（`file://migrations?format=goose`）的升级 SQL；goose 以 Go 库形式嵌进 app（`embed.FS`），`store.Open` 时 `goose.NewProvider(goose.DialectSQLite3, db, fsys)` + `Up`；只写升级不写降级。只支持从 v0.0.6（`user_version=27`）起升级。  
**Method:** 一手来源：Atlas 与 goose 的官方文档、release、源码（Atlas 用 tag `v1.3.0`，goose 用 tag `v3.29.0`，按行引用）、GitHub issue；SQLite 官方文档。实测在临时目录 `/tmp/mig-research` 里做（不在仓库里）：从源码构建 Atlas CE v1.3.0，同时下载官方 CE 与标准版 v1.3.0 二进制核对；用仓库当前的 `schema.sql` 跑 `migrate diff`；用 goose v3.29.0 + `mattn/go-sqlite3` v1.14.49 写小段 Go 程序，按 `store.openDB` 同样的连接串和 `SetMaxOpenConns(1)` 建 27 版数据库、写入数据后跑升级。版本号与许可以查证当日为准。  
**Outcome:** 采用了下面推荐的手写 SQL + 按 `user_version` 逐版升级；目录与文件命名照 goose（`migrations/NNNNN_<做了什么>.sql`），升级 SQL 由 `migrationgen` 按「整表重建」的规则生成。见 [ADR 0003](../adr/0003-database-upgrades-step-by-step.md)。

---

## 结论

### 对本仓库的推荐

**不采用 Atlas 生成、goose 执行的方案。升级步骤手写 SQL，由 `store` 包里一个按 `PRAGMA user_version` 逐版前进的小循环执行。不新增依赖。** 具体：

1. 版本号仍然只有 `PRAGMA user_version`。27 起每一版结构变更加一个嵌入的 SQL 文件（如 `migrations/028.sql`），从 N-1 升到 N。
2. 空数据库照旧直接执行最新的 `schema.sql`，再把 `user_version` 设成最新版本。旧数据库（`27 ≤ user_version < 最新`）逐版升级：
   - 先用现有的 `backup()`（SQLite online backup）备份一份（ADR 0003 的要求）。
   - 每一步：用 `db.Conn` 固定一条连接，在事务外 `PRAGMA foreign_keys = OFF`；`BeginTx`（连接串带 `_txlock=immediate`，即 `BEGIN IMMEDIATE`）；在事务内重新读 `user_version`，确认等于 N-1；执行整份脚本（`mattn/go-sqlite3` 一次 `Exec` 会执行多条语句，现有 `initialize` 执行 `schema.sql` 就是这样做的）；`PRAGMA foreign_key_check` 有输出就回滚；`PRAGMA user_version = N`；提交；最后 `PRAGMA foreign_keys = ON`。
3. 需要重建表的变更，照 [SQLite 官方的 12 步做法](https://www.sqlite.org/lang_altertable.html#otheralter)手写：
   - 新表的 `CREATE TABLE` 从 `schema.sql` **原样复制**（连注释），只把表名换成 `new_X`。
   - 有 `AUTOINCREMENT` 的表，复制数据之前先把 `sqlite_sequence` 的高水位抄给 `new_X`。
   - `DROP TABLE X`，`ALTER TABLE new_X RENAME TO X`，再按 `schema.sql` 原文重建 X 上的索引。
4. 保留 `validateSchema` 的原文指纹，只加一条归一化：`ALTER TABLE ... RENAME` 会把存下来的建表语句改成 `CREATE TABLE "X"`（带引号），指纹计算时把开头的 `CREATE TABLE "X"` 还原成 `CREATE TABLE X`。这样升级后的数据库与新建的数据库指纹相同，运行时仍能校验「结构 = `schema.sql`」，`schema.sql` 里「列上的注释随建表语句存进数据库、参与表结构校验」这条也继续成立。
5. CI 只需一个 Go 测试：用 v0.0.6 的 `schema.sql` 快照（放 testdata）建数据库，跑完全部升级，指纹必须等于当前 `schema.sql`。`schema.sql` 改了却没写升级、或升级写错，都会让这个测试失败。
6. 同步拉回：`OpenImage` 写出的临时文件先跑同一套升级，再 `validateSchema`。`CannotPullOlderApp` 只剩 `user_version < 27` 的情况。版本比 app 新的仍报 `ErrNewerSchema` / `CannotPullNewerApp`，逻辑不变。

上面这套已整体实测：建 27 版数据库并写入数据，按 2 跑一步「`skills` 加列 + 改 CHECK」的原文重建。升级后指纹（归一化后）等于新 `schema.sql` 的指纹；`foreign_key_check` 无违例，`sqlite_sequence` 高水位保留，连接上的 `foreign_keys` 恢复为 1。故意让脚本中途出错时，整步回滚：`user_version` 仍为 27，表和数据原样。

### 会推翻或修改设想方案的发现

1. **Atlas 在本仓库的 `schema.sql` 上每次 diff 都会多出错误语句。** 表级或列级 `UNIQUE` 约束（`sources`、`skills`、`groups`、`projects`、`rolled_back_renames` 五张表）在比较时被当成「先删再建」的唯一索引，生成 `DROP INDEX \`sources_type_locator\`` 等语句。结构完全没改时也会生成，执行时报 `no such index`。生成文件一旦留在目录里，下一次 `migrate diff` 回放目录时也会失败。这个问题对应 [ariga/atlas#1597](https://github.com/ariga/atlas/issues/1597)，2023 年开到现在仍未关闭；标准版 v1.3.0 实测同样有。所以每个生成文件都要手改，也不能拿「`migrate diff` 不产出文件」当 CI 检查。（实测，见[多余的 DROP/CREATE INDEX](#unique-约束导致多余的-dropcreate-index)）
2. **Atlas 的重建表语句和 goose 的事务冲突。** Atlas 把 `PRAGMA foreign_keys = off/on` 写进迁移文件。goose 默认把每个文件包在事务里，而 SQLite 规定这条 pragma 在事务内不起作用，Atlas 源码注释也承认这一点。结果 `DROP TABLE skills` 时外键仍然生效，报 `FOREIGN KEY constraint failed`。加 `-- +goose NO TRANSACTION` 能跑通，但中途出错时数据库会停在半截：`skills` 表已删、`new_skills` 还在。连接上的 `foreign_keys` 也停在 0，而我们只有一条连接。Atlas 不会输出 `NO TRANSACTION`。（实测）
3. **Atlas 重建表会丢掉 `AUTOINCREMENT` 的高水位。** 重建后 `sqlite_sequence` 回到当前最大 id，删掉的技能的 id 会被新技能重新用上。这违反 `schema.sql` 对 `skills.id`、`projects.id` 等「移除后也不复用」的承诺。要手动补 `sqlite_sequence`；补了以后 `migrate lint` 又会报 DS102（删表），还得加 `-- atlas:nolint DS102`。（实测）
4. **Atlas 生成的建表语句和 `schema.sql` 原文不一致。** 生成的语句用反引号、没有注释、外键命名为 `CONSTRAINT \`0\``，RENAME 后表名还带双引号。第一次重建表之后，现有 `validateSchema` 的指纹就对不上了。（实测）
5. **goose 发现数据库版本比 app 新时不报错。** 数据库已到 29、app 只认识 27 和 28 时，`HasPending` 返回 false，`Up` 返回 `nil, nil`。要自己比较 `GetVersions` 返回的 `current > target`，才能给出「需要更新的 app」。（实测、源码）
6. **goose 默认的版本表 `goose_db_version` 会进到用户数据库。** 它会被 `validateSchema` 的指纹查询和同步推送的 `contentDigest` 一起算进去，表里还有每台机器各不相同的 `tstamp`。不想要这张表，可以用 `DialectCustom` + `WithStore` 写一个基于 `user_version` 的 Store，实测可行。（实测、源码）
7. **goose 在 SQLite 上没有锁，判断待执行版本也在事务外。** 另外，不带事务的 Go 迁移在 `SetMaxOpenConns(1)` 下会死锁：goose 占着唯一的连接，迁移函数又拿 `*sql.DB` 去要连接。（实测、源码）
8. **Atlas 不能用 `go install` 或 `go tool` 装。** `cmd/atlas` 的 `go.mod` 有 `replace` 指令；`@latest` 只解析到 2023 年的 v0.13.1。CI 里用官方安装脚本 `--community`，或按版本直接下载并校验 sha256，或从源码 clone 后 `go build`。（实测）

### 需要拍板的点

1. 推荐的做法：不用 Atlas 和 goose，手写升级 SQL，由 `store` 里按 `user_version` 逐版前进的循环执行。这样行不行？
2. 改表结构一律按 `schema.sql` 原文重建表，不用 `ALTER TABLE ADD COLUMN`。这样数据库里的建表语句（含注释）始终与 `schema.sql` 一致，原文指纹校验可以保留。代价是改哪张表就整表复制一次，例如以后改 `blobs` 要复制全部文件内容。`ADD COLUMN` 会把新列插进原文中间（实测见[`validateSchema` 的原文指纹](#validateschema-的原文指纹)），和自然书写的 `schema.sql` 对不上。这样行不行？
3. 升级前的备份放在数据库旁边，每个旧版本留一份（例如 `library.db` 旁边的 `library-v27.db`），不自动删除。文件名只是建议。这样行不行？
4. 同步拉回时，版本较旧（≥27）的备份在本机临时文件上自动升级后再拉回，不再报 `CannotPullOlderApp`。这样行不行？
5. v0.0.6 的 `schema.sql` 快照放进测试数据，作为 CI 测试「从 27 升到最新 = 当前 `schema.sql`」的起点。这样行不行？

如果仍要用 Atlas + goose，必须做的改动见[如果仍用 Atlas + goose](#如果仍用-atlas--goose)。

---

## Atlas

### 版本、许可与安装（查证于 2026-11-02）

- 最新 release 是 [v1.3.0](https://github.com/ariga/atlas/releases/tag/v1.3.0)（2026-08-02）。
- 仓库许可证是 [Apache-2.0](https://github.com/ariga/atlas/blob/v1.3.0/LICENSE)。官方 [Community Edition 页面](https://atlasgo.io/community-edition)写明 CE 是「a minimal build distributed under the Apache 2.0 license」，「built directly from the Atlas GitHub repository」。
- 实测 `atlas license`：CE 二进制输出 Apache 2.0；标准版二进制输出「licensed under the Ariga End User License Agreement」（[EULA](https://ariga.io/legal/atlas/eula)）。

安装方式：

| 方式 | 结果 |
| --- | --- |
| `curl -sSf https://atlasgo.sh \| sh -s -- --community` | 官方文档给出的方式。安装脚本（2026-11-02 下载，脚本自报 `C_VERSION="0.5.0"`）支持环境变量 `ATLAS_VERSION` 固定版本，以及 `-o PATH`、`-y`、`--no-install`。脚本注释写明「Community builds are only published to the primary server」，即只有 `https://atlasbinaries.com/atlas` |
| 直接下载 | 实测 `https://atlasbinaries.com/atlas/atlas-community-darwin-arm64-v1.3.0` 与同名 `.sha256` 都可下载，sha256 一致（`4e5ffdc1…3db991`），`atlas version` 输出 `atlas community version v1.3.0`。`atlas-community-linux-amd64-v1.3.0` 返回 HTTP 200 |
| Docker | 官方页面：`arigaio/atlas:latest-community` |
| `go install ariga.io/atlas/cmd/atlas@latest` | 实测解析到 `v0.13.1`。`cmd/atlas` 子模块的 tag 停在 `cmd/atlas/v0.13.1`，装到的是 2023 年的旧版 |
| `go install ...@v1.3.0` | 实测报错：`module ariga.io/atlas@v1.3.0 found, but does not contain package ariga.io/atlas/cmd/atlas` |
| `go install ...@master` | 实测报错：`The go.mod file for the module providing named packages contains one or more replace directives`。原因是 [`cmd/atlas/go.mod` 第 5 行](https://github.com/ariga/atlas/blob/v1.3.0/cmd/atlas/go.mod#L5) `replace ariga.io/atlas => ../..` |
| `go get -tool ariga.io/atlas/cmd/atlas@master` | 实测解析到伪版本 `v0.13.2-0.20241117…`，编译失败（`r.Objects undefined`） |
| 源码构建 | 实测 `git clone --branch v1.3.0` 后在 `cmd/atlas` 下 `go build` 成功。需要 cgo（`mattn/go-sqlite3`），首次要下载大量依赖（gocloud、aws-sdk 等）。产物版本显示 `atlas community version - development`，正式版本号要靠 ldflags 注入（[`cmdapi.go` 第 62 行注释](https://github.com/ariga/atlas/blob/v1.3.0/cmd/atlas/internal/cmdapi/cmdapi.go#L62)） |

维护者在 [#3143](https://github.com/ariga/atlas/issues/3143) 里写过（2024-09-23）：「We deprecated the Go installation more than a year a Go」（原文如此）。

CI 推荐：按固定版本直接下载 CE 二进制并校验 `.sha256`。官方页面把「GitHub Actions」列为 CE 不含的集成，所以不能用官方 Action。

### Community Edition 能用哪些命令

官方页面列出 CE 有 `migrate diff`、`apply`、`status`；没有 `checkpoint`、`down`、`rebase`、`edit`、`rm`、`migrate lint`、`schema lint`、`schema plan`、`migrate test`、`schema test`、views/triggers 等对象，也没有 GitHub Actions、Go SDK 等集成。

实测 v1.3.0 的官方 CE 二进制与源码构建结果一致：

| 命令 | CE v1.3.0 实测 |
| --- | --- |
| `migrate diff` | 可用 |
| `migrate hash`（维护 `atlas.sum`） | 可用 |
| `migrate validate`（校验 `atlas.sum` 并在 dev 数据库上回放全部文件） | 可用。文件被改过时报 `checksum mismatch`，退出码 1；回放失败退出码 1 |
| `migrate lint` | **有这个子命令且能运行**：能报出 DS102（删表），能报出执行失败，有诊断时退出码 1。这与官方页面「lint 不在 CE」不一致，以后的版本会不会去掉未确认 |
| `schema inspect` / `schema diff` | 可用 |
| `migrate apply --baseline` | 有这个参数（[`migrate.go` 第 152 行](https://github.com/ariga/atlas/blob/v1.3.0/cmd/atlas/internal/cmdapi/migrate.go#L152)），但它属于 Atlas 自己执行迁移的流程，goose 执行时用不上 |

每个命令都会先打印一段「This Atlas edition lacks support for …」提示（实测）。

### 用 schema.sql 生成 goose 格式

用法依据：[Atlas 文档 · migrate diff](https://atlasgo.io/versioned/diff)（`--to "file://schema.sql"`，文件名默认 `{{ now }}_{{ name }}.sql`，格式写在目录 URL 的 `format` 参数里）、[dev database](https://atlasgo.io/concepts/dev-database)（SQLite 用 `sqlite://dev?mode=memory`）、[SQL schema](https://atlasgo.io/atlas-schema/sql)（「loads the raw SQL schema by executing the statements defined in the files one by one」）。

`atlas.hcl`（实测可用）：

```hcl
env "local" {
  src = "file://internal/library/internal/store/schema.sql"
  dev = "sqlite://dev?mode=memory"
  migration {
    dir = "file://internal/library/internal/store/migrations?format=goose"
  }
}
```

命令（与上面等价，实测）：

```sh
atlas migrate diff <name> \
  --to file://schema.sql \
  --dev-url "sqlite://dev?mode=memory" \
  --dir "file://migrations?format=goose"
atlas migrate hash --dir "file://migrations?format=goose"      # 手改文件后重算 atlas.sum
atlas migrate validate --env local                              # CI：校验 atlas.sum + 回放
```

goose 格式的模板写死在 [`sql/sqltool/tool.go` 第 34–40 行](https://github.com/ariga/atlas/blob/v1.3.0/sql/sqltool/tool.go#L34-L40)：

- 文件名是 `{{ now }}_{{ name }}.sql`，即 14 位时间戳。要用 `00028_xxx.sql` 这样的版本号，只能生成后手动改名再 `migrate hash`。
- 内容固定是 `-- +goose Up`、各语句、空行、`-- +goose Down`、逆序的 reverse 语句。模板里**没有** `-- +goose NO TRANSACTION`，也没有选项能去掉 Down。

### 对本仓库 schema.sql 语法的支持

逐项对照仓库 `schema.sql` 实际用到的写法（实测 `schema inspect` 与 `migrate diff` 输出）：

| 写法 | 本仓库 | Atlas 的处理 |
| --- | --- | --- |
| `CREATE TABLE IF NOT EXISTS` | 全部表和索引 | 在 dev 数据库上正常执行；生成的语句不带 `IF NOT EXISTS` |
| 行内 `--` 注释（中文） | 几乎每列 | 能解析；生成的语句不带注释 |
| `PRAGMA` | `schema.sql` 里没有（`application_id`、`user_version` 在 Go 里设置） | 不比较 |
| `CHECK` | 多处 | 支持；新增或修改 CHECK 一律重建表（[`alterable`](https://github.com/ariga/atlas/blob/v1.3.0/sql/sqlite/migrate.go#L541-L568) 只允许加索引、删索引、改名和简单加列） |
| 外键 `REFERENCES`（含 `skills` ↔ `skill_versions` 互相引用） | 多处 | 支持；生成时命名为 `CONSTRAINT \`0\``、`\`1\``…，显式写出 `ON UPDATE NO ACTION ON DELETE NO ACTION` |
| `id INTEGER PRIMARY KEY` | 多处 | 改写为 `` `id` integer NULL, … PRIMARY KEY (`id`) ``；实测仍是 rowid 别名（插入 NULL 会自动分配） |
| `AUTOINCREMENT` | `skills`、`skill_versions`、`projects`、`rolled_back_renames` | 保留关键字；重建表时**不保留** `sqlite_sequence` 高水位，见下文 |
| 列级、表级 `UNIQUE` | 5 张表 | 改写为单独的 `CREATE UNIQUE INDEX <表>_<列>`；diff 时产生多余语句，见下一节 |
| 部分索引 `WHERE` | 5 个 | 支持 |
| 表达式索引 `ifnull(project_id, 0)` | `history_installs_place` | 支持（生成 `(ifnull(project_id, 0))`） |
| 复合主键 | 多处 | 支持 |
| `WITHOUT ROWID` / `STRICT` | 未用 | 支持（实测 inspect 输出保留；[`sqlspec.go`](https://github.com/ariga/atlas/blob/v1.3.0/sql/sqlite/sqlspec.go#L96-L105)） |
| view / trigger | 未用 | CE 不支持（官方页面） |

Atlas 回放 goose 文件时用的是自己的解析器（[`GooseFile.StmtDecls`，`tool.go` 第 173 行起](https://github.com/ariga/atlas/blob/v1.3.0/sql/sqltool/tool.go#L173-L235)），和 goose 自己的解析器不同：

- Atlas 只要某行以 `;` 结尾（不以 `--` 开头），就当作语句结束，**哪怕这个 `;` 在行内注释里**。goose 的 [`endsWithSemicolon`](https://github.com/pressly/goose/blob/v3.29.0/internal/sqlparser/parser.go#L375-L394) 遇到以 `--` 开头的词就停止，不会这样判断。
- [第 481 行](https://github.com/ariga/atlas/blob/v1.3.0/sql/sqltool/tool.go#L481)的正则 `-- \+goose Up|Down|StatementBegin|StatementEnd` 有优先级错误：任何含 `Down`、`StatementBegin`、`StatementEnd` 的行都会被丢掉（[#3792](https://github.com/ariga/atlas/issues/3792)，open）。

当前 `schema.sql` 两种情况都没有（实测 grep），所以原文作为 goose 文件时 Atlas 能正确回放。

### UNIQUE 约束导致多余的 DROP/CREATE INDEX

最小复现（实测，CE 与标准版 v1.3.0 结果相同）：

```sh
$ printf 'CREATE TABLE t (id INTEGER PRIMARY KEY, a TEXT NOT NULL UNIQUE);\n' > u.sql
$ atlas schema diff --from file://u.sql --to file://u.sql --dev-url "sqlite://dev?mode=memory"
DROP INDEX `t_a`;
CREATE UNIQUE INDEX `t_a` ON `t` (`a`);
```

两边是同一个文件，结果却不为空。改写成 `CREATE UNIQUE INDEX t_a ON t(a)` 时输出「Schemas are synced」。真实数据库里这个索引叫 `sqlite_autoindex_t_1`，所以 `DROP INDEX t_a` 报 `no such index`；即使写对名字，SQLite 也不允许删除（`index associated with UNIQUE or PRIMARY KEY constraint cannot be dropped`，实测）。

原因（源码）：

- 规划时 [`normalizeIdxName`](https://github.com/ariga/atlas/blob/v1.3.0/sql/sqlite/migrate.go#L311-L333) 把 desired 一侧的 `sqlite_autoindex_*` 改名成 `<表>_<列>`（注释：「SQLite does not allow creating indexes with such names manually」）。
- 比较时 [`IsGeneratedIndexName` / `FindGeneratedIndex`](https://github.com/ariga/atlas/blob/v1.3.0/sql/sqlite/diff.go#L129-L150) 对两侧的处理不对称。
- 同一问题见 [#1597「SQLite: index dropped and created always」](https://github.com/ariga/atlas/issues/1597)，2023-04-20 提出，至今 open。

对本仓库的影响（实测）：

- 以 `schema.sql` 原文作 baseline，`schema.sql` 不做任何改动，`migrate diff` 也会生成一份文件，里面是 `sources_type_locator`、`skills_source_id_name`、`groups_name`、`projects_path`、`rolled_back_renames_skill_id` 五对 DROP/CREATE。
- 生成文件不手改就留在目录里，下一次 `migrate diff` 报 `executing statement "DROP INDEX \`sources_type_locator\`;" … no such index`，`migrate validate` 与 `migrate lint` 也失败。
- 改用 Atlas 生成的 baseline（`UNIQUE` 已改写成命名索引）时，`migrate diff` 能得到「synced」。但新建数据库的结构就和已经发出去的 27 版数据库（自动索引）不同了，以后的迁移在两种数据库上的行为可能不一样。不可取。
- 某张表被 Atlas 重建一次后，它的唯一约束变成命名索引，这张表就不再出现多余语句；其余表照旧。

### 重建表生成的语句

`skills` 改 CHECK（加 `'renamed'`）时 Atlas 生成的 Up（实测，已删去上一节的多余索引语句，长语句截断）：

```sql
-- +goose Up
PRAGMA foreign_keys = off;
CREATE TABLE `new_skills` (`id` integer NULL PRIMARY KEY AUTOINCREMENT, … `pinned` integer NOT NULL DEFAULT 0,
  CONSTRAINT `0` FOREIGN KEY (`current_version_id`) REFERENCES `skill_versions` (`id`) ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT `1` FOREIGN KEY (`source_id`) REFERENCES `sources` (`id`) ON UPDATE NO ACTION ON DELETE NO ACTION,
  CHECK (update_check_status IN ('', 'available', 'current', 'deleted', 'failed', 'renamed')));
INSERT INTO `new_skills` (`id`, …) SELECT `id`, … FROM `skills`;
DROP TABLE `skills`;
ALTER TABLE `new_skills` RENAME TO `skills`;
CREATE UNIQUE INDEX `skills_source_id_name` ON `skills` (`source_id`, `name`);
PRAGMA foreign_keys = on;

-- +goose Down
```

逐项看：

- **顺序**是「建 `new_X` → 复制 → 删 X → 改名」（[`modifyTable`](https://github.com/ariga/atlas/blob/v1.3.0/sql/sqlite/migrate.go#L192-L226)），符合 SQLite 文档推荐的安全顺序。SQLite 文档特别警告不要先把旧表改名，那样会破坏触发器、视图和外键里对这张表的引用（[ALTER TABLE 第 8 节 Caution](https://www.sqlite.org/lang_altertable.html#otheralter)）。
- **`PRAGMA foreign_keys`**：只要计划里有重建，就在首尾加 off/on（[第 49–56 行](https://github.com/ariga/atlas/blob/v1.3.0/sql/sqlite/migrate.go#L49-L56)），源码注释写着「Callers should note that these 2 pragmas are no-op in transactions」。SQLite 文档：「This pragma is a no-op within a transaction」（[PRAGMA foreign_keys](https://www.sqlite.org/pragma.html#pragma_foreign_keys)）。
- **引用这张表的其他表**：`skill_versions`、`skill_scopes` 等表的建表语句不改，`REFERENCES skills(id)` 保持原文（实测）。外键关闭时先删旧表再把 `new_skills` 改名，引用自然落到新表上。
- **这张表上的索引**：先建表、不带索引，改名后按 desired 状态重建（第 225 行）。
- **改名后的原文**：实测 `sqlite_master.sql` 变成 `CREATE TABLE "skills" (\`id\` integer NULL PRIMARY KEY AUTOINCREMENT, …`：表名由 SQLite 加上双引号，列定义是 Atlas 的单行写法，没有注释。
- **`AUTOINCREMENT`**：只有在 schema 里显式给了起始值时才写 `sqlite_sequence`（[`tableSeq`](https://github.com/ariga/atlas/blob/v1.3.0/sql/sqlite/migrate.go#L507-L535)），复制已有数据时不保留高水位。实测：删掉 id 3 后 `sqlite_sequence` 是 3，重建后变成 2，下一个新技能拿到 id 3。SQLite 文档说 `AUTOINCREMENT` 的目的就是「prevent the reuse of ROWIDs from previously deleted rows」（[autoinc](https://www.sqlite.org/autoinc.html)）。
- **加列**：不带约束的简单加列生成 `ALTER TABLE \`skills\` ADD COLUMN \`pinned\` integer NOT NULL DEFAULT 0;`。带 `CHECK` 的列（如 `hidden INTEGER NOT NULL DEFAULT 0 CHECK(hidden IN (0, 1))`）会重建整张表，尽管 SQLite 3.37 起 `ADD COLUMN` 已能带 CHECK（[ALTER TABLE 第 4 节](https://www.sqlite.org/lang_altertable.html#altertabaddcol)）。带 `REFERENCES` 的新列也重建（实测、`alterable` 源码）。

### goose 格式下的 Down 与事务

- **Down 总是生成。** 重建表的 Down 只有 `DROP TABLE \`new_X\``，Up 跑完后这张表已经不存在，执行会失败（[#3818](https://github.com/ariga/atlas/issues/3818)，2026-10-14 提出，open；评论里有社区修复提交，尚未合并）。我们只升级、不调用 Down，留着不会被执行，但内容是错的，应当删掉。goose 的 README 写明 Down 段可选（[README 第 298–300 行](https://github.com/pressly/goose/blob/v3.29.0/README.md#L298-L300)）。
- **事务。** Atlas 从不输出 `-- +goose NO TRANSACTION`，goose 默认把每个文件包在事务里，两者一起导致上面说的 pragma 不起作用（实测见[goose · 事务语义](#每个迁移的事务语义)）。

### CI 检查与 baseline

- **`atlas.sum` 与回放**：`atlas migrate validate` 能抓到「文件被改了没重算 hash」和「目录在空数据库上回放失败」（实测，退出码 1）。
- **「`schema.sql` 改了但没生成迁移」**：Atlas 文档的思路是 `migrate diff` 不产出新文件即一致。因为 #1597，本仓库的结构**无论如何都会产出文件**，这个检查用不了。也试过比较两边 `schema inspect --format '{{ sql . }}'` 的输出：表的顺序（重建过的表排到最后）和外键编号顺序都会不同，不能直接拿文本比较（实测）。可行的是在仓库里写 Go 测试比较（见[结合本仓库](#结合本仓库)）。
- **「空数据库跑完全部迁移 = `schema.sql`」**：同上，用 Go 测试比较。
- **baseline**：Atlas 用迁移目录里的第一份文件当起点，回放后再和 `--to` 比较。
  - 本仓库的 baseline 是 `00027_baseline.sql` = `-- +goose Up` + v0.0.6 的 `schema.sql` 原文。实测 Atlas 能回放，goose 在空数据库上执行它得到的指纹与直接执行 `schema.sql` 相同（都是 `20fef5151503`）。
  - 因为全是 `IF NOT EXISTS`，在已有的 27 版数据库上执行是空操作，goose 会把 27 记成已执行。实测数据完好。
  - 但它对 24 版数据库也会「成功」（只补缺的表，不改列），所以执行之前必须先检查 `user_version == 27`。

### Atlas 相关 issue（查证于 2026-11-02）

| issue | 状态 | 与本仓库的关系 |
| --- | --- | --- |
| [#1597](https://github.com/ariga/atlas/issues/1597) SQLite: index dropped and created always | open（2023-04-20） | UNIQUE 约束每次产生多余的 DROP/CREATE INDEX，直接影响 |
| [#3818](https://github.com/ariga/atlas/issues/3818) goose Down for a SQLite table rebuild only drops the temporary table | open（2026-10-14） | Down 是错的；只升级时影响小 |
| [#3792](https://github.com/ariga/atlas/issues/3792) goose format: statement line containing "Down" is treated as the Down boundary | open（2026-09-03） | 迁移里有含 `Down` 的行（包括注释）时，Atlas 回放与 goose 实际执行不一致 |
| [#3231](https://github.com/ariga/atlas/issues/3231) Support sqlite alter table drop column | open | 删列一律重建表 |
| [#2700](https://github.com/ariga/atlas/issues/2700) SQLite Triggers aren't re-created after column change | open | 本仓库没有 trigger |

---

## goose

### 版本、许可、依赖（查证于 2026-11-02）

- 最新 release 是 [v3.29.0](https://github.com/pressly/goose/releases/tag/v3.29.0)（2026-10-11），[MIT 许可](https://github.com/pressly/goose/blob/v3.29.0/LICENSE)。
- 自 v3.28.0 起「Minimum Go version is now 1.26」（[CHANGELOG](https://github.com/pressly/goose/blob/v3.29.0/CHANGELOG.md)），本仓库是 `go 1.26.0`，满足。
- [`go.mod`](https://github.com/pressly/goose/blob/v3.29.0/go.mod) 列了 ClickHouse、MySQL、pgx、MSSQL、`modernc.org/sqlite`、YDB 等很多驱动，但都是给 CLI 和测试用的。实测只 import 根包 `github.com/pressly/goose/v3` 时：
  - `go list -deps` 里非标准库的只有 goose 自己的包和 `github.com/mfridman/interpolate`、`go.uber.org/multierr`、`github.com/sethvargo/go-retry`、`golang.org/x/sync`。**不会链接其他数据库驱动。**
  - `go.mod` 多 4 个 indirect 依赖；`go.sum` 会出现 `modernc.org/sqlite` 等条目，因为 goose 的测试要用（`go mod why` 显示路径经过 `github.com/pressly/goose/v3.test`），但它们不进二进制。
  - 最小程序 `-ldflags="-s -w"` 对比：只用 sqlite3 时 3.56 MB，加上 goose `NewProvider` + `Up` 后 5.31 MB。桌面端本来就链接了大部分标准库，实际增量会更小，未在桌面端实测。

### Provider API

签名与行为都取自 v3.29.0 源码：

- [`NewProvider(dialect Dialect, db *sql.DB, fsys fs.FS, opts ...ProviderOption)`](https://github.com/pressly/goose/blob/v3.29.0/provider.go#L58)
  - `fsys` 传 `embed.FS` 或 `fs.Sub` 都可以（注释原文）。
  - 文件系统里和注册的 Go 迁移加起来一个都没有时，返回 `ErrNoMigrations`（第 148 行）。
  - SQL 文件到执行时才解析，执行前会先把这一批全部解析一遍（[provider_run.go 第 124–131 行](https://github.com/pressly/goose/blob/v3.29.0/provider_run.go#L124-L131)）。
- 选项（[provider_options.go](https://github.com/pressly/goose/blob/v3.29.0/provider_options.go)）：

| 选项 | 行为 |
| --- | --- |
| `WithStore(database.Store)` | 自定义版本存储；必须配 `DialectCustom`，不能和 `WithTableName` 同用（第 24–46 行；`NewProvider` 第 79–89 行检查） |
| `WithTableName(name)` | 改版本表名，默认 `goose_db_version`（第 48–60 行） |
| `WithSessionLocker` / `WithLocker` | 开启锁；不调用就不加锁（第 71–118 行）。`lock` 包只有 [`NewPostgresSessionLocker`、`NewPostgresTableLocker`、`NewMySQLTableLocker`](https://github.com/pressly/goose/tree/v3.29.0/lock)，**没有 SQLite 的** |
| `WithGoMigrations(...*Migration)` | 注册 Go 迁移，用 `NewGoMigration(version, up, down *GoFunc)` 构造；`GoFunc` 有 `RunTx`（带事务）和 `RunDB`（不带事务）两种 |
| `WithDisableGlobalRegistry(true)` | 不合并全局 `goose.AddMigration` 注册的迁移 |
| `WithAllowOutofOrder(true)` | 允许补跑版本号低于数据库内最大版本、但还没执行过的迁移；默认遇到这种情况报错（第 177–192 行） |
| `WithDisableVersioning(true)` | 不记录版本，每次跑全部；`GetDBVersion` 返回错误（第 194–202 行） |
| `WithLogger` / `WithSlog` / `WithVerbose` | 日志 |
| `WithExcludeNames` / `WithExcludeVersions` / `WithIsolateDDL` | 排除部分迁移；DDL 与 DML 分开执行（给 Aurora DSQL 用） |

- 方法（[provider.go](https://github.com/pressly/goose/blob/v3.29.0/provider.go#L161-L345)）：
  - `Up` 先 `HasPending`，没有待执行的就返回 `nil, nil`；否则跑完全部待执行迁移。`UpTo(version)` 跑到指定版本为止（含）。`UpByOne` 只跑一个。
  - `GetDBVersion` 返回数据库内最大版本；`GetVersions` 返回 `(current, target)`，`target` 是已知迁移中最大的版本号。
  - `Status` 列出每个已知迁移是否已执行。
  - **`HasPending` 与 `GetVersions` 也会建版本表**：它们调用 `initialize` → `ensureVersionTable`（[provider_run.go 第 277–336 行](https://github.com/pressly/goose/blob/v3.29.0/provider_run.go#L277-L336)）。
- 出错时返回 [`*PartialError{Applied, Failed, Err}`](https://github.com/pressly/goose/blob/v3.29.0/provider_errors.go#L29-L37)。

### 版本表与自定义 Store

- SQLite 方言的版本表（[`internal/dialects/sqlite3.go` 第 18–26 行](https://github.com/pressly/goose/blob/v3.29.0/internal/dialects/sqlite3.go#L18-L26)）：

  ```sql
  CREATE TABLE goose_db_version (
      id INTEGER PRIMARY KEY AUTOINCREMENT,
      version_id INTEGER NOT NULL,
      is_applied INTEGER NOT NULL,
      tstamp TIMESTAMP DEFAULT (datetime('now'))
  )
  ```

  建表时插入版本 0（[provider_run.go 第 397–401 行](https://github.com/pressly/goose/blob/v3.29.0/provider_run.go#L397-L401)）。当前版本取 `SELECT MAX(version_id)`。
- 表名用 `WithTableName` 改。
- 也可以完全不要这张表：实现 [`database.Store`](https://github.com/pressly/goose/blob/v3.29.0/database/store.go#L26-L47)（`Tablename`、`CreateVersionTable`、`Insert`、`Delete`、`GetMigration`、`GetLatestVersion`、`ListMigrations`），用 `NewProvider(goose.DialectCustom, db, fsys, goose.WithStore(s))`。
  - 实测写了一个基于 `PRAGMA user_version` 的 Store：`Insert` 执行 `PRAGMA user_version = N`，`ListMigrations` 返回已知版本里所有 ≤ `user_version` 的版本。
  - 已有 27 版数据库（没有 goose 表）直接跑 28、29，结束后 `user_version = 29`，没有建 `goose_db_version`。
  - 迁移失败时 `user_version` 一起回滚：`Insert` 和迁移在同一个事务里，`PRAGMA user_version` 写的是数据库头，属于事务的一部分（实测：`BEGIN; PRAGMA user_version = 99; ROLLBACK;` 之后仍是 27）。SQLite 文档没写这条 pragma 是否随事务回滚（[PRAGMA user_version](https://www.sqlite.org/pragma.html#pragma_user_version)），这里依据的是实测。
  - 限制：`ListMigrations` 必须返回「所有」已执行的版本。只返回最大版本时，[`UpVersions`](https://github.com/pressly/goose/blob/v3.29.0/internal/gooseutil/resolve.go#L19-L84) 会把更低的版本当成漏跑，要么报错，要么在 `WithAllowOutofOrder` 下重跑。所以 Store 要知道迁移列表（可以从 `Provider.ListSources()` 取）。文件版本号也必须和 `user_version` 一致（如 `00028_xxx.sql`）。
- 已有数据库从某个版本起接入（baseline）：
  - 默认 Store 下，可以放一份 `IF NOT EXISTS` 的 baseline 文件（见 [CI 检查与 baseline](#ci-检查与-baseline)），或者在 `Up` 之前自己往版本表插一行。Provider 没有「标记为已执行」的公开方法（[Provider 方法列表](https://github.com/pressly/goose/blob/v3.29.0/provider.go#L161-L345)）。
  - 用 `user_version` Store 时不需要 baseline：27 版数据库的 `user_version` 本来就是 27。

### 每个迁移的事务语义

- SQL 迁移默认带事务（[parser.go 第 112 行](https://github.com/pressly/goose/blob/v3.29.0/internal/sqlparser/parser.go#L112) `useTx = true`），文件里有 `-- +goose NO TRANSACTION` 时关闭（第 181–183 行）。README：「By default, all migrations are run within a transaction」（[第 306–309 行](https://github.com/pressly/goose/blob/v3.29.0/README.md#L306-L309)）。
- 每个迁移单独一个事务，迁移本身和版本记录在同一个事务里提交（[`runIndividually` 第 203–241 行](https://github.com/pressly/goose/blob/v3.29.0/provider_run.go#L203-L241)）。不会把多个迁移合进一个事务（第 133–144 行注释）。
- 失败时这一个迁移回滚，数据库停在上一个成功的版本，返回 `PartialError`（实测：28 失败后版本表为 `0,27`，加的列不存在）。
- `NO TRANSACTION` 时逐条执行，失败不会回滚已执行的语句，版本也不会记录（实测：重建到一半失败后 `skills` 表没了，`new_skills` 还在，`foreign_key_check` 有 3 个违例，连接上 `foreign_keys = 0`）。
- SQL 语句按行尾分号切分；以 `--` 开头的词之后的内容不参与判断（`endsWithSemicolon`）；语句开始之前的整行注释会丢掉（第 200–206 行）。实测 `schema.sql` 原文作为 goose 文件能正确执行。

### 锁与并发

- SQLite 没有现成的 locker（见上表）。
- `Up` 先在事务外 `ListMigrations` 决定要跑哪些版本，再逐个开事务执行（[`up` 第 347–410 行](https://github.com/pressly/goose/blob/v3.29.0/provider.go#L347-L410)）。两个进程同时打开同一个数据库时，后者可能基于旧的列表重跑已执行的迁移：`_txlock=immediate` 让它在 `BEGIN IMMEDIATE` 处等前者提交，然后执行，一般会报错（如重复加列）并回滚。不会损坏数据，但会打不开（推断，未实测）。
- 桌面端已经按数据库路径做了单实例锁（`cmd/desktop/main.go` 的 `SingleInstance`），所以这个风险在本仓库里很小。

### 数据库比 app 新

[`UpVersions`](https://github.com/pressly/goose/blob/v3.29.0/internal/gooseutil/resolve.go#L19-L84) 只看「已知但没执行」的版本，数据库里多出来的版本不算错误。实测：数据库已执行 27、28、29，Provider 只认识 27、28：

```
HasPending: false <nil>
GetVersions current/target: 29 28 <nil>
GetDBVersion: 29 <nil>
Up: 0 <nil>
Status: 27 applied / 28 applied
```

要给出「需要更新的 app」，就在 `Up` 之前比较 `current > target`，映射到现有的 `ErrNewerSchema`。

### 与 mattn/go-sqlite3、`SetMaxOpenConns(1)`、`_txlock=immediate`、WAL 的配合

- **驱动**：`DialectSQLite3` 与 driver 名 `sqlite3`（mattn）配合正常（实测，所有用例都用它）。
- **连接**：goose 每次操作先 `db.Conn(ctx)` 占住一条连接，用完才放回（`initialize`）。`SetMaxOpenConns(1)` 下，这期间我们自己的代码不能用同一个 `*sql.DB`。在 `store.Open` 里先跑完 goose 再交给业务代码，就没有冲突。
- **Go 迁移死锁**：不带事务的 Go 迁移（`RunDB`）用的是 `*sql.DB` 而不是 goose 占着的连接（第 221–233 行）。连接数为 1 时会一直等下去。goose 只在配置了 session locker 时检查这种情况（第 41–54 行注释写明是已知问题）。实测：注册一个 `RunDB` 迁移后 `Up` 在 3 秒超时时报 `context deadline exceeded`。带事务的 Go 迁移（`RunTx`）走 goose 的连接，没有这个问题。
- **`_txlock=immediate`**：goose 用 `conn.BeginTx` 开事务，mattn 按连接串执行 `BEGIN IMMEDIATE`（[mattn sqlite3.go 第 1266–1276 行](https://github.com/mattn/go-sqlite3/blob/v1.14.49/sqlite3.go#L1266-L1276)），一开始就拿写锁，与 `_busy_timeout` 配合正常。
- **WAL**：实测在 WAL 模式的数据库上跑 goose，没有发现问题。
- **外键**：`store.openDB` 打开后执行 `PRAGMA foreign_keys = ON`，goose 的事务里改不了它。要在关闭外键的情况下重建表，有两种做法：
  - 给升级单独开一个 `*sql.DB`，连接串加 `_foreign_keys=0`（[mattn 第 1370–1385 行](https://github.com/mattn/go-sqlite3/blob/v1.14.49/sqlite3.go#L1370-L1385)）。实测：这样跑 Atlas 生成的重建（带事务）成功；故意中途出错时整步回滚到 28，`new_skills` 不残留。
  - 用 `db.Conn` 固定一条连接，事务外 `PRAGMA foreign_keys = OFF`（推荐方案用的就是这种）。

---

## 结合本仓库

### `validateSchema` 的原文指纹

现状：`schemaFingerprint` 对 `sqlite_master` 里所有表和索引（排除 `sqlite_%`）的 `name` 与空白归一化后的 `sql` 取 sha256，和用 `schema.sql` 新建的内存数据库比较。

- **Atlas 重建表之后不能用**（实测）：
  - 升级后的指纹是 `2cb2bbc897b6`，用新 `schema.sql` 建数据库的指纹是 `43eb95f5c641`。
  - 差异来自 Atlas 的单行写法、没有注释、`CONSTRAINT \`0\``，以及 RENAME 后的 `CREATE TABLE "skills"`。
- **goose 默认版本表**也会被算进去：`goose_db_version` 不以 `sqlite_` 开头。要么在查询里排除，要么换成 `user_version` Store。
- **SQLite 存原文的规则**：
  - [sqlite_schema 文档](https://www.sqlite.org/schematab.html)：存的是原始 CREATE 语句，开头关键字转大写、去掉 TEMP 和数据库名限定，「and as modified by subsequent ALTER TABLE statements」。
  - 实测还会去掉 `IF NOT EXISTS`：`CREATE TABLE IF NOT EXISTS t (...)` 存成 `CREATE TABLE t (...)`。
  - `ALTER TABLE new_t RENAME TO t` 之后存成 `CREATE TABLE "t" (...)`，其余原文（含注释）不变（实测）。
  - `ALTER TABLE ... ADD COLUMN` 把新列插在最后一个列定义之后、表约束之前，和那一行的行内注释挤在同一行（实测：`a TEXT NOT NULL, b INTEGER NOT NULL DEFAULT 0,        -- c2`），和自然书写的 `schema.sql` 对不上。

替代办法对比：

| 办法 | 能发现的问题 | 代价 |
| --- | --- | --- |
| 保留原文指纹，表名引号归一化，升级一律按 `schema.sql` 原文重建表（推荐） | 全部：列、类型、默认值、CHECK、外键、索引、注释 | 改列要整表复制；手写升级时要从 `schema.sql` 复制原文。实测指纹一致 |
| 用 `PRAGMA table_xinfo`、`index_list`、`index_info`、`foreign_key_list` 做语义比较 | 列、类型、非空、默认值、主键、索引列、部分索引、外键 | **看不到 CHECK**：实测少了 `'renamed'` 的数据库在 `skills` 的列和外键上比较结果相同。`index_list.origin` 会出现 `u`（UNIQUE 约束）和 `c`（CREATE INDEX）的差别（实测重建后唯一的差异），要当作相同。`AUTOINCREMENT` 也看不到 |
| 运行时只看版本号，结构一致交给 CI | 运行时什么都不查 | 用户数据库被别的工具改过时发现不了；同步拉回失去结构校验 |

### 推荐的升级流程（`store.Open`）

1. `requireIdentity` 改为：`application_id` 不对 → `ErrUnacceptable`；`user_version > 最新` → `ErrNewerSchema`；`user_version < 27` → `ErrOlderSchema`（不变）；`27 ≤ user_version < 最新` → 先备份，再逐版升级。
2. 备份用现有的 `backup(ctx, destination, source)`（SQLite online backup API）写到备份文件。ADR 0003 要求「让桌面端在迁移之前先备份库」。
3. 逐版执行，每一步一个事务（见[结论](#对本仓库的推荐)第 2 条）。单个步骤失败时这一步回滚，数据库停在上一个版本，`Open` 返回错误。已经升级完成的步骤不回退，升级前的那份备份仍在。
4. 升级完成后照旧 `writeBuiltinAgents` → `validateSchema`。
5. 需要 Go 代码处理数据的步骤（如改写 JSON 列），可以在步骤表里放一个函数，签名为 `func(ctx, *sql.Tx) error`，和 SQL 文件走同一个事务。

### 同步拉回的旧备份

`OpenImage` 把下载的文件写到临时文件并单独打开，这时可以对临时文件跑同一套升级（不需要备份，S3 上的对象不变），然后 `validateImage`。这样：

- `user_version` 27 到最新之间的备份可以拉回，`cannotPull` 只在 `< 27` 时返回 `CannotPullOlderApp`。
- `> 最新` 仍返回 `CannotPullNewerApp`。
- `contentDigest` 已经把 `user_version` 算进去了。用 `user_version` 记版本时，推送端的摘要逻辑不用改。如果用 goose 默认版本表，`goose_db_version` 也会被 `contentDigest` 当成普通表算进摘要，其中 `tstamp` 因机器而异。

### 如果仍用 Atlas + goose

必须做到以下几点：

1. CI 里按固定版本下载 CE 二进制并校验 sha256，不能用 `go install` 或 `go tool`。
2. `00027_baseline.sql` 用 v0.0.6 的 `schema.sql` 原文。
3. 每个生成文件都要手改：
   - 删掉 #1597 产生的 DROP/CREATE INDEX；
   - 删掉 Down 段；
   - 有 `AUTOINCREMENT` 的表在 `CREATE TABLE new_X` 之后加 `INSERT INTO sqlite_sequence (name, seq) SELECT 'new_X', seq FROM sqlite_sequence WHERE name = 'X';`，并在 `DROP TABLE X` 前加 `-- atlas:nolint DS102`（实测两者都有效）；
   - 改名成 `000NN_xxx.sql`；
   - 最后 `atlas migrate hash`。
4. goose 在外键关闭的连接上运行（`_foreign_keys=0` 的独立 `*sql.DB`），每步之后 `PRAGMA foreign_key_check`。不用 `NO TRANSACTION`。
5. 用 `user_version` Store，或者接受 `goose_db_version` 并在 `schemaFingerprint` 与 `contentDigest` 里排除它。
6. `Up` 之前比较 `GetVersions` 的 `current > target`，映射到 `ErrNewerSchema`。
7. `validateSchema` 的原文指纹换成语义比较（会漏掉 CHECK），或者不再在运行时校验结构。
8. CI 用 `atlas migrate validate`，加上一个 Go 测试比较「空数据库跑完全部迁移」与 `schema.sql`。

---

## 替代方案

| 方案 | 做法 | 取舍 |
| --- | --- | --- |
| **手写 SQL + `user_version` 逐版前进（推荐）** | 嵌入 `migrations/0NN.sql`，`store` 里一个循环；每步在固定连接上关外键、`BEGIN IMMEDIATE`、事务内复核 `user_version`、执行脚本、`foreign_key_check`、写 `user_version`、提交 | 不加依赖。版本判断和执行在同一个事务里，并发更严谨。保留现有的 `requireIdentity`、指纹、同步逻辑。SQL 要自己写，重建表照 SQLite 12 步。实测整套可行 |
| goose + 自定义 `user_version` Store | 同上，但由 goose 负责解析 SQL 文件和执行循环 | 实测可行。多一个依赖（体积小）。版本判断在事务外。Go 迁移只能用 `RunTx`（`RunDB` 在单连接下死锁）。要自己处理数据库比 app 新的情况。省下的代码量与自写循环差不多 |
| goose 默认版本表 | 设想方案 | 要在指纹与同步摘要里处理 `goose_db_version`，baseline 要用 `IF NOT EXISTS` 文件或手动插行 |
| [golang-migrate](https://github.com/golang-migrate/migrate) v4.20.1（2026-09-09） | `schema_migrations(version, dirty)` 表；SQLite 驱动默认把每个迁移包在事务里，`x-no-tx-wrap` 可关闭（[sqlite3 README](https://github.com/golang-migrate/migrate/blob/v4.20.1/database/sqlite3/README.md)） | 版本写入与迁移不在同一个事务里（先写 dirty 再执行）；没有比 goose 更适合本仓库的地方 |
| Atlas Go 库嵌入 app（运行时声明式 `schema apply`） | app 启动时比较实际结构与 `schema.sql` 并自动执行差异 | 在用户数据上执行运行时才算出来的计划，事先审不了；#1597 会让它每次都要执行 DROP INDEX 并失败；会把 `ariga.io/atlas` 及其依赖带进 app。不推荐 |
| Atlas 只当起草工具 | 本地 `atlas schema diff --from <旧> --to file://schema.sql` 看一眼生成的语句，再按推荐方案手写 | 可选，不进 CI、不进流程 |

---

## 实测记录

环境：macOS arm64，Go 1.27.3，系统 `sqlite3` 3.54.0；Atlas CE v1.3.0（源码构建 + 官方二进制），Atlas 标准版 v1.3.0；goose v3.29.0；`mattn/go-sqlite3` v1.14.49。数据库按 `store.openDB` 的连接串打开（`_txlock=immediate&_secure_delete=on&_busy_timeout=5000`、`SetMaxOpenConns(1)`、`PRAGMA foreign_keys = ON`），用仓库 `schema.sql` 建表，设置 `application_id` 和 `user_version = 27`，开启 WAL。写入：一个 Agent、一个来源、技能 1/2/3、技能 1 和 2 各一个版本、技能 1 的一条安装记录，然后删掉技能 3（`sqlite_sequence` 为 3，最大 id 为 2）。测试用的改动是在临时副本里给 `skills` 加列 `pinned INTEGER NOT NULL DEFAULT 0`，并给 `update_check_status` 的 CHECK 加上 `'renamed'`。

| 实验 | 要点 |
| --- | --- |
| `schema diff` 同一个 `schema.sql` | 输出五对 DROP/CREATE INDEX（`sources_type_locator`、`skills_source_id_name`、`groups_name`、`projects_path`、`rolled_back_renames_skill_id`） |
| `migrate diff`（原文 baseline，不改 `schema.sql`） | 仍然生成文件，内容同上 |
| 生成的加列文件原样留在目录，再 `migrate diff` | `Error: … executing statement "DROP INDEX \`sources_type_locator\`;" … no such index` |
| goose 跑原样的加列文件（带事务） | `PartialError: applied=1 failed=28 err=no such index: sources_type_locator`；版本表 `0,27`，`pinned` 不存在 |
| goose 跑 Atlas 重建（已删多余索引，带事务） | `failed=29 err=FOREIGN KEY constraint failed`；停在 28，数据完好 |
| 同上，加 `-- +goose NO TRANSACTION` | 成功；`sqlite_sequence` 从 3 变成 2，新插入的技能 id 为 3（复用）；`skills` 原文变成 `CREATE TABLE "skills" (\`id\` integer NULL …`；指纹 `2cb2bbc897b6` ≠ `43eb95f5c641` |
| `NO TRANSACTION` 中途出错 | `skills` 不存在，残留 `new_skills`，`foreign_key_check` 3 个违例，连接上 `foreign_keys = 0` |
| 独立 `*sql.DB`（`_foreign_keys=0`）跑重建（带事务，补了 `sqlite_sequence`） | 成功，`sqlite_sequence` 保持 3，新技能 id 为 4；中途出错时整步回滚到 28 |
| 数据库已到 29，Provider 只认识 27、28 | `HasPending false`，`Up` 返回 0 个结果、无错误，`GetVersions` 返回 29/28 |
| `RunDB` Go 迁移 + `SetMaxOpenConns(1)` | `context deadline exceeded`（3 秒超时） |
| 事务内 `PRAGMA user_version = 99` 后回滚 | 仍为 27；事务内设 `foreign_keys = OFF` 后读到的仍是 1 |
| `user_version` 自定义 Store | 27 → 29 成功，无 `goose_db_version`；失败时 `user_version` 保持 27 |
| goose 在空数据库上执行原文 baseline | 指纹 `20fef5151503`，与直接执行 `schema.sql` 相同 |
| `migrate lint`（CE） | 能运行；补 `sqlite_sequence` 后报 DS102，加 `-- atlas:nolint DS102` 后无诊断 |
| 手写重建 + 逐版循环（推荐方案） | 成功，`user_version = 28`，`sqlite_sequence` 保持 3，归一化指纹 `43eb95f5c641` 等于新 `schema.sql`；中途出错时 `user_version` 仍为 27、表与数据原样，连接上 `foreign_keys` 恢复为 1 |

实验脚本留在 `/tmp/mig-research`（`proj*/` 是 Atlas 目录，`gooseexp/` 是 Go 程序），没有放进仓库。

---

## 出处

Atlas
- [Community Edition](https://atlasgo.io/community-edition)（许可、安装、功能对比）
- [migrate diff](https://atlasgo.io/versioned/diff)、[Dev database](https://atlasgo.io/concepts/dev-database)、[SQL schema](https://atlasgo.io/atlas-schema/sql)、[Lint analyzers · DS102](https://atlasgo.io/lint/analyzers#DS102)
- [Release v1.3.0](https://github.com/ariga/atlas/releases/tag/v1.3.0)、[LICENSE](https://github.com/ariga/atlas/blob/v1.3.0/LICENSE)、[Ariga EULA](https://ariga.io/legal/atlas/eula)
- 安装脚本 `https://atlasgo.sh`（2026-11-02 下载；`--community`、`ATLAS_VERSION`、`-o`、`--no-install`）
- 源码（v1.3.0）：[`cmd/atlas/go.mod#L5`](https://github.com/ariga/atlas/blob/v1.3.0/cmd/atlas/go.mod#L5)、[`cmdapi/version.go`](https://github.com/ariga/atlas/blob/v1.3.0/cmd/atlas/internal/cmdapi/version.go)、[`cmdapi/migrate.go#L152`](https://github.com/ariga/atlas/blob/v1.3.0/cmd/atlas/internal/cmdapi/migrate.go#L152)、[`sql/sqlite/migrate.go#L49-L56`](https://github.com/ariga/atlas/blob/v1.3.0/sql/sqlite/migrate.go#L49-L56)、[`#L192-L226`](https://github.com/ariga/atlas/blob/v1.3.0/sql/sqlite/migrate.go#L192-L226)、[`#L311-L333`](https://github.com/ariga/atlas/blob/v1.3.0/sql/sqlite/migrate.go#L311-L333)、[`#L507-L535`](https://github.com/ariga/atlas/blob/v1.3.0/sql/sqlite/migrate.go#L507-L535)、[`#L541-L568`](https://github.com/ariga/atlas/blob/v1.3.0/sql/sqlite/migrate.go#L541-L568)、[`sql/sqlite/diff.go#L129-L150`](https://github.com/ariga/atlas/blob/v1.3.0/sql/sqlite/diff.go#L129-L150)、[`sql/sqlite/inspect.go#L120`](https://github.com/ariga/atlas/blob/v1.3.0/sql/sqlite/inspect.go#L120)、[`sql/sqlite/sqlspec.go#L96-L105`](https://github.com/ariga/atlas/blob/v1.3.0/sql/sqlite/sqlspec.go#L96-L105)、[`sql/sqltool/tool.go#L34-L40`](https://github.com/ariga/atlas/blob/v1.3.0/sql/sqltool/tool.go#L34-L40)、[`#L173-L235`](https://github.com/ariga/atlas/blob/v1.3.0/sql/sqltool/tool.go#L173-L235)、[`#L481`](https://github.com/ariga/atlas/blob/v1.3.0/sql/sqltool/tool.go#L481)
- Issue：[#1597](https://github.com/ariga/atlas/issues/1597)、[#3818](https://github.com/ariga/atlas/issues/3818)、[#3792](https://github.com/ariga/atlas/issues/3792)、[#3231](https://github.com/ariga/atlas/issues/3231)、[#2700](https://github.com/ariga/atlas/issues/2700)、[#3143](https://github.com/ariga/atlas/issues/3143)

goose
- [Release v3.29.0](https://github.com/pressly/goose/releases/tag/v3.29.0)、[LICENSE](https://github.com/pressly/goose/blob/v3.29.0/LICENSE)、[CHANGELOG](https://github.com/pressly/goose/blob/v3.29.0/CHANGELOG.md)、[go.mod](https://github.com/pressly/goose/blob/v3.29.0/go.mod)、[README](https://github.com/pressly/goose/blob/v3.29.0/README.md)、[pkg.go.dev](https://pkg.go.dev/github.com/pressly/goose/v3@v3.29.0)
- [Provider 介绍（2023-11-11）](https://pressly.github.io/goose/blog/2023/goose-provider/)
- 源码（v3.29.0）：[`provider.go`](https://github.com/pressly/goose/blob/v3.29.0/provider.go)、[`provider_options.go`](https://github.com/pressly/goose/blob/v3.29.0/provider_options.go)、[`provider_run.go`](https://github.com/pressly/goose/blob/v3.29.0/provider_run.go)、[`provider_errors.go`](https://github.com/pressly/goose/blob/v3.29.0/provider_errors.go)、[`database/store.go`](https://github.com/pressly/goose/blob/v3.29.0/database/store.go)、[`internal/dialects/sqlite3.go`](https://github.com/pressly/goose/blob/v3.29.0/internal/dialects/sqlite3.go)、[`internal/gooseutil/resolve.go`](https://github.com/pressly/goose/blob/v3.29.0/internal/gooseutil/resolve.go)、[`internal/sqlparser/parser.go`](https://github.com/pressly/goose/blob/v3.29.0/internal/sqlparser/parser.go)、[`lock/`](https://github.com/pressly/goose/tree/v3.29.0/lock)

SQLite
- [ALTER TABLE](https://www.sqlite.org/lang_altertable.html)（第 4 节 ADD COLUMN、第 8 节 12 步重建与 Caution）
- [PRAGMA](https://www.sqlite.org/pragma.html)（`foreign_keys`、`user_version`、`application_id`、`foreign_key_check`、`table_xinfo`、`index_list`、`foreign_key_list`）
- [AUTOINCREMENT](https://www.sqlite.org/autoinc.html)、[sqlite_schema](https://www.sqlite.org/schematab.html)

驱动与其他
- [mattn/go-sqlite3 v1.14.49 `sqlite3.go`](https://github.com/mattn/go-sqlite3/blob/v1.14.49/sqlite3.go)（`_txlock`、`_foreign_keys`）
- [golang-migrate v4.20.1](https://github.com/golang-migrate/migrate/releases/tag/v4.20.1)、[sqlite3 驱动 README](https://github.com/golang-migrate/migrate/blob/v4.20.1/database/sqlite3/README.md)

本仓库
- `internal/library/internal/store/schema.sql`、`schema.go`、`store.go`、`image.go`；`internal/library/sync.go`；`cmd/desktop/main.go`；`docs/adr/0003-no-migrations-before-release.md`（现为 `docs/adr/0003-database-upgrades-step-by-step.md`）
