package library

import (
	"context"
	"errors"
	"time"

	"github.com/tiaot33/skills-tui/internal/library/internal/agentdir"
	"github.com/tiaot33/skills-tui/internal/library/internal/skillfile"
	"github.com/tiaot33/skills-tui/internal/library/internal/source"
	"github.com/tiaot33/skills-tui/internal/library/internal/store"
)

// SkillID 是库里技能的数据库主键。各实体的 ID 定义为不同类型,以免互相传错。
type SkillID int64

// SourceID 是来源的数据库主键。
type SourceID int64

// SourceType 是 skill 来源的类型(见 ADR 0004)。
type SourceType string

const (
	SourceGitHub SourceType = "github" // GitHub 仓库 + Git ref
	SourceURL    SourceType = "url"    // 直接下载的 zip 压缩包
	SourceLocal  SourceType = "local"  // 本地目录
)

// FileMode 记录文件是否可执行,安装到磁盘时据此设置权限位。
type FileMode = skillfile.Mode

const (
	ModeRegular    = skillfile.ModeRegular
	ModeExecutable = skillfile.ModeExecutable
)

// Source 是一个已记录的 skill 来源。展示用的字段由库按来源类型生成,调用方不必区分类型去拼。
type Source struct {
	ID      SourceID   `json:"id"`
	Type    SourceType `json:"type"`
	Label   string     `json:"label"`              // 展示名称:GitHub 为 owner/repo,URL 为压缩包地址,本地目录为绝对路径
	Ref     string     `json:"ref,omitempty"`      // Git 类来源入库时使用的分支、tag 或完整 SHA;其他来源为空
	ViewURL string     `json:"view_url,omitempty"` // 在来源上查看的网页;URL 与本地目录为空
}

// File 是 skill 里的一个文件;Path 是 skill 内斜杠分隔的相对路径。
type File = skillfile.File

// FileMetadata 是文件列表用的元数据,不含文件内容。
type FileMetadata struct {
	Path string   `json:"path"` // skill 内斜杠分隔的相对路径
	Size int64    `json:"size"` // 内容的字节数
	Mode FileMode `json:"mode"` // regular 或 executable
}

// Skill 是库里的一个技能;列表、详情、入库结果与组成员都返回它。不含文件内容,
// 文件用 VersionID 经 ListVersionFiles / GetVersionFile 读取。
type Skill struct {
	LastUpdateCheck *UpdateCheckState `json:"last_update_check,omitempty"` // 最近一次更新检查的结论;nil 表示从未检查
	ID              SkillID           `json:"id"`
	Name            string            `json:"name"`
	Description     string            `json:"description"` // 当前版本 SKILL.md frontmatter 的 description;解析不到则为空
	Source          Source            `json:"source"`
	VersionID       VersionID         `json:"version_id"`          // 当前版本的 SkillVersion.ID,用于读取文件、查看版本历史与差异
	Revision        string            `json:"revision,omitempty"`  // 当前版本对应的来源标识,与入库预览的 Revision 同义:GitHub 为 commit,URL 为内容哈希,本地目录为空
	FetchedAt       time.Time         `json:"fetched_at,omitzero"` // 入库时间
	UpdatedAt       time.Time         `json:"updated_at,omitzero"` // 最近一次更新带来新版本的时间;从未因更新产生新版本时为零值
}

// FolderPolicy 决定移除 skill 或注销项目时,磁盘上已安装的技能文件夹如何处理。
type FolderPolicy string

const (
	Keep   FolderPolicy = "keep"   // 保留磁盘上的技能文件夹,只删除数据库记录;必须显式选择
	Remove FolderPolicy = "remove" // 连同磁盘上的技能文件夹一起删除(默认,空字符串同义)
)

// RemoveSkillOptions 是 RemoveSkill 的选项。
type RemoveSkillOptions struct {
	Folders FolderPolicy `json:"folders"` // 默认删除技能文件夹,只有显式 Keep 保留
}

func (l *Library) sourceOf(row store.Source) (Source, error) {
	description, err := l.sources.Describe(row.Type, row.Locator)
	if err != nil {
		return Source{}, err
	}
	return Source{
		ID:      SourceID(row.ID),
		Type:    SourceType(row.Type),
		Label:   description.Label,
		Ref:     description.Ref,
		ViewURL: description.ViewURL,
	}, nil
}

