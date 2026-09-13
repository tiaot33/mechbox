package library

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const (
	libraryApplicationID = 1397443650 // "SKLB"
	librarySchemaVersion = 2
)

// fetchTTL 是一次 Fetch 暂存被后端回收前的存活时间;超过后视为被放弃。
const fetchTTL = 30 * time.Minute

//go:embed schema.sql
var schemaSQL string

type fetchedSkill struct {
	name       string
	preview    FetchSkill
	files      []File
	gitTreeSHA string       // GitHub 来源该 Skill 目录的 tree SHA;其他来源为空
	blobs      []githubBlob // GitHub 快路径:安装时按 blob SHA 取所选文件
}

// fetchSession 是一次 Fetch 抓取的内存暂存:来源规格 + 看到的远端标识 + 全部候选 skill。
// 存活于进程内存,不落库、不参与 Sync;由后续安装、Close 或 TTL 过期回收。
type fetchSession struct {
	source      Source
	ref         string
	skills      []fetchedSkill
	diagnostics []CandidateDiagnostic
	timer       *time.Timer
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

type sqliteLibrary struct {
	db *sql.DB
	mu sync.RWMutex

	fetchMu sync.Mutex
	fetches map[FetchID]*fetchSession
	closed  bool
}

type libraryOperationKey struct{}

func (l *sqliteLibrary) readOperation(ctx context.Context) (context.Context, func()) {
	if ctx.Value(libraryOperationKey{}) == l {
		return ctx, func() {}
	}
	l.mu.RLock()
	return context.WithValue(ctx, libraryOperationKey{}, l), l.mu.RUnlock
}

// Open 打开已有 Library 或在 path 初始化新库。已有 v1 库会迁移到当前 schema。
func Open(path string) (Library, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("open library: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("configure library: %w", err)
	}
	var tableCount int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`).Scan(&tableCount); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("inspect library: %w", err)
	}
	if tableCount > 0 {
		if err := migrateLibrary(context.Background(), db); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("open library: %w", err)
		}
		if err := validateLibrarySchema(context.Background(), db); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("open library: %w", err)
		}
		return &sqliteLibrary{db: db, fetches: map[FetchID]*fetchSession{}}, nil
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize library: %w", err)
	}
	if _, err := db.Exec(fmt.Sprintf(`PRAGMA application_id = %d; PRAGMA user_version = %d`, libraryApplicationID, librarySchemaVersion)); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("identify library: %w", err)
	}
	return &sqliteLibrary{db: db, fetches: map[FetchID]*fetchSession{}}, nil
}

// migrateLibrary 把已有库升到当前 schema;身份不符则不改文件。
func migrateLibrary(ctx context.Context, db *sql.DB) error {
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
	case 1:
		return migrateLibraryV1ToV2(ctx, db)
	default:
		return fmt.Errorf("%w: unsupported database identity application_id=%d user_version=%d", errUnacceptableSchema, applicationID, userVersion)
	}
}

// migrateLibraryV1ToV2 给 skill_versions 补上 git_tree_sha,并把 sqlite_master 写成与当前 schema.sql 一致。
func migrateLibraryV1ToV2(ctx context.Context, db *sql.DB) error {
	createSQL, err := canonicalTableSQL(ctx, "skill_versions")
	if err != nil {
		return fmt.Errorf("migrate library schema: %w", err)
	}
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return fmt.Errorf("migrate library schema: %w", err)
	}
	defer db.ExecContext(ctx, `PRAGMA foreign_keys = ON`)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migrate library schema: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE skill_versions_migrate (id INTEGER PRIMARY KEY, skill_id INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("migrate library schema: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO skill_versions_migrate(id, skill_id) SELECT id, skill_id FROM skill_versions`); err != nil {
		return fmt.Errorf("migrate library schema: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE skill_versions`); err != nil {
		return fmt.Errorf("migrate library schema: %w", err)
	}
	if _, err := tx.ExecContext(ctx, createSQL); err != nil {
		return fmt.Errorf("migrate library schema: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO skill_versions(id, skill_id, git_tree_sha) SELECT id, skill_id, '' FROM skill_versions_migrate`); err != nil {
		return fmt.Errorf("migrate library schema: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE skill_versions_migrate`); err != nil {
		return fmt.Errorf("migrate library schema: %w", err)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, librarySchemaVersion)); err != nil {
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

func (l *sqliteLibrary) CreateGroup(ctx context.Context, name string) (group Group, err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if strings.TrimSpace(name) == "" {
		return Group{}, fmt.Errorf("create group: name is required")
	}
	result, err := l.db.ExecContext(ctx, `INSERT INTO groups(name) VALUES (?)`, name)
	if err != nil {
		return Group{}, fmt.Errorf("create group: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Group{}, fmt.Errorf("read group id: %w", err)
	}
	return Group{ID: GroupID(id), Name: name}, nil
}

func (l *sqliteLibrary) ListGroups(ctx context.Context) (groups []Group, err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	rows, err := l.db.QueryContext(ctx, `SELECT id, name FROM groups ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list groups: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var group Group
		if err := rows.Scan(&group.ID, &group.Name); err != nil {
			return nil, fmt.Errorf("read group: %w", err)
		}
		groups = append(groups, group)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read groups: %w", err)
	}
	return groups, nil
}

func (l *sqliteLibrary) RenameGroup(ctx context.Context, id GroupID, name string) (err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("rename group: name is required")
	}
	result, err := l.db.ExecContext(ctx, `UPDATE groups SET name = ? WHERE id = ?`, name, id)
	if err != nil {
		return fmt.Errorf("rename group: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rename group: %w", err)
	}
	if changed == 0 {
		return fmt.Errorf("rename group: %w", sql.ErrNoRows)
	}
	return nil
}

func (l *sqliteLibrary) DeleteGroup(ctx context.Context, id GroupID) (err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete group: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE activations SET individual = 1
		WHERE individual = 0
			AND EXISTS (
				SELECT 1 FROM activation_groups
				WHERE activation_groups.skill_id = activations.skill_id
					AND activation_groups.agent = activations.agent
					AND activation_groups.scope_path = activations.scope_path
					AND activation_groups.group_id = ?
			)
			AND NOT EXISTS (
				SELECT 1 FROM activation_groups
				WHERE activation_groups.skill_id = activations.skill_id
					AND activation_groups.agent = activations.agent
					AND activation_groups.scope_path = activations.scope_path
					AND activation_groups.group_id <> ?
			)`, id, id); err != nil {
		return fmt.Errorf("preserve group activations: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM activation_groups WHERE group_id = ?`, id); err != nil {
		return fmt.Errorf("delete group activations: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM skill_groups WHERE group_id = ?`, id); err != nil {
		return fmt.Errorf("delete group members: %w", err)
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM groups WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete group: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete group: %w", err)
	}
	if changed == 0 {
		return fmt.Errorf("delete group: %w", sql.ErrNoRows)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete group: %w", err)
	}
	return nil
}

func (l *sqliteLibrary) AddSkillToGroup(ctx context.Context, groupID GroupID, skillID SkillID) (err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if err := l.requireGroup(ctx, groupID); err != nil {
		return err
	}
	if err := l.requireSkill(ctx, skillID); err != nil {
		return err
	}
	if _, err := l.db.ExecContext(ctx, `INSERT INTO skill_groups(skill_id, group_id) VALUES (?, ?)`, skillID, groupID); err != nil {
		return fmt.Errorf("add skill to group: %w", err)
	}
	return nil
}

func (l *sqliteLibrary) RemoveSkillFromGroup(ctx context.Context, groupID GroupID, skillID SkillID) (err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	result, err := l.db.ExecContext(ctx, `DELETE FROM skill_groups WHERE group_id = ? AND skill_id = ?`, groupID, skillID)
	if err != nil {
		return fmt.Errorf("remove skill from group: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("remove skill from group: %w", err)
	}
	if changed == 0 {
		return fmt.Errorf("remove skill from group: %w", sql.ErrNoRows)
	}
	return nil
}

func (l *sqliteLibrary) ListGroupSkills(ctx context.Context, groupID GroupID) (skills []SkillSummary, err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if err := l.requireGroup(ctx, groupID); err != nil {
		return nil, err
	}
	rows, err := l.db.QueryContext(ctx, `SELECT skill_id FROM skill_groups WHERE group_id = ? ORDER BY skill_id`, groupID)
	if err != nil {
		return nil, fmt.Errorf("list group skills: %w", err)
	}
	var ids []SkillID
	for rows.Next() {
		var id SkillID
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("read group skill: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close group skills: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read group skills: %w", err)
	}

	// ponytail: one lookup per member; replace with a joined mapper if large groups make this measurable.
	skills = make([]SkillSummary, 0, len(ids))
	for _, id := range ids {
		summary, err := l.getSummary(ctx, id)
		if err != nil {
			return nil, err
		}
		skills = append(skills, summary)
	}
	return skills, nil
}

// getSummary 读取一个 skill 的身份、名字与来源元数据,不含文件正文。
func (l *sqliteLibrary) getSummary(ctx context.Context, id SkillID) (SkillSummary, error) {
	var summary SkillSummary
	var locator []byte
	var fetched string
	err := l.db.QueryRowContext(ctx, `
		SELECT skills.id, skills.name, sources.id, sources.type, sources.locator, sources.last_fetched, sources.last_seen_ref
		FROM skills JOIN sources ON sources.id = skills.source_id
		WHERE skills.id = ?
	`, id).Scan(&summary.ID, &summary.Name, &summary.Source.ID, &summary.Source.Type, &locator, &fetched, &summary.Source.LastSeenRef)
	if err != nil {
		return SkillSummary{}, fmt.Errorf("get skill: %w", err)
	}
	if err := json.Unmarshal(locator, &summary.Source.Locator); err != nil {
		return SkillSummary{}, fmt.Errorf("decode source: %w", err)
	}
	if fetched != "" {
		summary.Source.LastFetched, err = time.Parse(time.RFC3339Nano, fetched)
		if err != nil {
			return SkillSummary{}, fmt.Errorf("decode last fetched: %w", err)
		}
	}
	return summary, nil
}

func (l *sqliteLibrary) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fetchMu.Lock()
	l.closed = true
	for _, session := range l.fetches {
		session.timer.Stop()
	}
	clear(l.fetches)
	l.fetchMu.Unlock()
	return l.db.Close()
}

func (l *sqliteLibrary) Install(ctx context.Context, source LocalSource) (skill Skill, err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	root, err := filepath.Abs(source.Path)
	if err != nil {
		return Skill{}, fmt.Errorf("resolve source path: %w", err)
	}
	files, err := readFiles(ctx, root)
	if err != nil {
		return Skill{}, err
	}
	return l.install(ctx, Source{Type: SourceLocal, Locator: SourceLocator{Path: root}}, filepath.Base(root), files)
}

func (l *sqliteLibrary) Fetch(ctx context.Context, spec SourceSpec) (fetch Fetch, err error) {
	defer func() { err = typedError(err) }()
	l.fetchMu.Lock()
	closed := l.closed
	l.fetchMu.Unlock()
	if closed {
		return Fetch{}, fmt.Errorf("fetch: library is closed")
	}
	if spec.Type == SourceLocal {
		return Fetch{}, &Error{Code: InvalidArgument, Message: "fetch: local sources must use Install"}
	}
	source := Source{Type: spec.Type, Locator: spec.Locator}
	if err := validateSource(source); err != nil {
		return Fetch{}, err
	}
	if !validSubpath(spec.Subpath) || (spec.Subpath != "" && spec.Type != SourceGitHub) {
		return Fetch{}, &Error{Code: InvalidArgument, Message: "invalid Fetch subpath"}
	}
	for _, name := range spec.Names {
		if !validSelectionName(name) {
			return Fetch{}, &Error{Code: InvalidArgument, Message: "invalid Fetch name selection"}
		}
	}
	discovered, err := fetchRemote(ctx, source, spec.Subpath)
	if err != nil {
		return Fetch{}, err
	}
	discovered.skills, err = selectSkills(discovered.skills, spec.Names)
	if err != nil {
		return Fetch{}, err
	}
	source.LastFetched = time.Now().UTC()
	source.LastSeenRef = discovered.ref
	id, err := newFetchID()
	if err != nil {
		return Fetch{}, err
	}
	l.fetchMu.Lock()
	defer l.fetchMu.Unlock()
	if l.closed {
		return Fetch{}, fmt.Errorf("fetch: library is closed")
	}
	if err := ctx.Err(); err != nil {
		return Fetch{}, err
	}
	session := &fetchSession{source: source, ref: discovered.ref, skills: discovered.skills, diagnostics: discovered.diagnostics}
	l.fetches[id] = session
	session.timer = time.AfterFunc(fetchTTL, func() {
		l.fetchMu.Lock()
		defer l.fetchMu.Unlock()
		delete(l.fetches, id)
	})
	return Fetch{ID: id, Ref: discovered.ref, FetchCandidates: session.candidates()}, nil
}

func (s *fetchSession) candidates() FetchCandidates {
	result := FetchCandidates{Skills: make([]FetchSkill, len(s.skills)), Diagnostics: slices.Clone(s.diagnostics)}
	for i, skill := range s.skills {
		result.Skills[i] = skill.preview
	}
	return result
}

func (l *sqliteLibrary) ListFetchSkills(ctx context.Context, id FetchID) (result FetchCandidates, err error) {
	defer func() { err = typedError(err) }()
	if err := ctx.Err(); err != nil {
		return result, err
	}
	l.fetchMu.Lock()
	defer l.fetchMu.Unlock()
	session, ok := l.fetches[id]
	if !ok {
		return result, &Error{Code: NotFound, Message: "fetch session not found"}
	}
	return session.candidates(), nil
}

func (l *sqliteLibrary) InstallFetch(ctx context.Context, id FetchID, names []string) (installed []Skill, err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()

	l.fetchMu.Lock()
	// 持锁直到提交并丢弃句柄,避免并发调用消费同一次 Fetch。
	defer l.fetchMu.Unlock()
	session := l.fetches[id]
	if session == nil {
		return nil, &Error{Code: NotFound, Message: "install fetch: unknown or discarded fetch"}
	}
	skills, err := selectSkills(session.skills, names)
	if err != nil {
		return nil, err
	}
	if owner, repo, ok := githubOwnerRepo(session.source.Locator.Repository); ok {
		if err := materializeGitHubSkills(ctx, owner, repo, skills); err != nil {
			return nil, err
		}
	}
	installed, err = l.installAll(ctx, session.source, skills)
	if err != nil {
		return nil, err
	}
	// 成功提交入库后丢弃该 Fetch:同一句柄不能再次使用。
	if session.timer != nil {
		session.timer.Stop()
	}
	delete(l.fetches, id)
	return installed, nil
}

func newFetchID() (FetchID, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("issue fetch handle: %w", err)
	}
	return FetchID(hex.EncodeToString(buffer)), nil
}

// installAll 在单个事务里原子提交多个来自同一来源的 skill:要么全部入库,要么库不变。
func (l *sqliteLibrary) installAll(ctx context.Context, source Source, skills []fetchedSkill) ([]Skill, error) {
	if err := validateSource(source); err != nil {
		return nil, err
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin install: %w", err)
	}
	defer tx.Rollback()
	sourceID, err := upsertSource(ctx, tx, source)
	if err != nil {
		return nil, err
	}
	installed := make([]Skill, 0, len(skills))
	for _, skill := range skills {
		result, err := insertSkill(ctx, tx, sourceID, source, skill.name, skill.files, skill.gitTreeSHA)
		if err != nil {
			return nil, err
		}
		installed = append(installed, result)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit install: %w", err)
	}
	return installed, nil
}

func (l *sqliteLibrary) install(ctx context.Context, source Source, name string, files []File) (Skill, error) {
	skills, err := l.installAll(ctx, source, []fetchedSkill{{name: name, files: files}})
	if err != nil {
		return Skill{}, err
	}
	return skills[0], nil
}

// upsertSource 在事务内插入或更新来源行,返回其 id。
func upsertSource(ctx context.Context, tx *sql.Tx, source Source) (int64, error) {
	locator, err := json.Marshal(source.Locator)
	if err != nil {
		return 0, fmt.Errorf("encode source: %w", err)
	}
	fetched := ""
	if !source.LastFetched.IsZero() {
		fetched = source.LastFetched.UTC().Format(time.RFC3339Nano)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sources(type, locator, last_fetched, last_seen_ref) VALUES (?, ?, ?, ?)
		ON CONFLICT(type, locator) DO UPDATE SET last_fetched = excluded.last_fetched, last_seen_ref = excluded.last_seen_ref
	`, source.Type, locator, fetched, source.LastSeenRef); err != nil {
		return 0, fmt.Errorf("store source: %w", err)
	}
	var sourceID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM sources WHERE type = ? AND locator = ?`, source.Type, locator).Scan(&sourceID); err != nil {
		return 0, fmt.Errorf("read source: %w", err)
	}
	return sourceID, nil
}

// insertSkill 在事务内写入一个 skill 及其首个版本,并设为当前版本。
func insertSkill(ctx context.Context, tx *sql.Tx, sourceID int64, source Source, name string, files []File, gitTreeSHA string) (Skill, error) {
	if name == "" {
		return Skill{}, fmt.Errorf("install skill: name is required")
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO skills(source_id, name) VALUES (?, ?)`, sourceID, name)
	if err != nil {
		return Skill{}, fmt.Errorf("store skill: %w", err)
	}
	skillID, err := result.LastInsertId()
	if err != nil {
		return Skill{}, fmt.Errorf("read skill id: %w", err)
	}
	versionID, err := createVersion(ctx, tx, skillID, files, gitTreeSHA)
	if err != nil {
		return Skill{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE skills SET current_version_id = ? WHERE id = ?`, versionID, skillID); err != nil {
		return Skill{}, fmt.Errorf("select current version: %w", err)
	}
	source.ID = SourceID(sourceID)
	source.LastFetched = source.LastFetched.UTC()
	return Skill{ID: SkillID(skillID), Name: name, Source: source, GitTreeSHA: gitTreeSHA, Files: files}, nil
}

func (l *sqliteLibrary) Update(ctx context.Context, id SkillID) (result UpdateResult, err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if id <= 0 {
		return UpdateResult{}, &Error{Code: InvalidArgument, Message: "update: skill ID must be positive"}
	}
	skill, err := l.GetSkill(ctx, id)
	if err != nil {
		return UpdateResult{}, err
	}
	persistedSHA := skill.GitTreeSHA
	changed := false
	if skill.Source.Type == SourceGitHub {
		skill, changed, err = readGitHubUpdate(ctx, skill)
	} else {
		var latest Skill
		latest, err = readUpdate(ctx, skill)
		if err == nil {
			changed = !filesEqual(skill.Files, latest.Files)
			skill = latest
		}
	}
	if err != nil {
		return UpdateResult{}, err
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return UpdateResult{}, fmt.Errorf("begin update: %w", err)
	}
	defer tx.Rollback()
	if changed {
		versionID, err := createVersion(ctx, tx, int64(id), skill.Files, skill.GitTreeSHA)
		if err != nil {
			return UpdateResult{}, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE skills SET current_version_id = ? WHERE id = ?`, versionID, id); err != nil {
			return UpdateResult{}, fmt.Errorf("select current version: %w", err)
		}
	} else if persistedSHA == "" && skill.GitTreeSHA != "" {
		if _, err := tx.ExecContext(ctx, `
			UPDATE skill_versions SET git_tree_sha = ?
			WHERE id = (SELECT current_version_id FROM skills WHERE id = ?)
		`, skill.GitTreeSHA, id); err != nil {
			return UpdateResult{}, fmt.Errorf("backfill git tree sha: %w", err)
		}
	}
	if skill.Source.Type != SourceLocal {
		if _, err := tx.ExecContext(ctx, `UPDATE sources SET last_fetched = ?, last_seen_ref = ? WHERE id = ?`,
			skill.Source.LastFetched.Format(time.RFC3339Nano), skill.Source.LastSeenRef, skill.Source.ID); err != nil {
			return UpdateResult{}, fmt.Errorf("update source: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return UpdateResult{}, fmt.Errorf("commit update: %w", err)
	}
	return UpdateResult{Skill: skill, Changed: changed, Note: "已激活的副本未重写,可能落后于 Library;需要时请重新激活。"}, nil
}

// readGitHubUpdate 按目录 tree SHA 决定是否取文件;SHA 相同不取 blob、不克隆(API 可用时)。
func readGitHubUpdate(ctx context.Context, skill Skill) (Skill, bool, error) {
	snap, err := readGitHubSnapshot(ctx, skill.Source)
	if err != nil {
		return Skill{}, false, err
	}
	remote, err := snap.locate(ctx, skill)
	if err != nil {
		return Skill{}, false, err
	}
	persistedSHA := skill.GitTreeSHA
	shaKnown := persistedSHA != "" && remote.gitTreeSHA != ""
	changed := shaKnown && persistedSHA != remote.gitTreeSHA
	if !shaKnown || changed {
		files, err := snap.files(ctx, skill, remote)
		if err != nil {
			return Skill{}, false, err
		}
		if !shaKnown {
			changed = !filesEqual(skill.Files, files)
		}
		skill.Files = files
	}
	skill.GitTreeSHA = remote.gitTreeSHA
	skill.Source.LastSeenRef = snap.ref
	skill.Source.LastFetched = time.Now().UTC()
	return skill, changed, nil
}

// readUpdate 读取目标 skill 的最新来源内容,不持久化来源状态。GitHub 走 readGitHubUpdate。
func readUpdate(ctx context.Context, skill Skill) (Skill, error) {
	var err error
	var files []File
	if skill.Source.Type == SourceLocal {
		files, err = readFiles(ctx, skill.Source.Locator.Path)
	} else {
		var discovered discoveredSource
		discovered, err = fetchRemote(ctx, skill.Source, "")
		fetched := discovered.skills
		skill.Source.LastSeenRef = discovered.ref
		if err == nil {
			fetched, err = selectSkills(fetched, []string{skill.Name})
		}
		if err == nil {
			files = fetched[0].files
			skill.GitTreeSHA = fetched[0].gitTreeSHA
			skill.Source.LastFetched = time.Now().UTC()
		}
	}
	if err != nil {
		return Skill{}, err
	}
	skill.Files = files
	return skill, nil
}

func createVersion(ctx context.Context, tx *sql.Tx, skillID int64, files []File, gitTreeSHA string) (int64, error) {
	result, err := tx.ExecContext(ctx, `INSERT INTO skill_versions(skill_id, git_tree_sha) VALUES (?, ?)`, skillID, gitTreeSHA)
	if err != nil {
		return 0, fmt.Errorf("store skill version: %w", err)
	}
	versionID, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read version id: %w", err)
	}
	for _, file := range files {
		sum := sha256.Sum256(file.Content)
		fingerprint := hex.EncodeToString(sum[:])
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO blobs(fingerprint, content) VALUES (?, ?)`, fingerprint, file.Content); err != nil {
			return 0, fmt.Errorf("store %s: %w", file.Path, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO version_files(version_id, path, fingerprint, mode) VALUES (?, ?, ?, ?)`, versionID, file.Path, fingerprint, file.Mode); err != nil {
			return 0, fmt.Errorf("store %s metadata: %w", file.Path, err)
		}
	}
	return versionID, nil
}

func (l *sqliteLibrary) GetSkill(ctx context.Context, id SkillID) (skill Skill, err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	summary, err := l.getSummary(ctx, id)
	if err != nil {
		return Skill{}, err
	}
	skill = Skill{ID: summary.ID, Name: summary.Name, Source: summary.Source}
	if err := l.db.QueryRowContext(ctx, `
		SELECT skill_versions.git_tree_sha
		FROM skills JOIN skill_versions ON skill_versions.id = skills.current_version_id
		WHERE skills.id = ?
	`, id).Scan(&skill.GitTreeSHA); err != nil {
		return Skill{}, fmt.Errorf("get skill version: %w", err)
	}

	rows, err := l.db.QueryContext(ctx, `
		SELECT version_files.path, blobs.content, version_files.mode
		FROM skills
		JOIN version_files ON version_files.version_id = skills.current_version_id
		JOIN blobs ON blobs.fingerprint = version_files.fingerprint
		WHERE skills.id = ?
		ORDER BY version_files.path
	`, id)
	if err != nil {
		return Skill{}, fmt.Errorf("get skill files: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var file File
		if err := rows.Scan(&file.Path, &file.Content, &file.Mode); err != nil {
			return Skill{}, fmt.Errorf("read skill file: %w", err)
		}
		skill.Files = append(skill.Files, file)
	}
	if err := rows.Err(); err != nil {
		return Skill{}, fmt.Errorf("read skill files: %w", err)
	}
	return skill, nil
}

func (l *sqliteLibrary) ListVersions(ctx context.Context, id SkillID) (versions []SkillVersion, err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	rows, err := l.db.QueryContext(ctx, `SELECT id, git_tree_sha FROM skill_versions WHERE skill_id = ? ORDER BY id`, id)
	if err != nil {
		return nil, fmt.Errorf("list skill versions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		version := SkillVersion{SkillID: id}
		if err := rows.Scan(&version.ID, &version.GitTreeSHA); err != nil {
			return nil, fmt.Errorf("read skill version: %w", err)
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read skill versions: %w", err)
	}
	return versions, nil
}

func (l *sqliteLibrary) GetVersion(ctx context.Context, id VersionID) (version SkillVersion, err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	version = SkillVersion{ID: id}
	if err := l.db.QueryRowContext(ctx, `SELECT skill_id, git_tree_sha FROM skill_versions WHERE id = ?`, id).Scan(&version.SkillID, &version.GitTreeSHA); err != nil {
		return SkillVersion{}, fmt.Errorf("get skill version: %w", err)
	}
	rows, err := l.db.QueryContext(ctx, `
		SELECT version_files.path, blobs.content, version_files.mode
		FROM version_files JOIN blobs ON blobs.fingerprint = version_files.fingerprint
		WHERE version_files.version_id = ?
		ORDER BY version_files.path
	`, id)
	if err != nil {
		return SkillVersion{}, fmt.Errorf("get skill version files: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var file File
		if err := rows.Scan(&file.Path, &file.Content, &file.Mode); err != nil {
			return SkillVersion{}, fmt.Errorf("read skill version file: %w", err)
		}
		version.Files = append(version.Files, file)
	}
	if err := rows.Err(); err != nil {
		return SkillVersion{}, fmt.Errorf("read skill version files: %w", err)
	}
	return version, nil
}

func (l *sqliteLibrary) DiffVersions(ctx context.Context, oldID, newID VersionID) (diff VersionDiff, err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	oldVersion, err := l.GetVersion(ctx, oldID)
	if err != nil {
		return VersionDiff{}, err
	}
	newVersion, err := l.GetVersion(ctx, newID)
	if err != nil {
		return VersionDiff{}, err
	}
	if oldVersion.SkillID != newVersion.SkillID {
		return VersionDiff{}, fmt.Errorf("diff skill versions: versions belong to different skills")
	}
	oldFiles := make(map[string][sha256.Size]byte, len(oldVersion.Files))
	for _, file := range oldVersion.Files {
		oldFiles[file.Path] = sha256.Sum256(file.Content)
	}
	newFiles := make(map[string][sha256.Size]byte, len(newVersion.Files))
	for _, file := range newVersion.Files {
		newFiles[file.Path] = sha256.Sum256(file.Content)
	}
	for _, file := range oldVersion.Files {
		newSum, ok := newFiles[file.Path]
		if !ok {
			diff.Deleted = append(diff.Deleted, file.Path)
		} else if oldFiles[file.Path] != newSum {
			diff.Changed = append(diff.Changed, file.Path)
		}
	}
	for _, file := range newVersion.Files {
		if _, ok := oldFiles[file.Path]; !ok {
			diff.Added = append(diff.Added, file.Path)
		}
	}
	return diff, nil
}

func (l *sqliteLibrary) CheckUpdate(ctx context.Context, id SkillID) (available bool, err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if id <= 0 {
		return false, &Error{Code: InvalidArgument, Message: "check update: skill ID must be positive"}
	}
	skill, err := l.GetSkill(ctx, id)
	if err != nil {
		return false, err
	}
	if skill.Source.Type == SourceGitHub {
		snap, err := readGitHubSnapshot(ctx, skill.Source)
		if err != nil {
			return false, err
		}
		return githubSkillHasUpdate(ctx, &snap, skill)
	}
	latest, err := readUpdate(ctx, skill)
	if err != nil {
		return false, err
	}
	return !filesEqual(skill.Files, latest.Files), nil
}

func validateSource(source Source) error {
	switch source.Type {
	case SourceGitHub:
		if source.Locator.Repository == "" || source.Locator.Branch == "" {
			return &Error{Code: InvalidArgument, Message: "install skill: github repository and branch are required"}
		}
	case SourceURL:
		if source.Locator.URL == "" {
			return &Error{Code: InvalidArgument, Message: "install skill: url is required"}
		}
	case SourceStore:
		if source.Locator.PackageID == "" || source.Locator.Version == "" {
			return &Error{Code: InvalidArgument, Message: "install skill: store package id and version are required"}
		}
	case SourceLocal:
		if source.Locator.Path == "" {
			return &Error{Code: InvalidArgument, Message: "install skill: local path is required"}
		}
	default:
		return &Error{Code: InvalidArgument, Message: fmt.Sprintf("install skill: unsupported source type %q", source.Type)}
	}
	return nil
}

func (l *sqliteLibrary) ListSkills(ctx context.Context) (skills []SkillSummary, err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	rows, err := l.db.QueryContext(ctx, `SELECT id FROM skills ORDER BY name, id`)
	if err != nil {
		return nil, fmt.Errorf("list skills: %w", err)
	}
	var ids []SkillID
	for rows.Next() {
		var id SkillID
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("read skill id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("read skill ids: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read skill ids: %w", err)
	}
	for _, id := range ids {
		summary, err := l.getSummary(ctx, id)
		if err != nil {
			return nil, err
		}
		skills = append(skills, summary)
	}
	return skills, nil
}

func (l *sqliteLibrary) RemoveSkill(ctx context.Context, id SkillID, opts RemoveSkillOptions) (err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if opts.ProjectedCopies != "" && opts.ProjectedCopies != Keep && opts.ProjectedCopies != Remove {
		return fmt.Errorf("invalid projected copies option")
	}
	if _, err := l.GetSkill(ctx, id); err != nil {
		return err
	}
	if opts.ProjectedCopies == Remove {
		activations, err := l.ListActivations(ctx)
		if err != nil {
			return err
		}
		for _, activation := range activations {
			if activation.SkillID == id {
				if err := l.removeProjectedCopy(ctx, id, activation.Scope); err != nil {
					return err
				}
			}
		}
	}

	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin remove skill: %w", err)
	}
	defer tx.Rollback()
	statements := []string{
		`DELETE FROM activations WHERE skill_id = ?`,
		`DELETE FROM skill_groups WHERE skill_id = ?`,
		`UPDATE skills SET current_version_id = NULL WHERE id = ?`,
		`DELETE FROM version_files WHERE version_id IN (SELECT id FROM skill_versions WHERE skill_id = ?)`,
		`DELETE FROM skill_versions WHERE skill_id = ?`,
		`DELETE FROM skills WHERE id = ?`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement, id); err != nil {
			return fmt.Errorf("remove skill: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit remove skill: %w", err)
	}
	return nil
}

func (l *sqliteLibrary) RegisterProject(ctx context.Context, path string) (project RegisteredProject, err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if path == "" {
		return RegisteredProject{}, fmt.Errorf("register project: path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return RegisteredProject{}, fmt.Errorf("resolve project path: %w", err)
	}
	project.Path, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return RegisteredProject{}, fmt.Errorf("resolve project path: %w", err)
	}
	info, err := os.Stat(project.Path)
	if err != nil {
		return RegisteredProject{}, fmt.Errorf("read project path: %w", err)
	}
	if !info.IsDir() {
		return RegisteredProject{}, fmt.Errorf("register project: %s is not a directory", project.Path)
	}
	project.Path = filepath.Clean(project.Path)
	result, err := l.db.ExecContext(ctx, `INSERT OR IGNORE INTO projects(path) VALUES (?)`, project.Path)
	if err != nil {
		return RegisteredProject{}, fmt.Errorf("register project: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return RegisteredProject{}, fmt.Errorf("register project: %w", err)
	}
	if inserted == 0 {
		return RegisteredProject{}, &Error{Code: AlreadyExists, Message: "register project: path already registered"}
	}
	id, err := result.LastInsertId()
	if err != nil {
		return RegisteredProject{}, fmt.Errorf("read project id: %w", err)
	}
	project.ID = ProjectID(id)
	return project, nil
}

func (l *sqliteLibrary) ListRegisteredProjects(ctx context.Context) (projects []RegisteredProject, err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	rows, err := l.db.QueryContext(ctx, `SELECT id, path FROM projects ORDER BY path, id`)
	if err != nil {
		return nil, fmt.Errorf("list registered projects: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var project RegisteredProject
		if err := rows.Scan(&project.ID, &project.Path); err != nil {
			return nil, fmt.Errorf("read registered project: %w", err)
		}
		projects = append(projects, project)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read registered projects: %w", err)
	}
	return projects, nil
}

func projectScopes(path string) []Scope {
	return []Scope{
		{Agent: AgentClaudeCode, Path: filepath.Join(path, ".claude", "skills")},
		{Agent: AgentCodex, Path: filepath.Join(path, ".codex", "skills")},
	}
}

func (l *sqliteLibrary) UnregisterProject(ctx context.Context, id ProjectID, opts UnregisterProjectOptions) (err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if opts.ProjectedCopies != "" && opts.ProjectedCopies != Keep && opts.ProjectedCopies != Remove {
		return fmt.Errorf("invalid projected copies option")
	}
	var path string
	if err := l.db.QueryRowContext(ctx, `SELECT path FROM projects WHERE id = ?`, id).Scan(&path); err != nil {
		return fmt.Errorf("get registered project: %w", err)
	}
	scopes := projectScopes(path)
	if opts.ProjectedCopies == Remove {
		activations, err := l.ListActivations(ctx)
		if err != nil {
			return err
		}
		for _, activation := range activations {
			for _, scope := range scopes {
				if activation.Scope == scope {
					if err := l.removeProjectedCopy(ctx, activation.SkillID, scope); err != nil {
						return err
					}
				}
			}
		}
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin unregister project: %w", err)
	}
	defer tx.Rollback()
	for _, scope := range scopes {
		if _, err := tx.ExecContext(ctx, `DELETE FROM activations WHERE agent = ? AND scope_path = ?`, scope.Agent, scope.Path); err != nil {
			return fmt.Errorf("remove project activations: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id); err != nil {
		return fmt.Errorf("unregister project: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit unregister project: %w", err)
	}
	return nil
}

func (l *sqliteLibrary) removeProjectedCopy(ctx context.Context, id SkillID, scope Scope) error {
	skill, err := l.GetSkill(ctx, id)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(scope.Path, skill.Name)); err != nil {
		return fmt.Errorf("remove projected copy: %w", err)
	}
	return nil
}

func (l *sqliteLibrary) ListScopes(ctx context.Context) (scopes []Scope, err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, typedError(fmt.Errorf("find home directory: %w", err))
	}
	scopes = []Scope{
		{Agent: AgentClaudeCode, Path: filepath.Join(home, ".claude", "skills")},
		{Agent: AgentCodex, Path: filepath.Join(home, ".codex", "skills")},
	}
	projects, err := l.ListRegisteredProjects(ctx)
	if err != nil {
		return nil, err
	}
	for _, project := range projects {
		scopes = append(scopes, projectScopes(project.Path)...)
	}
	return scopes, nil
}

func (l *sqliteLibrary) Activate(ctx context.Context, id SkillID, scope Scope, opts ActivateOptions) (err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	return l.activate(ctx, id, scope, opts, true, 0)
}

func (l *sqliteLibrary) activate(ctx context.Context, id SkillID, scope Scope, opts ActivateOptions, individual bool, groupID GroupID) error {
	if err := validateScope(scope); err != nil {
		return err
	}
	skill, err := l.GetSkill(ctx, id)
	if err != nil {
		return err
	}
	// 已有本 skill 在该 Scope 的投影记录时,目标目录是我们自己的投影,再次激活应当
	// 幂等成功(刷新内容 / 补记依据),而非因目录存在而报冲突。
	active, err := l.hasActivation(ctx, id, scope)
	if err != nil {
		return err
	}
	target := filepath.Join(scope.Path, skill.Name)
	if _, err := os.Lstat(target); err == nil {
		if !active && opts.OnConflict != Overwrite {
			return &Error{Code: AlreadyExists, Message: "activate skill: target already exists"}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect activation target: %w", err)
	}
	if err := os.MkdirAll(scope.Path, 0o755); err != nil {
		return fmt.Errorf("create scope: %w", err)
	}
	stage, err := l.stageSkill(scope, skill)
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	backup, err := moveStageIntoPlace(stage, target)
	if err != nil {
		return err
	}
	restore := func() {
		_ = os.RemoveAll(target)
		if backup != "" {
			_ = os.Rename(backup, target)
		}
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		restore()
		return fmt.Errorf("begin activation record: %w", err)
	}
	defer tx.Rollback()
	if err := recordActivationTx(ctx, tx, id, scope, individual, groupID); err != nil {
		restore()
		return err
	}
	if err := tx.Commit(); err != nil {
		restore()
		return fmt.Errorf("commit activation record: %w", err)
	}
	if backup != "" {
		_ = os.RemoveAll(backup)
	}
	return nil
}

// stageSkill 把 skill 的全部文件写入 scope 下的一个临时暂存目录,返回暂存路径。
// 暂存好但尚未移入目标位置,便于分组激活先把所有成员备齐再统一落盘。
func (l *sqliteLibrary) stageSkill(scope Scope, skill Skill) (string, error) {
	stage, err := os.MkdirTemp(scope.Path, "."+skill.Name+"-")
	if err != nil {
		return "", fmt.Errorf("stage activation: %w", err)
	}
	for _, file := range skill.Files {
		if !filepath.IsLocal(file.Path) {
			_ = os.RemoveAll(stage)
			return "", fmt.Errorf("invalid skill path: %s", file.Path)
		}
		path := filepath.Join(stage, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			_ = os.RemoveAll(stage)
			return "", fmt.Errorf("create activation directory: %w", err)
		}
		mode := os.FileMode(0o644)
		if file.Mode == ModeExecutable {
			mode = 0o755
		}
		if err := os.WriteFile(path, file.Content, mode); err != nil {
			_ = os.RemoveAll(stage)
			return "", fmt.Errorf("write activation file: %w", err)
		}
	}
	return stage, nil
}

// moveStageIntoPlace 把暂存目录移到目标位置;目标已存在则先备份到返回的 backup 路径
// (backup 为空表示目标原本不存在)。失败时尽力把备份复位。
func moveStageIntoPlace(stage, target string) (backup string, err error) {
	if _, err := os.Lstat(target); err == nil {
		backup = stage + ".old"
		if err := os.Rename(target, backup); err != nil {
			return "", fmt.Errorf("replace activation: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect activation target: %w", err)
	}
	if err := os.Rename(stage, target); err != nil {
		if backup != "" {
			_ = os.Rename(backup, target)
		}
		return "", fmt.Errorf("activate skill: %w", err)
	}
	return backup, nil
}

// recordActivationTx 在事务内写入投影记录:活动行 + 可选的分组依据行。
func recordActivationTx(ctx context.Context, tx *sql.Tx, id SkillID, scope Scope, individual bool, groupID GroupID) error {
	individualValue := 0
	if individual {
		individualValue = 1
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO activations(skill_id, agent, scope_path, individual) VALUES (?, ?, ?, ?)
		ON CONFLICT(skill_id, agent, scope_path) DO UPDATE SET individual = MAX(individual, excluded.individual)
	`, id, scope.Agent, scope.Path, individualValue); err != nil {
		return fmt.Errorf("record activation: %w", err)
	}
	if groupID != 0 {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO activation_groups(skill_id, agent, scope_path, group_id) VALUES (?, ?, ?, ?)`, id, scope.Agent, scope.Path, groupID); err != nil {
			return fmt.Errorf("record group activation: %w", err)
		}
	}
	return nil
}

func (l *sqliteLibrary) Deactivate(ctx context.Context, id SkillID, scope Scope) (err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if err := validateScope(scope); err != nil {
		return err
	}
	var individual bool
	if err := l.db.QueryRowContext(ctx, `SELECT individual FROM activations WHERE skill_id = ? AND agent = ? AND scope_path = ?`, id, scope.Agent, scope.Path).Scan(&individual); err != nil {
		return fmt.Errorf("find activation: %w", err)
	}
	if !individual {
		return &Error{Code: NotFound, Message: "deactivate skill: no independent activation"}
	}
	return l.deactivateOrigin(ctx, id, scope)
}

func (l *sqliteLibrary) deactivateOrigin(ctx context.Context, id SkillID, scope Scope) error {
	skill, err := l.GetSkill(ctx, id)
	if err != nil {
		return err
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin deactivation: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE activations SET individual = 0 WHERE skill_id = ? AND agent = ? AND scope_path = ?`, id, scope.Agent, scope.Path); err != nil {
		return fmt.Errorf("remove independent activation: %w", err)
	}
	keep, err := keepActivation(ctx, tx, id, scope)
	if err != nil {
		return err
	}
	if !keep {
		if err := os.RemoveAll(filepath.Join(scope.Path, skill.Name)); err != nil {
			return fmt.Errorf("deactivate skill: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM activations WHERE skill_id = ? AND agent = ? AND scope_path = ?`, id, scope.Agent, scope.Path); err != nil {
			return fmt.Errorf("remove activation: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit deactivation: %w", err)
	}
	return nil
}

// keepActivation 在撤销依据后判断投影是否仍需保留。
func keepActivation(ctx context.Context, tx *sql.Tx, id SkillID, scope Scope) (bool, error) {
	var keep bool
	if err := tx.QueryRowContext(ctx, `
		SELECT individual OR EXISTS(
			SELECT 1 FROM activation_groups
			WHERE skill_id = ? AND agent = ? AND scope_path = ?
		)
		FROM activations WHERE skill_id = ? AND agent = ? AND scope_path = ?
	`, id, scope.Agent, scope.Path, id, scope.Agent, scope.Path).Scan(&keep); err != nil {
		return false, fmt.Errorf("check activation origins: %w", err)
	}
	return keep, nil
}

func (l *sqliteLibrary) ActivateGroup(ctx context.Context, groupID GroupID, scope Scope, opts ActivateOptions) (err error) {
	// ponytail: 整组操作独占库锁直到提交或回滚完成;若并发吞吐成为瓶颈再细化到 Scope。
	l.mu.Lock()
	defer l.mu.Unlock()
	ctx = context.WithValue(ctx, libraryOperationKey{}, l)
	defer func() { err = typedError(err) }()
	if err := validateScope(scope); err != nil {
		return err
	}
	if err := l.requireGroup(ctx, groupID); err != nil {
		return err
	}
	members, err := l.ListGroupSkills(ctx, groupID)
	if err != nil {
		return err
	}
	// 组成员可跨来源并重名,但同名成员会投影到同一路径;整批落盘前拒绝这种冲突。
	seen := make(map[string]bool, len(members))
	for _, member := range members {
		if seen[member.Name] {
			return &Error{Code: AlreadyExists, Message: fmt.Sprintf("activate group: multiple members named %q project to the same target", member.Name)}
		}
		seen[member.Name] = true
	}
	if opts.OnConflict != Overwrite {
		for _, member := range members {
			// 已属本组/本 skill 自己的既有投影不算冲突,分组再次激活应幂等成功。
			active, err := l.hasActivation(ctx, member.ID, scope)
			if err != nil {
				return err
			}
			if active {
				continue
			}
			if _, err := os.Lstat(filepath.Join(scope.Path, member.Name)); err == nil {
				return &Error{Code: AlreadyExists, Message: "activate group: target already exists"}
			} else if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("inspect activation target: %w", err)
			}
		}
	}
	if len(members) == 0 {
		return nil
	}
	if err := os.MkdirAll(scope.Path, 0o755); err != nil {
		return fmt.Errorf("create scope: %w", err)
	}

	// 全组作为一个操作单元:先把每个成员备齐并统一落盘,再在单事务里记录;
	// 任何一步失败都回滚本次操作的磁盘改动与记录,既有的独立激活不受影响。
	type staged struct {
		id     SkillID
		target string
		stage  string // 暂存目录;移入目标后置空
		backup string // 被替换的原目标备份;"" 表示原本无目标
		moved  bool   // 暂存是否已移入目标位置
	}
	var applied []*staged
	rollback := func() error {
		var restoreErr error
		for i := len(applied) - 1; i >= 0; i-- {
			member := applied[i]
			if member.moved {
				if err := os.RemoveAll(member.target); err != nil {
					restoreErr = errors.Join(restoreErr, err)
				} else if member.backup != "" {
					// 复位失败时保留备份,避免丢失用户原始内容。
					restoreErr = errors.Join(restoreErr, os.Rename(member.backup, member.target))
				}
			}
			if member.stage != "" {
				restoreErr = errors.Join(restoreErr, os.RemoveAll(member.stage))
			}
		}
		return restoreErr
	}

	for _, member := range members {
		skill, err := l.GetSkill(ctx, member.ID)
		if err != nil {
			return errors.Join(err, rollback())
		}
		stage, err := l.stageSkill(scope, skill)
		if err != nil {
			return errors.Join(err, rollback())
		}
		applied = append(applied, &staged{id: skill.ID, target: filepath.Join(scope.Path, skill.Name), stage: stage})
	}
	for _, member := range applied {
		if err := ctx.Err(); err != nil {
			return errors.Join(err, rollback())
		}
		backup, err := moveStageIntoPlace(member.stage, member.target)
		if err != nil {
			return errors.Join(err, rollback())
		}
		member.backup = backup
		member.moved = true
		member.stage = ""
	}

	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.Join(fmt.Errorf("begin group activation: %w", err), rollback())
	}
	defer tx.Rollback()
	for _, member := range applied {
		if err := recordActivationTx(ctx, tx, member.id, scope, false, groupID); err != nil {
			return errors.Join(err, rollback())
		}
	}
	if err := tx.Commit(); err != nil {
		return errors.Join(fmt.Errorf("commit group activation: %w", err), rollback())
	}
	for _, member := range applied {
		if member.backup != "" {
			_ = os.RemoveAll(member.backup)
		}
	}
	return nil
}

func (l *sqliteLibrary) DeactivateGroup(ctx context.Context, groupID GroupID, scope Scope) (err error) {
	// ponytail: 整组操作独占库锁直到提交或回滚完成;若并发吞吐成为瓶颈再细化到 Scope。
	l.mu.Lock()
	defer l.mu.Unlock()
	ctx = context.WithValue(ctx, libraryOperationKey{}, l)
	defer func() { err = typedError(err) }()
	if err := validateScope(scope); err != nil {
		return err
	}
	if err := l.requireGroup(ctx, groupID); err != nil {
		return err
	}
	rows, err := l.db.QueryContext(ctx, `SELECT skill_id FROM activation_groups WHERE group_id = ? AND agent = ? AND scope_path = ? ORDER BY skill_id`, groupID, scope.Agent, scope.Path)
	if err != nil {
		return fmt.Errorf("list group activations: %w", err)
	}
	var ids []SkillID
	for rows.Next() {
		var id SkillID
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return fmt.Errorf("read group activation: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close group activations: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read group activations: %w", err)
	}
	if len(ids) == 0 {
		return &Error{Code: NotFound, Message: "deactivate group: group is not active in scope"}
	}

	// 单连接约束下,先在开事务前把成员名字取齐(事务期间不能再走 l.db)。
	names := make(map[SkillID]string, len(ids))
	for _, id := range ids {
		skill, err := l.GetSkill(ctx, id)
		if err != nil {
			return err
		}
		names[id] = skill.Name
	}

	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin group deactivation: %w", err)
	}
	defer tx.Rollback()
	var removeTargets []string
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `DELETE FROM activation_groups WHERE skill_id = ? AND agent = ? AND scope_path = ? AND group_id = ?`, id, scope.Agent, scope.Path, groupID); err != nil {
			return fmt.Errorf("remove group activation: %w", err)
		}
		keep, err := keepActivation(ctx, tx, id, scope)
		if err != nil {
			return err
		}
		if !keep {
			if _, err := tx.ExecContext(ctx, `DELETE FROM activations WHERE skill_id = ? AND agent = ? AND scope_path = ?`, id, scope.Agent, scope.Path); err != nil {
				return fmt.Errorf("remove activation: %w", err)
			}
			removeTargets = append(removeTargets, filepath.Join(scope.Path, names[id]))
		}
	}

	// 磁盘删除做成可逆:先把待删目录改名到暂存,提交后再真正删除;
	// 任一步失败则撤销改名并回滚事务,该组去激活全有或全无。
	type trashedProjection struct{ trash, target string }
	var trashed []trashedProjection
	restore := func() error {
		var restoreErr error
		for i := len(trashed) - 1; i >= 0; i-- {
			restoreErr = errors.Join(restoreErr, os.Rename(trashed[i].trash, trashed[i].target))
		}
		return restoreErr
	}
	for _, target := range removeTargets {
		if err := ctx.Err(); err != nil {
			return errors.Join(err, restore())
		}
		if _, err := os.Lstat(target); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return errors.Join(fmt.Errorf("inspect deactivation target: %w", err), restore())
		}
		trash, err := randomName(filepath.Join(scope.Path, ".deactivate-"))
		if err != nil {
			return errors.Join(err, restore())
		}
		if err := os.Rename(target, trash); err != nil {
			return errors.Join(fmt.Errorf("deactivate skill: %w", err), restore())
		}
		trashed = append(trashed, trashedProjection{trash: trash, target: target})
	}
	if err := tx.Commit(); err != nil {
		return errors.Join(fmt.Errorf("commit group deactivation: %w", err), restore())
	}
	for _, entry := range trashed {
		_ = os.RemoveAll(entry.trash)
	}
	return nil
}

func randomName(prefix string) (string, error) {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate temp name: %w", err)
	}
	return prefix + hex.EncodeToString(buffer), nil
}

func (l *sqliteLibrary) requireGroup(ctx context.Context, id GroupID) error {
	var exists int
	if err := l.db.QueryRowContext(ctx, `SELECT 1 FROM groups WHERE id = ?`, id).Scan(&exists); err != nil {
		return fmt.Errorf("get group: %w", err)
	}
	return nil
}

func (l *sqliteLibrary) requireSkill(ctx context.Context, id SkillID) error {
	var exists int
	if err := l.db.QueryRowContext(ctx, `SELECT 1 FROM skills WHERE id = ?`, id).Scan(&exists); err != nil {
		return fmt.Errorf("get skill: %w", err)
	}
	return nil
}

// hasActivation 判断某 skill 在某 Scope 是否已有投影记录(不论依据是独立还是分组)。
func (l *sqliteLibrary) hasActivation(ctx context.Context, id SkillID, scope Scope) (bool, error) {
	var exists int
	err := l.db.QueryRowContext(ctx, `SELECT 1 FROM activations WHERE skill_id = ? AND agent = ? AND scope_path = ?`, id, scope.Agent, scope.Path).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check activation: %w", err)
	}
	return true, nil
}

func validateScope(scope Scope) error {
	if scope.Path == "" || (scope.Agent != AgentClaudeCode && scope.Agent != AgentCodex) {
		return fmt.Errorf("invalid scope")
	}
	return nil
}

func (l *sqliteLibrary) ListActivations(ctx context.Context) (activations []Activation, err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	rows, err := l.db.QueryContext(ctx, `SELECT skill_id, agent, scope_path, individual FROM activations ORDER BY skill_id, agent, scope_path`)
	if err != nil {
		return nil, fmt.Errorf("list activations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var activation Activation
		if err := rows.Scan(&activation.SkillID, &activation.Scope.Agent, &activation.Scope.Path, &activation.Individual); err != nil {
			return nil, fmt.Errorf("read activation: %w", err)
		}
		activations = append(activations, activation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read activations: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close activations: %w", err)
	}
	for i := range activations {
		rows, err := l.db.QueryContext(ctx, `SELECT group_id FROM activation_groups WHERE skill_id = ? AND agent = ? AND scope_path = ? ORDER BY group_id`, activations[i].SkillID, activations[i].Scope.Agent, activations[i].Scope.Path)
		if err != nil {
			return nil, fmt.Errorf("list activation groups: %w", err)
		}
		for rows.Next() {
			var id GroupID
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("read activation group: %w", err)
			}
			activations[i].ViaGroups = append(activations[i].ViaGroups, id)
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("close activation groups: %w", err)
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("read activation groups: %w", err)
		}
	}
	return activations, nil
}

func typedError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := errors.AsType[*Error](err); ok {
		return err
	}
	code := Internal
	if errors.Is(err, sql.ErrNoRows) {
		code = NotFound
	} else if errors.Is(err, fs.ErrPermission) {
		code = Permission
	} else if errors.Is(err, fs.ErrNotExist) {
		code = SourceUnreachable
	}
	return &Error{Code: code, Message: err.Error(), Err: err}
}

func (l *sqliteLibrary) ListCollections(ctx context.Context) (collections []Collection, err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	skills, err := l.ListSkills(ctx)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(skills, func(a, b SkillSummary) int {
		return cmp.Or(cmp.Compare(a.Source.ID, b.Source.ID), cmp.Compare(a.ID, b.ID))
	})
	for _, skill := range skills {
		if len(collections) == 0 || collections[len(collections)-1].Source.ID != skill.Source.ID {
			collections = append(collections, Collection{Source: skill.Source})
		}
		last := len(collections) - 1
		collections[last].Skills = append(collections[last].Skills, skill)
	}
	return collections, nil
}

func selectSkills(skills []fetchedSkill, selected []string) ([]fetchedSkill, error) {
	if len(selected) == 0 {
		return skills, nil
	}
	wanted := make(map[string]bool, len(selected))
	for _, name := range selected {
		wanted[name] = true
	}
	result := make([]fetchedSkill, 0, len(selected))
	for _, skill := range skills {
		if wanted[skill.name] {
			result = append(result, skill)
			delete(wanted, skill.name)
		}
	}
	if len(wanted) != 0 {
		for name := range wanted {
			return nil, &Error{Code: NotFound, Message: fmt.Sprintf("source skill %q not found", name)}
		}
	}
	return result, nil
}

func filesEqual(first, second []File) bool {
	if len(first) != len(second) {
		return false
	}
	for i := range first {
		if first[i].Path != second[i].Path || first[i].Mode != second[i].Mode || !bytes.Equal(first[i].Content, second[i].Content) {
			return false
		}
	}
	return true
}

func fetchError(code ErrorCode, operation string, err error) error {
	return &Error{Code: code, Message: operation + ": " + err.Error(), Err: err}
}

func fetchErrorCode(err error) ErrorCode {
	if errors.Is(err, fs.ErrPermission) {
		return Permission
	}
	return SourceUnreachable
}

func readFiles(ctx context.Context, root string) ([]File, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("read local source: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("read local source: %s is not a directory", root)
	}

	var files []File
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported file type: %s", path)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		mode := ModeRegular
		if info.Mode().Perm()&0o111 != 0 {
			mode = ModeExecutable
		}
		files = append(files, File{Path: filepath.ToSlash(relative), Content: content, Mode: mode})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read local source: %w", err)
	}
	return files, nil
}
