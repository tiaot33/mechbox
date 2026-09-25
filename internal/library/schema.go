package library

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

const (
	libraryApplicationID = 1397443650 // "SKLB"
	librarySchemaVersion = 9
	agentSeedSQL         = `
		INSERT INTO agents(code, name, global_path, project_path) VALUES
			('claude-code', 'Claude Code', '.claude/skills', '.claude/skills'),
			('codex', 'Codex', '.agents/skills', '.agents/skills')
		ON CONFLICT(code) DO NOTHING
	`
)

//go:embed schema.sql
var schemaSQL string

// seedAgents 只补缺失代码,已有名称与布局即使不同也不覆盖;库校验失败则撤销补种子。
func seedAgents(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, agentSeedSQL); err != nil {
		return err
	}
	if err := validateLibrarySchema(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

// schemaReader 让 Open/Sync 的连接校验与迁移提交前的事务校验共用同一套规则。
type schemaReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// errUnacceptableSchema 只标记已确认的库校验失败,不包含本地 I/O 或取消。
var errUnacceptableSchema = errors.New("unacceptable library schema")

func validateLibrarySchema(ctx context.Context, db schemaReader) error {
	var applicationID, userVersion int
	if err := db.QueryRowContext(ctx, `PRAGMA application_id`).Scan(&applicationID); err != nil {
		return err
	}
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&userVersion); err != nil {
		return err
	}
	if applicationID != libraryApplicationID || userVersion != librarySchemaVersion {
		return fmt.Errorf("%w: unsupported database identity application_id=%d user_version=%d", errUnacceptableSchema, applicationID, userVersion)
	}
	actual, err := schemaFingerprint(ctx, db)
	if err != nil {
		return err
	}
	expectedDB, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		return err
	}
	defer expectedDB.Close()
	if _, err := expectedDB.ExecContext(ctx, schemaSQL); err != nil {
		return err
	}
	expected, err := schemaFingerprint(ctx, expectedDB)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("%w: schema fingerprint mismatch", errUnacceptableSchema)
	}
	return validateForeignKeys(ctx, db)
}

func validateForeignKeys(ctx context.Context, db schemaReader) error {
	rows, err := db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return fmt.Errorf("%w: foreign key check failed", errUnacceptableSchema)
	}
	return rows.Err()
}

