-- Library 的 SQLite 表结构,由 library.Open 嵌入执行。
-- 建表后 Open 会设置 PRAGMA application_id 和 user_version,作为库文件的身份标识。
-- 产品尚未发布,不迁移旧版本库:Open 只接受当前 user_version 的库。

-- 这个库文件里这一份数据的标识,只有一行。同步拉回用另一份库整个替换本机的库后换成新的随机值;
-- 先读库、再联网读来源、最后写回的操作(检查更新、更新计划之后的跳过)据此发现中途被替换,放弃写回,
-- 以免写到新库里 ID 恰好相同的技能上。
CREATE TABLE IF NOT EXISTS replica (
    id INTEGER PRIMARY KEY CHECK(id = 1), -- 固定为 1
    token TEXT NOT NULL                   -- 随机值,十六进制
);

-- skill 的来源。一个来源就是一个集合,id 也是集合的身份(library.CollectionID)。
-- type 还可以是 git(其他 Git 仓库)与 well-known,locator 相应为仓库+Git ref 与 well-known 索引地址;url 是直接下载的单个 SKILL.md 或压缩包。
-- 类型在入库预览时识别一次,之后检查更新与更新都按它读取。(列上的注释随建表语句存进数据库、参与表结构校验,所以补充说明写在这里。)
CREATE TABLE IF NOT EXISTS sources (
    id INTEGER PRIMARY KEY,                -- 来源主键,由 SQLite 分配;同一来源可包含多个 skill
    type TEXT NOT NULL,                     -- 来源类型:github / url / local
    locator TEXT NOT NULL,                  -- 定位信息,JSON(仓库+Git ref / URL / 本地路径)
    listed_revision TEXT NOT NULL DEFAULT '', -- 最近一次按整个来源记下 source_skills 时的来源版本;检查更新看到同一个来源版本时不再重记。本地目录来源为空,每次都重记
    listed_serial INTEGER NOT NULL DEFAULT 0,  -- 每重记一次 source_skills 加 1;检查更新写回前据此发现期间别处已重记过,不拿较旧的名单覆盖
    UNIQUE(type, locator)                   -- 同一个来源只记录一行
);

-- 来源里出现过的技能名,用来判断哪些是新技能。入库时按整个来源记下当时有哪些技能(不限入库时的子目录,
-- 也不限从 skills.sh 搜到的那一个),都算用户见过;之后检查更新在来源里看到的、没见过的名字记为新技能。
-- 用户对新技能点「不感兴趣」,或之后从这个来源入库时它在来源里,它也变成见过。见过的名字一直留着;
-- 新技能在来源里又没有了就删掉这一行。随来源一并保留,技能从库里移除不影响。
CREATE TABLE IF NOT EXISTS source_skills (
    source_id INTEGER NOT NULL REFERENCES sources(id), -- 所在来源
    name TEXT NOT NULL,                                -- 技能名
    new INTEGER NOT NULL CHECK(new IN (0, 1)),         -- 1 = 入库之后检查更新时才出现的新技能;0 = 用户见过
    PRIMARY KEY(source_id, name)
);

-- 库中的 skill;同一来源下名字唯一。
-- 没有来源的技能(从作用域入库的未纳管技能,作者就是用户自己)source_id 为 NULL:不在任何集合里,不检查更新,
-- 只能用 Agent 目录里的修改更新;同名的可以有几个(NULL 不参与 UNIQUE 去重)。
CREATE TABLE IF NOT EXISTS skills (
    id INTEGER PRIMARY KEY AUTOINCREMENT,             -- skill 主键,由 SQLite 分配;跨版本保持不变,移除后也不复用,先读库、联网、再写回的操作据此认出技能
    source_id INTEGER REFERENCES sources(id),         -- 入库时的来源;NULL = 没有来源
    name TEXT NOT NULL,                                -- skill 名,取 SKILL.md frontmatter 的 name
    current_version_id INTEGER REFERENCES skill_versions(id), -- 当前版本;读取和安装都用它。入库事务内先插入 skill,再创建版本并回填,期间可为 NULL
    update_checked_at TEXT NOT NULL DEFAULT '',        -- 最近一次确认来源(检查更新或更新)的时间,RFC3339Nano;同时有几次检查时,用它判断库里的结论是不是检查开始后才写下的
    -- 最近一次确认来源的结论:available 有新版本 / current 已是最新 / deleted 来源里已删除 / failed 检查失败;
    -- 空字符串表示入库后从未检查、也从未更新过。随 skill 一并删除
    update_check_status TEXT NOT NULL DEFAULT '' CHECK(update_check_status IN ('', 'available', 'current', 'deleted', 'failed')),
    update_check_revision TEXT NOT NULL DEFAULT '',    -- 最近一次确认来源时看到的来源版本:GitHub 为 commit,URL 为压缩包内容哈希;本地目录与检查失败为空
    update_check_content TEXT NOT NULL DEFAULT '',     -- 结论为 available 时,来源里这个技能内容的指纹(各文件路径、模式与内容指纹合成);据此和 skipped_changes 比对是否跳过了这次改动。其他结论为空
    update_check_renamed_to TEXT NOT NULL DEFAULT '', -- 结论为 deleted 时,推测这个技能在来源里改名成的新名字:同一来源里它消失了,同时出现了内容大部分相同的技能;没有推测为空
    kept_serial INTEGER NOT NULL DEFAULT 0,            -- 来源里已删除,用户选了继续保留时为当时的 update_check_serial(大于 0);0 = 没有这个标记。之后开始的检查更新、更新计划或更新确认来源里又有这个技能时清回 0
    update_check_serial INTEGER NOT NULL DEFAULT 0,    -- 每写一次结论或继续保留标记加 1;检查更新写回前据此发现期间别处已写过,不拿较旧的结论覆盖
    UNIQUE(source_id, name)
);

