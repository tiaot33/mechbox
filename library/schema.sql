-- Canonical SQLite v1 table schema, embedded by library.Open.
-- Open sets PRAGMA application_id and user_version after creating these tables.

CREATE TABLE IF NOT EXISTS sources (
    id INTEGER PRIMARY KEY,
    type TEXT NOT NULL,
    locator TEXT NOT NULL,
    last_fetched TEXT NOT NULL DEFAULT '',
    last_seen_ref TEXT NOT NULL DEFAULT '',
    UNIQUE(type, locator)
);

CREATE TABLE IF NOT EXISTS skills (
    id INTEGER PRIMARY KEY,
    source_id INTEGER NOT NULL REFERENCES sources(id),
    name TEXT NOT NULL,
    current_version_id INTEGER REFERENCES skill_versions(id),
    UNIQUE(source_id, name)
);

CREATE TABLE IF NOT EXISTS skill_versions (
    id INTEGER PRIMARY KEY,
    skill_id INTEGER NOT NULL REFERENCES skills(id)
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
    name TEXT NOT NULL
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

CREATE TABLE IF NOT EXISTS activations (
    skill_id INTEGER NOT NULL REFERENCES skills(id),
    agent TEXT NOT NULL,
    scope_path TEXT NOT NULL,
    individual INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY(skill_id, agent, scope_path)
);

CREATE TABLE IF NOT EXISTS activation_groups (
    skill_id INTEGER NOT NULL,
    agent TEXT NOT NULL,
    scope_path TEXT NOT NULL,
    group_id INTEGER NOT NULL REFERENCES groups(id),
    PRIMARY KEY(skill_id, agent, scope_path, group_id),
    FOREIGN KEY(skill_id, agent, scope_path)
        REFERENCES activations(skill_id, agent, scope_path) ON DELETE CASCADE
);
