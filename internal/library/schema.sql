-- Library 的 SQLite 表结构,由 library.Open 嵌入执行。
-- 建表后 Open 会设置 PRAGMA application_id 和 user_version,作为库文件的身份标识。
-- 产品尚未发布,不迁移旧版本库:Open 只接受当前 user_version 的库。

-- skill 的来源。
CREATE TABLE IF NOT EXISTS sources (
    id INTEGER PRIMARY KEY,                -- 来源主键,由 SQLite 分配;同一来源可包含多个 skill
    type TEXT NOT NULL,                     -- 来源类型:github / url / store / local
    locator TEXT NOT NULL,                  -- 定位信息,JSON(仓库+Git ref / URL / 包ID+版本 / 本地路径)
    last_fetched TEXT NOT NULL DEFAULT '',  -- 来源内容最近一次被入库或经更新确认的时间,RFC3339Nano;空串 = 从未读取
    last_seen_ref TEXT NOT NULL DEFAULT '', -- 上次看到的远端标识(git commit 或压缩包哈希);Source 级展示信息,
                                            -- 同来源下各 Skill 共享,更新判定走 skill_versions.confirmed_commit / http_validator
    http_validator TEXT NOT NULL DEFAULT '',-- URL / Store 上次成功读取时服务端给出的 HTTP validator 原文
                                            -- (优先 ETag,否则合法 HTTP-date 的 Last-Modified);检查与更新据它发条件请求,
                                            -- 能解析为日期的填 If-Modified-Since、其余填 If-None-Match,304 即无更新。
                                            -- 服务端未给出、GitHub / local 来源恒为空 = 不带条件头
    UNIQUE(type, locator)                   -- 同一个来源只记录一行
);

-- 库中的 skill;同一来源下名字唯一。
CREATE TABLE IF NOT EXISTS skills (
    id INTEGER PRIMARY KEY,                           -- skill 主键,由 SQLite 分配;跨版本保持不变
    source_id INTEGER NOT NULL REFERENCES sources(id), -- 入库时的来源
    name TEXT NOT NULL,                                -- skill 名,取 SKILL.md frontmatter 的 name
    current_version_id INTEGER REFERENCES skill_versions(id), -- 当前版本;读取和安装都用它。入库事务内先插入 skill,再创建版本并回填,期间可为 NULL
    update_checked_at TEXT NOT NULL DEFAULT '',        -- 最近检查或成功 Update 的确认时间,RFC3339Nano;用于防止较早发出的检查回写
    update_check_status TEXT NOT NULL DEFAULT '' CHECK(update_check_status IN ('', 'available', 'current', 'failed')), -- 最近结论;随 skill 一并删除
    UNIQUE(source_id, name)
);