-- skill 的历史版本及快照元数据;文件清单在 version_files。
-- source_state 是该版本的来源读取记录,内容由来源定义:GitHub 与其他 Git 仓库记该 Skill 目录的 tree SHA 与确认它是远端最新时所在的 commit,
-- 直接下载记服务端给出的 HTTP validator,well-known 新格式记索引给出的 digest(旧格式不记),本地目录不记。
-- revision 是该版本内容在来源上对应的标识,供展示与按来源比较各 skill 是否在同一版本。
-- 用某处 Agent 目录里的修改更新进来的版本不是从来源读到的:source_state 为空,revision 沿用修改所基于的那个版本的,base_version_id 指向那个版本。
-- 当前版本是这种版本时,检查更新顺着 base_version_id 找到最早那个从来源读到的版本,拿它的 source_state 与文件和来源比较;本地目录来源除外。
-- 从作用域把未纳管技能连同来源入库、而磁盘内容和来源里的不一样时,版本就是磁盘上的内容:source_state 与 revision 为空(来源版本未知),
-- 检查更新按内容和来源比较。能确认磁盘是当初装的原样(npx skills 锁文件里的哈希对得上)时 maybe_edited 为 0,否则为 1。
-- description 与 created_at 属版本内容:描述随 SKILL.md 变化,时间是该快照写入 Library 的时刻。
CREATE TABLE IF NOT EXISTS skill_versions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,            -- 版本主键,由 SQLite 分配;同一 skill 内按 id 升序即版本先后顺序;不复用
    skill_id INTEGER NOT NULL REFERENCES skills(id), -- 此版本所属的 skill,关联 skills.id
    source_state TEXT NOT NULL DEFAULT '',           -- 来源自己记录的该版本读取状态,JSON;由来源子包定义和解释,Library 只存取不解析。空 = 无状态,检查更新时比较内容
    revision TEXT NOT NULL DEFAULT '',               -- 最近一次确认此版本内容时来源的标识:GitHub 为 commit,URL 为压缩包内容哈希,本地目录为空;更新确认无变化时刷新
    description TEXT NOT NULL DEFAULT '',            -- 该版本 SKILL.md frontmatter 的 description;解析不到为空
    created_at TEXT NOT NULL DEFAULT '',             -- 版本写入时间,RFC3339Nano
    base_version_id INTEGER,                         -- 用某处 Agent 目录里的修改更新进来时,修改所基于的版本(当时库里的当前版本);从来源入库或更新的为 NULL
    maybe_edited INTEGER NOT NULL DEFAULT 0 CHECK(maybe_edited IN (0, 1)) -- 1 = 从作用域入库时和来源里的不一样、说不清有没有用户的修改;更新计划据此提示会换掉修改。更新时确认来源里的内容和它一样时清回 0
);

-- 内容寻址的文件内容存储:指纹是内容的 sha256,相同内容跨版本、跨 skill 只存一份。
CREATE TABLE IF NOT EXISTS blobs (
    fingerprint TEXT PRIMARY KEY, -- 内容 sha256 的十六进制
    content BLOB NOT NULL         -- 文件原始内容
);