func schemaFingerprint(ctx context.Context, db schemaReader) (string, error) {
	rows, err := db.QueryContext(ctx, `SELECT name, sql FROM sqlite_master WHERE type IN ('table', 'index') AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	hash := sha256.New()
	for rows.Next() {
		var name, statement string
		if err := rows.Scan(&name, &statement); err != nil {
			return "", err
		}
		_, _ = fmt.Fprintf(hash, "%s\n%s\n", name, strings.Join(strings.Fields(statement), " "))
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// migrateLibrary 把已有库升到当前 schema;身份不符则不改文件。
func migrateLibrary(ctx context.Context, db *sql.DB, home string) error {
	var applicationID, userVersion int
	if err := db.QueryRowContext(ctx, `PRAGMA application_id`).Scan(&applicationID); err != nil {
		return err
	}
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&userVersion); err != nil {
		return err
	}
	if applicationID != libraryApplicationID {
		return fmt.Errorf("%w: unsupported database identity application_id=%d user_version=%d", errUnacceptableSchema, applicationID, userVersion)
	}
	switch userVersion {
	case librarySchemaVersion:
		return nil
	case 1, 2, 3, 4:
		return migrateToScopeIdentity(ctx, db, userVersion, home)
	case 5, 6:
		return migrateFetchRecords(ctx, db, userVersion)
	case 7:
		return migrateReadModel(ctx, db)
	case 8:
		return migrateGroupActivations(ctx, db)
	default:
		return fmt.Errorf("%w: unsupported database identity application_id=%d user_version=%d", errUnacceptableSchema, applicationID, userVersion)
	}
}

// legacyProjection 是 v1–v4 库里的一条投影记录:Scope 以绝对路径记录;v3/v4 的部分行已带身份。
type legacyProjection struct {
	skillID    SkillID
	agent      AgentCode
	path       string
	individual bool
	identity   *scopeRow // v3/v4 已按身份记录的行;路径型为 nil
	groups     []GroupID
}

// legacyLayouts 是本产品在 Scope 改为身份之前写入磁盘的目录名(只有 Codex 与现行不同)。
// 仅供迁移识别旧路径;Resolve 与投影一律用 agents 表的现行规则。
var legacyLayouts = map[AgentCode]agentLayout{
	AgentCodex: {global: ".codex/skills", project: ".codex/skills"},
}

// legacySourceColumns 是 v1–v6 库 sources 表已有的列;v7 新增的 http_validator 取默认空值。
const legacySourceColumns = "id, type, locator, last_fetched, last_seen_ref"

// migrateFetchRecords 把 v5 / v6 库升到当前版本:v5 先给 skill_versions 补确认 commit 列,两者都给 sources 补
// HTTP validator 列。只加列不回填——sources.last_seen_ref 是 Source 级的,对多 Skill 仓库不能当作某个版本的
// 确认 commit,空值即"未知,走完整比较";validator 只能由服务端给出,空值即"不带条件头"。
func migrateFetchRecords(ctx context.Context, db *sql.DB, version int) error {
	return applySchemaMigration(ctx, db, func(tx *sql.Tx) error {
		var statements []string
		if version == 5 {
			rebuild, err := rebuildTableSQL(ctx, "skill_versions", "id, skill_id, git_tree_sha")
			if err != nil {
				return err
			}
			statements = append(statements, rebuild)
		}
		rebuild, err := rebuildTableSQL(ctx, "sources", legacySourceColumns)
		if err != nil {
			return err
		}
		statements = append(statements, rebuild)
		for _, statement := range statements {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
		if err := upgradeToReadModel(ctx, tx); err != nil {
			return err
		}
		if err := migrateProjectionRecords(ctx, tx, false); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, librarySchemaVersion))
		return err
	})
}

// migrateReadModel 把 v7 库升到当前版本:补描述、时间列与 Group 名 UNIQUE,时间不回填。
func migrateReadModel(ctx context.Context, db *sql.DB) error {
	return applySchemaMigration(ctx, db, func(tx *sql.Tx) error {
		if err := upgradeToReadModel(ctx, tx); err != nil {
			return err
		}
		if err := migrateProjectionRecords(ctx, tx, false); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, librarySchemaVersion))
		return err
	})
}

// migrateGroupActivations 把 v8 库升到当前版本:投影记录改到 projections 表,组激活成为正式记录。
func migrateGroupActivations(ctx context.Context, db *sql.DB) error {
	return applySchemaMigration(ctx, db, func(tx *sql.Tx) error {
		if err := migrateProjectionRecords(ctx, tx, true); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, librarySchemaVersion))
		return err
	})
}

// migrateProjectionRecords 把 v5–v8 的 activations / activation_groups 搬到 projections / group_activations:
// 投影保留原 ID;组激活按去重的 Group × Scope 回填,时间未知留空;withActivatedAt 为 false 时旧表还没有
// 时间列,投影时间也留空。之后按 backfillDirectActivations 补直接激活。磁盘不动。
func migrateProjectionRecords(ctx context.Context, tx *sql.Tx, withActivatedAt bool) error {
	activatedAt := "''"
	if withActivatedAt {
		activatedAt = "activated_at"
	}
	statements := []string{
		schemaSQL,
		`INSERT INTO projections(id, skill_id, agent, project_id, direct, activated_at)
		 SELECT id, skill_id, agent, project_id, individual, ` + activatedAt + ` FROM activations`,
		`INSERT INTO group_activations(group_id, agent, project_id)
		 SELECT DISTINCT g.group_id, a.agent, a.project_id FROM activation_groups g JOIN activations a ON a.id = g.activation_id
		 ORDER BY g.group_id, a.agent, a.project_id`,
		`DROP TABLE activation_groups`,
		`DROP TABLE activations`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return backfillDirectActivations(ctx, tx)
}

// backfillDirectActivations 让迁移后不再被任何激活撑着的投影改为直接激活。旧库只把组挂在投影上,
// 成员移出组后投影仍挂着它;改为由成员关系推出后这类投影会失去全部激活,按移出成员的默认 keep
// 规则转为直接激活,不丢投影。
func backfillDirectActivations(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `UPDATE projections SET direct = 1 WHERE direct = 0 AND NOT `+groupCoverageSQL, 0); err != nil {
		return fmt.Errorf("backfill direct activations: %w", err)
	}
	return nil
}

// rebuildTableSQL 按当前 schema 重建 name 表,只搬旧库已有的 columns;新列取默认值。
// 不用 ALTER TABLE ADD COLUMN:它会改写 sqlite_master 里的建表文本,与 schema.sql 的指纹对不上。
func rebuildTableSQL(ctx context.Context, name, columns string) (string, error) {
	createSQL, err := canonicalTableSQL(ctx, name)
	if err != nil {
		return "", err
	}
	indexes, err := canonicalIndexSQL(ctx, name)
	if err != nil {
		return "", err
	}
	return strings.Join(append([]string{
		`CREATE TABLE ` + name + `_migrate AS SELECT ` + columns + ` FROM ` + name,
		`DROP TABLE ` + name, createSQL,
		`INSERT INTO ` + name + `(` + columns + `) SELECT ` + columns + ` FROM ` + name + `_migrate`,
		`DROP TABLE ` + name + `_migrate`,
	}, indexes...), ";\n"), nil
}

// migrateToScopeIdentity 在一个事务内把 v1–v4 库升到当前版本:重建 skill_versions 补齐 v1 缺的 tree SHA 列与
// v6 新增的确认 commit 列,重建 sources 补 v7 新增的 HTTP validator 列,v3 补 Agent 名称,然后把投影记录改为
// 只存 Scope 身份。路径型行能识别为某 Agent 的全局或某已登记项目的写成身份,映射到同一身份的多行合并各自的激活;
// 无法识别的行丢弃。磁盘上任何目录都不动(见 ADR 0011)。
func migrateToScopeIdentity(ctx context.Context, db *sql.DB, version int, home string) error {
	return applySchemaMigration(ctx, db, func(tx *sql.Tx) error {
		legacy, err := readLegacyProjections(ctx, tx, version)
		if err != nil {
			return err
		}
		statements := []string{`DROP TABLE activation_groups`, `DROP TABLE activations`}
		versionColumns := "id, skill_id, git_tree_sha"
		if version == 1 {
			versionColumns = "id, skill_id"
		}
		for _, table := range [][2]string{{"skill_versions", versionColumns}, {"sources", legacySourceColumns}} {
			rebuild, err := rebuildTableSQL(ctx, table[0], table[1])
			if err != nil {
				return err
			}
			statements = append(statements, rebuild)
		}
		if version == 3 {
			createSQL, err := canonicalTableSQL(ctx, "agents")
			if err != nil {
				return err
			}
			statements = append(statements,
				`CREATE TABLE agents_migrate AS SELECT code, global_path, project_path FROM agents`,
				`DROP TABLE agents`, createSQL,
				agentSeedSQL,
				// 先用种子提供已知 Agent 的名称,再还原旧规则;其他已存代码暂以代码作为名称。
				`INSERT INTO agents(code, name, global_path, project_path)
				 SELECT code, code, global_path, project_path FROM agents_migrate WHERE true
				 ON CONFLICT(code) DO UPDATE SET global_path = excluded.global_path, project_path = excluded.project_path`,
				`DROP TABLE agents_migrate`,
			)
		}
		statements = append(statements, schemaSQL, agentSeedSQL)
		for _, statement := range statements {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
		layouts, err := readAgentLayouts(ctx, tx)
		if err != nil {
			return err
		}
		projects, err := readProjects(ctx, tx)
		if err != nil {
			return err
		}
		// 能识别的路径行按身份写入;同一身份的多行合并直接激活标记,组激活按 Group × Scope 去重。
		for _, row := range legacy {
			scope, ok := identifyLegacyScope(row, layouts, projects, home)
			if !ok {
				continue
			}
			if err := recordLegacyProjectionTx(ctx, tx, row, scope); err != nil {
				return err
			}
		}
		if err := upgradeToReadModel(ctx, tx); err != nil {
			return err
		}
		if err := backfillDirectActivations(ctx, tx); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, librarySchemaVersion))
		return err
	})
}

// upgradeToReadModel 把描述、时间列与 Group 名 UNIQUE 补到当前 schema 的 groups 与 skill_versions;
// 投影时间列随 migrateProjectionRecords 搬表时补上。
func upgradeToReadModel(ctx context.Context, tx *sql.Tx) error {
	if err := uniquifyGroupNames(ctx, tx); err != nil {
		return err
	}
	rebuild, err := rebuildTableSQL(ctx, "groups", "id, name")
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, rebuild); err != nil {
		return err
	}
	rebuild, err = rebuildTableSQL(ctx, "skill_versions", "id, skill_id, git_tree_sha, confirmed_commit")
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, rebuild); err != nil {
		return err
	}
	return backfillVersionDescriptions(ctx, tx)
}

// recordLegacyProjectionTx 把一条已认出 Scope 的旧投影写成当前记录:同一身份已有投影时合并直接激活标记;
// 旧行挂着的每个组都记为该组在此 Scope 的组激活(时间未知)。
func recordLegacyProjectionTx(ctx context.Context, tx *sql.Tx, row legacyProjection, scope scopeRow) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO projections(skill_id, agent, project_id, direct) VALUES (?, ?, ?, ?)
		ON CONFLICT DO UPDATE SET direct = MAX(direct, excluded.direct)`, row.skillID, scope.agent, scope.projectID, row.individual); err != nil {
		return fmt.Errorf("record legacy projection: %w", err)
	}
	for _, groupID := range row.groups {
		if err := recordGroupActivationTx(ctx, tx, groupID, scope, time.Time{}); err != nil {
			return err
		}
	}
	return nil
}

