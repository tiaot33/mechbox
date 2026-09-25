// Package library 用 SQLite 数据库统一管理 Agent Skills,并把激活的 skill
// 投影(复制)到 Claude Code 与 Codex 的 skills 目录。数据库是唯一权威数据源,
// 磁盘上的 skills 目录只是它的投影。
package library

import (
	"context"
	"errors"
	"time"
)

// SkillID 各实体的数据库主键,定义为不同类型以免互相传错。
type SkillID int64
type SourceID int64
type VersionID int64
type GroupID int64
type ProjectID int64

// ErrorCode 是稳定的错误类别,调用方(TUI / 桌面端)据此决定界面表现。
// 码按 UI 动作划分:改输入 / 改状态 / 重试 / 收起 / 报 bug。每个码的注释写明
// 适用场景、是否可重试、建议动作。
type ErrorCode string

const (
	// InvalidArgument 调用方参数本身无效(空名、非目录路径、非法枚举、ref / URL / subpath 语法)。
	// 不可重试。UI:当作表单校验错误,按 Details.Field 高亮输入框。
	InvalidArgument ErrorCode = "invalid_argument"
	// NotFound 被引用的实体不存在(各类 ID、Fetch 句柄、候选名、远端不存在的 git ref)。
	// 不可重试。UI:提示目标不存在;名字类看 Details.Name。
	NotFound ErrorCode = "not_found"
	// AlreadyExists 要创建的东西已存在(同来源同名 skill、分组成员、项目路径、无记录但目录已存在的激活目标)。
	// 不可重试,但可提供"打开已有"或"覆盖"。UI:按 Details.SkillID / ProjectID / Name / Path 跳转或展示路径。
	AlreadyExists ErrorCode = "already_exists"
	// Conflict 预览到执行之间状态已变(Sync 的远端身份;Update 读取来源期间该 skill 的当前版本已被别处改写)。
	// 可重新预览 / 重新发起后再执行。
	Conflict ErrorCode = "conflict"
	// FailedPrecondition 请求形状合法、引用的对象都存在,但 Library 当前状态不允许该操作
	// (激活目标被占用见 ConflictPolicy;仅由组激活撑着的投影单独停用;组内同名成员;同一 Fetch 句柄的安装正在进行)。
	// 覆盖或重试都无效,用户需先改状态。UI:说明原因,不提供覆盖 / 重试;占用者看 Details.SkillID / Name / Path。
	FailedPrecondition ErrorCode = "failed_precondition"
	// Canceled 调用方的 ctx 被取消或超过 deadline。
	// UI:静默收起。Error.Err 保留原始 context 错误,errors.Is 仍成立。
	Canceled ErrorCode = "canceled"
	// SourceUnreachable 可重试的远端 / 网络 / 本地路径缺失故障(DNS、连接、超时、5xx、S3 非 2xx、缺 ETag、本地来源目录不存在)。
	// 可重试。UI:提供"重试"。
	SourceUnreachable ErrorCode = "source_unreachable"
	// SourceInvalid 来源可达,但其内容不可用(zip 损坏、超限、无有效候选、响应不合规、不支持的文件类型)。
	// 重试无效。UI:展示原因,建议换来源。
	SourceInvalid ErrorCode = "source_invalid"
	// Busy 另一进程(同时打开同一库的 TUI / 桌面端)正持有库的写锁,等待超过上限(5 s)。
	// 可重试;与 SourceUnreachable 无关,是本机库冲突而非网络故障。UI:提示"另一个进程正在写库,稍后重试",不当 bug。
	// Library 只报码,是否自动重试由调用方决定。Error.Err 保留底层 SQLite 错误。
	Busy ErrorCode = "busy"
	// Permission 鉴权失败(文件系统或远端拒认凭证)。
	// 不可靠地靠重试恢复。UI:提示权限问题。
	Permission ErrorCode = "permission"
	// NotConfigured 所需配置缺失(Sync 未配置 S3/凭证、Store 来源未配置 Store URL)。
	// UI:引导到设置页。
	NotConfigured ErrorCode = "not_configured"
	// Internal 真正的 bug 与 I/O 故障。UI:报 bug;不要当成用户可恢复的输入或状态问题。
	Internal ErrorCode = "internal"
)

// Error 是 Library 所有操作返回的可序列化错误。
// Message 是英文、开发者面向的 operation: reason,不是 UI 文案;UI 契约是码 + Details。
// Err 不进入 JSON,供 errors.Is / Unwrap。
type Error struct {
	Code    ErrorCode    `json:"code"`
	Message string       `json:"message"`
	Details ErrorDetails `json:"details,omitzero"`
	Err     error        `json:"-"`
}

