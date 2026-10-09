// Package library 用 SQLite 数据库统一管理 Agent Skills:先对来源做入库预览再入库,然后把 skill 安装到
// 各 Agent(内置的 Claude Code、Codex 与用户添加的自定义 Agent)在各作用域读取的 skills 目录。数据库是唯一权威数据源:
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
// 设置(library.go、sync.go):
//   - [Library.SetGitHubToken] 传入访问 GitHub 用的个人访问令牌;没传入时读 GITHUB_TOKEN / GH_TOKEN 环境变量。
//   - [Library.SetSyncSettings] 传入同步用的 S3 设置,整组取用;没传入时读 SKILLS_S3_*、AWS_* 环境变量。
//     两者都只在这个 Library 实例上有效,改完下一次请求就用新值。
//
// 搜索与入库(fetch.go):
//   - [Library.SearchSkills] 搜索 skills.sh,结果里带可直接用于入库预览的请求。
//   - [Library.PreviewFetch] 对 GitHub、URL 或本地目录做入库预览,列出候选;对库里已有的集合再预览一次,看作者又发布了哪些技能。
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
//   - [Library.CheckUpdates] 按集合检查更新,结论写在集合里每个技能的 Skill.LastUpdateCheck 上,技能详情页的「有新版本」读它;
//     只读,可以自动发起。没有按技能、按组检查的入口。
//   - [Library.Update] 对一个集合发起,带进勾选的技能:从集合发起传有新版本的全部技能,从技能详情页发起传
//     Skill.CollectionID 和这一个技能。不要求先检查,更新自己读来源;库里全成或全不成,结果带每个技能更新前后的版本。
//     只改库,不重新安装。
//
// 分组(groups.go):
//   - [Library.CreateGroup]、[Library.RenameGroup]、[Library.DeleteGroup] 管理分组。
//   - [Library.ListGroups] 列出全部组及成员的技能 ID。
//   - [Library.AddSkillToGroup]、[Library.RemoveSkillFromGroup] 增删成员,并同步到该组已安装到的 Scope。
//
// 设置 Agent(agents.go):
//   - [Library.ListAgents] 列出 Agent,带是否在这台机器上检测到、是否自定义,以及它读取技能的目录。
//   - [Library.AddAgent]、[Library.EditAgent]、[Library.DeleteAgent] 添加、修改、删除自定义 Agent;改目录时选择是否迁移,
//     删除时选择是否一并删除文件夹。内置 Agent 不能改。
//   - 几个 Agent 的目录相同时共用技能文件夹:装到其中一个,共用的 Agent 也算已装;从其中一个卸载,它们都不再算已装。
//     列表与巡检里每个共用的 Agent 各列一处,[SkillScope].SharedWith 给出共用的 Agent,卸载前界面据此提示。
//
// 选择安装位置(projects.go):
//   - [Library.ListAgents]、[Library.ListRegisteredProjects] 列出可选的 Agent 与项目;界面上的
//     「Claude Code · 全局」「Codex · 某项目」由这两张表拼出,任何地方出现的 Scope 都按代码和项目 ID 从中查名字。
//   - [Library.RegisterProject]、[Library.RelocateProject]、[Library.UnregisterProject] 登记项目、改项目位置与注销项目。
//   - [Library.ScopeDirectory] 查某个作用域的目录。
//
// 安装(install.go):
//   - [Library.PlanInstall] 再 [Library.Install]:先看会装到哪个文件夹、那里现在是什么情况,再直接安装;
//     目标处有不是从库里装的同名文件夹时,用户同意后才覆盖。[Library.Uninstall] 撤销。
//   - [Library.PlanGroupInstall] 再 [Library.InstallGroup]:组安装;[Library.UninstallGroup] 撤销。
//   - [Library.ListSkillScopes]、[Library.ListGroupInstalls] 列出安装记录。看一个技能装在哪些地方,用
//     [Library.ListSkillScopes] 或 [Library.InspectSkillScopes] 按技能筛选;移除技能的确认框用后者说明哪几处用户改过。
//
// 安装与安装计划方法都只接受 Scope 身份:Agent 目录与 [Library.ScopeDirectory] 给出的相同,记录按身份落库,
// 未知 Agent 或未登记项目返回 NotFound。Agent 目录变了之后只认新目录,旧目录里的文件夹不管。
//
// 巡检与重新安装(reinstall.go):
//   - [Library.InspectSkillScopes] 看每一处安装现在的状态:是当前版本、落后、用户改过、文件夹不在了,还是项目不在这台机器上。
//   - [Library.Reinstall] 把用户选中的几处换成技能的当前版本;改过的那一处要单独同意覆盖。
//
// 同步(sync.go):
//   - [Library.PlanSyncPush] 再 [Library.SyncPush]:看 S3 上是哪一份库(谁、什么时候推送的),再用本机的库覆盖它。
//   - [Library.PlanSyncPull] 再 [Library.SyncPull]:看 S3 上的库里有什么、能不能拉回,再用它替换本机的库。
//     拉回不动 Agent 目录;之后用 [Library.RelocateProject] 把不在这台机器上的项目指到本机位置,
//     用 [Library.InspectSkillScopes] 与 [Library.Reinstall] 把各处安装对上。
package library
