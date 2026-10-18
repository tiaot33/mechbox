package library

import (
	"context"
	"time"

	"github.com/tiaot33/skills-tui/internal/library/internal/store"
)

// VersionID 是技能版本的数据库主键:全库唯一,同一技能内写入越晚越大。哪个是技能的当前版本看 Skill.VersionID。
type VersionID int64

// SkillVersion 是库里存着的技能的一个版本,由入库或更新(含用目录里的修改更新)写进库里;版本列表与按 ID 读取返回同一个值。
// 不含文件,文件用 ID 经 ListVersionFiles / GetVersionFile 读取。
type SkillVersion struct {
	ID          VersionID `json:"id"`
	SkillID     SkillID   `json:"skill_id"`            // 所属技能
	Description string    `json:"description"`         // 该版本 SKILL.md frontmatter 的 description;解析不到则为空
	Revision    string    `json:"revision,omitempty"`  // 这份内容最后一次被确认时来源所在的来源版本,与入库预览的 Revision 同义:Git 类来源为 commit,直接下载为文件内容哈希,well-known 为索引内容哈希,本地目录为空。当前版本在更新时确认来源没有变化,会前移到来源的最新来源版本;一旦不再是当前版本就不再变。用目录里的修改更新进来的版本沿用 BasedOn 那个版本的来源版本
	CreatedAt   time.Time `json:"created_at,omitzero"` // 这个版本由入库或更新写进库里的时间
	// BasedOn 是用目录里的修改更新进来的版本(PlanUpdateFromScope)所基于的版本,即当时库里的当前版本;从来源入库或更新的为零值。
	// 从作用域连同来源入库、能确认磁盘是在来源现在这一版上改出来的(见 FetchUnmanaged)时,是入库时一并记下的来源里的那一版。
	BasedOn VersionID `json:"based_on,omitzero"`
	// MaybeEdited 为 true 表示这一版是从作用域连同来源入库的未纳管技能(FetchUnmanaged),入库时磁盘上的内容和来源里的不一样,
	// 又不能确认是当初装的原样:说不清有没有用户的修改。版本历史里写「入库时和来源里的不一样，可能有你的修改」;
	// 它是当前版本时更新计划里这个技能为 PlannedSkill.Edited。更新时确认来源里的内容和它一样(更新计划里没有变化)时清为 false。
	MaybeEdited bool `json:"maybe_edited,omitzero"`
}

// FileMode 记录文件是否可执行,安装到磁盘时据此设置权限位。
type FileMode string

const (
	ModeRegular    FileMode = "regular"    // 普通文件
	ModeExecutable FileMode = "executable" // 可执行文件,安装到磁盘时带可执行权限位
)

// File 是技能某个版本里的一个文件;Path 是技能内斜杠分隔的相对路径。
type File struct {
	Path    string   `json:"path"`
	Content []byte   `json:"content"`
	Mode    FileMode `json:"mode"` // regular 或 executable
}

// FileMetadata 是文件列表用的元数据,不含文件内容。
type FileMetadata struct {
	Path string   `json:"path"` // 技能内斜杠分隔的相对路径
	Size int64    `json:"size"` // 内容的字节数
	Mode FileMode `json:"mode"` // regular 或 executable
}

// VersionDiff 是从一个版本变到另一个版本改了哪些文件;三类各自按路径排序,没有时为空数组。
// 只给路径,文件正文用 GetVersionFile 分别读两边。
type VersionDiff struct {
	Changed []string `json:"changed"` // 两边都有、内容或可执行权限不同
	Added   []string `json:"added"`   // 只有 to 有
	Deleted []string `json:"deleted"` // 只有 from 有
}

func versionOf(row store.Version) SkillVersion {
	return SkillVersion{ID: VersionID(row.ID), SkillID: SkillID(row.SkillID), Description: row.Description, Revision: row.Revision, CreatedAt: row.CreatedAt, BasedOn: VersionID(row.Base), MaybeEdited: row.MaybeEdited}
}

// ListVersions 按写入先后列出技能在库里的全部版本,最早的在前;哪个是当前版本看 Skill.VersionID。
// 技能不存在返回 NotFound。
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

// GetVersion 按 ID 读取一个版本,与 ListVersions 里对应的那一项相同;用于只拿到版本 ID 时取出它的时间、描述和来源版本。
// 版本不存在返回 NotFound。
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

// ListVersionFiles 按路径排序列出某个版本的文件元数据,不读取文件正文;版本不存在返回 NotFound。
// 查看技能当前的文件时传 Skill.VersionID,浏览途中技能被更新也仍读到同一版本;查看历史版本的文件时传 SkillVersion.ID。
func (l *Library) ListVersionFiles(ctx context.Context, id VersionID) (files []FileMetadata, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	rows, err := l.store.ListVersionFiles(ctx, int64(id))
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		files = append(files, FileMetadata{Path: row.Path, Size: row.Size, Mode: FileMode(row.Mode)})
	}
	return files, nil
}

// GetVersionFile 按技能内的相对路径精确读取某个版本的单个文件,不做路径归一化。
// 版本不存在返回 NotFound,路径不存在返回 NotFound(Details.Path)。
func (l *Library) GetVersionFile(ctx context.Context, id VersionID, path string) (file File, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	stored, found, err := l.store.GetVersionFile(ctx, int64(id), path)
	if err != nil {
		return File{}, err
	}
	if !found {
		return File{}, &Error{Code: NotFound, Message: "get version file: file not found", Details: ErrorDetails{Path: path}}
	}
	return File{Path: stored.Path, Content: stored.Content, Mode: FileMode(stored.Mode)}, nil
}

// DiffVersions 比较同一技能的两个版本,返回从 from 变到 to 改了哪些文件。两个参数不要求先后顺序:
// 看「这一版改了什么」传上一版和这一版,看「回到较早的版本会改什么」传当前版本和那个版本;同一个版本传两次得到空差异。
// 版本不存在返回 NotFound,不属于同一技能返回 InvalidArgument。
func (l *Library) DiffVersions(ctx context.Context, from, to VersionID) (diff VersionDiff, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	fromVersion, err := l.store.GetVersion(ctx, int64(from))
	if err != nil {
		return VersionDiff{}, err
	}
	toVersion, err := l.store.GetVersion(ctx, int64(to))
	if err != nil {
		return VersionDiff{}, err
	}
	if fromVersion.SkillID != toVersion.SkillID {
		return VersionDiff{}, argumentError("diff skill versions", "", "versions belong to different skills")
	}
	fromFiles, err := l.store.VersionDigests(ctx, int64(from))
	if err != nil {
		return VersionDiff{}, err
	}
	toFiles, err := l.store.VersionDigests(ctx, int64(to))
	if err != nil {
		return VersionDiff{}, err
	}
	diff = diffDigests(fromFiles, toFiles)
	return diff, nil
}
