package library

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type SkillID int64
type SourceID int64

type VersionID int64
type GroupID int64

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

type Library interface {
	Install(context.Context, LocalSource) (Skill, error)
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
	Close() error
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
	`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize library: %w", err)
	}
	return &sqliteLibrary{db: db}, nil
}

func (l *sqliteLibrary) CreateGroup(ctx context.Context, name string) (Group, error) {
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

func (l *sqliteLibrary) AddSkillToGroup(ctx context.Context, groupID GroupID, skillID SkillID) error {
	if _, err := l.db.ExecContext(ctx, `INSERT INTO skill_groups(skill_id, group_id) VALUES (?, ?)`, skillID, groupID); err != nil {
		return fmt.Errorf("add skill to group: %w", err)
	}
	return nil
}

func (l *sqliteLibrary) ListGroupSkills(ctx context.Context, groupID GroupID) ([]Skill, error) {
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
	skills := make([]Skill, 0, len(ids))
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

func (l *sqliteLibrary) Install(ctx context.Context, source LocalSource) (Skill, error) {
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

func (l *sqliteLibrary) InstallFetched(ctx context.Context, source Source, name string, files []File) (Skill, error) {
	if source.Type == SourceLocal {
		return Skill{}, fmt.Errorf("install fetched skill: local sources must use Install")
	}
	return l.install(ctx, source, name, files)
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

func (l *sqliteLibrary) Update(ctx context.Context, id SkillID) (Skill, error) {
	skill, err := l.GetSkill(ctx, id)
	if err != nil {
		return Skill{}, err
	}
	files, err := readFiles(ctx, skill.Source.Locator.Path)
	if err != nil {
		return Skill{}, err
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return Skill{}, fmt.Errorf("begin update: %w", err)
	}
	defer tx.Rollback()
	versionID, err := createVersion(ctx, tx, int64(id), files)
	if err != nil {
		return Skill{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE skills SET current_version_id = ? WHERE id = ?`, versionID, id); err != nil {
		return Skill{}, fmt.Errorf("select current version: %w", err)
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

func (l *sqliteLibrary) GetSkill(ctx context.Context, id SkillID) (Skill, error) {
	var skill Skill
	var locator []byte
	var fetched string
	err := l.db.QueryRowContext(ctx, `
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

func (l *sqliteLibrary) ListVersions(ctx context.Context, id SkillID) ([]SkillVersion, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT id FROM skill_versions WHERE skill_id = ? ORDER BY id`, id)
	if err != nil {
		return nil, fmt.Errorf("list skill versions: %w", err)
	}
	defer rows.Close()
	var versions []SkillVersion
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

func (l *sqliteLibrary) GetVersion(ctx context.Context, id VersionID) (SkillVersion, error) {
	version := SkillVersion{ID: id}
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

func (l *sqliteLibrary) DiffVersions(ctx context.Context, oldID, newID VersionID) (VersionDiff, error) {
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
	var diff VersionDiff
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

func (l *sqliteLibrary) SourceHasUpdate(ctx context.Context, id SourceID, latestRef string) (bool, error) {
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
