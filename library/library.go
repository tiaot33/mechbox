package library

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type SkillID int64
type SourceID int64

type VersionID int64
type GroupID int64
type ProjectID int64

type ErrorCode string

const (
	NotFound          ErrorCode = "not_found"
	AlreadyExists     ErrorCode = "already_exists"
	SourceUnreachable ErrorCode = "source_unreachable"
	Permission        ErrorCode = "permission"
	Internal          ErrorCode = "internal"
)

type Error struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
	Err     error     `json:"-"`
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.Err }

func IsCode(err error, code ErrorCode) bool {
	var libraryErr *Error
	return errors.As(err, &libraryErr) && libraryErr.Code == code
}

type SourceType string

const (
	SourceGitHub SourceType = "github"
	SourceURL    SourceType = "url"
	SourceStore  SourceType = "store"
	SourceLocal  SourceType = "local"
)

type FileMode string

const (
	ModeRegular    FileMode = "regular"
	ModeExecutable FileMode = "executable"
)

type LocalSource struct {
	Path string `json:"path"`
}

type SourceLocator struct {
	Repository string `json:"repository,omitempty"`
	Branch     string `json:"branch,omitempty"`
	URL        string `json:"url,omitempty"`
	PackageID  string `json:"package_id,omitempty"`
	Version    string `json:"version,omitempty"`
	Path       string `json:"path,omitempty"`
}

type Source struct {
	ID          SourceID      `json:"id"`
	Type        SourceType    `json:"type"`
	Locator     SourceLocator `json:"locator"`
	LastFetched time.Time     `json:"last_fetched"`
	LastSeenRef string        `json:"last_seen_ref"`
}

type File struct {
	Path    string   `json:"path"`
	Content []byte   `json:"content"`
	Mode    FileMode `json:"mode"`
}

type Skill struct {
	ID     SkillID `json:"id"`
	Name   string  `json:"name"`
	Source Source  `json:"source"`
	Files  []File  `json:"files"`
}

type SkillVersion struct {
	ID      VersionID `json:"id"`
	SkillID SkillID   `json:"skill_id"`
	Files   []File    `json:"files,omitempty"`
}

type VersionDiff struct {
	Changed []string `json:"changed"`
	Added   []string `json:"added"`
	Deleted []string `json:"deleted"`
}

type Group struct {
	ID   GroupID `json:"id"`
	Name string  `json:"name"`
}

type Agent string

const (
	AgentClaudeCode Agent = "claude-code"
	AgentCodex      Agent = "codex"
)

type Scope struct {
	Agent Agent  `json:"agent"`
	Path  string `json:"path"`
}

type ConflictPolicy string

const (
	Fail      ConflictPolicy = "fail"
	Overwrite ConflictPolicy = "overwrite"
)

type ActivateOptions struct {
	OnConflict ConflictPolicy `json:"on_conflict"`
}

type Activation struct {
	SkillID    SkillID   `json:"skill_id"`
	Scope      Scope     `json:"scope"`
	Individual bool      `json:"individual"`
	ViaGroups  []GroupID `json:"via_groups,omitempty"`
}

type Collection struct {
	Source Source  `json:"source"`
	Skills []Skill `json:"skills"`
}

type RegisteredProject struct {
	ID   ProjectID `json:"id"`
	Path string    `json:"path"`
}

type ProjectedCopiesPolicy string

const (
	Keep   ProjectedCopiesPolicy = "keep"
	Remove ProjectedCopiesPolicy = "remove"
)

type RemoveSkillOptions struct {
	ProjectedCopies ProjectedCopiesPolicy `json:"projected_copies"`
}

type UnregisterProjectOptions struct {
	ProjectedCopies ProjectedCopiesPolicy `json:"projected_copies"`
}

type InstallOptions struct {
	Skills []string `json:"skills,omitempty"`
}