-- skill 的历史版本及快照元数据;文件清单在 version_files。
-- git_tree_sha 与 confirmed_commit 一起构成该版本的来源读取记录:目录内容是什么、在哪个 commit 上确认过它是远端最新。
-- description 与 created_at 属版本内容:描述随 SKILL.md 变化,时间是该快照写入 Library 的时刻。
CREATE TABLE IF NOT EXISTS skill_versions (
    id INTEGER PRIMARY KEY,                          -- 版本主键,由 SQLite 分配;同一 skill 内按 id 升序即版本先后顺序
    skill_id INTEGER NOT NULL REFERENCES skills(id), -- 此版本所属的 skill,关联 skills.id
    git_tree_sha TEXT NOT NULL DEFAULT '',           -- GitHub 来源该 Skill 目录的 git tree SHA;非 GitHub 来源或尚未记录时为空
    confirmed_commit TEXT NOT NULL DEFAULT '',       -- GitHub 来源该目录 tree 最后一次被确认为远端最新时所在的 commit;查时远端 commit 与之相同即无更新、不取 tree。非 git 或尚未记录时为空 = 未知,走完整比较
    description TEXT NOT NULL DEFAULT '',            -- 该版本 SKILL.md frontmatter 的 description;解析不到为空
    created_at TEXT NOT NULL DEFAULT ''              -- 版本写入时间,RFC3339Nano
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

-- 手动创建的场景分组;与按来源自动形成的 Collection 相对(见 ADR 0007)。
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
CREATE TABLE IF NOT EXISTS projects (
    id INTEGER PRIMARY KEY,  -- 已注册项目主键,由 SQLite 分配;用作 ProjectScope 的项目身份
    path TEXT NOT NULL UNIQUE -- 项目根目录的绝对路径(已解析符号链接)
);

-- Agent 的稳定代码、展示名称与布局;规则分别相对用户主目录和已注册项目根,技能库不提供修改方法。
CREATE TABLE IF NOT EXISTS agents (
    code TEXT PRIMARY KEY NOT NULL, -- Agent 稳定代码,如 claude-code / codex;Scope 和安装项记录以此标识 Agent
    name TEXT NOT NULL,             -- Agent 展示名称,如 Claude Code / Codex;不参与 Scope 身份判定
    global_path TEXT NOT NULL,      -- 全局 skills 目录相对用户主目录的路径,斜杠分隔,如 .claude/skills
    project_path TEXT NOT NULL      -- 项目 skills 目录相对已注册项目根的路径,斜杠分隔,如 .agents/skills
);

-- 安装项记录:某 skill 出现在某个 Scope 里。Scope 只以身份记录——Agent 代码 + 可选的已注册项目,
-- 不存目录;目录在列表和操作时按 agents 表的现行布局算出。
-- 一个安装项由若干安装撑着:直接安装(direct)和/或覆盖它的组安装(该 skill 是成员且组已安装到此 Scope,
-- 由 skill_groups × group_installs 推出,不另存);全部安装都撤销后,安装项文件夹和这行记录一起删除。
CREATE TABLE IF NOT EXISTS installations (
    id INTEGER PRIMARY KEY,                 -- 稳定记录 ID
    skill_id INTEGER NOT NULL REFERENCES skills(id), -- 被安装的 skill 主键;此记录不绑定版本,更新不会自动重新安装
    agent TEXT NOT NULL REFERENCES agents(code), -- Agent 的稳定代码
    project_id INTEGER REFERENCES projects(id), -- NULL 为全局 Scope;非 NULL 为该已注册项目的项目 Scope
    direct INTEGER NOT NULL DEFAULT 0,      -- 1 = 有直接安装;0 = 无直接安装,由覆盖它的组安装保留安装项
    created_at TEXT NOT NULL DEFAULT ''     -- 安装项建立(首次安装)的时间,RFC3339Nano。再次安装不更新
);

-- NULL 不参与普通 UNIQUE 去重,因此全局与项目身份分别约束。
CREATE UNIQUE INDEX IF NOT EXISTS installations_global_scope ON installations(skill_id, agent) WHERE project_id IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS installations_project_scope ON installations(skill_id, agent, project_id) WHERE project_id IS NOT NULL;

-- 组安装:某 Group 安装到某 Scope,覆盖该组的当前和未来成员(见 ADR 0007)。空组也可以有组安装。
CREATE TABLE IF NOT EXISTS group_installs (
    id INTEGER PRIMARY KEY,                          -- 稳定记录 ID
    group_id INTEGER NOT NULL REFERENCES groups(id), -- 被安装的分组主键
    agent TEXT NOT NULL REFERENCES agents(code),     -- Agent 的稳定代码
    project_id INTEGER REFERENCES projects(id),      -- NULL 为全局 Scope;非 NULL 为该已注册项目的项目 Scope
    installed_at TEXT NOT NULL DEFAULT ''            -- 首次组安装时间,RFC3339Nano。再次组安装不更新
);

CREATE UNIQUE INDEX IF NOT EXISTS group_installs_global_scope ON group_installs(group_id, agent) WHERE project_id IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS group_installs_project_scope ON group_installs(group_id, agent, project_id) WHERE project_id IS NOT NULL;
