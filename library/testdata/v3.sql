-- 冻结的 user_version=3 schema,用于测试 Open 迁移。
-- Library 的 SQLite 表结构,由 library.Open 嵌入执行。
-- 建表后 Open 会设置 PRAGMA application_id 和 user_version,作为库文件的身份标识。
-- user_version=3 增加 Agent 布局与 Scope 身份;Open 将已有 v1/v2 库迁移到当前版本。

-- skill 的安装来源。
CREATE TABLE IF NOT EXISTS sources (
    id INTEGER PRIMARY KEY,
    type TEXT NOT NULL,                     -- 来源类型:github / url / store / local
    locator TEXT NOT NULL,                  -- 定位信息,JSON(仓库+Git ref / URL / 包ID+版本 / 本地路径)
    last_fetched TEXT NOT NULL DEFAULT '',  -- 上次抓取时间,RFC3339Nano;空串 = 从未抓取(本地来源恒为空)
    last_seen_ref TEXT NOT NULL DEFAULT '', -- 上次看到的远端标识(git commit 或压缩包哈希),用于判断有无更新
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
CREATE TABLE IF NOT EXISTS skill_versions (
    id INTEGER PRIMARY KEY,                          -- 自增;同一 skill 内按 id 升序即版本先后顺序
    skill_id INTEGER NOT NULL REFERENCES skills(id),
    git_tree_sha TEXT NOT NULL DEFAULT ''            -- GitHub 来源该 Skill 目录的 git tree SHA;非 git 为空
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
CREATE TABLE IF NOT EXISTS groups (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL
);

-- skill 与分组的多对多关系:一个 skill 可以同时属于多个组。
CREATE TABLE IF NOT EXISTS skill_groups (
    skill_id INTEGER NOT NULL REFERENCES skills(id),
    group_id INTEGER NOT NULL REFERENCES groups(id),
    PRIMARY KEY(skill_id, group_id)
);

-- 已注册的项目目录;注册后其 .claude/skills 与 .codex/skills 成为可投影的 Scope。
CREATE TABLE IF NOT EXISTS projects (
    id INTEGER PRIMARY KEY,
    path TEXT NOT NULL UNIQUE -- 项目根目录的绝对路径(已解析符号链接)
);

-- Agent 的稳定代码与布局;规则分别相对用户主目录和已注册项目根,不提供修改门面。
CREATE TABLE IF NOT EXISTS agents (
    code TEXT PRIMARY KEY NOT NULL,
    global_path TEXT NOT NULL,
    project_path TEXT NOT NULL
);

-- 投影记录:某 skill 被复制进了某个 agent 的某个 skills 目录。
-- 一条投影可能同时有多个保留依据:独立激活(individual)和/或若干个分组(见 activation_groups);
-- 全部依据都撤销后,磁盘副本和这行记录一起删除。
CREATE TABLE IF NOT EXISTS activations (
    id INTEGER PRIMARY KEY,                 -- 稳定记录 ID,分组依据不再以路径作为主键
    skill_id INTEGER NOT NULL REFERENCES skills(id),
    agent TEXT NOT NULL REFERENCES agents(code), -- Agent 的稳定代码
    project_id INTEGER REFERENCES projects(id), -- NULL 为全局;项目身份引用已注册项目
    scope_kind TEXT NOT NULL DEFAULT 'legacy' CHECK(scope_kind IN ('legacy', 'global', 'project')),
    scope_path TEXT NOT NULL,               -- expand 期间保留的路径;旧 Activate 仍按它操作
    individual INTEGER NOT NULL DEFAULT 0,  -- 1 = 存在独立(非分组)激活
    CHECK((scope_kind = 'project' AND project_id IS NOT NULL) OR (scope_kind <> 'project' AND project_id IS NULL)),
    UNIQUE(skill_id, agent, scope_path),     -- 兼容旧路径型 Activate 的 upsert
    UNIQUE(id, skill_id, agent, scope_path)  -- 过渡期确保分组依据的旧字段与记录 ID 一致
);

-- NULL 不参与普通 UNIQUE 去重,因此全局与项目身份分别约束;legacy 行留待 contract 迁移。
CREATE UNIQUE INDEX IF NOT EXISTS activations_global_scope ON activations(skill_id, agent) WHERE scope_kind = 'global';
CREATE UNIQUE INDEX IF NOT EXISTS activations_project_scope ON activations(skill_id, agent, project_id) WHERE scope_kind = 'project';

-- 投影的分组依据:由 activation_id 关联 Skill + Agent + 可选 ProjectID;投影删除时级联清理。
CREATE TABLE IF NOT EXISTS activation_groups (
    activation_id INTEGER NOT NULL,
    skill_id INTEGER NOT NULL,
    agent TEXT NOT NULL,
    scope_path TEXT NOT NULL,               -- expand 期间保留的查询字段
    group_id INTEGER NOT NULL REFERENCES groups(id),
    PRIMARY KEY(activation_id, group_id),
    FOREIGN KEY(activation_id, skill_id, agent, scope_path)
        REFERENCES activations(id, skill_id, agent, scope_path) ON DELETE CASCADE
);