// uniquifyGroupNames 按 id 顺序保留每个名字的第一个 Group,其余改为 name#N(N 从 2 起最小空闲)。
func uniquifyGroupNames(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT id, name FROM groups ORDER BY id`)
	if err != nil {
		return err
	}
	var groups []Group
	for rows.Next() {
		var group Group
		if err := rows.Scan(&group.ID, &group.Name); err != nil {
			_ = rows.Close()
			return err
		}
		groups = append(groups, group)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	taken := map[string]GroupID{}
	for _, group := range groups {
		name := uniqueGroupName(group.Name, taken)
		taken[name] = group.ID
		if name == group.Name {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE groups SET name = ? WHERE id = ?`, name, group.ID); err != nil {
			return err
		}
	}
	return nil
}

// backfillVersionDescriptions 从已有 SKILL.md blob 解析 description 写回版本行;失败留空。
func backfillVersionDescriptions(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT skill_versions.id, blobs.content
		FROM version_files
		JOIN blobs ON blobs.fingerprint = version_files.fingerprint
		JOIN skill_versions ON skill_versions.id = version_files.version_id
		WHERE version_files.path = 'SKILL.md'
	`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id      VersionID
			content []byte
		)
		if err := rows.Scan(&id, &content); err != nil {
			return err
		}
		meta, err := candidateMetadata(content, ".")
		if err != nil {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE skill_versions SET description = ? WHERE id = ?`, meta.Description, id); err != nil {
			return err
		}
	}
	return rows.Err()
}