-- 某个版本包含哪些文件:路径 → 内容指纹 + 文件模式。
CREATE TABLE IF NOT EXISTS version_files (
    version_id INTEGER NOT NULL REFERENCES skill_versions(id), -- 文件所属的快照版本,关联 skill_versions.id
    path TEXT NOT NULL,                                       -- skill 内的相对路径,斜杠分隔
    fingerprint TEXT NOT NULL REFERENCES blobs(fingerprint),  -- 指向 blobs 里的内容
    mode TEXT NOT NULL CHECK(mode IN ('regular', 'executable')), -- regular = 普通文件,executable = 可执行文件;安装时据此设置可执行位
    PRIMARY KEY(version_id, path)
);

-- 手动创建的场景分组;与按来源自动形成的 Collection 相对(见 ADR 0006)。
-- 名字 UNIQUE:界面按名字挑选,重名无法区分;写入时重名自动改为 name#N。
CREATE TABLE IF NOT EXISTS groups (
    id INTEGER PRIMARY KEY,            -- 分组主键,由 SQLite 分配;成员关系和组安装均引用它
    name TEXT NOT NULL UNIQUE,          -- 分组展示名,去除首尾空白且全库唯一;重名时追加 #N(N 从 2 起取最小空闲值)
    created_at TEXT NOT NULL DEFAULT '' -- 创建时间,RFC3339Nano
);

-- skill 与分组的多对多关系:一个 skill 可以同时属于多个组。
CREATE TABLE IF NOT EXISTS skill_groups (
    skill_id INTEGER NOT NULL REFERENCES skills(id), -- 分组成员的 skill 主键
    group_id INTEGER NOT NULL REFERENCES groups(id), -- 成员所属的分组主键;与 skill_id 共同保证成员不重复
    PRIMARY KEY(skill_id, group_id)
);

-- 已注册的项目目录;注册后与每个 Agent 组成项目 Scope,目录由 agents.project_path 算出。
-- 改项目位置只改写 path,id 不变,引用它的安装记录与组安装随之指向新位置。
CREATE TABLE IF NOT EXISTS projects (
    id INTEGER PRIMARY KEY AUTOINCREMENT, -- 已注册项目主键,由 SQLite 分配;用作 ProjectScope 的项目身份;注销后不复用,历史里记的项目 ID 不会指到别的项目
    path TEXT NOT NULL UNIQUE -- 项目根目录的绝对路径(已解析符号链接)
);

-- Agent 的稳定代码、展示名称、Agent 目录与检测依据;目录分别相对用户主目录和已注册项目根。
-- 内置 Agent 每次打开库时按本版应用写入,已有的行以应用为准覆盖名称、目录和检测依据;本版应用不认识的行与自定义 Agent 保持原样。
-- 自定义 Agent 由用户经技能库添加、修改、删除;内置 Agent 不能修改或删除。
-- 几个 Agent 在同一层级的目录相同(全局比 global_path,项目比 project_path)时共用文件夹:技能装在其中一个的作用域,
-- 共用这个目录的作用域也算已装;这由目录推算,不另存记录。
CREATE TABLE IF NOT EXISTS agents (
    code TEXT PRIMARY KEY NOT NULL, -- Agent 稳定代码,内置如 claude-code / codex,自定义为 custom- 加随机后缀;Scope 和 SkillScope 以此标识 Agent
    name TEXT NOT NULL,             -- Agent 展示名称,如 Claude Code / Codex;不参与 Scope 身份判定
    global_path TEXT NOT NULL,      -- 全局 skills 目录相对用户主目录的路径,斜杠分隔,如 .claude/skills
    project_path TEXT NOT NULL,     -- 项目 skills 目录相对已注册项目根的路径,斜杠分隔,如 .agents/skills
    custom INTEGER NOT NULL DEFAULT 0 CHECK(custom IN (0, 1)), -- 1 = 用户添加的自定义 Agent;0 = 应用内置(含本版应用不认识的)
    detect_path TEXT NOT NULL DEFAULT '' -- 检测这台机器上是否装了该 Agent 时看的目录,相对用户主目录,如 .claude;空 = 不检测(自定义 Agent)
);