// ErrorDetails 是类型化的错误细节,零值字段省略。读取用 errors.AsType[*Error]。
type ErrorDetails struct {
	Field     string    `json:"field,omitempty"`     // InvalidArgument:与 JSON 字段名一致的参数名
	Path      string    `json:"path,omitempty"`      // AlreadyExists / FailedPrecondition:冲突或占用的路径
	Name      string    `json:"name,omitempty"`      // NotFound 的 ref / 名字;AlreadyExists 的 skill 名;FailedPrecondition 的冲突名、撑着投影的 Group 名或被占用的投影名
	SkillID   SkillID   `json:"skill_id,omitzero"`   // AlreadyExists:已存在的 skill;FailedPrecondition:占用激活目标的 skill
	ProjectID ProjectID `json:"project_id,omitzero"` // AlreadyExists:已登记的项目
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.Err }

// ErrorOf 把任意 error 规范成 *Error:链上已有 *Error 时原样取出(含 Err),否则按门面同一套规则
// 归类(无可识别原因的记为 Internal)并保留原错误;nil 返回 nil。跨 JSON 边界的接入层只调它编码错误,
// 映射规则只在 Library 里写一份。
func ErrorOf(err error) *Error {
	if err == nil {
		return nil
	}
	if libraryErr, ok := errors.AsType[*Error](err); ok {
		return libraryErr
	}
	return typedError(err).(*Error)
}

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
	ID          SkillID   `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"` // 当前版本 SKILL.md frontmatter 的 description;解析不到或迁移前回填失败则为空
	Source      Source    `json:"source"`
	GitTreeSHA  string    `json:"git_tree_sha"`          // GitHub 来源当前版本的目录 tree SHA;非 git 或尚未记录时为空
	InstalledAt time.Time `json:"installed_at,omitzero"` // 最早版本的写入时间;零值 = 未知(迁移前数据)
	UpdatedAt   time.Time `json:"updated_at,omitzero"`   // 当前版本的写入时间;零值 = 未知(迁移前数据)
	Files       []File    `json:"files"`
}

// SkillSummary 是浏览列表用的精简条目:身份、名字、描述与来源元数据,不含文件正文。
// 列表接口(ListSkills / ListGroupSkills / ListCollections)返回它以保持轻量;
// 完整文件通过 GetSkill / GetVersion 获取。
type SkillSummary struct {
	ID          SkillID   `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"` // 当前版本 SKILL.md frontmatter 的 description;解析不到或迁移前回填失败则为空
	Source      Source    `json:"source"`
	InstalledAt time.Time `json:"installed_at,omitzero"` // 最早版本的写入时间;零值 = 未知(迁移前数据)
	UpdatedAt   time.Time `json:"updated_at,omitzero"`   // 当前版本的写入时间;零值 = 未知(迁移前数据)
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
	Installed   SkillID     `json:"installed,omitzero"`    // 同一来源同名的库内技能 ID;每次列出时现查,零值表示未安装
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
	ID          VersionID `json:"id"`
	SkillID     SkillID   `json:"skill_id"`
	Description string    `json:"description"`         // 该版本 SKILL.md frontmatter 的 description;解析不到或迁移前无法回填则为空
	GitTreeSHA  string    `json:"git_tree_sha"`        // GitHub 来源该版本 Skill 目录的 git tree SHA;非 git 或尚未记录时为空
	CreatedAt   time.Time `json:"created_at,omitzero"` // 版本写入时间;零值 = 未知(迁移前数据)
	Files       []File    `json:"files,omitempty"`
}

// VersionDiff 是两个版本之间按文件路径归类的差异。
type VersionDiff struct {
	Changed []string `json:"changed"` // 两个版本都有、内容不同
	Added   []string `json:"added"`   // 仅新版本有
	Deleted []string `json:"deleted"` // 仅旧版本有
}

// UpdateResult 描述 Library 的更新结果;已有投影不会自动重写。
type UpdateResult struct {
	Skill   Skill `json:"skill"`
	Changed bool  `json:"changed"` // GitHub 为目录 tree SHA 变化并生成了新版本;其他来源为内容或权限变化
}

// UpdateCheck 是一次批量检查中某个 Skill 的结果;Error 非空时 Available 无意义。
type UpdateCheck struct {
	SkillID   SkillID `json:"skill_id"`
	Available bool    `json:"available"`
	Error     *Error  `json:"error,omitempty"`
}

