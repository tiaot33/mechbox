package library

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"time"
)

// summaryQuery 选出 SkillSummary 的全部列;调用方追加 WHERE / ORDER BY。
// InstalledAt 是最早版本的 created_at,UpdatedAt 与 Description 来自当前版本。
const summaryQuery = `
	SELECT skills.id, skills.name, current_version.description,
		sources.id, sources.type, sources.locator, sources.last_fetched, sources.last_seen_ref,
		first_version.created_at, current_version.created_at
	FROM skills
	JOIN sources ON sources.id = skills.source_id
	JOIN skill_versions AS current_version ON current_version.id = skills.current_version_id
	JOIN skill_versions AS first_version ON first_version.id = (
		SELECT id FROM skill_versions WHERE skill_id = skills.id ORDER BY id LIMIT 1
	)
`

// getSummary 读取一个 skill 的身份、名字与来源元数据,不含文件正文。
func (l *sqliteLibrary) getSummary(ctx context.Context, id SkillID) (SkillSummary, error) {
	summary, err := scanSummary(l.db.QueryRowContext(ctx, summaryQuery+`WHERE skills.id = ?`, id))
	if err != nil {
		return SkillSummary{}, fmt.Errorf("get skill: %w", err)
	}
	return summary, nil
}

// listSummaries 用一条语句读出一组 skill 的摘要:列表在一个快照里完成,另一进程在两条语句之间
// 删掉一个 skill 不会让整个列表失败。
func (l *sqliteLibrary) listSummaries(ctx context.Context, operation, clause string, args ...any) ([]SkillSummary, error) {
	rows, err := l.db.QueryContext(ctx, summaryQuery+clause, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", operation, err)
	}
	defer rows.Close()
	var skills []SkillSummary
	for rows.Next() {
		summary, err := scanSummary(rows)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", operation, err)
		}
		skills = append(skills, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", operation, err)
	}
	return skills, nil
}

func scanSummary(row interface{ Scan(...any) error }) (SkillSummary, error) {
	var summary SkillSummary
	var locator []byte
	var fetched, installedAt, updatedAt string
	err := row.Scan(&summary.ID, &summary.Name, &summary.Description, &summary.Source.ID, &summary.Source.Type, &locator, &fetched, &summary.Source.LastSeenRef, &installedAt, &updatedAt)
	if err != nil {
		return SkillSummary{}, err
	}
	if err := json.Unmarshal(locator, &summary.Source.Locator); err != nil {
		return SkillSummary{}, fmt.Errorf("decode source: %w", err)
	}
	if summary.Source.LastFetched, err = parseStoredTime(fetched); err != nil {
		return SkillSummary{}, fmt.Errorf("decode last fetched: %w", err)
	}
	if summary.InstalledAt, err = parseStoredTime(installedAt); err != nil {
		return SkillSummary{}, fmt.Errorf("decode installed at: %w", err)
	}
	if summary.UpdatedAt, err = parseStoredTime(updatedAt); err != nil {
		return SkillSummary{}, fmt.Errorf("decode updated at: %w", err)
	}
	return summary, nil
}

func parseStoredTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, value)
}

func formatStoredTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

// clockNow 返回写入 TEXT 列再读回后的 UTC 时间,去掉单调时钟,便于与 Get/List 的解析结果 DeepEqual。
func clockNow() time.Time {
	now, _ := parseStoredTime(formatStoredTime(time.Now().UTC()))
	return now
}

// skillDescription 从文件集里的 SKILL.md 取 frontmatter description;没有或解析失败则为空。
func skillDescription(files []File) string {
	for _, file := range files {
		if file.Path != "SKILL.md" {
			continue
		}
		meta, err := candidateMetadata(file.Content, ".")
		if err != nil {
			return ""
		}
		return meta.Description
	}
	return ""
}