-- SkillScope:某 skill 安装在某个 Scope。Scope 只以身份记录——Agent 代码 + 可选的已注册项目,
-- 不存目录;目录在列表和操作时按 agents 表算出。
-- 由直接安装和组安装留着:直接安装(direct)和/或覆盖它的组安装(该 skill 是成员且组已安装到此 Scope,
-- 由 skill_groups × group_installs 推出,不另存);全部撤销后,技能文件夹和这行记录一起删除。
CREATE TABLE IF NOT EXISTS skill_scopes (
    id INTEGER PRIMARY KEY,                 -- 稳定记录 ID
    skill_id INTEGER NOT NULL REFERENCES skills(id), -- 被安装的 skill 主键;此记录不绑定版本,更新不会自动重新安装
    agent TEXT NOT NULL REFERENCES agents(code), -- Agent 的稳定代码
    project_id INTEGER REFERENCES projects(id), -- NULL 为全局 Scope;非 NULL 为该已注册项目的项目 Scope
    direct INTEGER NOT NULL DEFAULT 0,      -- 1 = 有直接安装;0 = 无直接安装,由覆盖它的组安装留着
    folder TEXT NOT NULL DEFAULT '',        -- 装出去的文件夹名;空 = 和技能现在的名字相同。确认改名后没跟着重新安装的地方记下旧名字,巡检、重新安装、卸载、回滚都按它找到文件夹;重新安装换成新名字后清回空
    created_at TEXT NOT NULL DEFAULT ''     -- 首次安装到此 Scope 的时间,RFC3339Nano。再次安装不更新
);

-- NULL 不参与普通 UNIQUE 去重,因此全局与项目身份分别约束。
CREATE UNIQUE INDEX IF NOT EXISTS skill_scopes_global_scope ON skill_scopes(skill_id, agent) WHERE project_id IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS skill_scopes_project_scope ON skill_scopes(skill_id, agent, project_id) WHERE project_id IS NOT NULL;

-- 组安装:某 Group 安装到某 Scope,覆盖该组的当前和未来成员(见 ADR 0006)。空组也可以有组安装。
CREATE TABLE IF NOT EXISTS group_installs (
    id INTEGER PRIMARY KEY,                          -- 稳定记录 ID
    group_id INTEGER NOT NULL REFERENCES groups(id), -- 被安装的分组主键
    agent TEXT NOT NULL REFERENCES agents(code),     -- Agent 的稳定代码
    project_id INTEGER REFERENCES projects(id),      -- NULL 为全局 Scope;非 NULL 为该已注册项目的项目 Scope
    created_at TEXT NOT NULL DEFAULT ''              -- 组安装记录建立(首次组安装)的时间,RFC3339Nano。再次组安装不更新
);

CREATE UNIQUE INDEX IF NOT EXISTS group_installs_global_scope ON group_installs(group_id, agent) WHERE project_id IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS group_installs_project_scope ON group_installs(group_id, agent, project_id) WHERE project_id IS NOT NULL;

-- 集合的历史:每次更新、跳过、回滚各一条。记录里的技能、版本、地方都只记 ID 与身份,不引用 skills / skill_versions / projects / agents:
-- 技能移除、项目注销后记录仍在;地方只记 Agent 与全局还是哪个项目,不记磁盘路径,同步到别的机器也对得上。
CREATE TABLE IF NOT EXISTS update_history (
    id INTEGER PRIMARY KEY,                             -- 记录主键,由 SQLite 分配;按 id 升序即先后顺序
    source_id INTEGER REFERENCES sources(id),           -- 所在集合(来源);NULL = 没有来源的技能的记录(用目录里的修改更新与回滚它)
    kind TEXT NOT NULL CHECK(kind IN ('update', 'skip', 'rollback')), -- update = 更新,skip = 跳过,rollback = 回滚
    revision TEXT NOT NULL DEFAULT '',                  -- 当时来源的来源版本:GitHub 为 commit,URL 为压缩包内容哈希,本地目录与用 Agent 目录里的修改更新时为空;回滚记被回滚那次的
    whole_revision INTEGER NOT NULL DEFAULT 0 CHECK(whole_revision IN (0, 1)), -- 跳过时:1 = 跳过整个来源版本(更新计划里有变化的技能全部跳过),0 = 跳过选中的几个技能
    commits TEXT NOT NULL DEFAULT '',                   -- 更新时:这段时间的提交说明,JSON 数组 [{sha, message, author, time}];只有 GitHub 来源有,其他为空
    rolls_back INTEGER REFERENCES update_history(id),   -- 回滚时:回滚的是哪一次更新;其他为 NULL
    from_agent TEXT NOT NULL DEFAULT '',                -- 用 Agent 目录里的修改更新时:取内容的那一处的 Agent 代码;从来源更新与其他记录为空
    from_project_id INTEGER,                            -- 用 Agent 目录里的修改更新时:取内容的那一处的项目 ID,NULL 为全局
    created_at TEXT NOT NULL DEFAULT ''                 -- 记录时间,RFC3339Nano
);