type Library interface {
	Install(context.Context, LocalSource) (Skill, error)
	InstallRemote(context.Context, Source, InstallOptions) ([]Skill, error)
	InstallFetched(context.Context, Source, string, []File) (Skill, error)
	Update(context.Context, SkillID) (Skill, error)
	GetSkill(context.Context, SkillID) (Skill, error)
	SourceHasUpdate(context.Context, SourceID, string) (bool, error)
	ListVersions(context.Context, SkillID) ([]SkillVersion, error)
	GetVersion(context.Context, VersionID) (SkillVersion, error)
	DiffVersions(context.Context, VersionID, VersionID) (VersionDiff, error)
	CreateGroup(context.Context, string) (Group, error)
	AddSkillToGroup(context.Context, GroupID, SkillID) error
	ListGroupSkills(context.Context, GroupID) ([]Skill, error)
	ListSkills(context.Context) ([]Skill, error)
	RemoveSkill(context.Context, SkillID, RemoveSkillOptions) error
	RegisterProject(context.Context, string) (RegisteredProject, error)
	ListRegisteredProjects(context.Context) ([]RegisteredProject, error)
	UnregisterProject(context.Context, ProjectID, UnregisterProjectOptions) error
	ListScopes(context.Context) ([]Scope, error)
	Activate(context.Context, SkillID, Scope, ActivateOptions) error
	Deactivate(context.Context, SkillID, Scope) error
	ActivateGroup(context.Context, GroupID, Scope, ActivateOptions) error
	DeactivateGroup(context.Context, GroupID, Scope) error
	ListActivations(context.Context) ([]Activation, error)
	ListCollections(context.Context) ([]Collection, error)
	SyncPush(context.Context) error
	SyncPull(context.Context) error
	Close() error
}

type fetchedSkill struct {
	name  string
	files []File
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
}

