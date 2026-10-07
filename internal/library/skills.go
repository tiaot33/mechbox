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
type FileMode string

const (
	ModeRegular    FileMode = "regular"
	ModeExecutable FileMode = "executable"
)

// Source 是一个已记录的 skill 来源。展示用的字段由库按来源类型生成,调用方不必区分类型去拼。
type Source struct {
	ID          SourceID   `json:"id"`
	Type        SourceType `json:"type"`
	Label       string     `json:"label"`              // 展示名称:GitHub 为 owner/repo,URL 为压缩包地址,本地目录为绝对路径
	Ref         string     `json:"ref,omitempty"`      // Git 类来源入库时使用的分支、tag 或完整 SHA;其他来源为空
	ViewURL     string     `json:"view_url,omitempty"` // 在来源上查看的网页;URL 与本地目录为空
	LastFetched time.Time  `json:"last_fetched"`       // 来源内容最近一次被入库或经更新确认的时间
	LastSeenRef string     `json:"last_seen_ref"`      // 上次看到的远端标识(git commit 或内容哈希)
}

// File 是 skill 里的一个文件;Path 是 skill 内斜杠分隔的相对路径。
type File struct {
	Path    string   `json:"path"`
	Content []byte   `json:"content"`
	Mode    FileMode `json:"mode"` // regular 或 executable
}

func publicFiles(files []skillfile.File) []File {
	if files == nil {
		return nil
	}
	converted := make([]File, len(files))
	for i, file := range files {
		converted[i] = File{Path: file.Path, Content: file.Content, Mode: FileMode(file.Mode)}
	}
	return converted
}

func internalFiles(files []File) []skillfile.File {
	if files == nil {
		return nil
	}
	converted := make([]skillfile.File, len(files))
	for i, file := range files {
		converted[i] = skillfile.File{Path: file.Path, Content: file.Content, Mode: skillfile.Mode(file.Mode)}
	}
	return converted
}

// FileMetadata 是文件列表用的元数据,不含文件内容。
type FileMetadata struct {
	Path string   `json:"path"` // skill 内斜杠分隔的相对路径
	Size int64    `json:"size"` // 内容的字节数
	Mode FileMode `json:"mode"` // regular 或 executable
}

// Skill 是库中的一个 skill;Files 是当前版本的完整文件内容。
type Skill struct {
	ID          SkillID   `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"` // 当前版本 SKILL.md frontmatter 的 description;解析不到则为空
	Source      Source    `json:"source"`
	VersionID   VersionID `json:"version_id"`          // 当前版本的 SkillVersion.ID,可直接传给 GetVersion / DiffVersions
	FetchedAt   time.Time `json:"fetched_at,omitzero"` // 入库时间:最早版本的写入时间
	UpdatedAt   time.Time `json:"updated_at,omitzero"` // 当前版本的写入时间
	Files       []File    `json:"files"`
}

// SkillSummary 是浏览列表用的精简条目:身份、名字、描述、来源元数据与最近更新检查,不含文件正文。
// 列表接口(ListSkills / ListGroupSkills / ListCollections)与入库(Fetch)返回它以保持轻量;
// 完整文件通过 GetSkill / GetVersion 获取。
type SkillSummary struct {
	LastUpdateCheck *UpdateCheckState `json:"last_update_check,omitempty"` // 最近检查结果;nil 表示从未检查
	ID              SkillID           `json:"id"`
	Name            string            `json:"name"`
	Description     string            `json:"description"` // 当前版本 SKILL.md frontmatter 的 description;解析不到则为空
	Source          Source            `json:"source"`
	FetchedAt       time.Time         `json:"fetched_at,omitzero"` // 入库时间:最早版本的写入时间
	UpdatedAt       time.Time         `json:"updated_at,omitzero"` // 当前版本的写入时间
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
		ID:          SourceID(row.ID),
		Type:        SourceType(row.Type),
		Label:       description.Label,
		Ref:         description.Ref,
		ViewURL:     description.ViewURL,
		LastFetched: row.LastFetched,
		LastSeenRef: row.LastSeenRef,
	}, nil
}

