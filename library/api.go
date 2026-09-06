// Package library 用 SQLite 数据库统一管理 Agent Skills,并把激活的 skill
// 投影(复制)到 Claude Code 与 Codex 的 skills 目录。数据库是唯一权威数据源,
// 磁盘上的 skills 目录只是它的投影。
package library

import (
	"context"
	"errors"
	"time"
)

// 各实体的数据库主键,定义为不同类型以免互相传错。
type SkillID int64
type SourceID int64
type VersionID int64
type GroupID int64
type ProjectID int64

// ErrorCode 是稳定的错误类别,调用方(TUI)据此决定界面表现。
type ErrorCode string

const (
	NotFound          ErrorCode = "not_found"          // 目标不存在
	AlreadyExists     ErrorCode = "already_exists"     // 目标已存在(激活目录冲突、路径重复注册等)
	SourceUnreachable ErrorCode = "source_unreachable" // 来源无法访问(网络故障、路径不存在等)
	Permission        ErrorCode = "permission"         // 权限不足(文件系统或远端鉴权)
	Internal          ErrorCode = "internal"           // 其他内部错误
)

// Error 是 Library 所有操作返回的可序列化错误。
type Error struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
	Err     error     `json:"-"`
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.Err }

// IsCode 判断 err 是否属于给定的 Library 错误类别。
func IsCode(err error, code ErrorCode) bool {
	var libraryErr *Error
	return errors.As(err, &libraryErr) && libraryErr.Code == code
}

// SourceType 是 skill 来源的类型(见 ADR 0006)。
type SourceType string

const (
	SourceGitHub SourceType = "github" // GitHub 仓库 + 分支
	SourceURL    SourceType = "url"    // 直接下载的 zip 压缩包
	SourceStore  SourceType = "store"  // skill 商店(包 ID + 版本)
	SourceLocal  SourceType = "local"  // 本地目录
)

// FileMode 记录文件是否可执行,投影到磁盘时据此设置权限位。
type FileMode string

const (
	ModeRegular    FileMode = "regular"
	ModeExecutable FileMode = "executable"
)

// LocalSource 是本地安装的入参:一个本地目录路径。
type LocalSource struct {
	Path string `json:"path"`
}

// SourceLocator 是来源的定位信息;不同 SourceType 只使用其中一部分字段。
type SourceLocator struct {
	Repository string `json:"repository,omitempty"` // github:仓库地址
	Branch     string `json:"branch,omitempty"`     // github:分支
	URL        string `json:"url,omitempty"`        // url:压缩包地址
	PackageID  string `json:"package_id,omitempty"` // store:包 ID
	Version    string `json:"version,omitempty"`    // store:包版本
	Path       string `json:"path,omitempty"`       // local:本地目录的绝对路径
}

// Source 是一个已记录的 skill 来源。
type Source struct {
	ID          SourceID      `json:"id"`
	Type        SourceType    `json:"type"`
	Locator     SourceLocator `json:"locator"`
	LastFetched time.Time     `json:"last_fetched"`  // 上次抓取时间(本地来源为零值)
	LastSeenRef string        `json:"last_seen_ref"` // 上次看到的远端标识(git commit 或内容哈希)
}

// File 是 skill 里的一个文件;Path 是 skill 内斜杠分隔的相对路径。
type File struct {
	Path    string   `json:"path"`
	Content []byte   `json:"content"`
	Mode    FileMode `json:"mode"`
}

// Skill 是库中的一个 skill;Files 是当前版本的完整文件内容。
type Skill struct {
	ID     SkillID `json:"id"`
	Name   string  `json:"name"`
	Source Source  `json:"source"`
	Files  []File  `json:"files"`
}

// SkillSummary 是浏览列表用的精简条目:身份、名字与来源元数据,不含文件正文。
// 列表接口(ListSkills / ListGroupSkills / ListCollections)返回它以保持轻量;
// 完整文件通过 GetSkill / GetVersion 获取。
type SkillSummary struct {
	ID     SkillID `json:"id"`
	Name   string  `json:"name"`
	Source Source  `json:"source"`
}