func (l *sqliteLibrary) Install(ctx context.Context, source LocalSource) (skill Skill, err error) {
	ctx, unlock := l.sharedOperation(ctx)
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
// validator 是本次抓取时 URL / Store 服务端给出的 HTTP validator,随 Source 一起写入;其他来源为空。
func (l *sqliteLibrary) installAll(ctx context.Context, source Source, skills []fetchedSkill, validator string) ([]Skill, error) {
	if err := validateSource(source); err != nil {
		return nil, err
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin install: %w", err)
	}
	defer tx.Rollback()
	sourceID, err := upsertSource(ctx, tx, source, validator)
	if err != nil {
		return nil, err
	}
	for _, skill := range skills {
		id, err := findSkillBySourceName(ctx, tx, sourceID, skill.name)
		if err != nil {
			return nil, err
		}
		if id != 0 {
			return nil, existsError("install skill", "skill already installed", ErrorDetails{SkillID: id, Name: skill.name})
		}
	}
	installed := make([]Skill, 0, len(skills))
	for _, skill := range skills {
		// GitHub 文件取自 Fetch 钉住的 commit(source.LastSeenRef),它就是该目录 tree 首次被确认为远端最新的地方。
		description := skill.preview.Description
		if description == "" {
			description = skillDescription(skill.files)
		}
		result, err := insertSkill(ctx, tx, sourceID, source, skill.name, skill.files, sourceGitRecord(source, skill.gitTreeSHA), description)
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
	skills, err := l.installAll(ctx, source, []fetchedSkill{{name: name, files: files}}, "")
	if err != nil {
		return Skill{}, err
	}
	return skills[0], nil
}

// upsertSource 在事务内插入或更新来源行(含本次抓取的 HTTP validator),返回其 id。
func upsertSource(ctx context.Context, tx *sql.Tx, source Source, validator string) (int64, error) {
	locator, err := json.Marshal(source.Locator)
	if err != nil {
		return 0, fmt.Errorf("encode source: %w", err)
	}
	fetched := formatStoredTime(source.LastFetched)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sources(type, locator, last_fetched, last_seen_ref, http_validator) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(type, locator) DO UPDATE SET
			last_fetched = excluded.last_fetched, last_seen_ref = excluded.last_seen_ref, http_validator = excluded.http_validator
	`, source.Type, locator, fetched, source.LastSeenRef, validator); err != nil {
		return 0, fmt.Errorf("store source: %w", err)
	}
	var sourceID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM sources WHERE type = ? AND locator = ?`, source.Type, locator).Scan(&sourceID); err != nil {
		return 0, fmt.Errorf("read source: %w", err)
	}
	return sourceID, nil
}

// findSkillBySourceName 在事务内查找同一来源下的同名 skill;不存在时返回 0。
func findSkillBySourceName(ctx context.Context, tx *sql.Tx, sourceID int64, name string) (SkillID, error) {
	var id SkillID
	err := tx.QueryRowContext(ctx, `SELECT id FROM skills WHERE source_id = ? AND name = ?`, sourceID, name).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("find skill: %w", err)
	}
	return id, nil
}

