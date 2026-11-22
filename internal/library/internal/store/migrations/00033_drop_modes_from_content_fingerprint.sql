CREATE TABLE new_skills (
    id INTEGER PRIMARY KEY AUTOINCREMENT,             -- skill 主键,由 SQLite 分配;跨版本保持不变,移除后也不复用,先读库、联网、再写回的操作据此认出技能
    source_id INTEGER REFERENCES sources(id),         -- 入库时的来源;NULL = 没有来源
    name TEXT NOT NULL,                                -- skill 名,取 SKILL.md frontmatter 的 name
    current_version_id INTEGER REFERENCES skill_versions(id), -- 当前版本;读取和安装都用它。入库事务内先插入 skill,再创建版本并回填,期间可为 NULL
    update_checked_at TEXT NOT NULL DEFAULT '',        -- 最近一次确认来源(检查更新、更新计划、更新或回滚)的时间,包括没读成来源的那次,RFC3339Nano
    -- 最近一次读成来源时的结论:available 有新版本 / current 已是最新 / deleted 来源里已删除;
    -- 空字符串表示入库后还没读成过来源、也从未更新过。随 skill 一并删除
    update_check_status TEXT NOT NULL DEFAULT '' CHECK(update_check_status IN ('', 'available', 'current', 'deleted')),
    -- 1 = 最近一次检查没读成来源(检查失败):给出去的结论是检查失败,不带来源版本、新版本与改名推测;
    -- 结论这几列仍是之前读成的那一次,回滚据此知道来源里现在是哪一份。再读成来源、更新或回滚时清回 0
    update_check_failed INTEGER NOT NULL DEFAULT 0 CHECK(update_check_failed IN (0, 1)),
    update_check_revision TEXT NOT NULL DEFAULT '',    -- 最近一次读成来源时看到的来源版本:GitHub 为 commit,URL 为压缩包内容哈希;本地目录为空
    -- 来源里这个技能现在那份内容的指纹(各文件路径与内容指纹合成,不含可执行位):结论为 available、current 时是这个技能的,
    -- 结论为 deleted 且推测出改名时是新名字那个技能的;其他结论为空,升级前写下的 current 结论也为空。
    -- available 与推测出改名时据此和 skipped_changes 比对是否跳过了这次改动;回滚据此跳过来源里现在的那一份
    update_check_content TEXT NOT NULL DEFAULT '',
    update_check_renamed_to TEXT NOT NULL DEFAULT '', -- 结论为 deleted 时,推测这个技能在来源里改名成的新名字:同一来源里它消失了,同时出现了内容大部分相同的技能;没有推测为空
    kept_serial INTEGER NOT NULL DEFAULT 0,            -- 来源里已删除,用户选了继续保留时为当时的 update_check_serial(大于 0);0 = 没有这个标记。之后开始的检查更新、更新计划或更新确认来源里又有这个技能时清回 0
    update_check_serial INTEGER NOT NULL DEFAULT 0,    -- 每写一次结论(含检查失败)或继续保留标记加 1;检查更新写回前据此发现期间别处已写过,不拿较旧的结论覆盖(别处写下的是检查失败时照样覆盖)
    note TEXT NOT NULL DEFAULT '',                     -- 用户写的备注,只供展示,不进技能文件;空 = 没有备注
    UNIQUE(source_id, name)
);

INSERT INTO new_skills ("id", "source_id", "name", "current_version_id", "update_checked_at", "update_check_status", "update_check_failed", "update_check_revision", "update_check_content", "update_check_renamed_to", "kept_serial", "update_check_serial", "note")
SELECT "id", "source_id", "name", "current_version_id", "update_checked_at", "update_check_status", "update_check_failed", "update_check_revision", "update_check_content", "update_check_renamed_to", "kept_serial", "update_check_serial", "note" FROM skills;

DELETE FROM sqlite_sequence WHERE name = 'new_skills';
INSERT INTO sqlite_sequence (name, seq) SELECT 'new_skills', seq FROM sqlite_sequence WHERE name = 'skills';

DROP TABLE skills;
ALTER TABLE new_skills RENAME TO skills;