func (l *Library) skillOf(row store.Skill) (Skill, error) {
	src, err := l.sourceOf(row.Source)
	if err != nil {
		return Skill{}, err
	}
	skill := Skill{
		ID:          SkillID(row.ID),
		Name:        row.Name,
		Description: row.Description,
		Source:      src,
		VersionID:   VersionID(row.VersionID),
		Revision:    row.Revision,
		FetchedAt:   row.FetchedAt,
		UpdatedAt:   row.UpdatedAt,
	}
	if row.CheckStatus != "" {
		skill.LastUpdateCheck = &UpdateCheckState{CheckedAt: row.CheckedAt, Status: UpdateCheckStatus(row.CheckStatus)}
	}
	return skill, nil
}

func (l *Library) skillsOf(rows []store.Skill, err error) ([]Skill, error) {
	if err != nil {
		return nil, err
	}
	var skills []Skill
	for _, row := range rows {
		skill, err := l.skillOf(row)
		if err != nil {
			return nil, err
		}
		skills = append(skills, skill)
	}
	return skills, nil
}

func clockNow() time.Time {
	return time.Now().UTC().Round(0)
}

// GetSkill 读取库里的一个技能,与 ListSkills 返回的条目相同;技能不存在返回 NotFound。
func (l *Library) GetSkill(ctx context.Context, id SkillID) (skill Skill, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	row, err := l.store.GetSkill(ctx, int64(id))
	if err != nil {
		return Skill{}, err
	}
	return l.skillOf(row)
}

type currentVersion struct {
	source    store.Source
	checkedAt time.Time
	versionID int64
	snapshot  source.State
	revision  string
	files     []File
}

func (l *Library) getCurrent(ctx context.Context, id SkillID) (Skill, currentVersion, error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	row, err := l.store.GetSkill(ctx, int64(id))
	if err != nil {
		return Skill{}, currentVersion{}, err
	}
	skill, err := l.skillOf(row)
	if err != nil {
		return Skill{}, currentVersion{}, err
	}
	current, err := l.store.GetCurrent(ctx, int64(id))
	if err != nil {
		return Skill{}, currentVersion{}, err
	}
	return skill, currentVersion{
		source:    row.Source,
		checkedAt: current.CheckedAt,
		versionID: current.VersionID,
		snapshot:  source.State(current.State),
		revision:  current.Revision,
		files:     current.Files,
	}, nil
}

// ListVersionFiles 按路径排序列出某个版本的文件元数据,不读取文件正文;版本不存在返回 NotFound。
// 查看技能当前的文件时传 Skill.VersionID,浏览途中技能被更新也仍读到同一版本。
func (l *Library) ListVersionFiles(ctx context.Context, id VersionID) (files []FileMetadata, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	rows, err := l.store.ListVersionFiles(ctx, int64(id))
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		files = append(files, FileMetadata{Path: row.Path, Size: row.Size, Mode: row.Mode})
	}
	return files, nil
}

// GetVersionFile 按 skill 内的相对路径精确读取某个版本的单个文件,不做路径归一化。
// 版本不存在返回 NotFound,路径不存在返回 NotFound(Details.Path)。
func (l *Library) GetVersionFile(ctx context.Context, id VersionID, path string) (file File, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	file, found, err := l.store.GetVersionFile(ctx, int64(id), path)
	if err != nil {
		return File{}, err
	}
	if !found {
		return File{}, &Error{Code: NotFound, Message: "get version file: file not found", Details: ErrorDetails{Path: path}}
	}
	return file, nil
}

// ListSkills 按名字列出库里的全部技能。
func (l *Library) ListSkills(ctx context.Context) (skills []Skill, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	return l.skillsOf(l.store.ListSkills(ctx))
}

// RemoveSkill 从库里移除技能及其全部版本、组成员关系与安装记录。默认同时删除它在各作用域里已安装的文件夹,
// opts.Folders 为 Keep 时保留这些文件夹;只从一个作用域撤掉而保留在库里用 Uninstall。
func (l *Library) RemoveSkill(ctx context.Context, id SkillID, opts RemoveSkillOptions) (err error) {
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
