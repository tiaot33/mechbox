package library

import (
	"context"
	"errors"
	"time"

	"github.com/tiaot33/skills-tui/internal/library/internal/agentdir"
	"github.com/tiaot33/skills-tui/internal/library/internal/skillfile"
	"github.com/tiaot33/skills-tui/internal/library/internal/source"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/local"
	"github.com/tiaot33/skills-tui/internal/library/internal/store"
)

// SkillID 是库里技能的数据库主键。各实体的 ID 定义为不同类型,以免互相传错。
type SkillID int64

// SourceType 是 skill 来源的类型(见 ADR 0004)。
type SourceType string

const (
	SourceGitHub SourceType = "github" // GitHub 仓库 + Git ref
	SourceURL    SourceType = "url"    // 直接下载的 zip 压缩包
	SourceLocal  SourceType = "local"  // 本地目录
)

// Source 是集合与技能上的「来自哪里」,只供展示;字段由库按来源类型生成,调用方不必区分类型去拼。
// 要引用集合本身(打开集合、更新)用 CollectionID。
type Source struct {
	Type    SourceType `json:"type"`
	Label   string     `json:"label"`              // 展示名称:GitHub 为 owner/repo,URL 为压缩包地址,本地目录为绝对路径
	Ref     string     `json:"ref,omitempty"`      // Git 类来源入库时使用的分支、tag 或完整 SHA;其他来源为空
	ViewURL string     `json:"view_url,omitempty"` // 在来源上查看的网页;URL 与本地目录为空
}

// Skill 是库里的一个技能;列表、详情与入库结果都返回它,集合和组只给出技能 ID。不含文件内容,
// 文件用 VersionID 经 ListVersionFiles / GetVersionFile 读取。
type Skill struct {
	// LastUpdateCheck 是最近一次检查更新或更新的结论,技能详情页的「有新版本」读它;
	// nil 表示入库后从未检查、也从未更新过(入库来自入库预览那一刻,不算一次检查)。
	LastUpdateCheck *UpdateCheck `json:"last_update_check,omitempty"`
	ID              SkillID      `json:"id"`
	Name            string       `json:"name"`
	Description     string       `json:"description"`   // 当前版本 SKILL.md frontmatter 的 description;解析不到则为空
	CollectionID    CollectionID `json:"collection_id"` // 所在集合;从技能打开集合或更新时用它
	Source          Source       `json:"source"`
	VersionID       VersionID    `json:"version_id"`          // 当前版本的 SkillVersion.ID,用于读取文件、在版本历史里标出当前版本与比较差异
	Revision        string       `json:"revision,omitempty"`  // 当前版本最后一次被确认时来源所在的来源版本,即当前版本的 SkillVersion.Revision,与入库预览的 Revision 同义:GitHub 为 commit,URL 为压缩包内容哈希,本地目录为空
	FetchedAt       time.Time    `json:"fetched_at,omitzero"` // 入库时间
	UpdatedAt       time.Time    `json:"updated_at,omitzero"` // 最近一次更新带来新版本的时间;从未因更新产生新版本时为零值
	// KeptDeleted 为 true 表示来源里已删除这个技能,用户选了继续保留(KeepDeletedSkill):不再计入角标,技能上留一个淡色标记。
	// 之后检查更新或更新确认来源里又有这个技能时自动变回 false。
	KeptDeleted bool `json:"kept_deleted,omitzero"`
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
		ID:           SkillID(row.ID),
		Name:         row.Name,
		Description:  row.Description,
		CollectionID: CollectionID(row.Source.ID),
		Source:       src,
		VersionID:    VersionID(row.VersionID),
		Revision:     row.Revision,
		FetchedAt:    row.FetchedAt,
		UpdatedAt:    row.UpdatedAt,
		KeptDeleted:  row.KeptDeleted,
	}
	if row.CheckStatus != "" {
		status := UpdateCheckStatus(row.CheckStatus)
		renamed := status == UpdateDeleted && row.RenamedTo != ""
		skipped := (status == UpdateAvailable || renamed) && row.Skipped
		pending := (status == UpdateAvailable || renamed) && !skipped || status == UpdateDeleted && !renamed && !row.KeptDeleted
		skill.LastUpdateCheck = &UpdateCheck{
			CheckedAt: row.CheckedAt,
			Status:    status,
			Revision:  row.CheckRevision,
			RenamedTo: row.RenamedTo,
			Skipped:   skipped,
			Pending:   pending && row.Source.Type != local.Type,
		}
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
	source      store.Source
	checkSerial int64
	versionID   int64
	snapshot    source.State
	revision    string
	files       []skillfile.File
}

func (l *Library) getCurrent(ctx context.Context, id SkillID) (Skill, currentVersion, error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	row, err := l.store.GetSkill(ctx, int64(id))
	if err != nil {
		return Skill{}, currentVersion{}, err
	}
	return l.currentOf(ctx, row)
}

func (l *Library) currentOf(ctx context.Context, row store.Skill) (Skill, currentVersion, error) {
	skill, err := l.skillOf(row)
	if err != nil {
		return Skill{}, currentVersion{}, err
	}
	current, err := l.store.GetCurrent(ctx, row.ID)
	if err != nil {
		return Skill{}, currentVersion{}, err
	}
	return skill, currentVersion{
		source:      row.Source,
		checkSerial: current.CheckSerial,
		versionID:   current.VersionID,
		snapshot:    source.State(current.State),
		revision:    current.Revision,
		files:       current.Files,
	}, nil
}

// ListSkills 按名字列出库里的全部技能。
func (l *Library) ListSkills(ctx context.Context) (skills []Skill, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	return l.skillsOf(l.store.ListSkills(ctx))
}

// RemoveSkill 从库里移除技能及其全部版本、组成员关系与安装记录。默认同时删除它在各作用域里已安装的文件夹,
// opts.Folders 为 Keep 时保留这些文件夹;只从一个作用域撤掉而保留在库里用 Uninstall。不检查文件夹是否被改过:
// 确认框用 InspectSkillScopes(SkillScopeFilter{SkillID: id}) 列出会一并删除的地方,并说明其中哪几处用户改过。
func (l *Library) RemoveSkill(ctx context.Context, id SkillID, opts RemoveSkillOptions) (err error) {
	ctx, unlock := l.exclusiveOperation(ctx)
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
		skillScopes, err := l.listSkillScopes(ctx, skill.ID, nil)
		if err != nil {
			return err
		}
		for _, skillScope := range skillScopes {
			change.Remove(skillScope.Folder)
		}
	}

	return l.applyChange(ctx, &change, func(tx *store.Tx) error {
		return tx.RemoveSkill(ctx, int64(id))
	})
}
