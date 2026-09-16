-- Library 的 SQLite 表结构,由 library.Open 嵌入执行。
-- 建表后 Open 会设置 PRAGMA application_id 和 user_version,作为库文件的身份标识。
-- user_version=8 让版本携带 description / created_at、激活与分组记录时间、Group 名唯一
-- (见 ADR 0005 / 0007);Open 将已有 v1–v7 库迁移到当前版本。

-- skill 的安装来源。
CREATE TABLE IF NOT EXISTS sources (
    id INTEGER PRIMARY KEY,
    type TEXT NOT NULL,                     -- 来源类型:github / url / store / local
    locator TEXT NOT NULL,                  -- 定位信息,JSON(仓库+Git ref / URL / 包ID+版本 / 本地路径)
    last_fetched TEXT NOT NULL DEFAULT '',  -- 上次抓取时间,RFC3339Nano;空串 = 从未抓取(本地来源恒为空)
    last_seen_ref TEXT NOT NULL DEFAULT '', -- 上次看到的远端标识(git commit 或压缩包哈希);Source 级展示信息,
                                            -- 同来源下各 Skill 共享,更新判定走 skill_versions.confirmed_commit / http_validator
    http_validator TEXT NOT NULL DEFAULT '',-- URL / Store 上次成功抓取时服务端给出的 HTTP validator 原文
                                            -- (优先 ETag,否则合法 HTTP-date 的 Last-Modified);检查与更新据它发条件请求,
                                            -- 能解析为日期的填 If-Modified-Since、其余填 If-None-Match,304 即无更新。
                                            -- 服务端未给出、GitHub / local 来源恒为空 = 不带条件头
    UNIQUE(type, locator)                   -- 同一个来源只记录一行
);

-- 库中的 skill;同一来源下名字唯一。
CREATE TABLE IF NOT EXISTS skills (
    id INTEGER PRIMARY KEY,
    source_id INTEGER NOT NULL REFERENCES sources(id), -- 安装时的来源
    name TEXT NOT NULL,                                -- skill 名(远程取 frontmatter name,本地安装取目录名)
    current_version_id INTEGER REFERENCES skill_versions(id), -- 当前版本;读取和投影都用它
    UNIQUE(source_id, name)
);

-- skill 的历史版本;本身只是一个版本号,文件清单在 version_files。
-- git_tree_sha 与 confirmed_commit 一起构成该版本的抓取记录:目录内容是什么、在哪个 commit 上确认过它是远端最新。
-- description 与 created_at 属版本内容:描述随 SKILL.md 变化,时间是该快照写入 Library 的时刻。
CREATE TABLE IF NOT EXISTS skill_versions (
    id INTEGER PRIMARY KEY,                          -- 自增;同一 skill 内按 id 升序即版本先后顺序
    skill_id INTEGER NOT NULL REFERENCES skills(id),
    git_tree_sha TEXT NOT NULL DEFAULT '',           -- GitHub 来源该 Skill 目录的 git tree SHA;非 git 为空
    confirmed_commit TEXT NOT NULL DEFAULT '',       -- GitHub 来源该目录 tree 最后一次被确认为远端最新时所在的 commit;
                                                     -- 检查时远端 commit 与之相同即无更新、不取 tree。非 git 或迁移前的旧版本为空 = 未知,走完整比较
    description TEXT NOT NULL DEFAULT '',            -- 该版本 SKILL.md frontmatter 的 description;解析不到为空
    created_at TEXT NOT NULL DEFAULT ''              -- 版本写入时间,RFC3339Nano;空 = 未知(迁移前的旧版本)
);

-- 内容寻址的文件内容存储:指纹是内容的 sha256,相同内容跨版本、跨 skill 只存一份。
CREATE TABLE IF NOT EXISTS blobs (
    fingerprint TEXT PRIMARY KEY, -- 内容 sha256 的十六进制
    content BLOB NOT NULL         -- 文件原始内容
);

-- 某个版本包含哪些文件:路径 → 内容指纹 + 文件模式。
CREATE TABLE IF NOT EXISTS version_files (
    version_id INTEGER NOT NULL REFERENCES skill_versions(id),
    path TEXT NOT NULL,                                       -- skill 内的相对路径,斜杠分隔
    fingerprint TEXT NOT NULL REFERENCES blobs(fingerprint),  -- 指向 blobs 里的内容
    mode TEXT NOT NULL CHECK(mode IN ('regular', 'executable')), -- 投影时据此设置可执行位
    PRIMARY KEY(version_id, path)
);

-- 手动创建的场景分组;与按来源自动形成的 Collection 相对(见 ADR 0007)。
-- 名字 UNIQUE:界面按名字挑选,重名无法区分;写入时重名自动改为 name#N。
CREATE TABLE IF NOT EXISTS groups (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL DEFAULT '' -- 创建时间,RFC3339Nano;空 = 未知
);

-- skill 与分组的多对多关系:一个 skill 可以同时属于多个组。
CREATE TABLE IF NOT EXISTS skill_groups (
    skill_id INTEGER NOT NULL REFERENCES skills(id),
    group_id INTEGER NOT NULL REFERENCES groups(id),
    PRIMARY KEY(skill_id, group_id)
);

-- 已注册的项目目录;注册后与每个 Agent 组成项目 Scope,目录由 agents.project_path 算出。
CREATE TABLE IF NOT EXISTS projects (
    id INTEGER PRIMARY KEY,
    path TEXT NOT NULL UNIQUE -- 项目根目录的绝对路径(已解析符号链接)
);

-- Agent 的稳定代码、展示名称与布局;规则分别相对用户主目录和已注册项目根,不提供修改门面。
CREATE TABLE IF NOT EXISTS agents (
    code TEXT PRIMARY KEY NOT NULL,
    name TEXT NOT NULL,
    global_path TEXT NOT NULL,
    project_path TEXT NOT NULL
);

-- 投影记录:某 skill 出现在某个 Scope 里。Scope 只以身份记录——Agent 代码 + 可选的已注册项目,
-- 不存目录;目录在列表和操作时按 agents 表的现行布局算出。
-- 一条投影可能同时有多个保留依据:独立激活(individual)和/或若干个分组(见 activation_groups);
-- 全部依据都撤销后,磁盘副本和这行记录一起删除。
CREATE TABLE IF NOT EXISTS activations (
    id INTEGER PRIMARY KEY,                 -- 稳定记录 ID,分组依据以它关联
    skill_id INTEGER NOT NULL REFERENCES skills(id),
    agent TEXT NOT NULL REFERENCES agents(code), -- Agent 的稳定代码
    project_id INTEGER REFERENCES projects(id), -- NULL 为全局 Scope;非 NULL 为该已注册项目的项目 Scope
    individual INTEGER NOT NULL DEFAULT 0,  -- 1 = 存在独立(非分组)激活
    activated_at TEXT NOT NULL DEFAULT ''   -- 首次记录时间,RFC3339Nano;空 = 未知。刷新不更新
);

-- NULL 不参与普通 UNIQUE 去重,因此全局与项目身份分别约束。
CREATE UNIQUE INDEX IF NOT EXISTS activations_global_scope ON activations(skill_id, agent) WHERE project_id IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS activations_project_scope ON activations(skill_id, agent, project_id) WHERE project_id IS NOT NULL;

-- 投影的分组依据;投影删除时级联清理。
CREATE TABLE IF NOT EXISTS activation_groups (
    activation_id INTEGER NOT NULL REFERENCES activations(id) ON DELETE CASCADE,
    group_id INTEGER NOT NULL REFERENCES groups(id),
    PRIMARY KEY(activation_id, group_id)
);
