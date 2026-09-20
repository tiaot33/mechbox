# Library 门面桌面化 Roadmap

**Date:** 2026-09-15(2026-09-21 同步第 4 步完成状态)
**目的:** 记录 `internal/library/api.go` 成为桌面端(Electron,经 ADR 0008 的 stdio sidecar)底层接口之前需要补的能力,按优先级排列。
**来源:** 2026-09-15 对门面的完整分析;标注"实测"的条目由一个临时探针程序(`replace` 引用本仓库,未入库)验证。
**用法:** 按文末"分步计划"推进,每一步对应一个 spec issue(或一个 bug issue),见 `docs/agents/issue-tracker.md`;开工时把编号写回条目;涉及决策的条目补 ADR。标注"schema"的条目会提升 `librarySchemaVersion`。schema 已是 v8(#77 / #82);下一次提升是 v9(E4)。

**进度(2026-09-21):**

- **A 全部完成** —— [#67](https://github.com/tiaot33/skills-tui/issues/67),PR [#70](https://github.com/tiaot33/skills-tui/pull/70) 已合入 main(`710bedb`)。B6 与 B3 的错误码部分随之落地。
- **B2 完成** —— [#71](https://github.com/tiaot33/skills-tui/issues/71),PR [#74](https://github.com/tiaot33/skills-tui/pull/74)(`556ca1b`)。决定:拒绝,`FailedPrecondition` 带占用者;转移语义另立条目。
- **I1 完成** —— [#75](https://github.com/tiaot33/skills-tui/issues/75),PR [#78](https://github.com/tiaot33/skills-tui/pull/78)(`8ca3e4e`)。
- **D1 / D2 完成** —— [#72](https://github.com/tiaot33/skills-tui/issues/72),PR [#79](https://github.com/tiaot33/skills-tui/pull/79)(`f4097c3`)。
- **D3 完成** —— [#73](https://github.com/tiaot33/skills-tui/issues/73),PR [#80](https://github.com/tiaot33/skills-tui/pull/80)(`b85c605`),ADR 0012。
- **C6 完成** —— [#76](https://github.com/tiaot33/skills-tui/issues/76),PR [#81](https://github.com/tiaot33/skills-tui/pull/81)。
- **C1 / C2 / I3 完成** —— [#77](https://github.com/tiaot33/skills-tui/issues/77),PR [#82](https://github.com/tiaot33/skills-tui/pull/82)。schema v8:描述入库、版本 / 激活 / 分组时间戳、Group 名 UNIQUE 并自动加 `#N`。
- **schema 基线**:main 为 v8。下一次 schema 变更是第 5 步 E4(v9)。
- **第 4 步完成** —— [#83](https://github.com/tiaot33/skills-tui/issues/83) 已关闭,实现提交 [`52a7145`](https://github.com/tiaot33/skills-tui/commit/52a7145)、简化提交 [`8d518f5`](https://github.com/tiaot33/skills-tui/commit/8d518f5) 已推送至 main:激活预检、Fetch 候选标明已在库中、删除/注销默认清掉投影、去掉 `UpdateResult.Note`。明确不做更新预览/绑版、不做过时投影列表、不写「Preview → Execute 统一模式」ADR。
- 下一步为第 5 步 E1–E6,先建 spec issue 并确定凭证存放 ADR。E、F1–F4、G–H、I4 / I5 尚未建 issue;F5 已随 #83 完成。I2 的无记录目录不追踪是已接受边界;B4 更新预览与绑定决定不做。

## 优先级定义

- **P0**:不做则桌面端无法正确交互,或会卡死 / 报错。
- **P1**:桌面端可用,但体验或可维护性明显受损。
- **P2**:小坑与整理项。

---

## P0

### A. 错误模型与校验映射 — ✅ 完成,spec: [#67](https://github.com/tiaot33/skills-tui/issues/67),PR [#70](https://github.com/tiaot33/skills-tui/pull/70)

原状:`typedError`(`internal/library/errors.go`)只映射 `sql.ErrNoRows` / `fs.ErrPermission` / `fs.ErrNotExist`,其余一律 `Internal`。#67 覆盖 A1–A7;`RateLimited` / `Unauthenticated` 移至 E2(需与配置 API 一起落地才对 UI 有意义)。以下条目保留作为现状记录与回归对照。

- [x] **A1 校验错误改用 `&Error{Code: InvalidArgument}`。** 现状(实测均为 `internal`):`CreateGroup` / `RenameGroup` 空名、`RegisterProject` 空路径或非目录、`RemoveSkill` / `UnregisterProject` 非法选项、`DiffVersions` 跨 skill、`insertSkill` 空名——都是 `fmt.Errorf`。→ #67
- [x] **A2 映射 SQLite 约束错误。** `sqlite3.ErrConstraint` 的 UNIQUE 冲突 → `AlreadyExists`(同路径重复 `Install`、`InstallFetch` 选中已安装候选、`AddSkillToGroup` 重复;实测均为 `internal`),FK 失败 → `NotFound`。→ #67
- [x] **A3 取消与超时不再是 `Internal`。** 实测:传入已取消的 ctx 调 `Update` → `internal("get skill: context canceled")`。新增 `Canceled`(必要时 `DeadlineExceeded`),`typedError` 识别 `context.Canceled` / `context.DeadlineExceeded`;ADR 0008 的 cancel 通知依赖它。→ #67
- [x] **A4 新增错误码。** `Canceled`(见 A3)、`FailedPrecondition`(`Deactivate` 仅分组激活的 skill 现返回 `NotFound`,实测;分组同名成员与共享目录现返回 `AlreadyExists` 但 `Overwrite` 对它们无效;`SyncPull` 远端 schema 不可接受现返回 `Internal`)。`RateLimited`(附 retry-after)与 `Unauthenticated` / `Permission` 的区分已移至 E2。→ #67
- [x] **A5 拆分过载的码。** 新增 `SourceInvalid`:`SourceUnreachable` 现同时表示网络不通、zip 损坏、超出 64 MB、S3 5xx、响应格式错;`InvalidArgument` 被用于 "no valid Skill candidates"(远端内容问题,不是调用方参数)。分界以"重试是否可能改变结果"为准。Store URL 未配置改为 `NotConfigured`;远端 ref 不存在改为 `NotFound`。→ #67
- [x] **A6 `Error` 增加结构化 `Details`。** 冲突路径、占用者 `SkillID`、缺失的候选名等不应只存在于英文 `Message` 里。→ #67
- [x] **A7 选项校验一致。** 实测:`ActivateOptions{OnConflict: "nope"}` 被静默当作 `Fail`,而 `RemoveSkillOptions{ProjectedCopies: "nope"}` 报 `internal`。所有 options 枚举统一校验并返回 `InvalidArgument`。→ #67

### B. 写操作预检(Preflight)

Sync 保留 Preview → Execute(`expected` 绑定)模式。激活已支持只读预检,Fetch 候选已标注库内技能 ID;激活预检不绑定执行令牌,更新始终拉取此刻最新内容。

- [x] **B1 `PreviewActivate(id, scope)` / `PreviewActivateGroup(groupID, scope)`。** 冲突作为说明返回:`ready` / `refresh` / `occupied` / `untracked`;分组另给 `duplicate_names`。无记录文件夹不比对内容、不单列「遗留副本」。未知技能、作用域或分组及 I/O、取消仍报错;执行期规则不变。→ spec: [#83](https://github.com/tiaot33/skills-tui/issues/83)
- [x] **B2 修同名跨 skill 的接管 bug。** 实测:A、B 同名不同源,A 已投影到某 Scope;`Activate(B, Overwrite)` 成功后**两条记录指向同一文件夹**;随后 `Deactivate(A)` 删掉 B 的内容并留下 B 的悬空记录。根因:`directoryClaimedByAnotherScope` 只查同一 skill 的其他 Scope,不查同 Scope 下的其他 skill。`Overwrite` 应转移记录或拒绝,不能留两条记录。→ spec: [#71](https://github.com/tiaot33/skills-tui/issues/71)(决定:拒绝,`FailedPrecondition` 带占用者;转移语义另立条目)
- [x] **B3 候选标注已安装。** `FetchSkill` 增加 `Installed SkillID`(`omitzero`);同一来源同一名字、库里已有则带上。`Fetch` / `ListFetchSkills` 现查库。→ spec: [#83](https://github.com/tiaot33/skills-tui/issues/83)
- **B4 更新预览与绑定 — 决定不做。** `PreviewUpdate` + `Update(Expected)` 绑预览时的远端身份。第 4 步决定不做:更新永远拉此刻最新,不预览差异、不绑版。
- [x] **B5 `UpdateResult.Note`。** 第 4 步已删除该中文字段,不改成 `StaleActivations`;刷新投影由客户端再 `Activate`。→ spec: [#83](https://github.com/tiaot33/skills-tui/issues/83)
- [x] **B6 `RegisterProject` 重复注册携带已有 ID。** 实测返回 `AlreadyExists` 但不带既有 `ProjectID`,UI 无法跳转;放入 `Details`(A6)或提供 `FindRegisteredProject(path)`。→ 已随 A6 放入 `Details.ProjectID`,#67

### C. 读模型补齐 — 2026-09-16 评审:只做 C1 / C2 / C6,其余推后(GUI 可绕过)

- [x] **C1 `Skill` / `SkillSummary` 增加 `Description`。**(schema)列表页第一需求;`candidateMetadata` 已解析出该字段但未入库。现状前端要对每行调 `GetSkill`(拉全部文件正文)再自行解析 frontmatter。描述放版本级,迁移从 `SKILL.md` blob 回填。→ #77,#82
- [x] **C2 时间戳。**(schema)`SkillVersion.CreatedAt`、`Activation.ActivatedAt`、`Group.CreatedAt` 落列;`Skill.InstalledAt` / `UpdatedAt` 由版本时间派生。迁移不回填(空 = 未知)。ADR 0009 已记录版本无时间戳。→ #77,#82
- [ ] **C3 暴露当前版本。** `Skill.CurrentVersionID` / `SkillVersion.IsCurrent`(表里已有 `current_version_id`,无 schema 变更)。现状 UI 只能靠"`ListVersions` 最后一个",一做回滚(H3)即失效。**推后**:随 H3 一起做。
- [ ] **C4 轻量文件访问。** `ListVersionFiles(versionID) → [{Path, Size, Mode}]` + `ReadFile(versionID, path)`。现状 `GetSkill` / `Update` / `InstallFetch` 总是返回全部文件字节(RPC 上是 base64),带 `assets/` 的 skill 每次详情页都很重。**推后**:多传字节,不阻塞功能。
- [ ] **C5 `Activation` 记录投影的 `VersionID`。**(schema)**推后**:B5 已删除提醒字段,不返回过时投影列表;C6 判定也不依赖它。
- [x] **C6 投影健康度。** `InspectActivations() → [{Activation, Status}]`,`Status ∈ {Current, Stale, Modified, Missing, DirectoryMissing}`。由 Library 对照磁盘,不再让前端拿 `Activation.Directory` 自行巡检。`SyncPull` 后、厂商改布局后(ADR 0011:旧目录不管)都靠它给出可见状态。Stale 的判定是"与某个非当前版本一致",扫全部版本,不需要 C5。→ spec: [#76](https://github.com/tiaot33/skills-tui/issues/76),PR [#81](https://github.com/tiaot33/skills-tui/pull/81)
- [ ] **C7 `RegisteredProject.Exists`**(或 `InspectProjects`)。Sync / 重映射后路径可能不在本机。**推后**:GUI 可自行 `stat`;C6 的 `DirectoryMissing` 已覆盖投影侧。
- [ ] **C8 单项 getter 与过滤。** `GetGroup`、`GetRegisteredProject`、`GetSource`;`ListActivations` 支持按 skill / scope 过滤。**推后**:GUI 拉全量自行过滤。

### D. 锁与并发 — ✅ 完成,spec: [#72](https://github.com/tiaot33/skills-tui/issues/72)(D1、D2,PR [#79](https://github.com/tiaot33/skills-tui/pull/79))、[#73](https://github.com/tiaot33/skills-tui/issues/73)(D3,PR [#80](https://github.com/tiaot33/skills-tui/pull/80))

- [x] **D1 网络 I/O 移出库锁。** `Update` / `CheckUpdates` / `InstallFetch` / `SyncPush` / `PreviewSyncPush` 在网络期间全程持 `l.mu`(`readOperation`,RLock);`Activate*` 需要 Lock;Go `RWMutex` 写者优先,一旦有写者排队,所有新读者阻塞。**实测**(URL 来源、服务端延迟 3 s):一个 skill 的 `Update` 跑 3 s → 无关 skill 的 `Activate` 等满 3 s → 其后发起的纯读 `ListSkills` 等 2.4 s。按 ADR 0008 的 per-request goroutine 模型,一次"检查全部更新"期间只要碰一下激活,整个应用的所有列表都会冻住。`InstallFetch` 还全程持 `fetchMu`(含 GitHub 物化的网络阶段),挡住其他 `Fetch` 收尾与 `Close`。目标:先取远端,再加锁复核、提交;`l.mu` 实为共享 / 独占锁(DB 串行化由单连接完成),`readOperation` / `writeOperation` 名字有误导,一并更名。→ #72(已更名为 `sharedOperation` / `exclusiveOperation`;`InstallFetch` 占用中的句柄再次安装返回 `FailedPrecondition`)
- [x] **D2 修网络阶段与提交之间的 TOCTOU。** `Update` 拉取期间另一请求 `RemoveSkill`。**实测**:远端有变化 → `createVersion` 撞 FK → `not_found`(#67 兜底映射,事务内已写 blob);远端无变化 → **静默成功**,返回 `changed=false` 与已不存在的 `Skill`,还更新孤儿来源行的 `last_fetched`。加锁后在事务内复核:行不在 → `NotFound`,当前版本已变 → `Conflict`。随 D1 处理。→ #72
- [x] **D3 多进程共存底线。** 现状 `SetMaxOpenConns(1)`、rollback journal、`BEGIN DEFERRED`;驱动默认 `busy_timeout` 已是 5 s。**实测**:两实例混合读写不会立刻 BUSY;但读写互阻,另一进程持写事务或长读超 5 s → 本进程写(含提交阶段)`internal("database is locked")`。`PRAGMA journal_mode=WAL`(身份校验之后执行,避免改写外来库)+ `_txlock=immediate` + 显式 `busy_timeout`,并把 `SQLITE_BUSY` / `SQLITE_LOCKED` 映射为新码 `Busy`。拓扑 B(守护进程)另议(ADR 0008)。→ #73,ADR 0012(推送快照需在 backup 后显式切回 `DELETE`;`ListSkills` / `ListGroupSkills` 改为单条语句读摘要)

---

## P1

### E. 运行时配置与 Sync 状态

现状全靠环境变量:`SKILLS_S3_*`、`AWS_*`、`SKILLS_STORE_URL`、`SKILLS_PROJECT_PATH_MAP`、`SKILLS_GIT_TIMEOUT`、`GITHUB_TOKEN` / `GH_TOKEN`。GUI 拉起的 sidecar 无法在运行时修改环境变量。

- [ ] **E1 配置 API 取代环境变量。** `GetConfig` / `SetConfig`(S3 endpoint / bucket / key / region / 凭证、Store URL、git 超时、GitHub token)。凭证的存放位置(库内 / 系统钥匙串 / 仅内存)需要 ADR。
- [ ] **E2 连接与鉴权探测。** `TestSyncConnection`;新增错误码 `RateLimited`(附 retry-after,现在限流最终表现为 `SourceUnreachable`)与 `Unauthenticated`(Library 无凭证可提供)以区分 `Permission`(凭证被拒),使 UI 能引导到配置页(从 A4 移入;依赖 E1 有地方可配)。
- [ ] **E3 拉库重映射参数化。** `PreviewSyncPull` 返回远端 Registered projects 列表;`SyncPull(expected, SyncPullOptions{ProjectPathMap})` 取代 `SKILLS_PROJECT_PATH_MAP`。桌面端这是一个对话框("远端登记了 `/Users/a/x`,映射到本机哪里?"),不该在 pull 时读环境变量。
- [ ] **E4 Sync 状态落库。**(schema)上次 push / pull 时间与当时的远端身份,使"远端自上次同步后被另一台机器改过"可判定(现只守住 preview → execute 那几秒的窗口);pull 前做本地备份。
- [ ] **E5 `Info()`。** 库路径、schema 版本、各实体计数、文件大小,供状态 / 关于面板。
- [ ] **E6 Logger 注入。** 接受 `slog.Handler`,配合 ADR 0008 "serve 模式下 stdout 只走协议 JSON"的要求。

### F. RPC 边界形状

- [ ] **F1 `Scope` 的 wire 形状。** 密封接口能 marshal 不能 unmarshal(ADR 0011 明确推给 RPC 层)。在边界定义 DTO `{kind, agent, project_id}` 或自定义 (un)marshal;TS 类型据此生成。
- [ ] **F2 DTO 去散文。** `SyncPreview.Warnings`、`SyncResult.Warnings` 是中文句子,`Error.Message` 却是英文。改为枚举 / 码,本地化归 UI(ADR 0003)。`UpdateResult.Note` 由 [#83](https://github.com/tiaot33/skills-tui/issues/83) 删除,不改成枚举。
- [ ] **F3 枚举默认值显式化。** `on_conflict: ""`、`projected_copies: ""` 在 wire 上应为明确值(如 `"fail"` / `"remove"`)。空 `projected_copies` 的含义随 [#83](https://github.com/tiaot33/skills-tui/issues/83) 改为删除。
- [ ] **F4 命名整理。** `Install`(本地)vs `InstallFetch`(远程);更新检查已统一为 `CheckUpdates`,单项与批量都返回 `[]UpdateCheck`。
- [x] **F5 刷新 ADR 0008。** 过时方法名与 cancel 覆盖的网络操作清单已随 [#83](https://github.com/tiaot33/skills-tui/issues/83) 更新;TS 生成器仍留到第 6 步实现 `internal/rpc/` 时再定。

### G. 进度与变更通知

- [ ] **G1 进度报告。** 长操作(`Fetch`、`InstallFetch`、`Update`、`CheckUpdates`、`SyncPush` / `SyncPull`)可报告进度:ctx 携带 `ProgressFunc`,或 Library 级 `Events()`;RPC 层映射为 JSON-RPC notification。ADR 0004 为 TUI 接受了"无进度",桌面端不成立。
- [ ] **G2 变更通知。** `Watch(ctx) <-chan Change` 或列表返回 `Revision` 计数,覆盖本进程写操作与 `SyncPull` 整库替换;多进程场景留给拓扑 B。现状任何写操作后前端只能全量重拉。

### H. 自研 skill 与版本操作

- [ ] **H1 `Install` 校验与 Fetch 对齐。** 实测:**没有 `SKILL.md` 的目录也能装进库**,不校验 frontmatter,名字取目录名。应要求 `SKILL.md` 存在、frontmatter 合法,并决定 name 与目录名的一致性策略(ADR 0001 已记录该差异)。
- [ ] **H2 自研 skill 编辑路径。** `CreateSkill` / `SaveSkillFiles(skillID, files) → 新版本` / `ExportSkill`。ADR 0001 承诺的 export / edit / re-import 或内置编辑器至今"待规定"。
- [ ] **H3 回滚。** `SetCurrentVersion(skillID, versionID)`;GUI 里只读的版本历史是半个功能。
- [ ] **H4 批量操作。** `RemoveSkills`、`UpdateMany`(逐项结果,形状同 `UpdateCheck`),对应多选交互。

---

## P2

### I. 数据完整性小坑

- [x] **I1 `RemoveSkill(Remove)` 顺序。** 先删磁盘副本再开事务;事务失败则副本已丢、记录仍在。`UnregisterProject(Remove)` 与 `Deactivate` 有同样问题。改为先改名暂存、提交后删除(同 `DeactivateGroup` 的做法),四处共用一个 helper。→ #75,#78
- **I2 `RemoveSkill(Keep)` 的遗留副本零追踪 — 已接受边界。** 第 4 步已落实默认删除投影,不新增目录追踪:无记录文件夹在预检里是 `untracked`;空选项改为默认删除,保留须显式 `Keep`。C6 仍覆盖不到无记录目录,这是已接受的边界,不列为待实现项。→ 见 [#83](https://github.com/tiaot33/skills-tui/issues/83)
- [x] **I3 分组名唯一。**(schema)实测可创建两个同名组。决定:`groups.name` 加 UNIQUE;`CreateGroup` / `RenameGroup` / 迁移遇重名自动改为 `名字#2`、`#3`(最小空闲数字),不报错;`RenameGroup` 改为返回最终 `Group`。→ #77,#82
- [ ] **I4 孤儿 `sources` 行与 blob GC。** `RemoveSkill` 不清理来源行与 blob;ADR 0005 的 GC "to be specified"。
- [ ] **I5 `Open` 迁移无预告。** 打开旧库即原地迁移;桌面端应能先探知"需要迁移"并备份(可与 E5 合并)。

---

## 分步计划

原则:每一步是一个可独立合入、可独立验收的 PR 序列;每步一个 spec issue(bug 单独开);schema 只在第 3 步(v8)、第 5 步(v9)各提升一次;需要决策的步骤先落 ADR 再写代码。每步末尾标出依赖,无依赖的步骤可并行。

### 第 0 步 — ✅ 收口进行中工作(前置)

- 合入 PR [#69](https://github.com/tiaot33/skills-tui/pull/69),main 到 schema v7。
- 出口:`librarySchemaVersion == 7`,之后本 roadmap 的 schema 变更从 v8 起。

### 第 1 步 — ✅ 修数据损坏 bug:B2

- spec: [#71](https://github.com/tiaot33/skills-tui/issues/71),PR [#74](https://github.com/tiaot33/skills-tui/pull/74)。
- 决定:拒绝跨 skill 同名目录,`FailedPrecondition` 带占用者;转移语义另立条目。
- 无 schema、无 ADR。

### 第 2 步 — ✅ 并发底线:D1–D3

- D1 / D2:[#72](https://github.com/tiaot33/skills-tui/issues/72),PR [#79](https://github.com/tiaot33/skills-tui/pull/79)。网络阶段移出 `l.mu`;`Update` 提交前复核 skill 仍在且版本未变;`readOperation` 更名为 `sharedOperation` / `exclusiveOperation`;`InstallFetch` 会话占用。
- D3:[#73](https://github.com/tiaot33/skills-tui/issues/73)+ ADR 0012,PR [#80](https://github.com/tiaot33/skills-tui/pull/80)。`PRAGMA journal_mode=WAL` + `BEGIN IMMEDIATE`;`SQLITE_BUSY` → `Busy`。
- 无 schema。与第 3 步无依赖。

### 第 3 步 — ✅ 读模型与 schema v8:C1 / C2 / C6 / I3

- schema v8:[#77](https://github.com/tiaot33/skills-tui/issues/77),PR [#82](https://github.com/tiaot33/skills-tui/pull/82)。C1 `Description`、C2 时间戳、I3 Group 名 UNIQUE 并自动加 `#N`。
- 纯代码:C6 `InspectActivations` 已完成,[#76](https://github.com/tiaot33/skills-tui/issues/76),PR [#81](https://github.com/tiaot33/skills-tui/pull/81)(不依赖 C5)。
- C3 / C4 / C5 / C7 / C8 按 2026-09-16 评审推后;I1 已完成。
- 依赖:第 0 步(已完成)。

### 第 4 步 — ✅ 写操作预检:B1、B3、B5 + 默认删除投影 + F5 过时方法名

- spec: [#83](https://github.com/tiaot33/skills-tui/issues/83) 已完成并关闭,`52a7145` / `8d518f5` 已推送至 main;`go test -race ./internal/library` 通过。schema 保持 v8,不写「Preview → Execute 统一模式」ADR。
- B1 `PreviewActivate` / `PreviewActivateGroup`:返回 `ready` / `refresh` / `occupied` / `untracked`(分组加 `duplicate_names`);无记录文件夹不比对内容。B3 `FetchSkill.Installed`。B5 删除 `UpdateResult.Note`,刷新投影由客户端再激活。`RemoveSkill` / `UnregisterProject` 空选项改为删除投影,显式 `Keep` 才保留。
- B4 决定不做(更新永远拉最新)。F5:已更新 ADR 0008 过时方法名与联网操作清单;TS 生成器留给第 6 步。
- 依赖:第 1 步(已完成;占用判定建立在 B2 修正后的语义上)。

### 第 5 步 — 下一步:运行时配置与 Sync 状态:E1–E6(spec issue + ADR "凭证存放位置")

- ADR 决定凭证存放(库内 / 系统钥匙串 / 仅内存),然后 E1 `GetConfig` / `SetConfig`。
- E2 `TestSyncConnection` + 新错误码 `RateLimited`(附 retry-after)/ `Unauthenticated`(依赖 E1)。
- E3 `PreviewSyncPull` 返回远端项目列表,`SyncPullOptions{ProjectPathMap}` 取代环境变量。
- E4 schema v9:Sync 状态落库(上次 push / pull 时间与远端身份),pull 前本地备份。
- E5 `Info()`(合并 I5 的"需要迁移"探知)、E6 `slog.Handler` 注入。
- 依赖:第 3 步(已完成,共用迁移框架)。尚未建 spec issue,先确定配置与凭证边界。

### 第 6 步 — RPC 边界与通知:F1–F3、G1–G2(与 `internal/rpc/` 落地同一批 issue)

- F1 `Scope` DTO `{kind, agent, project_id}` 的 (un)marshal;F2 Sync `Warnings` 去散文改枚举(`UpdateResult.Note` 已由 #83 删除);F3 wire 上枚举默认值显式化。
- G1 进度报告(ctx `ProgressFunc` 或 `Events()`,RPC 映射为 notification);G2 `Watch(ctx) <-chan Change` 或列表 `Revision`。
- 依赖:第 4 步(已完成,F5 过时方法名已随 #83 刷新;TS 生成器在本步选定)、第 5 步(E6 决定 serve 模式的日志出口)。B4 决定不做,F4 的 CheckUpdate 半边已在此前合入。

### 第 7 步 — 自研 skill 与批量:H1–H4、I4(视桌面端功能排期)

- H1 `Install` 校验对齐 Fetch(需决定 name 与目录名一致性策略,更新 ADR 0001);H3 `SetCurrentVersion` 回滚(依赖 C3);H2 `CreateSkill` / `SaveSkillFiles` / `ExportSkill`;H4 `RemoveSkills` / `UpdateMany`。
- I4 孤儿 `sources` 与 blob GC(补 ADR 0005 的 "to be specified")。
- 依赖:第 3 步。

### 关键路径与并行度

```
第 0 步 ✅ ──┬── 第 1 步 ✅(B2)
            ├── 第 2 步 ✅(D:#72 + #73)
            └── 第 3 步 ✅(C:#77+#76)──┬── 第 4 步 ✅(B 预检 + F5)──┐
                                        ├─────── 第 5 步(E, v9)──────┼── 第 6 步(F/G + internal/rpc/)
                                        └─────── 第 7 步(H/I)
```

第 0–4 步已完成。接下来推进第 5 步;第 7 步可按桌面端功能排期与其并行。第 6 步等待第 5 步完成,是桌面端接线点,在此之前门面应已稳定。
