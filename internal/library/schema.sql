CREATE TABLE IF NOT EXISTS sources (
    id INTEGER PRIMARY KEY,
    type TEXT NOT NULL,
    locator TEXT NOT NULL,
    last_fetched TEXT NOT NULL DEFAULT '',
    last_seen_ref TEXT NOT NULL DEFAULT '',
    http_validator TEXT NOT NULL DEFAULT '',
    UNIQUE(type, locator)
);

CREATE TABLE IF NOT EXISTS skills (
    id INTEGER PRIMARY KEY,
    source_id INTEGER NOT NULL REFERENCES sources(id),
    name TEXT NOT NULL,
    current_version_id INTEGER REFERENCES skill_versions(id),
    update_checked_at TEXT NOT NULL DEFAULT '',
    update_check_status TEXT NOT NULL DEFAULT '' CHECK(update_check_status IN ('', 'available', 'current', 'failed')),
    UNIQUE(source_id, name)
);

CREATE TABLE IF NOT EXISTS skill_versions (
    id INTEGER PRIMARY KEY,
    skill_id INTEGER NOT NULL REFERENCES skills(id),
    git_tree_sha TEXT NOT NULL DEFAULT '',
    confirmed_commit TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS blobs (
    fingerprint TEXT PRIMARY KEY,
    content BLOB NOT NULL
);

CREATE TABLE IF NOT EXISTS version_files (
    version_id INTEGER NOT NULL REFERENCES skill_versions(id),
    path TEXT NOT NULL,
    fingerprint TEXT NOT NULL REFERENCES blobs(fingerprint),
    mode TEXT NOT NULL CHECK(mode IN ('regular', 'executable')),
    PRIMARY KEY(version_id, path)
);

CREATE TABLE IF NOT EXISTS groups (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS skill_groups (
    skill_id INTEGER NOT NULL REFERENCES skills(id),
    group_id INTEGER NOT NULL REFERENCES groups(id),
    PRIMARY KEY(skill_id, group_id)
);

CREATE TABLE IF NOT EXISTS projects (
    id INTEGER PRIMARY KEY,
    path TEXT NOT NULL UNIQUE
);

CREATE TABLE IF NOT EXISTS agents (
    code TEXT PRIMARY KEY NOT NULL,
    name TEXT NOT NULL,
    global_path TEXT NOT NULL,
    project_path TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS skill_scopes (
    id INTEGER PRIMARY KEY,
    skill_id INTEGER NOT NULL REFERENCES skills(id),
    agent TEXT NOT NULL REFERENCES agents(code),
    project_id INTEGER REFERENCES projects(id),
    direct INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX IF NOT EXISTS skill_scopes_global_scope ON skill_scopes(skill_id, agent) WHERE project_id IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS skill_scopes_project_scope ON skill_scopes(skill_id, agent, project_id) WHERE project_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS group_installs (
    id INTEGER PRIMARY KEY,
    group_id INTEGER NOT NULL REFERENCES groups(id),
    agent TEXT NOT NULL REFERENCES agents(code),
    project_id INTEGER REFERENCES projects(id),
    created_at TEXT NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX IF NOT EXISTS group_installs_global_scope ON group_installs(group_id, agent) WHERE project_id IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS group_installs_project_scope ON group_installs(group_id, agent, project_id) WHERE project_id IS NOT NULL;
