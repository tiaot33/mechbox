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
)

const (
	libraryApplicationID = 1397443650 // "SKLB"
	librarySchemaVersion = 6
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
	case 5:
		return migrateToConfirmedCommit(ctx, db)
	default:
		return fmt.Errorf("%w: unsupported database identity application_id=%d user_version=%d", errUnacceptableSchema, applicationID, userVersion)
	}
}

// legacyActivation 是 v1–v4 库里的一行 Activation:Scope 以绝对路径记录;v3/v4 的部分行已带身份。
type legacyActivation struct {
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

// migrateToConfirmedCommit 把 v5 库升到当前版本:skill_versions 补确认 commit 列。只加列不回填——
// sources.last_seen_ref 是 Source 级的,对多 Skill 仓库不能当作某个版本的确认 commit;空值即"未知,走完整比较"。
func migrateToConfirmedCommit(ctx context.Context, db *sql.DB) error {
	return applySchemaMigration(ctx, db, func(tx *sql.Tx) error {
		statements, err := rebuildSkillVersionsSQL(ctx, "id, skill_id, git_tree_sha")
		if err != nil {
			return err
		}
		for _, statement := range statements {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, librarySchemaVersion))
		return err
	})
}

// rebuildSkillVersionsSQL 按当前 schema 重建 skill_versions,只搬旧库已有的 columns;新列取默认值。
// 不用 ALTER TABLE ADD COLUMN:它会改写 sqlite_master 里的建表文本,与 schema.sql 的指纹对不上。
func rebuildSkillVersionsSQL(ctx context.Context, columns string) ([]string, error) {
	createSQL, err := canonicalTableSQL(ctx, "skill_versions")
	if err != nil {
		return nil, err
	}
	return []string{
		`CREATE TABLE skill_versions_migrate AS SELECT ` + columns + ` FROM skill_versions`,
		`DROP TABLE skill_versions`, createSQL,
		`INSERT INTO skill_versions(` + columns + `) SELECT ` + columns + ` FROM skill_versions_migrate`,
		`DROP TABLE skill_versions_migrate`,
	}, nil
}

// migrateToScopeIdentity 在一个事务内把 v1–v4 库升到当前版本:重建 skill_versions 补齐 v1 缺的 tree SHA 列与
// v6 新增的确认 commit 列,v3 补 Agent 名称,然后把 Activation 改为只存 Scope 身份。路径型行能识别为某 Agent 的
// 全局或某已登记项目的写成身份,映射到同一身份的多行合并依据;无法识别的行丢弃。磁盘上任何目录都不动(见 ADR 0011)。
func migrateToScopeIdentity(ctx context.Context, db *sql.DB, version int, home string) error {
	return applySchemaMigration(ctx, db, func(tx *sql.Tx) error {
		legacy, err := readLegacyActivations(ctx, tx, version)
		if err != nil {
			return err
		}
		statements := []string{`DROP TABLE activation_groups`, `DROP TABLE activations`}
		versionColumns := "id, skill_id, git_tree_sha"
		if version == 1 {
			versionColumns = "id, skill_id"
		}
		rebuild, err := rebuildSkillVersionsSQL(ctx, versionColumns)
		if err != nil {
			return err
		}
		statements = append(statements, rebuild...)
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
		// 能识别的路径行按身份写入;同一身份的多行由 recordActivationTx 合并独立标记与分组依据。
		for _, row := range legacy {
			scope, ok := identifyLegacyScope(row, layouts, projects, home)
			if !ok {
				continue
			}
			if err := recordActivationTx(ctx, tx, row.skillID, scope, row.individual, 0); err != nil {
				return err
			}
			for _, groupID := range row.groups {
				if err := recordActivationTx(ctx, tx, row.skillID, scope, false, groupID); err != nil {
					return err
				}
			}
		}
		_, err = tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, librarySchemaVersion))
		return err
	})
}

// readLegacyActivations 读出旧表里的全部 Activation 及其分组依据;v1/v2 没有身份列,一律按路径型处理。
func readLegacyActivations(ctx context.Context, tx *sql.Tx, version int) ([]legacyActivation, error) {
	query := `SELECT skill_id, agent, scope_path, individual, 'legacy', NULL FROM activations`
	if version >= 3 {
		query = `SELECT skill_id, agent, scope_path, individual, scope_kind, project_id FROM activations`
	}
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	var activations []legacyActivation
	byPath := map[legacyKey]*legacyActivation{}
	for rows.Next() {
		var (
			row       legacyActivation
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
		activations = append(activations, row)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	for i := range activations {
		byPath[activations[i].key()] = &activations[i]
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
		if activation, ok := byPath[key]; ok {
			activation.groups = append(activation.groups, groupID)
		}
	}
	if err := errors.Join(groups.Err(), groups.Close()); err != nil {
		return nil, err
	}
	return activations, nil
}

// legacyKey 是旧表里唯一的 (skill_id, agent, scope_path) 三元组,用来把分组依据挂回对应行。
type legacyKey struct {
	skillID SkillID
	agent   AgentCode
	path    string
}

func (row legacyActivation) key() legacyKey {
	return legacyKey{skillID: row.skillID, agent: row.agent, path: row.path}
}

// identifyLegacyScope 把一行旧 Activation 认成 Scope 身份:已带身份的直接沿用;路径型的路径须等于该 Agent
// 现行布局(或本产品写过的旧目录名)在当前用户主目录或某已登记项目根下算出的目录,全局优先。
// 未知 Agent 或对不上任何目录的行无法识别。
func identifyLegacyScope(row legacyActivation, layouts map[AgentCode]agentLayout, projects []RegisteredProject, home string) (scopeRow, bool) {
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
	expectedDB, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		return "", err
	}
	defer expectedDB.Close()
	if _, err := expectedDB.ExecContext(ctx, schemaSQL); err != nil {
		return "", err
	}
	var createSQL string
	if err := expectedDB.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&createSQL); err != nil {
		return "", err
	}
	return createSQL, nil
}