func Open(path string) (Library, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("open library: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`
		PRAGMA foreign_keys = ON;
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
			FOREIGN KEY(skill_id, agent, scope_path) REFERENCES activations(skill_id, agent, scope_path) ON DELETE CASCADE
		);
	`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize library: %w", err)
	}
	return &sqliteLibrary{db: db}, nil
}

func (l *sqliteLibrary) CreateGroup(ctx context.Context, name string) (group Group, err error) {
	defer func() { err = typedError(err) }()
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

func (l *sqliteLibrary) AddSkillToGroup(ctx context.Context, groupID GroupID, skillID SkillID) (err error) {
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

func (l *sqliteLibrary) ListGroupSkills(ctx context.Context, groupID GroupID) (skills []Skill, err error) {
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
	skills = make([]Skill, 0, len(ids))
	for _, id := range ids {
		skill, err := l.GetSkill(ctx, id)
		if err != nil {
			return nil, err
		}
		skills = append(skills, skill)
	}
	return skills, nil
}

func (l *sqliteLibrary) Close() error {
	return l.db.Close()
}

func (l *sqliteLibrary) Install(ctx context.Context, source LocalSource) (skill Skill, err error) {
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

func (l *sqliteLibrary) InstallFetched(ctx context.Context, source Source, name string, files []File) (skill Skill, err error) {
	defer func() { err = typedError(err) }()
	if source.Type == SourceLocal {
		return Skill{}, fmt.Errorf("install fetched skill: local sources must use Install")
	}
	return l.install(ctx, source, name, files)
}

func (l *sqliteLibrary) InstallRemote(ctx context.Context, source Source, opts InstallOptions) (installed []Skill, err error) {
	defer func() { err = typedError(err) }()
	if err := validateSource(source); err != nil {
		return nil, err
	}
	if source.Type == SourceLocal {
		return nil, fmt.Errorf("install remote: local sources must use Install")
	}
	skills, ref, err := fetchRemote(ctx, source)
	if err != nil {
		return nil, err
	}
	skills, err = selectSkills(skills, opts.Skills)
	if err != nil {
		return nil, err
	}
	source.LastFetched = time.Now().UTC()
	source.LastSeenRef = ref
	installed = make([]Skill, 0, len(skills))
	// ponytail: skills commit independently; use one transaction if installs must become all-or-nothing.
	for _, skill := range skills {
		result, err := l.install(ctx, source, skill.name, skill.files)
		if err != nil {
			return nil, err
		}
		installed = append(installed, result)
	}
	return installed, nil
}

func (l *sqliteLibrary) install(ctx context.Context, source Source, name string, files []File) (Skill, error) {
	if err := validateSource(source); err != nil {
		return Skill{}, err
	}
	if name == "" {
		return Skill{}, fmt.Errorf("install skill: name is required")
	}
	locator, err := json.Marshal(source.Locator)
	if err != nil {
		return Skill{}, fmt.Errorf("encode source: %w", err)
	}
	fetched := ""
	if !source.LastFetched.IsZero() {
		source.LastFetched = source.LastFetched.UTC()
		fetched = source.LastFetched.Format(time.RFC3339Nano)
	}

	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return Skill{}, fmt.Errorf("begin install: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sources(type, locator, last_fetched, last_seen_ref) VALUES (?, ?, ?, ?)
		ON CONFLICT(type, locator) DO UPDATE SET last_fetched = excluded.last_fetched, last_seen_ref = excluded.last_seen_ref
	`, source.Type, locator, fetched, source.LastSeenRef); err != nil {
		return Skill{}, fmt.Errorf("store source: %w", err)
	}
	var sourceID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM sources WHERE type = ? AND locator = ?`, source.Type, locator).Scan(&sourceID); err != nil {
		return Skill{}, fmt.Errorf("read source: %w", err)
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO skills(source_id, name) VALUES (?, ?)`, sourceID, name)
	if err != nil {
		return Skill{}, fmt.Errorf("store skill: %w", err)
	}
	skillID, err := result.LastInsertId()
	if err != nil {
		return Skill{}, fmt.Errorf("read skill id: %w", err)
	}
	versionID, err := createVersion(ctx, tx, skillID, files)
	if err != nil {
		return Skill{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE skills SET current_version_id = ? WHERE id = ?`, versionID, skillID); err != nil {
		return Skill{}, fmt.Errorf("select current version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Skill{}, fmt.Errorf("commit install: %w", err)
	}
	source.ID = SourceID(sourceID)
	return Skill{ID: SkillID(skillID), Name: name, Source: source, Files: files}, nil
}

func (l *sqliteLibrary) Update(ctx context.Context, id SkillID) (skill Skill, err error) {
	defer func() { err = typedError(err) }()
	skill, err = l.GetSkill(ctx, id)
	if err != nil {
		return Skill{}, err
	}
	var files []File
	if skill.Source.Type == SourceLocal {
		files, err = readFiles(ctx, skill.Source.Locator.Path)
	} else {
		var fetched []fetchedSkill
		fetched, skill.Source.LastSeenRef, err = fetchRemote(ctx, skill.Source)
		if err == nil {
			fetched, err = selectSkills(fetched, []string{skill.Name})
		}
		if err == nil {
			files = fetched[0].files
			skill.Source.LastFetched = time.Now().UTC()
		}
	}
	if err != nil {
		return Skill{}, err
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return Skill{}, fmt.Errorf("begin update: %w", err)
	}
	defer tx.Rollback()
	if !filesEqual(skill.Files, files) {
		versionID, err := createVersion(ctx, tx, int64(id), files)
		if err != nil {
			return Skill{}, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE skills SET current_version_id = ? WHERE id = ?`, versionID, id); err != nil {
			return Skill{}, fmt.Errorf("select current version: %w", err)
		}
	}
	if skill.Source.Type != SourceLocal {
		if _, err := tx.ExecContext(ctx, `UPDATE sources SET last_fetched = ?, last_seen_ref = ? WHERE id = ?`,
			skill.Source.LastFetched.Format(time.RFC3339Nano), skill.Source.LastSeenRef, skill.Source.ID); err != nil {
			return Skill{}, fmt.Errorf("update source: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return Skill{}, fmt.Errorf("commit update: %w", err)
	}
	skill.Files = files
	return skill, nil
}

func createVersion(ctx context.Context, tx *sql.Tx, skillID int64, files []File) (int64, error) {
	result, err := tx.ExecContext(ctx, `INSERT INTO skill_versions(skill_id) VALUES (?)`, skillID)
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
	defer func() { err = typedError(err) }()
	var locator []byte
	var fetched string
	err = l.db.QueryRowContext(ctx, `
		SELECT skills.id, skills.name, sources.id, sources.type, sources.locator, sources.last_fetched, sources.last_seen_ref
		FROM skills JOIN sources ON sources.id = skills.source_id
		WHERE skills.id = ?
	`, id).Scan(&skill.ID, &skill.Name, &skill.Source.ID, &skill.Source.Type, &locator, &fetched, &skill.Source.LastSeenRef)
	if err != nil {
		return Skill{}, fmt.Errorf("get skill: %w", err)
	}
	if err := json.Unmarshal(locator, &skill.Source.Locator); err != nil {
		return Skill{}, fmt.Errorf("decode source: %w", err)
	}
	if fetched != "" {
		skill.Source.LastFetched, err = time.Parse(time.RFC3339Nano, fetched)
		if err != nil {
			return Skill{}, fmt.Errorf("decode last fetched: %w", err)
		}
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
	defer func() { err = typedError(err) }()
	rows, err := l.db.QueryContext(ctx, `SELECT id FROM skill_versions WHERE skill_id = ? ORDER BY id`, id)
	if err != nil {
		return nil, fmt.Errorf("list skill versions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		version := SkillVersion{SkillID: id}
		if err := rows.Scan(&version.ID); err != nil {
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
	defer func() { err = typedError(err) }()
	version = SkillVersion{ID: id}
	if err := l.db.QueryRowContext(ctx, `SELECT skill_id FROM skill_versions WHERE id = ?`, id).Scan(&version.SkillID); err != nil {
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

func (l *sqliteLibrary) SourceHasUpdate(ctx context.Context, id SourceID, latestRef string) (available bool, err error) {
	defer func() { err = typedError(err) }()
	var lastSeenRef string
	if err := l.db.QueryRowContext(ctx, `SELECT last_seen_ref FROM sources WHERE id = ?`, id).Scan(&lastSeenRef); err != nil {
		return false, fmt.Errorf("check source update: %w", err)
	}
	return latestRef != lastSeenRef, nil
}

func validateSource(source Source) error {
	switch source.Type {
	case SourceGitHub:
		if source.Locator.Repository == "" || source.Locator.Branch == "" {
			return fmt.Errorf("install skill: github repository and branch are required")
		}
	case SourceURL:
		if source.Locator.URL == "" {
			return fmt.Errorf("install skill: url is required")
		}
	case SourceStore:
		if source.Locator.PackageID == "" || source.Locator.Version == "" {
			return fmt.Errorf("install skill: store package id and version are required")
		}
	case SourceLocal:
		if source.Locator.Path == "" {
			return fmt.Errorf("install skill: local path is required")
		}
	default:
		return fmt.Errorf("install skill: unsupported source type %q", source.Type)
	}
	return nil
}

func (l *sqliteLibrary) ListSkills(ctx context.Context) (skills []Skill, err error) {
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
		skill, err := l.GetSkill(ctx, id)
		if err != nil {
			return nil, err
		}
		skills = append(skills, skill)
	}
	return skills, nil
}

const maxFetchSize = 64 << 20

func fetchRemote(ctx context.Context, source Source) ([]fetchedSkill, string, error) {
	switch source.Type {
	case SourceGitHub:
		return fetchGitHub(ctx, source.Locator)
	case SourceURL:
		return fetchArchive(ctx, source.Locator.URL)
	case SourceStore:
		base := os.Getenv("SKILLS_STORE_URL")
		if base == "" {
			return nil, "", fetchError(SourceUnreachable, "fetch store source", errors.New("SKILLS_STORE_URL is not configured"))
		}
		location := strings.TrimRight(base, "/") + "/" + url.PathEscape(source.Locator.PackageID) + "/" + url.PathEscape(source.Locator.Version)
		return fetchArchive(ctx, location)
	default:
		return nil, "", fmt.Errorf("install remote: unsupported source type %q", source.Type)
	}
}

func fetchGitHub(ctx context.Context, locator SourceLocator) ([]fetchedSkill, string, error) {
	root, err := os.MkdirTemp("", "skills-git-")
	if err != nil {
		return nil, "", fetchError(fetchErrorCode(err, ""), "fetch github source", err)
	}
	defer os.RemoveAll(root)
	repository := filepath.Join(root, "repository")
	command := exec.CommandContext(ctx, "git", "clone", "--quiet", "--depth", "1", "--single-branch", "--branch", locator.Branch, "--", locator.Repository, repository)
	if output, err := command.CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		return nil, "", fetchError(fetchErrorCode(err, string(output)), "fetch github source", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output))))
	}
	command = exec.CommandContext(ctx, "git", "-C", repository, "rev-parse", "HEAD")
	output, err := command.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		return nil, "", fetchError(fetchErrorCode(err, ""), "read github source ref", err)
	}
	if err := os.RemoveAll(filepath.Join(repository, ".git")); err != nil {
		return nil, "", fetchError(fetchErrorCode(err, ""), "prepare github source", err)
	}
	files, err := readFiles(ctx, repository)
	if err != nil {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		return nil, "", fetchError(fetchErrorCode(err, ""), "read github source", err)
	}
	fallback := strings.TrimSuffix(filepath.Base(strings.TrimRight(locator.Repository, "/")), ".git")
	skills, err := discoverSkills(files, fallback)
	return skills, strings.TrimSpace(string(output)), err
}

func fetchArchive(ctx context.Context, location string) ([]fetchedSkill, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return nil, "", fetchError(SourceUnreachable, "fetch source", err)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		return nil, "", fetchError(fetchErrorCode(err, ""), "fetch source", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		code := SourceUnreachable
		if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
			code = Permission
		}
		return nil, "", fetchError(code, "fetch source", errors.New(response.Status))
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, maxFetchSize+1))
	if err != nil {
		return nil, "", fetchError(fetchErrorCode(err, ""), "read source", err)
	}
	if len(content) > maxFetchSize {
		return nil, "", fetchError(SourceUnreachable, "read source", fmt.Errorf("archive exceeds %d bytes", maxFetchSize))
	}
	skills, err := readArchive(ctx, content)
	if err != nil {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		return nil, "", fetchError(SourceUnreachable, "read source", err)
	}
	sum := sha256.Sum256(content)
	return skills, hex.EncodeToString(sum[:]), nil
}