// insertSkill 在事务内写入一个 skill 及其首个版本,并设为当前版本。
func insertSkill(ctx context.Context, tx *sql.Tx, sourceID int64, source Source, name string, files []File, record gitRecord, description string) (Skill, error) {
	// 名字由来源发现阶段给出,调用方没有对应参数;为空是内部不变量被破坏,归 Internal。
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
	createdAt := clockNow()
	versionID, err := createVersion(ctx, tx, skillID, files, record, description, createdAt)
	if err != nil {
		return Skill{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE skills SET current_version_id = ? WHERE id = ?`, versionID, skillID); err != nil {
		return Skill{}, fmt.Errorf("select current version: %w", err)
	}
	source.ID = SourceID(sourceID)
	source.LastFetched = source.LastFetched.UTC()
	return Skill{ID: SkillID(skillID), Name: name, Description: description, Source: source, GitTreeSHA: record.treeSHA, InstalledAt: createdAt, UpdatedAt: createdAt, Files: files}, nil
}

func (l *sqliteLibrary) GetSkill(ctx context.Context, id SkillID) (skill Skill, err error) {
	defer func() { err = typedError(err) }()
	skill, _, err = l.getSkill(ctx, id)
	return skill, err
}

// fetchState 是门面不暴露、检查与更新据以走快路径的抓取记录,以及这份内容所属的版本 ID。
type fetchState struct {
	versionID       int64  // 返回文件所属的当前版本;Update 用它做提交阶段的复核依据
	confirmedCommit string // 当前版本的确认 commit;仅 GitHub 来源非空
	httpValidator   string // Source 上次成功抓取时服务端给出的 HTTP validator 原文;仅 URL / Store 来源非空
}

// getSkill 读取 skill 及其当前版本的完整文件,连同其抓取记录。
// 先钉住版本 ID,再按该版本取文件,因此即使期间别处提交了新版本,返回的文件与版本 ID 也彼此一致。
// 自带共享锁:Update / CheckUpdate 的网络阶段不持锁,读库这一步由它自己保护。
func (l *sqliteLibrary) getSkill(ctx context.Context, id SkillID) (Skill, fetchState, error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	summary, err := l.getSummary(ctx, id)
	if err != nil {
		return Skill{}, fetchState{}, err
	}
	skill := Skill{
		ID:          summary.ID,
		Name:        summary.Name,
		Description: summary.Description,
		Source:      summary.Source,
		InstalledAt: summary.InstalledAt,
		UpdatedAt:   summary.UpdatedAt,
	}
	var state fetchState
	if err := l.db.QueryRowContext(ctx, `
		SELECT skill_versions.id, skill_versions.git_tree_sha, skill_versions.confirmed_commit, sources.http_validator
		FROM skills
		JOIN skill_versions ON skill_versions.id = skills.current_version_id
		JOIN sources ON sources.id = skills.source_id
		WHERE skills.id = ?
	`, id).Scan(&state.versionID, &skill.GitTreeSHA, &state.confirmedCommit, &state.httpValidator); err != nil {
		return Skill{}, fetchState{}, fmt.Errorf("get skill version: %w", err)
	}

	rows, err := l.db.QueryContext(ctx, `
		SELECT version_files.path, blobs.content, version_files.mode
		FROM version_files
		JOIN blobs ON blobs.fingerprint = version_files.fingerprint
		WHERE version_files.version_id = ?
		ORDER BY version_files.path
	`, state.versionID)
	if err != nil {
		return Skill{}, fetchState{}, fmt.Errorf("get skill files: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var file File
		if err := rows.Scan(&file.Path, &file.Content, &file.Mode); err != nil {
			return Skill{}, fetchState{}, fmt.Errorf("read skill file: %w", err)
		}
		skill.Files = append(skill.Files, file)
	}
	if err := rows.Err(); err != nil {
		return Skill{}, fetchState{}, fmt.Errorf("read skill files: %w", err)
	}
	return skill, state, nil
}

func (l *sqliteLibrary) ListSkills(ctx context.Context) (skills []SkillSummary, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	return l.listSummaries(ctx, "list skills", `ORDER BY skills.name, skills.id`)
}

func (l *sqliteLibrary) RemoveSkill(ctx context.Context, id SkillID, opts RemoveSkillOptions) (err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if err := validateProjectedCopies("remove skill", opts.ProjectedCopies); err != nil {
		return err
	}
	skill, err := l.GetSkill(ctx, id)
	if err != nil {
		return err
	}
	// 待删的投影路径在事务外先算好:单连接模式下事务内不能再走 l.db。
	var trash projectionTrash
	if opts.ProjectedCopies == Remove {
		activations, err := l.ListActivations(ctx)
		if err != nil {
			return err
		}
		for _, activation := range activations {
			if activation.SkillID == id {
				trash.targets = append(trash.targets, filepath.Join(activation.Directory, skill.Name))
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
	return trash.commit(ctx, tx, "remove skill")
}

func (l *sqliteLibrary) requireSkill(ctx context.Context, id SkillID) error {
	var exists int
	if err := l.db.QueryRowContext(ctx, `SELECT 1 FROM skills WHERE id = ?`, id).Scan(&exists); err != nil {
		return fmt.Errorf("get skill: %w", err)
	}
	return nil
}

func (l *sqliteLibrary) ListCollections(ctx context.Context) (collections []Collection, err error) {
	ctx, unlock := l.sharedOperation(ctx)
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
