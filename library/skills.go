package library

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"time"
)

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

func (l *sqliteLibrary) requireSkill(ctx context.Context, id SkillID) error {
	var exists int
	if err := l.db.QueryRowContext(ctx, `SELECT 1 FROM skills WHERE id = ?`, id).Scan(&exists); err != nil {
		return fmt.Errorf("get skill: %w", err)
	}
	return nil
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
