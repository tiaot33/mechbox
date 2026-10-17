// Package library 用 SQLite 数据库统一管理 Agent Skills:先对来源做入库预览再入库,然后把 skill 安装到
// 各 Agent(内置的 Claude Code、Codex 与用户添加的自定义 Agent)在各作用域读取的 skills 目录。数据库是唯一权威数据源:
// Agent 目录里的技能文件夹都在安装时从库写出;在那里做的修改只有用户用它更新([Library.PlanUpdateFromScope])时才进库,巡检拿它们与库里的版本比对。
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
//   - [Library.SearchSkills] 搜索 skills.sh,结果里带可直接用于入库预览的请求(GitHub 仓库与 well-known 站点的技能)。
//   - [Library.PreviewFetch] 对用户粘贴的一个输入做入库预览:由技能库识别是本地目录、GitHub 仓库、其他 Git 仓库(GitLab、
//     GitHub Enterprise 等)、直接下载的 SKILL.md 或压缩包,还是带 well-known 索引的站点,结果里给出识别出的来源类型、展示名称与分支,
//     列出候选;对库里已有的集合再预览一次,看作者又发布了哪些技能。识别只在这里做一次,之后检查更新、更新都按入库时识别出的类型读取。
//     没写协议、以域名开头的输入(github.com/owner/repo)先补上 https:// 再识别。
//   - [Library.ListSourceRefs] 在入库预览之前列出 Git 类来源的全部分支和 tag、默认分支,以及输入本身带的分支:界面先给出分支下拉,
//     用户选好再把分支作为 PreviewFetchRequest.Ref 做入库预览,不必先按默认分支扫描一遍。只连一次来源,不克隆、不读技能文件。
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
// 更新(update.go、plan.go、history.go):
//   - [Library.CheckUpdates] 按集合检查更新,结论写在集合里每个技能的 Skill.LastUpdateCheck 上,技能详情页的「有新版本」读它;
//     只读,可以自动发起。没有按技能、按组检查的入口。侧边栏的更新计数与 Dock 角标都按结论上的 Pending 算;
//     检查同时记下来源里的新技能,列在 [Collection].NewSkills 上。
//   - [Library.PlanUpdate] 对一个集合生成更新计划(只读):每个技能有变化、没变化还是来源里已删除,风险、提交说明与文件差异,
//     以及有变化的技能装出去的各处现在的状态;[Library.GetUpdatePlanFile] 读来源里的新内容。
//   - [Library.SkipUpdate] 在更新计划里跳过几个技能的这次改动,或跳过整个来源版本;[Library.KeepDeletedSkill] 对来源里已删除的技能
//     选继续保留。两者之后都不计入角标。[Library.MarkNotInterested] 对新技能点「不感兴趣」,集合上不再提。
//   - 来源里改名的技能:检查更新与更新计划给出推测的新名字(UpdateCheck.RenamedTo、PlannedSkill.RenamedTo),计入角标;
//     勾上更新即确认,[Library.RejectRename] 回答「不是」。确认后装出去而没跟着重新安装的地方保留旧文件夹名,巡检、重新安装、
//     卸载、回滚都按它找到文件夹([SkillScope].Folder)。
//   - [Library.Update] 按更新计划带上勾选的技能和勾选的地方,一次确认:先换库(全成或全不成),再逐处重新安装;从集合发起勾计划里
//     有变化的全部技能,从技能详情页发起只勾这一个。本地目录来源的「重新读取」也是它。没换上的地方用 [Library.RetryUpdateInstalls] 单独重试。
//   - [Library.PlanUpdateFromScope] 对巡检为用户改过的那一处生成更新计划:用那里的内容更新库里的这个技能,成为新版本;
//     同样交给 [Library.Update] 执行,一次确认把勾选的其他地方一起换成这份修改,同样留记录、能回滚。
//     之后检查更新与更新计划只看来源从这份修改所基于的来源版本之后有没有改过;改过时更新计划标出 PlannedSkill.Edited,
//     勾上更新会换掉这份修改(能回滚回来),不想要就跳过。
//   - [Library.ListUpdateHistory] 按集合或技能列出历史:每次更新、跳过、回滚各一条;[Library.Rollback] 回滚一次更新,库与那次装过的地方
//     一起换回旧版,回滚掉的改动自动算作跳过。
//
// 分组(groups.go):
//   - [Library.CreateGroup]、[Library.RenameGroup]、[Library.DeleteGroup] 管理分组。
//   - [Library.ListGroups] 列出全部组及成员的技能 ID。
//   - [Library.AddSkillToGroup]、[Library.RemoveSkillFromGroup] 增删成员,并同步到该组已安装到的 Scope。
//
// 设置 Agent(agents.go):
//   - [Library.ListAgents] 列出 Agent,带是否在这台机器上检测到、是否自定义,以及它读取技能的目录。
//   - [Library.AddAgent]、[Library.EditAgent]、[Library.DeleteAgent] 添加、修改、删除自定义 Agent;改目录时选择是否迁移,
//     删除时选择是否一并删除文件夹。内置 Agent 不能改。改目录或删除都不动其他 Agent 还在读的文件夹:迁移时复制过去,
//     删除时即使选了一并删除也保留,安装记录转给还在读的 Agent。
//   - 几个 Agent 的目录相同时共用技能文件夹:装到其中一个,共用的 Agent 也算已装;从其中一个卸载,没有组安装留着时
//     文件夹随之删除,它们都不再算已装。列表与巡检里每个共用的 Agent 各列一处,[SkillScope].SharedWith 给出共用的 Agent,
//     卸载前界面据此提示。
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
// 看作用域里的未纳管技能与入库(unmanaged.go):
//   - [Library.ListUnmanagedSkills] 列出各作用域的技能目录里不是库装的技能文件夹(命令行工具装的、手动拷的、自己写的、
//     提交在项目仓库里的),和 [Library.ListSkillScopes] 放在一起,就是 Agent 在这个作用域能看到的技能。只列出,不改、不删。
//     npx skills 的来源记录里记了来源的,带出认出的来源([UnmanagedSkill].SourceInput),入库确认里预填。
//   - [Library.FetchUnmanaged] 把其中一个入库:磁盘上什么都不改,读出的内容成为库里的版本,记为装在这个作用域;
//     不带来源时,和库里某个版本完全一致只给那个技能补记安装,否则是没有来源的技能,不在集合里、不检查更新,
//     之后用 [Library.PlanUpdateFromScope] 拿目录里的修改更新。带来源时进这个来源的集合,照常检查更新、更新、回滚:
//     和来源里的一样就是来源里的那一版;不一样时版本就是磁盘上的内容、来源版本未知,第一次检查更新就报有新版本,
//     能用来源记录里的哈希确认是当初装的原样时是作者的旧版,否则标为 [SkillVersion].MaybeEdited,更新计划提示会换掉修改。
//   - [Library.ListSameNameSkills] 列出同一个 Agent 的全局和某个已登记项目里同名的技能,不论是不是库装的,两处都要标出来;
//     后果按 [Agent].SameName:Claude Code 里全局的那个生效,Codex 里两份都会出现,没有已知规则的 Agent 只标出同名。
//
// 巡检与重新安装(reinstall.go):
//   - [Library.InspectSkillScopes] 看每一处安装现在的状态:是当前版本、落后、用户改过、文件夹不在了,还是项目不在这台机器上。
//   - [Library.Reinstall] 把用户选中的几处换成技能的当前版本;改过的那一处要单独同意覆盖。
//     想留下改过的那一处的修改,用 [Library.PlanUpdateFromScope] 再 [Library.Update]。
//
// 同步(sync.go):
//   - [Library.PlanSyncPush] 再 [Library.SyncPush]:看 S3 上是哪一份库(谁、什么时候推送的),再用本机的库覆盖它。
//   - [Library.PlanSyncPull] 再 [Library.SyncPull]:看 S3 上的库里有什么、能不能拉回,再用它替换本机的库。
//     拉回不动 Agent 目录;之后用 [Library.RelocateProject] 把不在这台机器上的项目指到本机位置,
//     用 [Library.InspectSkillScopes] 与 [Library.Reinstall] 把各处安装对上。
package library