func readArchive(ctx context.Context, content []byte) ([]fetchedSkill, error) {
	reader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return nil, fmt.Errorf("read source archive: %w", err)
	}
	files := make([]File, 0, len(reader.File))
	seen := make(map[string]bool, len(reader.File))
	remaining := int64(maxFetchSize)
	for _, archived := range reader.File {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := strings.TrimSuffix(archived.Name, "/")
		if name == "" || !fs.ValidPath(name) || strings.Contains(name, `\`) {
			return nil, fmt.Errorf("read source archive: unsafe path %q", archived.Name)
		}
		if archived.FileInfo().IsDir() {
			continue
		}
		if !archived.Mode().IsRegular() {
			return nil, fmt.Errorf("read source archive: unsupported file type %q", name)
		}
		if seen[name] {
			return nil, fmt.Errorf("read source archive: duplicate path %q", name)
		}
		seen[name] = true
		file, err := archived.Open()
		if err != nil {
			return nil, fmt.Errorf("read source archive: %w", err)
		}
		data, readErr := io.ReadAll(io.LimitReader(contextReader{ctx: ctx, reader: file}, remaining+1))
		closeErr := file.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read source archive: %w", readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("read source archive: %w", closeErr)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		remaining -= int64(len(data))
		if remaining < 0 {
			return nil, fmt.Errorf("read source archive: expanded content exceeds %d bytes", maxFetchSize)
		}
		mode := ModeRegular
		if archived.Mode().Perm()&0o111 != 0 {
			mode = ModeExecutable
		}
		files = append(files, File{Path: name, Content: data, Mode: mode})
	}
	return discoverSkills(files, "skill")
}

func discoverSkills(files []File, fallbackName string) ([]fetchedSkill, error) {
	rootSet := make(map[string]bool)
	for _, file := range files {
		if path.Base(file.Path) == "SKILL.md" {
			rootSet[path.Dir(file.Path)] = true
		}
	}
	if len(rootSet) == 0 {
		return nil, fmt.Errorf("fetch source: no SKILL.md found")
	}
	roots := make([]string, 0, len(rootSet))
	for root := range rootSet {
		roots = append(roots, root)
	}
	sort.Strings(roots)
	if rootSet["."] {
		roots = []string{"."}
	}
	skills := make([]fetchedSkill, len(roots))
	names := make(map[string]bool, len(roots))
	for i, root := range roots {
		name := path.Base(root)
		if root == "." {
			name = fallbackName
		}
		if names[name] {
			return nil, fmt.Errorf("fetch source: duplicate skill name %q", name)
		}
		names[name] = true
		skills[i].name = name
		prefix := root + "/"
		for _, file := range files {
			if root == "." {
				skills[i].files = append(skills[i].files, file)
			} else if strings.HasPrefix(file.Path, prefix) {
				copy := file
				copy.Path = strings.TrimPrefix(file.Path, prefix)
				skills[i].files = append(skills[i].files, copy)
			}
		}
		sort.Slice(skills[i].files, func(a, b int) bool { return skills[i].files[a].Path < skills[i].files[b].Path })
	}
	return skills, nil
}

func (l *sqliteLibrary) RemoveSkill(ctx context.Context, id SkillID, opts RemoveSkillOptions) (err error) {
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
		`DELETE FROM activation_groups WHERE skill_id = ?`,
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
		if _, err := tx.ExecContext(ctx, `DELETE FROM activation_groups WHERE agent = ? AND scope_path = ?`, scope.Agent, scope.Path); err != nil {
			return fmt.Errorf("remove project group activations: %w", err)
		}
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
	target := filepath.Join(scope.Path, skill.Name)
	if _, err := os.Stat(target); err == nil && opts.OnConflict != Overwrite {
		return &Error{Code: AlreadyExists, Message: "activate skill: target already exists"}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect activation target: %w", err)
	}
	if err := os.MkdirAll(scope.Path, 0o755); err != nil {
		return fmt.Errorf("create scope: %w", err)
	}
	stage, err := os.MkdirTemp(scope.Path, "."+skill.Name+"-")
	if err != nil {
		return fmt.Errorf("stage activation: %w", err)
	}
	defer func() { _ = os.RemoveAll(stage) }()
	for _, file := range skill.Files {
		if !filepath.IsLocal(file.Path) {
			return fmt.Errorf("invalid skill path: %s", file.Path)
		}
		path := filepath.Join(stage, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("create activation directory: %w", err)
		}
		mode := os.FileMode(0o644)
		if file.Mode == ModeExecutable {
			mode = 0o755
		}
		if err := os.WriteFile(path, file.Content, mode); err != nil {
			return fmt.Errorf("write activation file: %w", err)
		}
	}

	backup := stage + ".old"
	if _, err := os.Stat(target); err == nil {
		if err := os.Rename(target, backup); err != nil {
			return fmt.Errorf("replace activation: %w", err)
		}
	}
	if err := os.Rename(stage, target); err != nil {
		_ = os.Rename(backup, target)
		return fmt.Errorf("activate skill: %w", err)
	}
	individualValue := 0
	if individual {
		individualValue = 1
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		_ = os.RemoveAll(target)
		_ = os.Rename(backup, target)
		return fmt.Errorf("begin activation record: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO activations(skill_id, agent, scope_path, individual) VALUES (?, ?, ?, ?)
		ON CONFLICT(skill_id, agent, scope_path) DO UPDATE SET individual = MAX(individual, excluded.individual)
	`, id, scope.Agent, scope.Path, individualValue); err != nil {
		_ = os.RemoveAll(target)
		_ = os.Rename(backup, target)
		return fmt.Errorf("record activation: %w", err)
	}
	if groupID != 0 {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO activation_groups(skill_id, agent, scope_path, group_id) VALUES (?, ?, ?, ?)`, id, scope.Agent, scope.Path, groupID); err != nil {
			_ = os.RemoveAll(target)
			_ = os.Rename(backup, target)
			return fmt.Errorf("record group activation: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		_ = os.RemoveAll(target)
		_ = os.Rename(backup, target)
		return fmt.Errorf("commit activation record: %w", err)
	}
	_ = os.RemoveAll(backup)
	return nil
}

func (l *sqliteLibrary) Deactivate(ctx context.Context, id SkillID, scope Scope) (err error) {
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
	return l.deactivateOrigin(ctx, id, scope, true, 0)
}

func (l *sqliteLibrary) deactivateOrigin(ctx context.Context, id SkillID, scope Scope, individual bool, groupID GroupID) error {
	skill, err := l.GetSkill(ctx, id)
	if err != nil {
		return err
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin deactivation: %w", err)
	}
	defer tx.Rollback()
	if individual {
		if _, err := tx.ExecContext(ctx, `UPDATE activations SET individual = 0 WHERE skill_id = ? AND agent = ? AND scope_path = ?`, id, scope.Agent, scope.Path); err != nil {
			return fmt.Errorf("remove independent activation: %w", err)
		}
	} else if _, err := tx.ExecContext(ctx, `DELETE FROM activation_groups WHERE skill_id = ? AND agent = ? AND scope_path = ? AND group_id = ?`, id, scope.Agent, scope.Path, groupID); err != nil {
		return fmt.Errorf("remove group activation: %w", err)
	}
	var keep bool
	if err := tx.QueryRowContext(ctx, `
		SELECT individual OR EXISTS(
			SELECT 1 FROM activation_groups
			WHERE skill_id = ? AND agent = ? AND scope_path = ?
		)
		FROM activations WHERE skill_id = ? AND agent = ? AND scope_path = ?
	`, id, scope.Agent, scope.Path, id, scope.Agent, scope.Path).Scan(&keep); err != nil {
		return fmt.Errorf("check activation origins: %w", err)
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

func (l *sqliteLibrary) ActivateGroup(ctx context.Context, groupID GroupID, scope Scope, opts ActivateOptions) (err error) {
	defer func() { err = typedError(err) }()
	if err := validateScope(scope); err != nil {
		return err
	}
	if err := l.requireGroup(ctx, groupID); err != nil {
		return err
	}
	skills, err := l.ListGroupSkills(ctx, groupID)
	if err != nil {
		return err
	}
	if opts.OnConflict != Overwrite {
		for _, skill := range skills {
			if _, err := os.Stat(filepath.Join(scope.Path, skill.Name)); err == nil {
				return &Error{Code: AlreadyExists, Message: "activate group: target already exists"}
			} else if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("inspect activation target: %w", err)
			}
		}
	}
	// ponytail: runtime I/O failures can leave a partial group; stage every member first if all-or-nothing filesystem commits become necessary.
	for _, skill := range skills {
		if err := l.activate(ctx, skill.ID, scope, opts, false, groupID); err != nil {
			return err
		}
	}
	return nil
}

func (l *sqliteLibrary) DeactivateGroup(ctx context.Context, groupID GroupID, scope Scope) (err error) {
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
	for _, id := range ids {
		if err := l.deactivateOrigin(ctx, id, scope, false, groupID); err != nil {
			return err
		}
	}
	return nil
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

func validateScope(scope Scope) error {
	if scope.Path == "" || (scope.Agent != AgentClaudeCode && scope.Agent != AgentCodex) {
		return fmt.Errorf("invalid scope")
	}
	return nil
}

func (l *sqliteLibrary) ListActivations(ctx context.Context) (activations []Activation, err error) {
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
	var libraryErr *Error
	if errors.As(err, &libraryErr) {
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
	defer func() { err = typedError(err) }()
	rows, err := l.db.QueryContext(ctx, `SELECT source_id, id FROM skills ORDER BY source_id, id`)
	if err != nil {
		return nil, fmt.Errorf("list collections: %w", err)
	}
	type membership struct {
		sourceID int64
		skillID  SkillID
	}
	var memberships []membership
	for rows.Next() {
		var item membership
		if err := rows.Scan(&item.sourceID, &item.skillID); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("read collection membership: %w", err)
		}
		memberships = append(memberships, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("read collections: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close collections: %w", err)
	}

	// ponytail: one lookup per skill; replace with a joined mapper if library size makes this measurable.
	var currentSourceID int64
	for _, item := range memberships {
		skill, err := l.GetSkill(ctx, item.skillID)
		if err != nil {
			return nil, err
		}
		if len(collections) == 0 || item.sourceID != currentSourceID {
			collections = append(collections, Collection{Source: skill.Source})
			currentSourceID = item.sourceID
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
			return nil, fmt.Errorf("install remote: skill %q not found", name)
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

func fetchErrorCode(err error, detail string) ErrorCode {
	if errors.Is(err, fs.ErrPermission) {
		return Permission
	}
	lower := strings.ToLower(detail)
	if strings.Contains(lower, "authentication") || strings.Contains(lower, "permission denied") || strings.Contains(lower, "access denied") || strings.Contains(lower, "could not read username") || strings.Contains(lower, "403") {
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
