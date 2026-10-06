package library

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/tiaot33/skills-tui/internal/library/internal/agentdir"
	"github.com/tiaot33/skills-tui/internal/library/internal/source"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/discovery"
	"github.com/tiaot33/skills-tui/internal/library/internal/store"
)

func sourceOf(row store.Source) (Source, error) {
	src := Source{ID: SourceID(row.ID), Type: SourceType(row.Type), LastFetched: row.LastFetched, LastSeenRef: row.LastSeenRef}
	if err := json.Unmarshal(row.Locator, &src.Locator); err != nil {
		return Source{}, fmt.Errorf("decode source: %w", err)
	}
	return src, nil
}

func sourceRecord(src Source) (store.Source, error) {
	locator, err := json.Marshal(src.Locator)
	if err != nil {
		return store.Source{}, fmt.Errorf("encode source: %w", err)
	}
	return store.Source{ID: int64(src.ID), Type: string(src.Type), Locator: locator, LastFetched: src.LastFetched, LastSeenRef: src.LastSeenRef}, nil
}

func summaryOf(row store.Summary) (SkillSummary, error) {
	src, err := sourceOf(row.Source)
	if err != nil {
		return SkillSummary{}, err
	}
	summary := SkillSummary{ID: SkillID(row.ID), Name: row.Name, Description: row.Description, Source: src, FetchedAt: row.FetchedAt, UpdatedAt: row.UpdatedAt}
	if row.CheckStatus != "" {
		summary.LastUpdateCheck = &UpdateCheckState{CheckedAt: row.CheckedAt, Status: UpdateCheckStatus(row.CheckStatus)}
	}
	return summary, nil
}

func summariesOf(rows []store.Summary, err error) ([]SkillSummary, error) {
	if err != nil {
		return nil, err
	}
	var skills []SkillSummary
	for _, row := range rows {
		summary, err := summaryOf(row)
		if err != nil {
			return nil, err
		}
		skills = append(skills, summary)
	}
	return skills, nil
}

