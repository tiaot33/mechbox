package library

import (
	"context"
	"time"

	"github.com/tiaot33/skills-tui/internal/library/internal/skillfile"
	"github.com/tiaot33/skills-tui/internal/library/internal/store"
)

// VersionID 是技能版本的数据库主键:全库唯一,同一技能内越新越大。
type VersionID int64

// SkillVersion 是 skill 的一个历史版本;列表场景下 Files 为空,详情才带内容。
type SkillVersion struct {
	ID          VersionID `json:"id"`
	SkillID     SkillID   `json:"skill_id"`
	Description string    `json:"description"`         // 该版本 SKILL.md frontmatter 的 description;解析不到则为空
	Revision    string    `json:"revision,omitempty"`  // 最近一次确认该版本内容时的来源标识:GitHub 为 commit,URL 为内容哈希,本地目录为空
	CreatedAt   time.Time `json:"created_at,omitzero"` // 版本写入时间
	Files       []File    `json:"files,omitempty"`
}

// VersionDiff 是两个版本之间按文件路径归类的差异。
type VersionDiff struct {
	Changed []string `json:"changed"` // 两个版本都有、内容不同
	Added   []string `json:"added"`   // 仅新版本有
	Deleted []string `json:"deleted"` // 仅旧版本有
}

func versionOf(row store.Version) SkillVersion {
	return SkillVersion{ID: VersionID(row.ID), SkillID: SkillID(row.SkillID), Description: row.Description, Revision: row.Revision, CreatedAt: row.CreatedAt, Files: row.Files}
}

// ListVersions 按先后顺序列出 skill 的全部版本(不含文件内容)。
func (l *Library) ListVersions(ctx context.Context, id SkillID) (versions []SkillVersion, err error) {
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

// GetVersion 读取某个版本及其完整文件。
func (l *Library) GetVersion(ctx context.Context, id VersionID) (version SkillVersion, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	row, err := l.store.GetVersion(ctx, int64(id))
	if err != nil {
		return SkillVersion{}, err
	}
	return versionOf(row), nil
}

// DiffVersions 比较同一 skill 的两个版本,返回按路径归类的差异。
func (l *Library) DiffVersions(ctx context.Context, oldID, newID VersionID) (diff VersionDiff, err error) {
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
