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
	InvalidArgument   ErrorCode = "invalid_argument"   // 参数无效
	NotFound          ErrorCode = "not_found"          // 目标不存在
	AlreadyExists     ErrorCode = "already_exists"     // 目标已存在(激活目录冲突、路径重复注册等)
	SourceUnreachable ErrorCode = "source_unreachable" // 来源无法访问(网络故障、路径不存在等)
	Permission        ErrorCode = "permission"         // 权限不足(文件系统或远端鉴权)
	NotConfigured     ErrorCode = "not_configured"     // 所需配置缺失(如 Sync 未配置 S3/凭证)
	Conflict          ErrorCode = "conflict"           // 与预期状态冲突(如 Sync 远端在预览后发生变化)
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
	libraryErr, ok := errors.AsType[*Error](err)
	return ok && libraryErr.Code == code
}

// SourceType 是 skill 来源的类型(见 ADR 0006)。
type SourceType string

const (
	SourceGitHub SourceType = "github" // GitHub 仓库 + Git ref
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
	Branch     string `json:"branch,omitempty"`     // github:Git ref(分支、tag 或完整 SHA),保留已有字段名
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

// SourceSpec 是发起 Fetch 时对远程来源的规格描述:只含类型与定位信息,不含
// 任何持久化字段(id / last_fetched / last_seen_ref),前端因此无法构造出非法的
// Source 记录。本地目录安装走 Install,不使用 SourceSpec。
type SourceSpec struct {
	Type    SourceType    `json:"type"`
	Locator SourceLocator `json:"locator"`
	Subpath string        `json:"subpath,omitempty"` // 本次 Fetch 的来源内目录,不进入 Source 身份
	Names   []string      `json:"names,omitempty"`   // 本次 Fetch 的名称条件,不持久化
}

// ResolveSourceOptions 提供明确的 ref 和来源内路径;带斜杠的 ref 应显式传入。
type ResolveSourceOptions struct {
	Ref     string `json:"ref,omitempty"`
	Subpath string `json:"subpath,omitempty"`
}

// SearchSkill 是市场展示结果;来源仍须经 ResolveSource 和 Fetch 验证。
type SearchSkill struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	SourceInput string `json:"source_input"`
	DetailURL   string `json:"detail_url"`
	Installs    *int64 `json:"installs,omitempty"`
}

// SearchDiagnostic 描述无法转换为可安装 GitHub 来源的搜索条目。
type SearchDiagnostic struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// SearchResult 包含可继续解析的搜索条目和不支持来源的诊断。
type SearchResult struct {
	Skills      []SearchSkill      `json:"skills"`
	Diagnostics []SearchDiagnostic `json:"diagnostics,omitempty"`
}

// File 是 skill 里的一个文件;Path 是 skill 内斜杠分隔的相对路径。
type File struct {
	Path    string   `json:"path"`
	Content []byte   `json:"content"`
	Mode    FileMode `json:"mode"`
}

// Skill 是库中的一个 skill;Files 是当前版本的完整文件内容。
type Skill struct {
	ID         SkillID `json:"id"`
	Name       string  `json:"name"`
	Source     Source  `json:"source"`
	GitTreeSHA string  `json:"git_tree_sha"` // GitHub 来源当前版本的目录 tree SHA;非 git 或尚未记录时为空
	Files      []File  `json:"files"`
}

// SkillSummary 是浏览列表用的精简条目:身份、名字与来源元数据,不含文件正文。
// 列表接口(ListSkills / ListGroupSkills / ListCollections)返回它以保持轻量;
// 完整文件通过 GetSkill / GetVersion 获取。
type SkillSummary struct {
	ID     SkillID `json:"id"`
	Name   string  `json:"name"`
	Source Source  `json:"source"`
}

// FetchID 是一次 Fetch 会话的不透明句柄,由 Library 签发。它不持久化、不参与
// Sync、也不跨 Library 实例共享;进程退出、Close 或超时后被后端回收。
type FetchID string