CREATE UNIQUE INDEX IF NOT EXISTS update_history_rolls_back ON update_history(rolls_back) WHERE rolls_back IS NOT NULL;

-- 记录涉及的技能。更新:从哪个版本换到哪个版本;回滚:从那次更新后的版本换回那次之前的版本;跳过:跳过时的当前版本(两列相同)。
CREATE TABLE IF NOT EXISTS history_skills (
    record_id INTEGER NOT NULL REFERENCES update_history(id), -- 哪一条记录
    skill_id INTEGER NOT NULL,                               -- 技能 ID;技能移除后仍保留
    name TEXT NOT NULL,                                      -- 当时的技能名,技能移除后用于展示;改名的更新记新名字,回滚的记换回的名字
    from_name TEXT NOT NULL DEFAULT '',                      -- 这次换了名字时之前的名字(确认改名的更新、回滚这样的更新);没换名字为空
    from_version_id INTEGER NOT NULL,                        -- 之前的版本 ID
    to_version_id INTEGER NOT NULL,                          -- 之后的版本 ID
    PRIMARY KEY(record_id, skill_id)
);

CREATE INDEX IF NOT EXISTS history_skills_skill ON history_skills(skill_id);

-- 更新或回滚时逐处重新安装的结果;更新之后单独重试的地方也记在那次更新上,覆盖原来的结果。
CREATE TABLE IF NOT EXISTS history_installs (
    record_id INTEGER NOT NULL REFERENCES update_history(id), -- 哪一条记录
    skill_id INTEGER NOT NULL,                               -- 技能 ID
    agent TEXT NOT NULL,                                     -- Agent 的稳定代码
    project_id INTEGER,                                      -- NULL 为全局;非 NULL 为已登记项目的 ID
    outcome TEXT NOT NULL,                                   -- 更新:reinstalled / current / failed(library.ReinstallOutcome);回滚:library.RollbackOutcome 的值。已记为换上了的,之后重试失败不覆盖
    error TEXT NOT NULL DEFAULT ''                           -- 没换上时的错误码(library.ErrorCode);其他为空
);

CREATE UNIQUE INDEX IF NOT EXISTS history_installs_place ON history_installs(record_id, skill_id, agent, ifnull(project_id, 0));

-- 跳过的改动:哪个技能的哪份来源内容,包括用户跳过的和回滚掉的。按内容记录:来源里这个技能的内容又变了,指纹不同,就重新算待办;
-- 同一来源里别的技能变了不影响。随技能一并删除。
CREATE TABLE IF NOT EXISTS skipped_changes (
    record_id INTEGER NOT NULL REFERENCES update_history(id), -- 哪一次跳过或回滚
    skill_id INTEGER NOT NULL REFERENCES skills(id),          -- 跳过的技能
    content TEXT NOT NULL,                                     -- 跳过的那份来源内容的指纹,与 skills.update_check_content 同算法
    PRIMARY KEY(record_id, skill_id)
);

CREATE INDEX IF NOT EXISTS skipped_changes_skill ON skipped_changes(skill_id, content);

-- 用户对改名推测回答了「不是」:这个技能不是改名成了来源里那份内容的新名字。来源里那个新名字的内容又变了,指纹不同,才会再推测;
-- 随技能一并删除。
CREATE TABLE IF NOT EXISTS rejected_renames (
    skill_id INTEGER NOT NULL REFERENCES skills(id), -- 来源里已删除的那个技能
    name TEXT NOT NULL,                              -- 推测的新名字
    content TEXT NOT NULL,                           -- 回答时来源里那个新名字的内容指纹,与 skills.update_check_content 同算法
    PRIMARY KEY(skill_id, name, content)
);

-- 确认过、后来又被回滚掉的改名:技能换回了旧名字,来源里还是新名字。检查更新据此直接推测这个改名(不再看内容是否相似),
-- 用户照样可以在更新计划里勾上再改名。连着回滚几次改名时只记第一次回滚时来源那一侧的名字;再确认改名、回答「不是」,
-- 或检查更新、更新计划、更新确认来源里就是这个技能现在的名字时删掉(只删它们开始读取时就有的那一条)。
-- 随技能一并删除。
CREATE TABLE IF NOT EXISTS rolled_back_renames (
    id INTEGER PRIMARY KEY AUTOINCREMENT,                    -- 这一条记录的身份,不复用;先读后写的操作按它只删开始读取时就有的那一条
    skill_id INTEGER NOT NULL UNIQUE REFERENCES skills(id),  -- 换回了旧名字的技能
    name TEXT NOT NULL                                       -- 回滚掉的那个新名字
);