// SkillVersion 是 skill 的一个历史版本;列表场景下 Files 为空,详情才带内容。
type SkillVersion struct {
	ID      VersionID `json:"id"`
	SkillID SkillID   `json:"skill_id"`
	Files   []File    `json:"files,omitempty"`
}

// VersionDiff 是两个版本之间按文件路径归类的差异。
type VersionDiff struct {
	Changed []string `json:"changed"` // 两个版本都有、内容不同
	Added   []string `json:"added"`   // 仅新版本有
	Deleted []string `json:"deleted"` // 仅旧版本有
}

// Group 是手动创建的场景分组;与按来源自动形成的 Collection 相对(见 ADR 0007)。
type Group struct {
	ID   GroupID `json:"id"`
	Name string  `json:"name"`
}

// Agent 是投影的目标 agent。
type Agent string

const (
	AgentClaudeCode Agent = "claude-code"
	AgentCodex      Agent = "codex"
)

// Scope 是一个投影目标:哪个 agent 的哪个 skills 目录。
type Scope struct {
	Agent Agent  `json:"agent"`
	Path  string `json:"path"`
}

// ConflictPolicy 决定激活时目标目录已存在的处理方式。
type ConflictPolicy string

const (
	Fail      ConflictPolicy = "fail"      // 返回 AlreadyExists 错误(默认)
	Overwrite ConflictPolicy = "overwrite" // 原子替换已有目录
)

type ActivateOptions struct {
	OnConflict ConflictPolicy `json:"on_conflict"`
}

// Activation 是一条投影记录:某 skill 出现在某个 Scope 里,以及它的保留依据。
type Activation struct {
	SkillID    SkillID   `json:"skill_id"`
	Scope      Scope     `json:"scope"`
	Individual bool      `json:"individual"`           // 存在独立(非分组)激活
	ViaGroups  []GroupID `json:"via_groups,omitempty"` // 经由哪些分组激活
}

// Collection 是按 Source 自动聚合的 skill 集合;派生数据,数据库里没有对应表。
// Skills 是精简摘要,不含文件正文。
type Collection struct {
	Source Source         `json:"source"`
	Skills []SkillSummary `json:"skills"`
}

// RegisteredProject 是已注册的项目目录;注册后其 .claude/skills 与
// .codex/skills 成为可投影的 Scope。
type RegisteredProject struct {
	ID   ProjectID `json:"id"`
	Path string    `json:"path"`
}

// ProjectedCopiesPolicy 决定移除 skill 或注销项目时,磁盘上已投影的副本如何处理。
type ProjectedCopiesPolicy string

const (
	Keep   ProjectedCopiesPolicy = "keep"   // 保留磁盘副本,只删除数据库记录(默认)
	Remove ProjectedCopiesPolicy = "remove" // 连同磁盘副本一起删除
)

type RemoveSkillOptions struct {
	ProjectedCopies ProjectedCopiesPolicy `json:"projected_copies"`
}

type UnregisterProjectOptions struct {
	ProjectedCopies ProjectedCopiesPolicy `json:"projected_copies"`
}

// InstallOptions 限定远程安装时只装来源中的某些 skill;为空则全部安装。
type InstallOptions struct {
	Skills []string `json:"skills,omitempty"`
}