// AuditAvailability 说明一个候选在 skills.sh 上的审计查询结果处于哪种状态。
type AuditAvailability string

const (
	AuditAvailable   AuditAvailability = "available"   // 至少拿到一条合作方结论
	AuditNone        AuditAvailability = "none"        // skills.sh 没有该 skill 的任何审计(404 或空列表)
	AuditUnavailable AuditAvailability = "unavailable" // 网络故障、超时、非 404 错误或响应无法解析
)

// AuditStatus 是 skills.sh 归一化后的合作方结论;文档三值以常量暴露,未知值原样透传。
type AuditStatus string

const (
	AuditPass AuditStatus = "pass"
	AuditWarn AuditStatus = "warn"
	AuditFail AuditStatus = "fail"
)

// AuditEntry 是 skills.sh 某个合作方对一个 skill 的审计结论;只供展示,不作决策依据。
type AuditEntry struct {
	Provider   string      `json:"provider"`             // 合作方展示名
	Slug       string      `json:"slug"`                 // 合作方 slug
	Status     AuditStatus `json:"status"`               // pass / warn / fail;未知值原样保留
	Summary    string      `json:"summary"`              // 一行摘要
	AuditedAt  time.Time   `json:"audited_at,omitzero"`  // 该次分析时间;解析失败为零值。它不代表对应当前文件树
	RiskLevel  string      `json:"risk_level,omitempty"` // 合作方风险等级,原样字符串(live 出现过文档外的 SAFE)
	Categories []string    `json:"categories,omitempty"` // 风险类别(目前仅 Agent Trust Hub)
	DetailURL  string      `json:"detail_url"`           // skills.sh 上该合作方的安全详情页
}

// SkillAudit 是一个 Fetch 候选在 skills.sh 上的审计概览:Fetch 时查一次,随会话暂存,
// 只供展示。不入库、不参与 Sync、不影响安装。Entries 仅在 Availability 为 AuditAvailable 时非空。
type SkillAudit struct {
	Availability AuditAvailability `json:"availability"`
	Entries      []AuditEntry      `json:"entries,omitempty"`
}

// FetchSkill 是安装前可浏览的候选;Path 相对来源根目录。Audit 是 skills.sh 的审计覆盖层:
// GitHub 来源一定非 nil,由 Availability 说明内容;URL / Store 来源为 nil(skills.sh 未收录)。
type FetchSkill struct {
	PluginName  string      `json:"plugin_name,omitempty"` // 清单显式声明的插件分组名,仅供预览
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Path        string      `json:"path"`
	Audit       *SkillAudit `json:"audit,omitempty"`
}

// CandidateDiagnostic 说明一个路径为何未成为可安装候选。
type CandidateDiagnostic struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// FetchCandidates 是一次暂存的候选与诊断;返回值不与会话共享可变数据。
type FetchCandidates struct {
	Skills      []FetchSkill          `json:"skills"`
	Diagnostics []CandidateDiagnostic `json:"diagnostics,omitempty"`
}

// Fetch 是远程内容的暂存句柄及预览;此时尚无内容进入 Library。
type Fetch struct {
	ID  FetchID `json:"id"`
	Ref string  `json:"ref"`
	FetchCandidates
}

// SkillVersion 是 skill 的一个历史版本;列表场景下 Files 为空,详情才带内容。
type SkillVersion struct {
	ID         VersionID `json:"id"`
	SkillID    SkillID   `json:"skill_id"`
	GitTreeSHA string    `json:"git_tree_sha"` // GitHub 来源该版本 Skill 目录的 git tree SHA;非 git 或尚未记录时为空
	Files      []File    `json:"files,omitempty"`
}

// VersionDiff 是两个版本之间按文件路径归类的差异。
type VersionDiff struct {
	Changed []string `json:"changed"` // 两个版本都有、内容不同
	Added   []string `json:"added"`   // 仅新版本有
	Deleted []string `json:"deleted"` // 仅旧版本有
}

