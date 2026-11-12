CREATE TABLE new_groups (
    id INTEGER PRIMARY KEY,            -- 分组主键,由 SQLite 分配;成员关系和组安装均引用它
    name TEXT NOT NULL UNIQUE,          -- 分组展示名,去除首尾空白且全库唯一;重名时追加 #N(N 从 2 起取最小空闲值)
    created_at TEXT NOT NULL DEFAULT '', -- 创建时间,RFC3339Nano
    position INTEGER NOT NULL DEFAULT 0  -- 用户拖出来的组先后,从 1 起,小的在前;0 = 没排过(新建的组、恢复默认顺序后),排在排过的后面,按 id
);

INSERT INTO new_groups ("id", "name", "created_at")
SELECT "id", "name", "created_at" FROM groups;

DROP TABLE groups;
ALTER TABLE new_groups RENAME TO groups;

CREATE TABLE new_sources (
    id INTEGER PRIMARY KEY,                -- 来源主键,由 SQLite 分配;同一来源可包含多个 skill
    type TEXT NOT NULL,                     -- 来源类型:github / url / local
    locator TEXT NOT NULL,                  -- 定位信息,JSON(仓库+Git ref / URL / 本地路径)
    listed_revision TEXT NOT NULL DEFAULT '', -- 最近一次按整个来源记下 source_skills 时的来源版本;检查更新看到同一个来源版本时不再重记。本地目录来源为空,每次都重记
    listed_serial INTEGER NOT NULL DEFAULT 0,  -- 每重记一次 source_skills 加 1;检查更新写回前据此发现期间别处已重记过,不拿较旧的名单覆盖
    position INTEGER NOT NULL DEFAULT 0,    -- 用户拖出来的集合先后,从 1 起,小的在前;0 = 没排过(新来源、恢复默认顺序后),排在排过的后面,按 id
    UNIQUE(type, locator)                   -- 同一个来源只记录一行
);

INSERT INTO new_sources ("id", "type", "locator", "listed_revision", "listed_serial")
SELECT "id", "type", "locator", "listed_revision", "listed_serial" FROM sources;

DROP TABLE sources;
ALTER TABLE new_sources RENAME TO sources;