// Library 是未来 TUI 使用的完整进程内 API 门面。它不是 HTTP 或 RPC 契约;
// 见 docs/adr/0004-backend-library-facade-seam.md。
type Library interface {
	// 安装与更新

	// Install 把一个本地目录作为 skill 安装入库。
	Install(ctx context.Context, source LocalSource) (Skill, error)
	// InstallRemote 抓取远程来源并安装其中发现的 skill;opts 可限定只安装一部分。
	InstallRemote(ctx context.Context, source Source, opts InstallOptions) ([]Skill, error)
	// InstallFetched 安装调用方已抓取好的文件,仅限远程来源(本地目录走 Install)。
	InstallFetched(ctx context.Context, source Source, name string, files []File) (Skill, error)
	// Update 重新读取来源;内容有变化则生成新版本并设为当前版本。
	Update(ctx context.Context, id SkillID) (Skill, error)
	// GetSkill 读取一个 skill 及其当前版本的完整文件。
	GetSkill(ctx context.Context, id SkillID) (Skill, error)
	// SourceHasUpdate 判断远端最新标识与上次抓取时看到的是否不同。
	SourceHasUpdate(ctx context.Context, id SourceID, latestRef string) (bool, error)

	// 版本历史

	// ListVersions 按先后顺序列出 skill 的全部版本(不含文件内容)。
	ListVersions(ctx context.Context, id SkillID) ([]SkillVersion, error)
	// GetVersion 读取某个版本及其完整文件。
	GetVersion(ctx context.Context, id VersionID) (SkillVersion, error)
	// DiffVersions 比较同一 skill 的两个版本,返回按路径归类的差异。
	DiffVersions(ctx context.Context, oldID, newID VersionID) (VersionDiff, error)

	// 分组

	CreateGroup(ctx context.Context, name string) (Group, error)
	ListGroups(ctx context.Context) ([]Group, error)
	RenameGroup(ctx context.Context, id GroupID, name string) error
	// DeleteGroup 删除分组。非破坏性:仅由该组支撑的投影会先转为独立保留,
	// 磁盘上已投影的副本不会因删除分组而被移除(见 ADR 0007)。
	DeleteGroup(ctx context.Context, id GroupID) error
	AddSkillToGroup(ctx context.Context, groupID GroupID, skillID SkillID) error
	RemoveSkillFromGroup(ctx context.Context, groupID GroupID, skillID SkillID) error
	// ListGroupSkills 列出分组成员的精简摘要(不含文件正文)。
	ListGroupSkills(ctx context.Context, groupID GroupID) ([]SkillSummary, error)

	// skill 管理

	// ListSkills 列出全部 skill 的精简摘要(不含文件正文)。
	ListSkills(ctx context.Context) ([]SkillSummary, error)
	// RemoveSkill 从库中移除 skill 及其全部版本;opts 决定磁盘投影副本的去留。
	RemoveSkill(ctx context.Context, id SkillID, opts RemoveSkillOptions) error

	// 项目与 Scope

	// RegisterProject 注册一个项目目录,使其 skills 目录成为可投影 Scope。
	RegisterProject(ctx context.Context, path string) (RegisteredProject, error)
	ListRegisteredProjects(ctx context.Context) ([]RegisteredProject, error)
	// UnregisterProject 注销项目并清掉其 Scope 下的全部投影记录;
	// opts 决定磁盘投影副本的去留。
	UnregisterProject(ctx context.Context, id ProjectID, opts UnregisterProjectOptions) error
	// ListScopes 列出全部可投影 Scope:两个 agent 的全局目录 + 各注册项目的目录。
	ListScopes(ctx context.Context) ([]Scope, error)

	// 激活(投影)

	// Activate 把 skill 投影到 Scope,记为独立激活。
	Activate(ctx context.Context, id SkillID, scope Scope, opts ActivateOptions) error
	// Deactivate 撤销独立激活;若仍有分组支撑该投影,磁盘副本保留。
	Deactivate(ctx context.Context, id SkillID, scope Scope) error
	// ActivateGroup 把分组内全部 skill 投影到 Scope,并记录分组依据。
	ActivateGroup(ctx context.Context, groupID GroupID, scope Scope, opts ActivateOptions) error
	// DeactivateGroup 撤销该组在此 Scope 的激活;仅由它支撑的投影会被移除。
	DeactivateGroup(ctx context.Context, groupID GroupID, scope Scope) error
	// ListActivations 列出全部投影记录及各自的保留依据。
	ListActivations(ctx context.Context) ([]Activation, error)

	// 浏览

	// ListCollections 按 Source 聚合列出全部 skill。
	ListCollections(ctx context.Context) ([]Collection, error)

	// 同步

	// SyncPush 把整个数据库推送到 S3。
	SyncPush(ctx context.Context) error
	// SyncPull 从 S3 拉回整库并原子替换本地,替换前校验 schema 并重映射项目路径。
	SyncPull(ctx context.Context) error

	Close() error
}