// readLegacyProjections 读出旧表里的全部投影记录及其组激活;v1/v2 没有身份列,一律按路径型处理。
func readLegacyProjections(ctx context.Context, tx *sql.Tx, version int) ([]legacyProjection, error) {
	query := `SELECT skill_id, agent, scope_path, individual, 'legacy', NULL FROM activations`
	if version >= 3 {
		query = `SELECT skill_id, agent, scope_path, individual, scope_kind, project_id FROM activations`
	}
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	var projections []legacyProjection
	byPath := map[legacyKey]*legacyProjection{}
	for rows.Next() {
		var (
			row       legacyProjection
			kind      string
			projectID *ProjectID
		)
		if err := rows.Scan(&row.skillID, &row.agent, &row.path, &row.individual, &kind, &projectID); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if kind != "legacy" {
			row.identity = &scopeRow{agent: row.agent, projectID: projectID}
		}
		projections = append(projections, row)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	for i := range projections {
		byPath[projections[i].key()] = &projections[i]
	}
	// 各版本的 activation_groups 都带 (skill_id, agent, scope_path),而该三元组在旧表里唯一。
	groups, err := tx.QueryContext(ctx, `SELECT skill_id, agent, scope_path, group_id FROM activation_groups`)
	if err != nil {
		return nil, err
	}
	for groups.Next() {
		var (
			key     legacyKey
			groupID GroupID
		)
		if err := groups.Scan(&key.skillID, &key.agent, &key.path, &groupID); err != nil {
			_ = groups.Close()
			return nil, err
		}
		if projection, ok := byPath[key]; ok {
			projection.groups = append(projection.groups, groupID)
		}
	}
	if err := errors.Join(groups.Err(), groups.Close()); err != nil {
		return nil, err
	}
	return projections, nil
}

// legacyKey 是旧表里唯一的 (skill_id, agent, scope_path) 三元组,用来把组激活挂回对应行。
type legacyKey struct {
	skillID SkillID
	agent   AgentCode
	path    string
}

func (row legacyProjection) key() legacyKey {
	return legacyKey{skillID: row.skillID, agent: row.agent, path: row.path}
}

// identifyLegacyScope 把一条旧投影记录认成 Scope 身份:已带身份的直接沿用;路径型的路径须等于该 Agent
// 现行布局(或本产品写过的旧目录名)在当前用户主目录或某已登记项目根下算出的目录,全局优先。
// 未知 Agent 或对不上任何目录的行无法识别。
func identifyLegacyScope(row legacyProjection, layouts map[AgentCode]agentLayout, projects []RegisteredProject, home string) (scopeRow, bool) {
	if row.identity != nil {
		return *row.identity, true
	}
	layout, ok := layouts[row.agent]
	if !ok {
		return scopeRow{}, false
	}
	candidates := []agentLayout{layout}
	if old, ok := legacyLayouts[row.agent]; ok {
		candidates = append(candidates, old)
	}
	for _, candidate := range candidates {
		if sameLibraryPath(row.path, candidate.globalDir(home)) {
			return scopeRow{agent: row.agent}, true
		}
		for _, project := range projects {
			if sameLibraryPath(row.path, candidate.projectDir(project.Path)) {
				return scopeRow{agent: row.agent, projectID: new(project.ID)}, true
			}
		}
	}
	return scopeRow{}, false
}

// sameLibraryPath 判断旧库记下的路径与布局算出的目录是否词法等价(忽略末尾 / 与 /.)。
func sameLibraryPath(stored, computed string) bool {
	return filepath.Clean(stored) == filepath.Clean(computed)
}

func readAgentLayouts(ctx context.Context, tx *sql.Tx) (map[AgentCode]agentLayout, error) {
	rows, err := tx.QueryContext(ctx, `SELECT code, global_path, project_path FROM agents`)
	if err != nil {
		return nil, err
	}
	layouts := map[AgentCode]agentLayout{}
	for rows.Next() {
		var (
			code   AgentCode
			layout agentLayout
		)
		if err := rows.Scan(&code, &layout.global, &layout.project); err != nil {
			_ = rows.Close()
			return nil, err
		}
		layouts[code] = layout
	}
	return layouts, errors.Join(rows.Err(), rows.Close())
}

func readProjects(ctx context.Context, tx *sql.Tx) ([]RegisteredProject, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, path FROM projects`)
	if err != nil {
		return nil, err
	}
	var projects []RegisteredProject
	for rows.Next() {
		var project RegisteredProject
		if err := rows.Scan(&project.ID, &project.Path); err != nil {
			_ = rows.Close()
			return nil, err
		}
		projects = append(projects, project)
	}
	return projects, errors.Join(rows.Err(), rows.Close())
}

// applySchemaMigration 在关闭外键、开启事务的前提下执行 migrate,提交前按当前 schema 校验;
// 任一步失败则整体回滚,库文件保持迁移前的内容。
func applySchemaMigration(ctx context.Context, db *sql.DB, migrate func(*sql.Tx) error) error {
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return fmt.Errorf("migrate library schema: %w", err)
	}
	defer db.ExecContext(ctx, `PRAGMA foreign_keys = ON`)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migrate library schema: %w", err)
	}
	defer tx.Rollback()
	if err := migrate(tx); err != nil {
		return fmt.Errorf("migrate library schema: %w", err)
	}
	if err := validateLibrarySchema(ctx, tx); err != nil {
		return fmt.Errorf("migrate library schema: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migrate library schema: %w", err)
	}
	return nil
}

func canonicalTableSQL(ctx context.Context, name string) (string, error) {
	expectedDB, err := openCanonicalSchema(ctx)
	if err != nil {
		return "", err
	}
	defer expectedDB.Close()
	var createSQL string
	if err := expectedDB.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&createSQL); err != nil {
		return "", err
	}
	return createSQL, nil
}

// canonicalIndexSQL 取出当前 schema 里建在 name 表上的显式索引(不含 sqlite_autoindex)。
// DROP TABLE 会丢掉这些索引,重建表后必须一并建回,否则指纹对不上。
func canonicalIndexSQL(ctx context.Context, name string) ([]string, error) {
	expectedDB, err := openCanonicalSchema(ctx)
	if err != nil {
		return nil, err
	}
	defer expectedDB.Close()
	rows, err := expectedDB.QueryContext(ctx, `SELECT sql FROM sqlite_master WHERE type = 'index' AND tbl_name = ? AND sql IS NOT NULL ORDER BY name`, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var statements []string
	for rows.Next() {
		var sqlText string
		if err := rows.Scan(&sqlText); err != nil {
			return nil, err
		}
		statements = append(statements, sqlText)
	}
	return statements, rows.Err()
}

func openCanonicalSchema(ctx context.Context) (*sql.DB, error) {
	expectedDB, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		return nil, err
	}
	if _, err := expectedDB.ExecContext(ctx, schemaSQL); err != nil {
		_ = expectedDB.Close()
		return nil, err
	}
	return expectedDB, nil
}