// UpdateResult 描述 Library 的更新结果;已激活的副本不会自动重写。
type UpdateResult struct {
	Skill   Skill  `json:"skill"`
	Changed bool   `json:"changed"` // GitHub 为目录 tree SHA 变化并生成了新版本;其他来源为内容或权限变化
	Note    string `json:"note"`    // 提醒前端已激活的副本可能落后于 Library
}

// UpdateCheck 是一次批量检查中某个 Skill 的结果;Error 非空时 Available 无意义。
type UpdateCheck struct {
	SkillID   SkillID `json:"skill_id"`
	Available bool    `json:"available"`
	Error     *Error  `json:"error,omitempty"`
}

// Group 是手动创建的场景分组;与按来源自动形成的 Collection 相对(见 ADR 0007)。
type Group struct {
	ID   GroupID `json:"id"`
	Name string  `json:"name"`
}

// AgentCode 是 Agent 的稳定代码:跨库、跨 Sync 仍指同一个 Agent。
type AgentCode string

const (
	AgentClaudeCode AgentCode = "claude-code"
	AgentCodex      AgentCode = "codex"
)

// Agent 是 Library 持有的一个投影目标 agent。暴露稳定代码与展示名称;布局规则与
// 绝对目录不出现在这里,目录由 Library 按现行布局算出,调用方不自行拼路径。
type Agent struct {
	Code AgentCode `json:"code"`
	Name string    `json:"name"`
}

// Scope 是投影所在的层级身份:GlobalScope 或 ProjectScope 之一,不含路径(见 ADR 0011)。
// 目录由 Library 按该 Agent 的现行布局算出(见 Resolve),调用方不自行拼路径,也无法
// 传入一条目录来 Activate。密封接口:只有本包的两种 Scope 类型能实现它。
// 作为接口字段它可以 marshal,但 JSON 没有种类判别符、无法直接 unmarshal;
// RPC 边界(ADR 0008)需要时自行加判别符。
type Scope interface {
	sealedScope()
}

// GlobalScope 是某个 Agent 的全局 Scope:目录相对用户主目录。
type GlobalScope struct {
	Agent AgentCode `json:"agent"`
}

// ProjectScope 是某个 Agent 在某个 Registered project 下的 Scope:目录相对项目根。
type ProjectScope struct {
	Agent     AgentCode `json:"agent"`
	ProjectID ProjectID `json:"project_id"`
}

func (GlobalScope) sealedScope()  {}
func (ProjectScope) sealedScope() {}

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
// 记录只存 Scope 身份;Directory 是列表时按现行布局 Resolve 出的派生目录,只供对照磁盘,
// 不是 Activate 的输入(Activate 也不接受目录)。
type Activation struct {
	SkillID    SkillID   `json:"skill_id"`
	Scope      Scope     `json:"scope"`
	Directory  string    `json:"directory"`
	Individual bool      `json:"individual"`           // 存在独立(非分组)激活
	ViaGroups  []GroupID `json:"via_groups,omitempty"` // 经由哪些分组激活
}

// Collection 是按 Source 自动聚合的 skill 集合;派生数据,数据库里没有对应表。
// Skills 是精简摘要,不含文件正文。
type Collection struct {
	Source Source         `json:"source"`
	Skills []SkillSummary `json:"skills"`
}

// RegisteredProject 是已注册的项目目录;注册后它与每个 Agent 组成一个 ProjectScope,
// 目录由该 Agent 的项目级布局算出。
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

// RemoteIdentity 是 Sync 远端对象的身份标识(S3 ETag);空值表示远端当前无对象。
// 预览时返回,执行时回传以绑定"用户确认的"与"实际推/拉的"是同一个远端对象。
type RemoteIdentity string

// SyncPreview 是 Sync 推/拉执行前的预览:远端身份 + 相关校验提示,不改动本地库,
// 也不提交推送。调用方据此确认后再带着 Remote 调用对应的执行方法。
type SyncPreview struct {
	Remote       RemoteIdentity `json:"remote"`        // 当前远端身份;RemoteExists 为 false 时无意义
	RemoteExists bool           `json:"remote_exists"` // 远端是否已存在对象
	SchemaOK     bool           `json:"schema_ok"`     // 仅 pull:远端库的 schema 是否可接受
	Warnings     []string       `json:"warnings,omitempty"`
}

