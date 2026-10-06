package library

import (
	"context"

	"github.com/tiaot33/skills-tui/internal/library/internal/skillfile"
	"github.com/tiaot33/skills-tui/internal/library/internal/store"
)

func versionOf(row store.Version) SkillVersion {
	return SkillVersion{ID: VersionID(row.ID), SkillID: SkillID(row.SkillID), Description: row.Description, CreatedAt: row.CreatedAt, Files: row.Files}
}

func (l *sqliteLibrary) ListVersions(ctx context.Context, id SkillID) (versions []SkillVersion, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	rows, err := l.store.ListVersions(ctx, int64(id))
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		versions = append(versions, versionOf(row))
	}
	return versions, nil
}

func (l *sqliteLibrary) GetVersion(ctx context.Context, id VersionID) (version SkillVersion, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	row, err := l.store.GetVersion(ctx, int64(id))
	if err != nil {
		return SkillVersion{}, err
	}
	return versionOf(row), nil
}

func (l *sqliteLibrary) DiffVersions(ctx context.Context, oldID, newID VersionID) (diff VersionDiff, err error) {
	ctx, unlock := l.sharedOperation(ctx)
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
		return VersionDiff{}, argumentError("diff skill versions", "", "versions belong to different skills")
	}
	oldFiles := make(map[string]string, len(oldVersion.Files))
	for _, file := range oldVersion.Files {
		oldFiles[file.Path] = skillfile.Fingerprint(file.Content)
	}
	newFiles := make(map[string]string, len(newVersion.Files))
	for _, file := range newVersion.Files {
		newFiles[file.Path] = skillfile.Fingerprint(file.Content)
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
