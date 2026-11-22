CREATE TABLE new_agents (
    code TEXT PRIMARY KEY NOT NULL, -- Agent 稳定代码:应用收录的如 claude-code / cursor,用户手填添加的为 custom- 加随机后缀;Scope 和 SkillScope 以此标识 Agent
    name TEXT NOT NULL,             -- Agent 展示名称,如 Claude Code / Cursor;不参与 Scope 身份判定
    global_paths TEXT NOT NULL CHECK(json_valid(global_paths) AND json_type(global_paths) = 'array' AND json_array_length(global_paths) >= 1),   -- 全局技能目录 JSON 数组(各项相对用户主目录,斜杠分隔):第 1 项装新技能,其余为附加目录
    project_paths TEXT NOT NULL CHECK(json_valid(project_paths) AND json_type(project_paths) = 'array' AND json_array_length(project_paths) >= 1), -- 项目技能目录 JSON 数组(各项相对已登记项目根,斜杠分隔):第 1 项装新技能,其余为附加目录
    detect_paths TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(detect_paths) AND json_type(detect_paths) = 'array'), -- 检测这台机器上是否装了该 Agent 时看的目录 JSON 数组(各项相对用户主目录,任一存在即算检测到);[] = 不检测
    same_name TEXT NOT NULL DEFAULT '' CHECK(same_name IN ('', 'global_wins', 'both_shown')), -- 全局和项目里有同名技能时的已知规则;'' = 不知道,只标出同名
    enabled INTEGER NOT NULL DEFAULT 0 CHECK(enabled IN (0, 1))  -- 1 = 已添加在用;0 = 在候选池里
);

INSERT INTO new_agents ("code", "name", "global_paths", "project_paths", "detect_paths", "same_name", "enabled")
SELECT "code", "name", json_array("global_path"), json_array("project_path"),
    CASE WHEN "detect_path" = '' THEN '[]' ELSE json_array("detect_path") END,
    CASE "code" WHEN 'claude-code' THEN 'global_wins' WHEN 'codex' THEN 'both_shown' ELSE '' END,
    1
FROM agents;

DROP TABLE agents;
ALTER TABLE new_agents RENAME TO agents;

CREATE TABLE new_skill_scopes (
    id INTEGER PRIMARY KEY,                 -- 稳定记录 ID
    skill_id INTEGER NOT NULL REFERENCES skills(id), -- 被安装的 skill 主键;此记录不绑定版本,更新不会自动重新安装
    agent TEXT NOT NULL REFERENCES agents(code), -- Agent 的稳定代码
    project_id INTEGER REFERENCES projects(id), -- NULL 为全局 Scope;非 NULL 为该已注册项目的项目 Scope
    direct INTEGER NOT NULL DEFAULT 0,      -- 1 = 有直接安装;0 = 无直接安装,由覆盖它的组安装留着
    directory TEXT NOT NULL DEFAULT '',     -- 这处安装实际所在的技能目录(相对用户主目录或项目根);空 = 该 Agent 这一层级的第 1 个目录
    folder TEXT NOT NULL DEFAULT '',        -- 装出去的文件夹名;空 = 和技能现在的名字相同。确认改名后没跟着重新安装的地方记下旧名字,巡检、重新安装、卸载、回滚都按它找到文件夹;重新安装换成新名字后清回空
    created_at TEXT NOT NULL DEFAULT ''     -- 首次安装到此 Scope 的时间,RFC3339Nano。再次安装不更新
);

INSERT INTO new_skill_scopes ("id", "skill_id", "agent", "project_id", "direct", "folder", "created_at")
SELECT "id", "skill_id", "agent", "project_id", "direct", "folder", "created_at" FROM skill_scopes;

DROP TABLE skill_scopes;
ALTER TABLE new_skill_scopes RENAME TO skill_scopes;

CREATE UNIQUE INDEX IF NOT EXISTS skill_scopes_global_scope ON skill_scopes(skill_id, agent) WHERE project_id IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS skill_scopes_project_scope ON skill_scopes(skill_id, agent, project_id) WHERE project_id IS NOT NULL;