// Group 是手动创建的场景分组;与按来源自动形成的 Collection 相对(见 ADR 0007)。
type Group struct {
	ID        GroupID   `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at,omitzero"` // 创建时间;零值 = 未知(迁移前数据)
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

// ScopeKind 是 Scope 的层级:某 Agent 的全局,或某 Agent 在某 Registered project 下。
type ScopeKind string

const (
	ScopeGlobal  ScopeKind = "global"  // 目录相对用户主目录
	ScopeProject ScopeKind = "project" // 目录相对 Registered project 根
)

// Scope 是投影所在的层级身份,不含路径(见 ADR 0011)。目录由 Library 按该 Agent 的现行布局
// 算出(见 Resolve),调用方不自行拼路径,也无法传入一条目录来 Activate。
// 用 GlobalScope / ProjectScope 构造;JSON 形状 {"kind","agent","project_id"?} 双向一致,
// ListProjections 读出的 Scope 可原样传回。每个接收 Scope 的方法在入口校验组合:
// 未知 Kind、全局带 ProjectID、项目缺 ProjectID 均为 InvalidArgument,Details.Field 指向对应字段。
type Scope struct {
	Kind      ScopeKind `json:"kind"`
	Agent     AgentCode `json:"agent"`
	ProjectID ProjectID `json:"project_id,omitzero"` // 仅 ScopeProject;全局为零值
}

// GlobalScope 是某个 Agent 的全局 Scope。
func GlobalScope(agent AgentCode) Scope {
	return Scope{Kind: ScopeGlobal, Agent: agent}
}

// ProjectScope 是某个 Agent 在某个 Registered project 下的 Scope。
func ProjectScope(agent AgentCode, projectID ProjectID) Scope {
	return Scope{Kind: ScopeProject, Agent: agent, ProjectID: projectID}
}

// ConflictPolicy 决定激活时目标目录已存在、且没有任何投影记录(外来目录)的处理方式。
// 目标已被另一条投影占用(同一 skill 的另一 Scope,或同名的另一 skill)时两种策略都返回
// FailedPrecondition:同一目录下的一个文件夹名至多归一条投影,需先 Deactivate 占用者。
type ConflictPolicy string

const (
	Fail      ConflictPolicy = "fail"      // 返回 AlreadyExists 错误(默认)
	Overwrite ConflictPolicy = "overwrite" // 原子替换无记录的外来目录
)

type ActivateOptions struct {
	OnConflict ConflictPolicy `json:"on_conflict"`
}

// ActivatePreviewStatus 是激活前的目标状态,不受执行时冲突策略影响。
type ActivatePreviewStatus string

const (
	ActivateReady     ActivatePreviewStatus = "ready"     // 目标不存在
	ActivateProjected ActivatePreviewStatus = "projected" // 本技能在本作用域已有投影
	ActivateOccupied  ActivatePreviewStatus = "occupied"  // 另一条投影占用目标
	ActivateUntracked ActivatePreviewStatus = "untracked" // 目标存在但没有投影记录
)

// ActivatePreview 只描述目标事实;不校验无记录文件夹的内容或来历,也不是执行令牌。
type ActivatePreview struct {
	SkillID         SkillID               `json:"skill_id"`
	Name            string                `json:"name"`
	Directory       string                `json:"directory"`
	Target          string                `json:"target"`
	Status          ActivatePreviewStatus `json:"status"`
	OccupierSkillID SkillID               `json:"occupier_skill_id,omitzero"`
	OccupierName    string                `json:"occupier_name,omitempty"`
}

// GroupActivatePreview 按组成员顺序报告各自状态;同名列表非空时整组不能激活。
type GroupActivatePreview struct {
	Members        []ActivatePreview `json:"members"`
	DuplicateNames []string          `json:"duplicate_names,omitempty"`
}

// Projection 是一条投影:skill 放进某个 Scope 目录里的文件夹,以及 Library 对它的记录。
// 至少有一个激活(直接激活或组激活)撑着就存在,最后一个撤掉时移除。
// 记录只存 Scope 身份;Directory 是列表时按现行布局 Resolve 出的派生目录,
// 不是 Activate 的输入(Activate 也不接受目录)。对照磁盘的投影状态由 InspectProjections 给出。
type Projection struct {
	SkillID     SkillID   `json:"skill_id"`
	Scope       Scope     `json:"scope"`
	Directory   string    `json:"directory"`
	Individual  bool      `json:"individual"`            // 有直接激活撑着
	ViaGroups   []GroupID `json:"via_groups,omitempty"`  // 撑着它的组激活所属的 Group
	ActivatedAt time.Time `json:"activated_at,omitzero"` // 投影建立(首次激活)的时间;零值 = 未知(迁移前数据)。再次激活不更新
}

// ProjectionStatus 是一条投影对照磁盘后的状态。判定按此顺序,前一条命中即停:
// Scope 目录不存在 → directory_missing;目标文件夹不存在 → missing;
// 磁盘文件集 == 当前版本 → current;磁盘文件集 == 某个非当前版本(按 id 倒序) → stale;
// 否则 modified(含非常规文件或空目录以外的异常)。
type ProjectionStatus string

const (
	ProjectionCurrent          ProjectionStatus = "current"           // 磁盘文件集 == 当前版本
	ProjectionStale            ProjectionStatus = "stale"             // 磁盘文件集 == 某个非当前版本
	ProjectionModified         ProjectionStatus = "modified"          // 目标文件夹存在,但与任何版本都不一致
	ProjectionMissing          ProjectionStatus = "missing"           // Scope 目录存在,目标文件夹不存在
	ProjectionDirectoryMissing ProjectionStatus = "directory_missing" // Scope 目录本身不存在
)

// ProjectionInspection 是一条投影及其对照磁盘后的状态。
type ProjectionInspection struct {
	Projection
	Status ProjectionStatus `json:"status"`
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
	Keep   ProjectedCopiesPolicy = "keep"   // 保留磁盘副本,只删除数据库记录;必须显式选择
	Remove ProjectedCopiesPolicy = "remove" // 连同磁盘副本一起删除(默认,空字符串同义)
)

type RemoveSkillOptions struct {
	ProjectedCopies ProjectedCopiesPolicy `json:"projected_copies"` // 默认删除投影,只有显式 Keep 保留
}

type UnregisterProjectOptions struct {
	ProjectedCopies ProjectedCopiesPolicy `json:"projected_copies"` // 默认删除投影,只有显式 Keep 保留
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
	// 返回的候选元数据与诊断供调用方保存、展示,修改返回值不影响会话。
	// 句柄在 30 分钟后或 Close 时失效;过期、未知或其他 Library 实例的句柄在安装时返回 NotFound。
	Fetch(ctx context.Context, spec SourceSpec) (Fetch, error)
	// ListFetchSkills 列出暂存候选与诊断,并现查库内安装状态;未知或已失效句柄返回 NotFound。
	ListFetchSkills(ctx context.Context, id FetchID) (FetchCandidates, error)
	// InstallFetch 从一次 Fetch 中选取 skill 原子提交入库(names 为空表示全部候选)。
	// GitHub 来源可在 Fetch 已钉住的 commit 上联网取所选 Skill 文件,不重新解析 ref,
	// 不跟随当前 HEAD。URL / Store 只读取暂存内容,不联网。要么所选 skill 全部入库,
	// 要么库保持不变。成功后该 Fetch 句柄被丢弃(含未选候选),再次使用返回 NotFound;
	// 失败时保留句柄供重试,直至 Close 或过期。同一句柄的一次安装尚在进行时,再次安装返回
	// FailedPrecondition;联网期间不持库锁,其他 Fetch / 列表 / 激活不必等它。
	InstallFetch(ctx context.Context, id FetchID, names []string) ([]Skill, error)
	// Update 重新读取来源。GitHub 先解析一次 commit:与当前版本记录的确认 commit 相同即无变化,不取 tree;
	// 否则发现候选、比较该 Skill 目录的 tree SHA:相同则不取辅助文件、不新增版本,不同才取文件并写入新版本
	// (带新 SHA 与本次 commit)。无变化时刷新当前版本的确认 commit 与 last_fetched / last_seen_ref;
	// 无 SHA 的旧 GitHub 记录回退为比较文件与可执行位,成功后补写 SHA 与确认 commit。
	// URL / Store 在 Source 记录了 HTTP validator 时带条件头:304 即无变化,不下载、不新增版本,只刷新 last_fetched;
	// 200 则整包比较文件,并把新 validator 随 last_seen_ref 写回。本地仍比较文件。只更新 Library,不重写投影。
	// 联网期间不持库锁;提交前复核:skill 已被移除返回 NotFound,当前版本已被别处改写返回 Conflict
	// (Details.SkillID),两者都不留下任何写入。
	Update(ctx context.Context, id SkillID) (UpdateResult, error)
	// GetSkill 读取一个 skill 及其当前版本的完整文件。
	GetSkill(ctx context.Context, id SkillID) (Skill, error)
	// CheckUpdates 检查指定技能是否有更新;单项检查传入仅含一个 SkillID 的列表。
	// 去重保序,每个 Source(含 ref)只远程读取一次:GitHub 解析一次 commit,确认 commit 命中的 Skill
	// 直接判定无更新;有未命中的才共用一次 tree,发现结果在同一 Source 内复用,各 Skill 用各自目录 SHA 比较。
	// URL / Store 整组只发一次请求;Source 记录了 HTTP validator 时带条件头,304 即整组无更新。
	// GitHub 无 SHA 的旧记录、本地来源以及 URL / Store 的完整响应,比较文件内容与可执行位。
	// 技能不存在或来源读取失败等逐项错误放在 UpdateCheck.Error,不阻断其他来源。
	// 整体取消返回已完成项与 context 错误,未完成项不伪装为无更新。检查不写 Library 或投影。
	CheckUpdates(ctx context.Context, ids []SkillID) ([]UpdateCheck, error)

	// 版本历史

	// ListVersions 按先后顺序列出 skill 的全部版本(不含文件内容)。
	ListVersions(ctx context.Context, id SkillID) ([]SkillVersion, error)
	// GetVersion 读取某个版本及其完整文件。
	GetVersion(ctx context.Context, id VersionID) (SkillVersion, error)
	// DiffVersions 比较同一 skill 的两个版本,返回按路径归类的差异。
	DiffVersions(ctx context.Context, oldID, newID VersionID) (VersionDiff, error)

	// 分组

	// CreateGroup 创建分组。名字先 TrimSpace;若已被其他 Group 占用,则改为 name#N
	// (N 从 2 起取最小空闲数字),大小写敏感、不加空格。空名仍是 InvalidArgument。
	CreateGroup(ctx context.Context, name string) (Group, error)
	ListGroups(ctx context.Context) ([]Group, error)
	// RenameGroup 重命名分组并返回最终生效的 Group。唯一规则与 CreateGroup 相同;
	// 改成自己当前的名字则不变。空名仍是 InvalidArgument。
	RenameGroup(ctx context.Context, id GroupID, name string) (Group, error)
	// DeleteGroup 删除分组。非破坏性:仅由该组激活撑着的投影会先转为直接激活,
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
	// 主目录,项目相对该 Registered project 根。只读:不写 Library、不创建目录、不产生投影。
	// 未知 Agent 代码或未登记项目返回 NotFound。可选目标没有单独的列举方法:调用方用
	// ListAgents ×(全局 | ListRegisteredProjects)自行组合。
	Resolve(ctx context.Context, scope Scope) (string, error)

	// 激活与投影
	//
	// 激活与预检方法都只接受 Scope 身份:目录由 Resolve 按现行布局算出,记录按身份落库,
	// 未知 Agent 或未登记项目返回 NotFound。厂商改布局后只认现行目录,旧目录里的文件夹不管。

	// PreviewActivate 只读报告解析目录与目标状态,不创建目录;未知技能或作用域返回 NotFound。
	PreviewActivate(ctx context.Context, id SkillID, scope Scope) (ActivatePreview, error)
	// Activate 为 skill 在 Scope 记一个直接激活并投影;本技能在本作用域已有投影时幂等成功,并把投影覆盖为当前版本。
	Activate(ctx context.Context, id SkillID, scope Scope, opts ActivateOptions) error
	// Deactivate 撤销直接激活;若仍有组激活撑着该投影,投影保留。
	Deactivate(ctx context.Context, id SkillID, scope Scope) error
	// PreviewActivateGroup 只读报告组内同名和各成员目标状态;空组成功返回空成员列表。
	PreviewActivateGroup(ctx context.Context, groupID GroupID, scope Scope) (GroupActivatePreview, error)
	// ActivateGroup 在 Scope 记一个组激活,并投影组内全部 skill;失败时撤销本次整组改动。
	ActivateGroup(ctx context.Context, groupID GroupID, scope Scope, opts ActivateOptions) error
	// DeactivateGroup 撤销该组在此 Scope 的激活;仅由它撑着的投影会被移除,失败时恢复整组状态。
	DeactivateGroup(ctx context.Context, groupID GroupID, scope Scope) error
	// ListProjections 列出全部投影:Scope 身份、按现行布局派生的目录及撑着各投影的激活。
	ListProjections(ctx context.Context) ([]Projection, error)
	// InspectProjections 对照磁盘给出每条投影的状态。只读:不改库、不改磁盘、不创建目录。
	// 结果顺序与 ListProjections 相同,每条内嵌完整 Projection。判定规则见 ProjectionStatus。
	InspectProjections(ctx context.Context) ([]ProjectionInspection, error)

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
