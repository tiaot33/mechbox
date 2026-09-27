# “Install”表示将技能放入 Agent 目录

此前，产品用 **Install** 表示将技能放入 Library，用 **Activation** 表示将技能从 Library 放到 Agent 的发现目录（ADR 0001）。Agent 目录中的文件夹称为 **Projection**，将其恢复到当前版本称为 **Rewrite**，查看 Source 提供哪些技能则称为一次 **Fetch** 会话。本 ADR 用生态中已有的约定取代这些旧用法；术语本身定义在 `CONTEXT.md` 中。

## 为什么替换旧约定

- **“Install”的含义与用户预期相反。** 各种技能 CLI 和 Claude Code 插件流程都用“install”表示“放到 Agent 能找到的位置，之后就能使用”。而在本产品中，用户点击 Install 后得到的技能仍无法被 Agent 看到，还得再找另一个操作。
- **“Activate”听起来像许可证激活**，也与 Agent Skills 规范冲突；规范中的 activation 指 Agent 在运行时加载技能正文。
- **“Projection”和“Rewrite”是开发者的比喻。** 用户会说“已安装”“安装在哪里”“更新到新版本”，不会说“投影”。
- **“Fetch”承担了三种含义**：查看 Source、将这次查看作为一个会话，以及将技能带入 Library。Sync 在推送／拉取前的只读报告也称为“preview”，与查看 Source 的含义冲突。

## 决定

| 代码标识符 | 界面术语 | 含义 |
|---|---|---|
| Preview | 预览 | 查看 Source 提供的技能，固定为当时的内容；不向 Library 写入任何技能 |
| Candidate | 候选 | Preview 中可供选择的技能 |
| Fetch | 入库 | 将 Preview 中选定的 Candidate 带入 Library，即原来的 Install |
| Install / Uninstall | 安装 / 卸载 | 选择让 Library 中的技能在某个 Scope 可用，即原来的 Activate / Deactivate |
| Group install | 组安装 | 原来的 group activation |
| Installation | 安装项 | Scope 中的文件夹及其记录，即原来的 Projection |
| Install plan | 安装计划 | Install 前的只读报告，即原来的 activation preview |
| Reinstall | 重新安装 | 将 Installation 恢复为与某个版本一致，即原来的 Rewrite |
| Sync plan | 同步计划 | 推送／拉取前的只读报告，即原来的 sync preview |

- **Fetch 和 Install 沿用原有单词，但含义发生反转。** 任何残留的旧用法都会被不知不觉地按新含义理解，无法通过搜索区分，因此代码、API、JSON、schema、注释、测试和文档一起切换。产品尚未发布，不保留旧名称。表示从 Source 读取内容的标识符（`fetchSource`、`maxFetchSize`、`Source.LastFetched`、JSON `last_fetched`）保留“fetch”，因为这种含义与新用法不冲突。
- **Installation 与 Install 使用不同的词**，因为一个文件夹可以同时由多个选择撑着：一个直接 Install，以及一个或多个 Group install。撤销其中一个选择时，只有不再有其他选择撑着该文件夹，才会删除它。
- **执行前的只读报告统一称为“Plan”**（Install plan、Sync plan）；“Preview”专用于查看 Source。
- **Schema 的表名和列名跟随术语调整**（`installations`、`group_installs`），并提升 schema 版本。现有本地库和 S3 上的 Sync 快照不再能打开，必须重建（见 ADR 0001，2026-09-26：发布前不做迁移）。

## 本地目录也经过 Preview

旧的 `Install(LocalSource)` 直接从一个本地文件夹入库，绕过了 Preview。按照“新技能只能从 Preview 入库”的规则，本地目录也与远程 Source 一样先预览：

- **Source 是被预览的目录**（绝对路径），不是每个技能的文件夹。一个目录可以包含多个技能，它们共享一个 Source，因此也属于同一个 Collection。Update 和更新检查会重新发现该目录，并按名称重新定位各个技能，与远程 Source 一致；如果目录中已找不到某个技能名称，该技能的更新返回 `NotFound`。
- **名称来自 `SKILL.md`**，与其他 Source 一致。本地技能不再使用文件夹名；如果文件夹缺少有效的 frontmatter `name` 和 `description`，它会作为诊断项报告，不成为 Candidate。
- **Preview 是快照。** 远程 Source 通过 commit 或已下载的压缩包固定内容；本地目录在 Preview 与 Fetch 之间可能变化，因此 Preview 将 Candidate 的文件读入内存，Fetch 严格保存该快照。为避免完整读入过大的目录，所有 Candidate 的文件合计最多 1000 个、64 MiB（与压缩包限制相同）；超过任一上限，就以 `SourceInvalid` 拒绝该 Preview。

## 考虑过但未采用的方案

- **保留旧词，在界面中解释。** 这些用法与用户接触的其他工具都不一致，解释也难以在用户第一次点击后继续起作用。
- **全部采用新词（例如用“Deploy”表示放入目录）。** 虽然避免了反转 Fetch 和 Install 的含义，却更偏离生态用语，违背了此次调整的目的。
- **在 Fetch 时校验本地目录，而不是在 Preview 时生成快照。** 需要读取目录两次，仍然要定义什么叫“未变化”；使用快照则与 URL / Store 压缩包已有的行为一致。

## 后果

- 面向集成方的 JSON 变更：字段 `installed` → `fetched`（Candidate）、`installed_at` → `fetched_at`（Skill、SkillSummary）、`activated_at` → `created_at`（GroupInstall，与 `Installation.created_at` 一致）、`projected_copies` → `installations`（RemoveSkillOptions、UnregisterProjectOptions）、`projections` → `unbacked_installations`（RemoveFromGroupOptions，只影响不再有任何安装关系撑着的 Installation，默认保留，与另外两个选项不同）；取值 `projected` → `already_installed`（安装计划状态，与其预测的组成员结果一致）及 `installed`（组成员结果）、`already_projected` → `already_installed`、`rewritten` → `reinstalled`。
- ADR 0001 中“Install 写入 SQLite”和“Activation 展开到 Agent 目录”的表述由本 ADR 取代；其他 ADR 保留原有决策，改用新术语。