func (l *sqliteLibrary) getSummary(ctx context.Context, id SkillID) (SkillSummary, error) {
	row, err := l.store.GetSummary(ctx, int64(id))
	if err != nil {
		return SkillSummary{}, err
	}
	return summaryOf(row)
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

func clockNow() time.Time {
	now, _ := parseStoredTime(formatStoredTime(time.Now().UTC()))
	return now
}

func skillDescription(files []File) string {
	for _, file := range files {
		if file.Path != "SKILL.md" {
			continue
		}
		meta, err := discovery.ParseSkillMD(file.Content, ".")
		if err != nil {
			return ""
		}
		return meta.Description
	}
	return ""
}

func (l *sqliteLibrary) fetchAll(ctx context.Context, src Source, skills []source.Skill) ([]Skill, error) {
	if err := validateSource(src); err != nil {
		return nil, err
	}
	record, err := sourceRecord(src)
	if err != nil {
		return nil, err
	}
	fetched := make([]Skill, 0, len(skills))
	err = l.store.InTx(ctx, func(tx *store.Tx) error {
		sourceID, err := tx.UpsertSource(ctx, record)
		if err != nil {
			return err
		}
		for _, skill := range skills {
			id, err := tx.FindSkill(ctx, sourceID, skill.Name)
			if err != nil {
				return err
			}
			if id != 0 {
				return existsError("fetch skill", "skill already fetched", ErrorDetails{SkillID: SkillID(id), Name: skill.Name})
			}
		}
		for _, skill := range skills {
			if skill.Name == "" {
				return fmt.Errorf("fetch skill: name is required")
			}
			description := skill.Description
			if description == "" {
				description = skillDescription(skill.Files)
			}
			createdAt := clockNow()
			skillID, versionID, err := tx.InsertSkill(ctx, sourceID, skill.Name, store.NewVersion{Description: description, Files: skill.Files, State: string(skill.State), CreatedAt: createdAt})
			if err != nil {
				return err
			}
			result := src
			result.ID = SourceID(sourceID)
			result.LastFetched = result.LastFetched.UTC()
			fetched = append(fetched, Skill{ID: SkillID(skillID), Name: skill.Name, Description: description, Source: result, VersionID: VersionID(versionID), FetchedAt: createdAt, UpdatedAt: createdAt, Files: skill.Files})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return fetched, nil
}

func validateSource(source Source) error {
	switch source.Type {
	case SourceGitHub:
		if source.Locator.Repository == "" || source.Locator.Branch == "" {
			return &Error{Code: InvalidArgument, Message: "invalid source: github repository and branch are required"}
		}
	case SourceURL:
		if source.Locator.URL == "" {
			return &Error{Code: InvalidArgument, Message: "invalid source: url is required"}
		}
	case sourceStore:
		if source.Locator.PackageID == "" || source.Locator.Version == "" {
			return &Error{Code: InvalidArgument, Message: "invalid source: store package id and version are required"}
		}
	case SourceLocal:
		if source.Locator.Path == "" {
			return &Error{Code: InvalidArgument, Message: "invalid source: local path is required"}
		}
	default:
		return &Error{Code: InvalidArgument, Message: fmt.Sprintf("invalid source: unsupported source type %q", source.Type)}
	}
	return nil
}

func (l *sqliteLibrary) GetSkill(ctx context.Context, id SkillID) (skill Skill, err error) {
	defer func() { err = typedError(err) }()
	skill, _, err = l.getSkill(ctx, id)
	return skill, err
}

type sourceState struct {
	checkedAt time.Time
	versionID int64
	snapshot  source.State
}

func (l *sqliteLibrary) getSkill(ctx context.Context, id SkillID) (Skill, sourceState, error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	summary, err := l.getSummary(ctx, id)
	if err != nil {
		return Skill{}, sourceState{}, err
	}
	current, err := l.store.GetCurrent(ctx, int64(id))
	if err != nil {
		return Skill{}, sourceState{}, err
	}
	skill := Skill{
		ID:          summary.ID,
		Name:        summary.Name,
		Description: summary.Description,
		Source:      summary.Source,
		VersionID:   VersionID(current.VersionID),
		FetchedAt:   summary.FetchedAt,
		UpdatedAt:   summary.UpdatedAt,
		Files:       current.Files,
	}
	return skill, sourceState{checkedAt: current.CheckedAt, versionID: current.VersionID, snapshot: source.State(current.State)}, nil
}

func (l *sqliteLibrary) ListSkillFiles(ctx context.Context, id SkillID) (files []FileMetadata, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	rows, err := l.store.ListCurrentFiles(ctx, int64(id))
	if errors.Is(err, store.ErrNotFound) {
		return nil, &Error{Code: NotFound, Message: "list skill files: skill not found", Details: ErrorDetails{SkillID: id}}
	}
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		files = append(files, FileMetadata{Path: row.Path, Size: row.Size, Mode: row.Mode})
	}
	return files, nil
}

func (l *sqliteLibrary) GetSkillFile(ctx context.Context, id SkillID, path string) (file File, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	file, found, err := l.store.GetCurrentFile(ctx, int64(id), path)
	if errors.Is(err, store.ErrNotFound) {
		return File{}, &Error{Code: NotFound, Message: "get skill file: skill not found", Details: ErrorDetails{SkillID: id}}
	}
	if err != nil {
		return File{}, err
	}
	if !found {
		return File{}, &Error{Code: NotFound, Message: "get skill file: file not found", Details: ErrorDetails{Path: path}}
	}
	return file, nil
}

func (l *sqliteLibrary) ListSkills(ctx context.Context) (skills []SkillSummary, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	return summariesOf(l.store.ListSummaries(ctx))
}

func (l *sqliteLibrary) RemoveSkill(ctx context.Context, id SkillID, opts RemoveSkillOptions) (err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if err := validateFolderPolicy("remove skill", opts.Folders); err != nil {
		return err
	}
	skill, err := l.GetSkill(ctx, id)
	if err != nil {
		return err
	}
	var change agentdir.Change
	defer func() { err = errors.Join(err, change.Rollback()) }()
	if opts.Folders != Keep {
		skillScopes, err := l.ListSkillScopes(ctx)
		if err != nil {
			return err
		}
		for _, skillScope := range skillScopes {
			if skillScope.SkillID == id {
				change.Remove(skillScope.Directory, skill.Name)
			}
		}
	}

	return l.applyChange(ctx, &change, func(tx *store.Tx) error {
		return tx.RemoveSkill(ctx, int64(id))
	})
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
