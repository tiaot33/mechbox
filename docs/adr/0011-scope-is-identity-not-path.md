# Scope 表示身份，不表示路径

**Scope** 只有两种：某个 Agent 的全局层级，或某个 Agent 与一个已登记项目的组合。每个 Agent 的现行布局（它在 `$HOME` 下和项目根目录下扫描的目录名称）由 Library 持有，不由调用方持有。Installation 只记录 Scope 身份；目标目录在列出或安装时根据布局推导。门面不接受任何安装目标目录，也没有列出“所有 Scope 及其路径”的方法。

我们选择此方案来替代早期的 `Scope{Agent, Path}`，因为目录名称由 Agent 厂商决定，不由用户决定：当 Codex 从 `.codex/skills` 改为 `.agents/skills` 时，以路径表示 Scope 会迫使目标列表、逐项目展开、Sync 重映射以及每个测试重复同一套路径后缀规则；以绝对路径为键的 Installation 在 Sync 后还得靠猜测映射到新的 `$HOME`。按身份作为键，意味着一次布局变更只涉及 `agents` 中的一行，同一个 Installation 在任何机器上都能正确解析。（术语依据 [ADR 0014](0014-install-means-agent-directory.md) 更新：Activation → Install、Projection → Installation。）

## 考虑过的选项

- **`Scope{Agent, Path}`（未采用）。** 允许调用方安装到任意位置，使 Library 成为复制工具，而非 Agent 发现位置的管理者；还会将过时的目录名称固化到记录中。
- **带可选项目字段的单一结构体（最初未采用，后来被取代）。** 可空字段隐藏了两种类型的区别；两个层级是不同的概念，因此最初采用封闭的 `Scope` 接口下的两个 Go 类型（`GlobalScope`、`ProjectScope`）。**2026-09-25（ADR 0013）：** 为支持 Wails 桌面客户端，门面类型必须能经 JSON 往返转换，而接口参数既不能直接反序列化，也不能生成绑定。`Scope` 现在是一个带显式 `Kind`（`global` | `project`）的结构体 `{Kind, Agent, ProjectID}`，由构造函数 `GlobalScope(agent)` / `ProjectScope(agent, projectID)` 创建。显式 `Kind` 回应了最初的反对理由：两种类型有明确名称，无需根据空字段推断；所有接受 `Scope` 的方法都拒绝无效组合（全局类型带项目、项目类型没有项目、未知类型），返回带 `Details.Field` 的 `invalid_argument`。
- **用户可编辑 Agent 布局（未采用）。** 这会让 Scope 指向 Agent 从不扫描的位置。布局由产品初始化和修复，不提供修改布局的门面接口。

## 后果

- `Install` / `Uninstall` / `InstallGroup` / `UninstallGroup` / `Resolve` 接受 `Scope` 身份。未知 Agent 代码或未登记项目返回 `NotFound`。调用方通过 `ListAgents` ×（全局 | `ListRegisteredProjects`）组合目标；`ListScopes` 已删除。
- `ListInstallations` 返回 `{SkillID, Scope, Directory, Direct, Groups, CreatedAt}`（经 #91 / #85 / #110 重命名）。`Directory` 是只读的派生值，不接受它作为输入传回。
- Installation 的健康状态由 Library 对照每个安装项与磁盘内容得出（`InspectInstallations`）；调用方只渲染状态，不自行检查磁盘。
- 存储以 `(skill or group, agent, project_id NULL|id)` 作为 Installation 和 Group install 的键，没有路径列。Sync 只重映射已登记项目的路径；Installation 无需改写，因为目录会根据本机 `$HOME` 和映射后的根目录重新推导。
- **Library 永不创建 Scope 根目录**，即全局 Scope 的用户主目录，或项目 Scope 的已登记路径；只创建其下的 Agent 布局目录。根目录缺失表示项目不在这台机器上（从另一台机器同步而来、已移动，或位于未挂载的磁盘上）；使用 `MkdirAll` 会凭空在系统盘上创建整条路径。`Install` / `InstallGroup` 返回 `failed_precondition`（`Details.Path` 为根目录，项目还带 `ProjectID`），且不写任何记录；加入组成员时则跳过该 Scope。（2026-09-26，#85）
- **布局变更不迁移磁盘内容。** 厂商修改布局后，只认现行目录；工具曾安装到旧目录的文件夹属于过时内容，既不移动也不删除。用户再次安装，将技能放到现行目录。
- **拒绝打开旧库，不做映射**（2026-09-26，#85；见 ADR 0001）。本 ADR 最初会将身份模型引入之前、以路径为键的旧库安装记录映射到身份，并宁可丢弃无法识别的行，也不拒绝打开。产品尚未发布，没有需要保留的用户库，因此全部迁移已删除：`Open` 拒绝应用标识或 schema 版本不符的库，不改动文件。
- `Scope` 按 `{"kind", "agent", "project_id"?}` 双向序列化；调用方从 `ListInstallations` 读到的 `Scope`，可以原样传回 `Uninstall`。身份而非路径的原则不变：传输数据中仍没有目录。