func (l *Library) summaryOf(row store.Summary) (SkillSummary, error) {
	src, err := l.sourceOf(row.Source)
	if err != nil {
		return SkillSummary{}, err
	}
	summary := SkillSummary{ID: SkillID(row.ID), Name: row.Name, Description: row.Description, Source: src, FetchedAt: row.FetchedAt, UpdatedAt: row.UpdatedAt}
	if row.CheckStatus != "" {
		summary.LastUpdateCheck = &UpdateCheckState{CheckedAt: row.CheckedAt, Status: UpdateCheckStatus(row.CheckStatus)}
	}
	return summary, nil
}

func (l *Library) summariesOf(rows []store.Summary, err error) ([]SkillSummary, error) {
	if err != nil {
		return nil, err
	}
	var skills []SkillSummary
	for _, row := range rows {
		summary, err := l.summaryOf(row)
		if err != nil {
			return nil, err
		}
		skills = append(skills, summary)
	}
	return skills, nil
}

func (l *Library) getSummary(ctx context.Context, id SkillID) (SkillSummary, error) {
	row, err := l.store.GetSummary(ctx, int64(id))
	if err != nil {
		return SkillSummary{}, err
	}
	return l.summaryOf(row)
}

func clockNow() time.Time {
	return time.Now().UTC().Round(0)
}

// GetSkill 读取一个 skill 及其当前版本的完整文件。
func (l *Library) GetSkill(ctx context.Context, id SkillID) (skill Skill, err error) {
	defer func() { err = typedError(err) }()
	skill, _, err = l.getSkill(ctx, id)
	return skill, err
}

type sourceState struct {
	source    store.Source
	checkedAt time.Time
	versionID int64
	snapshot  source.State
}

func (l *Library) getSkill(ctx context.Context, id SkillID) (Skill, sourceState, error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	row, err := l.store.GetSummary(ctx, int64(id))
	if err != nil {
		return Skill{}, sourceState{}, err
	}
	summary, err := l.summaryOf(row)
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
		Files:       publicFiles(current.Files),
	}
	return skill, sourceState{source: row.Source, checkedAt: current.CheckedAt, versionID: current.VersionID, snapshot: source.State(current.State)}, nil
}

// ListSkillFiles 按路径排序列出当前版本的文件元数据,不读取文件正文;技能不存在返回 NotFound(Details.SkillID)。
func (l *Library) ListSkillFiles(ctx context.Context, id SkillID) (files []FileMetadata, err error) {
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
		files = append(files, FileMetadata{Path: row.Path, Size: row.Size, Mode: FileMode(row.Mode)})
	}
	return files, nil
}

// GetSkillFile 按 skill 内的相对路径精确读取当前版本的单个文件,不做路径归一化。
// 技能不存在返回 NotFound(Details.SkillID),路径不存在返回 NotFound(Details.Path);
// 与 ListSkillFiles 分别读取各自调用时的当前版本。
func (l *Library) GetSkillFile(ctx context.Context, id SkillID, path string) (file File, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	current, found, err := l.store.GetCurrentFile(ctx, int64(id), path)
	if errors.Is(err, store.ErrNotFound) {
		return File{}, &Error{Code: NotFound, Message: "get skill file: skill not found", Details: ErrorDetails{SkillID: id}}
	}
	if err != nil {
		return File{}, err
	}
	if !found {
		return File{}, &Error{Code: NotFound, Message: "get skill file: file not found", Details: ErrorDetails{Path: path}}
	}
	return File{Path: current.Path, Content: current.Content, Mode: FileMode(current.Mode)}, nil
}

// ListSkills 列出全部 skill 的精简摘要(不含文件正文)。
func (l *Library) ListSkills(ctx context.Context) (skills []SkillSummary, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	return l.summariesOf(l.store.ListSummaries(ctx))
}

// RemoveSkill 从库中移除 skill 及其全部版本;opts 决定磁盘上技能文件夹的去留。
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
