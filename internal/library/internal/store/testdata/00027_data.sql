INSERT INTO replica (id, token) VALUES (1, 'REPLICA27');

INSERT INTO sources (id, type, locator, listed_revision, listed_serial) VALUES
    (1, 'github', '{"owner":"acme","repo":"skills","ref":"main"}', 'rev-2', 2),
    (2, 'local', '{"path":"/home/me/skills"}', '', 0);

INSERT INTO source_skills (source_id, name, new) VALUES
    (1, 'alpha', 0),
    (1, 'gamma', 1);

INSERT INTO skills (id, source_id, name, current_version_id, update_checked_at, update_check_status, update_check_revision, update_check_content, update_check_renamed_to, kept_serial, update_check_serial, note) VALUES
    (1, 1, 'alpha', 2, '2026-11-01T08:00:00Z', 'available', 'rev-3', 'content-alpha-3', '', 0, 3, '常用'),
    (2, 2, 'beta', 3, '2026-11-01T08:00:00Z', 'deleted', '', '', 'beta-renamed', 2, 2, ''),
    (3, 1, 'removed', NULL, '', '', '', '', '', 0, 0, '');
DELETE FROM skills WHERE id = 3;

INSERT INTO description_translations (skill_id, language, description, translation, translated_at) VALUES
    (1, 'zh', 'Alpha skill', 'Alpha 技能', '2026-11-01T09:00:00Z');

INSERT INTO skill_versions (id, skill_id, source_state, revision, description, created_at, base_version_id, maybe_edited) VALUES
    (1, 1, '{"tree":"tree-1","commit":"rev-1"}', 'rev-1', 'Alpha skill', '2026-10-01T00:00:00Z', NULL, 0),
    (2, 1, '{"tree":"tree-2","commit":"rev-2"}', 'rev-2', 'Alpha skill', '2026-10-02T00:00:00Z', 1, 1),
    (3, 2, '', '', 'Beta skill', '2026-10-03T00:00:00Z', NULL, 0),
    (4, 2, '', '', 'Beta skill draft', '2026-10-04T00:00:00Z', 3, 0);
DELETE FROM skill_versions WHERE id = 4;

INSERT INTO blobs (fingerprint, content) VALUES
    ('fp-skill', X'2320416C7068610A'),
    ('fp-script', X'23212F62696E2F73680A6563686F206869');

INSERT INTO version_files (version_id, path, fingerprint, mode) VALUES
    (1, 'SKILL.md', 'fp-skill', 'regular'),
    (2, 'SKILL.md', 'fp-skill', 'regular'),
    (2, 'scripts/run.sh', 'fp-script', 'executable'),
    (3, 'SKILL.md', 'fp-skill', 'regular');

INSERT INTO groups (id, name, created_at) VALUES
    (1, '工作', '2026-10-05T00:00:00Z');

INSERT INTO skill_groups (skill_id, group_id) VALUES
    (1, 1),
    (2, 1);

INSERT INTO projects (id, path) VALUES
    (1, '/home/me/app'),
    (2, '/home/me/old');
DELETE FROM projects WHERE id = 2;

INSERT INTO agents (code, name, global_path, project_path, custom, detect_path) VALUES
    ('claude-code', 'Claude Code', '.claude/skills', '.claude/skills', 0, '.claude'),
    ('codex', 'Codex', '.agents/skills', '.agents/skills', 0, '.codex'),
    ('my-agent', 'My Agent', '.my-agent/skills', '.my-agent/skills', 1, '');

INSERT INTO skill_scopes (id, skill_id, agent, project_id, direct, folder, created_at) VALUES
    (1, 1, 'claude-code', NULL, 1, '', '2026-10-06T00:00:00Z'),
    (2, 2, 'codex', 1, 0, '/home/me/app/.agents/skills/beta', '2026-10-06T00:00:00Z');

INSERT INTO group_installs (id, group_id, agent, project_id, created_at) VALUES
    (1, 1, 'my-agent', NULL, '2026-10-07T00:00:00Z');

INSERT INTO update_history (id, source_id, kind, revision, whole_revision, commits, rolls_back, from_agent, from_project_id, created_at) VALUES
    (1, 1, 'update', 'rev-2', 1, '[{"sha":"rev-2"}]', NULL, '', NULL, '2026-10-08T00:00:00Z'),
    (2, 1, 'rollback', '', 0, '', 1, '', NULL, '2026-10-09T00:00:00Z'),
    (3, 1, 'skip', 'rev-3', 0, '', NULL, 'codex', 1, '2026-10-10T00:00:00Z');

INSERT INTO history_skills (record_id, skill_id, name, from_name, from_version_id, to_version_id) VALUES
    (1, 1, 'alpha', '', 1, 2),
    (2, 1, 'alpha', '', 2, 1);

INSERT INTO history_installs (record_id, skill_id, agent, project_id, outcome, error) VALUES
    (1, 1, 'claude-code', NULL, 'installed', ''),
    (1, 1, 'codex', 1, 'failed', 'permission denied');

INSERT INTO skipped_changes (record_id, skill_id, content) VALUES
    (3, 1, 'content-alpha-3');

INSERT INTO rejected_renames (skill_id, name, content) VALUES
    (1, 'alpha-two', 'content-alpha-3');

INSERT INTO rolled_back_renames (id, skill_id, name) VALUES
    (1, 1, 'alpha-next'),
    (2, 2, 'beta-next');
DELETE FROM rolled_back_renames WHERE id = 2;

INSERT INTO config (key, value) VALUES
    ('github_token', 'ghp_fixture'),
    ('app.language', 'zh'),
    ('ai.model', 'fixture-model'),
    ('sync.bucket', 'fixture-bucket');