// SyncResult 是 Sync 推/拉成功后的结果:操作后的远端身份 + 可能的警告。
// 警告表示操作已成功但调用方需留意某事(如 pull 后磁盘投影可能与新库不一致)。
type SyncResult struct {
	Remote   RemoteIdentity `json:"remote"`
	Warnings []string       `json:"warnings,omitempty"`
}

// Library 是未来 TUI 使用的完整进程内 API 门面。它不是 HTTP 或 RPC 契约;
// 见 docs/adr/0004-backend-library-facade-seam.md。
type Library interface {
	// 安装与更新

	// ResolveSource 解析 GitHub 输入,省略 ref 时发现默认分支;只读且不创建 Fetch 会话。
	ResolveSource(ctx context.Context, input string, opts ResolveSourceOptions) (SourceSpec, error)
	// SearchSkills 只读搜索 skills.sh,最多返回 20 项;服务失败与空结果分别返回。
	SearchSkills(ctx context.Context, query string) (SearchResult, error)

	// Install 把一个本地目录作为 skill 安装入库(一次性,不经 Fetch 会话)。
	Install(ctx context.Context, source LocalSource) (Skill, error)
	// Fetch 按来源规格抓取远程内容到 Library 之外的临时暂存区,返回句柄与候选 skill
	// 名清单;此时尚无内容进入 Library。GitHub 预览只取各候选 SKILL.md 并钉住当时的
	// commit;URL / Store 仍一次下载完整归档。本地目录不走 Fetch。
	Fetch(ctx context.Context, spec SourceSpec) (Fetch, error)
	// ListFetchSkills 只列出暂存中的候选元数据与诊断,不联网、不入库;句柄在 30 分钟后或
	// Close 时失效。过期、未知或其他 Library 实例的句柄返回 NotFound。
	ListFetchSkills(ctx context.Context, id FetchID) (FetchCandidates, error)
	// InstallFetch 从一次 Fetch 中选取 skill 原子提交入库(names 为空表示全部候选)。
	// GitHub 来源可在 Fetch 已钉住的 commit 上联网取所选 Skill 文件,不重新解析 ref,
	// 不跟随当前 HEAD。URL / Store 只读取暂存内容,不联网。要么所选 skill 全部入库,
	// 要么库保持不变。成功后该 Fetch 句柄被丢弃(含未选候选),再次使用返回 NotFound;
	// 失败时保留句柄供重试,直至 Close 或过期。
	InstallFetch(ctx context.Context, id FetchID, names []string) ([]Skill, error)
	// Update 重新读取来源。GitHub 先发现候选,再比较该 Skill 目录的 tree SHA:相同则不取辅助文件、不新增版本
	// (可刷新 last_fetched / last_seen_ref);不同才取文件并写入新版本和新 SHA。无 SHA 的旧
	// GitHub 记录回退为比较文件与可执行位,成功后补写 SHA。URL / Store / 本地仍比较文件。
	// 只更新 Library,不重写激活副本。
	Update(ctx context.Context, id SkillID) (UpdateResult, error)
	// GetSkill 读取一个 skill 及其当前版本的完整文件。
	GetSkill(ctx context.Context, id SkillID) (Skill, error)
	// CheckUpdate 自行读取来源。GitHub 先发现候选,再比较当前版本的目录 tree SHA,相同则不取辅助文件;
	// 无 SHA 时回退为比较文件与可执行位。其他来源比较文件内容及权限。不修改 Library 或激活副本。
	CheckUpdate(ctx context.Context, id SkillID) (bool, error)
	// CheckUpdates 去重保序,每个 Source(含 ref)只远程读取一次;GitHub 共用一次 tree,
	// 发现结果在同一 Source 内复用,各 Skill 用各自目录 SHA 比较。逐项错误不阻断其他来源。整体取消返回已完成项与
	// context 错误,未完成项不伪装为无更新。检查不写 Library。
	CheckUpdates(ctx context.Context, ids []SkillID) ([]UpdateCheck, error)

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

	// ListAgents 列出 Library 持有的全部 Agent(按代码排序)。只读;结果含稳定代码与展示名称,
	// 不含布局规则或任何绝对目录。Agent 由建库种子写入,门面上没有增删改 Agent 的方法。
	ListAgents(ctx context.Context) ([]Agent, error)
	// RegisterProject 注册一个项目目录,使它与各 Agent 组成可投影的 ProjectScope。
	RegisterProject(ctx context.Context, path string) (RegisteredProject, error)
	ListRegisteredProjects(ctx context.Context) ([]RegisteredProject, error)
	// UnregisterProject 注销项目并清掉其各 Agent 项目 Scope 下的全部投影记录;
	// opts 决定磁盘投影副本的去留。
	UnregisterProject(ctx context.Context, id ProjectID, opts UnregisterProjectOptions) error
	// Resolve 按 agents 表中该 Agent 的现行布局算出 Scope 对应的目录:全局相对 Open 时的用户
	// 主目录,项目相对该 Registered project 根。只读:不写 Library、不创建目录、不产生 Activation。
	// 未知 Agent 代码或未登记项目返回 NotFound。可选目标没有单独的列举方法:调用方用
	// ListAgents ×(全局 | ListRegisteredProjects)自行组合。
	Resolve(ctx context.Context, scope Scope) (string, error)

	// 激活(投影)
	//
	// 四个方法都只接受 Scope 身份:目录由 Resolve 按现行布局算出,记录按身份落库,
	// 未知 Agent 或未登记项目返回 NotFound。厂商改布局后只认现行目录,旧目录里的文件夹不管。

	// Activate 把 skill 投影到 Scope,记为独立激活。
	Activate(ctx context.Context, id SkillID, scope Scope, opts ActivateOptions) error
	// Deactivate 撤销独立激活;若仍有分组支撑该投影,磁盘副本保留。
	Deactivate(ctx context.Context, id SkillID, scope Scope) error
	// ActivateGroup 把分组内全部 skill 投影到 Scope,并记录分组依据;失败时撤销本次整组改动。
	ActivateGroup(ctx context.Context, groupID GroupID, scope Scope, opts ActivateOptions) error
	// DeactivateGroup 撤销该组在此 Scope 的激活;仅由它支撑的投影会被移除,失败时恢复整组状态。
	DeactivateGroup(ctx context.Context, groupID GroupID, scope Scope) error
	// ListActivations 列出全部投影记录:Scope 身份、按现行布局派生的目录及各自的保留依据。
	ListActivations(ctx context.Context) ([]Activation, error)

	// 浏览

	// ListCollections 按 Source 聚合列出全部 skill。
	ListCollections(ctx context.Context) ([]Collection, error)

	// 同步

	// PreviewSyncPush 报告推送目标的当前远端身份(若有),以便调用方知道可能覆盖远端。
	PreviewSyncPush(ctx context.Context) (SyncPreview, error)
	// PreviewSyncPull 报告远端库的身份与 schema 是否可接受,不替换本地库。
	PreviewSyncPull(ctx context.Context) (SyncPreview, error)
	// SyncPush 把整个数据库推送到 S3;expected 为预览时的远端身份,若远端已变则返回
	// Conflict,调用方须重新预览。
	SyncPush(ctx context.Context, expected RemoteIdentity) (SyncResult, error)
	// SyncPull 从 S3 拉回整库并原子替换本地,替换前校验 schema 并重映射项目路径;
	// expected 为预览时的远端身份,若远端已变则返回 Conflict。
	// 不重建或清理磁盘投影,结果可能带警告提示投影相对新库已过时。
	SyncPull(ctx context.Context, expected RemoteIdentity) (SyncResult, error)

	Close() error
}
