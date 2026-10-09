// Package library 用 SQLite 数据库统一管理 Agent Skills:先对来源做入库预览再入库,然后把 skill 安装到
// Claude Code 与 Codex 在各作用域读取的 skills 目录。数据库是唯一权威数据源:
// Agent 目录里的技能文件夹都在安装时从库写出;在那里做的修改不会进库,巡检只拿它们与库里的版本比对。
//
// 单说"技能"(Skill)指的都是库里的技能;市场搜到的是 SearchSkill,入库预览里的是 Candidate,
// 装出去的是 SkillScope。集合(Collection)与组(Group)只给出技能 ID,技能信息统一从 [Library.ListSkills] 取。
//
// 用 [Open] 打开库,得到 [*Library];用完调用 [Library.Close]。方法可在多个 goroutine 里同时调用。
// 方法返回的错误用 [ErrorOf] 取出 [*Error],或用 [IsCode] 按错误码判断。
//
// # 用户流程
//
// 下面按用户操作的顺序列出每一步用到的方法和所在文件。
//
// 搜索与入库(fetch.go):
//   - [Library.SearchSkills] 搜索 skills.sh,结果里带可直接用于入库预览的请求。
//   - [Library.PreviewFetch] 对 GitHub、URL 或本地目录做入库预览,列出候选。
//   - [Library.GetSkillAudit] 查看候选在 skills.sh 上的审计,可选。
//   - [Library.Fetch] 把选中的候选入库。
//
// 查看库里的技能(skills.go、collections.go、versions.go):
//   - [Library.ListSkills] 列出技能;[Library.ListCollections] 列出集合:每个来源一个,带集合里的技能 ID。
//   - [Library.GetSkill] 读取一个技能。
//   - [Library.ListVersions]、[Library.GetVersion] 列出和读取版本;[Library.ListVersionFiles]、[Library.GetVersionFile] 按版本 ID
//     列出和读取文件(当前版本传 Skill.VersionID,历史版本传 SkillVersion.ID);[Library.DiffVersions] 比较两个版本。
//   - [Library.RemoveSkill] 从库中移除技能,默认连同各作用域里已安装的文件夹;只撤掉一个作用域用 [Library.Uninstall]。
//
// 更新(update.go):
//   - [Library.CheckUpdates] 检查哪些技能有更新,结果随技能保存。
//   - [Library.Update] 重新读取来源,有变化时写入新版本;不重新安装。
//
// 分组(groups.go):
//   - [Library.CreateGroup]、[Library.RenameGroup]、[Library.DeleteGroup] 管理分组。
//   - [Library.ListGroups] 列出全部组及成员的技能 ID。
//   - [Library.AddSkillToGroup]、[Library.RemoveSkillFromGroup] 增删成员,并同步到该组已安装到的 Scope。
//
// 选择安装位置(projects.go):
//   - [Library.ListAgents]、[Library.ListRegisteredProjects] 列出可选的 Agent 与项目;界面上的
//     「Claude Code · 全局」「Codex · 某项目」由这两张表拼出,任何地方出现的 Scope 都按代码和项目 ID 从中查名字。
//   - [Library.RegisterProject]、[Library.RelocateProject]、[Library.UnregisterProject] 登记项目、改项目位置与注销项目。
//   - [Library.ScopeDirectory] 查某个作用域的目录。
//
// 安装(install.go):
//   - [Library.PlanInstall] 再 [Library.Install]:先看目标状态,再直接安装;[Library.Uninstall] 撤销。
//   - [Library.PlanGroupInstall] 再 [Library.InstallGroup]:组安装;[Library.UninstallGroup] 撤销。
//   - [Library.ListSkillScopes]、[Library.ListGroupInstalls] 列出安装记录。
//
// 安装与安装计划方法都只接受 Scope 身份:Agent 目录与 [Library.ScopeDirectory] 给出的相同,记录按身份落库,
// 未知 Agent 或未登记项目返回 NotFound。Agent 目录变了之后只认新目录,旧目录里的文件夹不管。
//
// 巡检与重新安装(reinstall.go):
//   - [Library.InspectSkillScopes] 对照磁盘检查已安装的技能文件夹。
//   - [Library.Reinstall] 把它们改回技能的当前版本。
//
// 同步(sync.go):
//   - [Library.PlanSyncPush] 再 [Library.SyncPush]:把整库推送到 S3。
//   - [Library.PlanSyncPull] 再 [Library.SyncPull]:从 S3 拉回整库替换本地。
package library
