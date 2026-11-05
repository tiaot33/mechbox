CREATE TABLE mention_judgments (
    version_id INTEGER NOT NULL REFERENCES skill_versions(id), -- 正文里出现这个名字的技能版本
    name TEXT NOT NULL,                                       -- 出现的单个词的技能名,区分大小写
    mentioned INTEGER NOT NULL CHECK(mentioned IN (0, 1)),    -- 1 = AI 判断为在指那个技能,关系图上画线;0 = 只是普通的词
    judged_at TEXT NOT NULL DEFAULT '',                       -- 判断时间,RFC3339Nano
    PRIMARY KEY(version_id, name)
);
