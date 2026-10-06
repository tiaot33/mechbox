package library

import (
	"errors"
	"time"

	"github.com/tiaot33/skills-tui/internal/library/internal/skillfile"
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
	// NotFound 被引用的实体不存在(各类 ID、入库预览句柄、候选名、远端不存在的 git ref)。
	// 不可重试。UI:提示目标不存在;名字类看 Details.Name。
	NotFound ErrorCode = "not_found"
	// AlreadyExists 要创建的东西已存在(同来源同名 skill、分组成员、项目路径、无记录但目录已存在的安装目标)。
	// 不可重试,但可提供"打开已有"或"覆盖"。UI:按 Details.SkillID / ProjectID / Name / Path 跳转或展示路径。
	AlreadyExists ErrorCode = "already_exists"
	// Conflict 计划到执行之间状态已变(Sync 的远端身份;Update 读取来源期间该 skill 的当前版本已被别处改写)。
	// 可重新生成同步计划 / 重新发起后再执行。
	Conflict ErrorCode = "conflict"
	// FailedPrecondition 请求形状合法、引用的对象都存在,但 Library 当前状态不允许该操作
	// (安装目标被占用见 ConflictPolicy;作用域根目录不存在;对只由组安装留着的技能单独卸载;同一入库预览句柄的入库正在进行;
	// 重新安装被手动改过的文件夹而未选 Overwrite)。
	// 组安装与加入成员时,占用和组内同名不使整个操作失败,而是放进被跳过成员的 GroupMemberResult.Error;
	// 加入成员时根目录不存在也是如此。重新安装时逐条的这类失败放进 ReinstallResult.Error。
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
	// NotConfigured 所需配置缺失(Sync 未配置 S3/凭证)。
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
	Path      string    `json:"path,omitempty"`      // AlreadyExists / FailedPrecondition:冲突或占用的路径;NotFound:skill 当前版本里不存在的文件路径
	Name      string    `json:"name,omitempty"`      // NotFound 的 ref / 名字;AlreadyExists 的 skill 名;FailedPrecondition 的组内同名、留着该技能的 Group 名或占用目标的技能名
	SkillID   SkillID   `json:"skill_id,omitzero"`   // AlreadyExists:已存在的 skill;FailedPrecondition:占用安装目标的 skill;NotFound:不存在的 skill
	ProjectID ProjectID `json:"project_id,omitzero"` // AlreadyExists:已登记的项目;FailedPrecondition:根目录不存在的项目
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.Err }

// ErrorOf 把任意 error 规范成 *Error:链上已有 *Error 时原样取出(含 Err),否则按技能库同一套规则
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

// SourceLocator 是来源的定位信息;不同 SourceType 只使用其中一部分字段。
type SourceLocator struct {
	Repository string `json:"repository,omitempty"` // github:仓库地址
	Branch     string `json:"branch,omitempty"`     // github:Git ref(分支、tag 或完整 SHA),保留已有字段名
	URL        string `json:"url,omitempty"`        // url:压缩包地址
	PackageID  string `json:"package_id,omitempty"` // 暂不支持的来源类型使用,公开 API 不产生
	Version    string `json:"version,omitempty"`    // 暂不支持的来源类型使用,公开 API 不产生
	Path       string `json:"path,omitempty"`       // local:本地目录的绝对路径
}

// Source 是一个已记录的 skill 来源。
type Source struct {
	ID          SourceID      `json:"id"`
	Type        SourceType    `json:"type"`
	Locator     SourceLocator `json:"locator"`
	LastFetched time.Time     `json:"last_fetched"`  // 来源内容最近一次被入库或经更新确认的时间
	LastSeenRef string        `json:"last_seen_ref"` // 上次看到的远端标识(git commit 或内容哈希)
}

// SearchSkill 是 skills.sh 搜索到的一个技能,只供展示;Preview 原样传给 PreviewFetch,入库预览只列这一个技能。
type SearchSkill struct {
	ID        string              `json:"id"`
	Name      string              `json:"name"`
	DetailURL string              `json:"detail_url"`
	Installs  *int64              `json:"installs,omitempty"` // skills.sh 统计的安装量
	Preview   PreviewFetchRequest `json:"preview"`            // 原样传给 PreviewFetch,入库预览只列这一个技能
}

// PreviewFetchRequest 是入库预览的输入:GitHub / URL / Local 恰好给一个。
type PreviewFetchRequest struct {
	GitHub *GitHubInput `json:"github,omitempty"`
	URL    string       `json:"url,omitempty"`   // zip 压缩包地址
	Local  string       `json:"local,omitempty"` // 本地目录;相对路径按当前工作目录解析为绝对路径
}

// GitHubInput 是用户输入的 GitHub 地址,由 PreviewFetch 解析。
type GitHubInput struct {
	Input   string `json:"input"`             // owner/repo、owner/repo@name、HTTPS / SSH 仓库地址、tree URL,可带 #ref;@name 时入库预览只列该技能
	Ref     string `json:"ref,omitempty"`     // 显式分支或 tag;带斜杠的必须从这里传;省略时发现默认分支
	Subpath string `json:"subpath,omitempty"` // 仓库内目录
}

// FetchPreviewID 是一次入库预览的不透明句柄,由 Library 签发。它不持久化、不参与
// Sync、也不跨 Library 实例共享;进程退出、Close 或超时后被后端回收。
type FetchPreviewID string

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

// SkillAudit 是一个入库预览候选在 skills.sh 上的审计概览:查看候选时经 GetSkillAudit 按需查询,随入库预览缓存,
// 只供展示。不入库、不参与 Sync、不影响入库。Entries 仅在 Availability 为 AuditAvailable 时非空。
type SkillAudit struct {
	Availability AuditAvailability `json:"availability"`
	Entries      []AuditEntry      `json:"entries,omitempty"`
}

// FetchPreview 是一次入库预览的句柄及其候选;此时尚无内容进入 Library。
type FetchPreview struct {
	ID          FetchPreviewID      `json:"id"`
	Ref         string              `json:"ref,omitempty"` // 仅 GitHub:实际使用的分支或 tag,省略时为发现的默认分支
	Revision    string              `json:"revision"`      // 钉住的内容:GitHub 为 commit,其他为内容哈希
	Candidates  []Candidate         `json:"candidates"`
	Diagnostics []PreviewDiagnostic `json:"diagnostics,omitempty"`
}

// Candidate 是入库预览中可挑选入库的候选;skills.sh 审计不随候选返回,见 GetSkillAudit。
type Candidate struct {
	Fetched     SkillID `json:"fetched,omitzero"`      // 同一来源同名的库内技能 ID;入库预览时查询,零值表示未入库
	PluginName  string  `json:"plugin_name,omitempty"` // 清单显式声明的插件分组名,仅在入库预览中展示
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Path        string  `json:"path"` // 相对来源根目录
}

// PreviewDiagnostic 说明一个路径为何未成为候选。
type PreviewDiagnostic struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// File 是 skill 里的一个文件;Path 是 skill 内斜杠分隔的相对路径。
type File = skillfile.File

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

// UpdateResult 描述 Library 的更新结果;已安装的文件夹不会自动重新安装。
type UpdateResult struct {
	Skill   Skill `json:"skill"`
	Changed bool  `json:"changed"` // GitHub 为目录 tree SHA 变化并生成了新版本;其他来源为内容或权限变化
}

// UpdateCheckStatus 是最近一次更新检查的结论;未检查由 LastUpdateCheck == nil 表达。
type UpdateCheckStatus string

const (
	UpdateAvailable UpdateCheckStatus = "available" // 有更新
	UpdateCurrent   UpdateCheckStatus = "current"   // 无更新
	UpdateFailed    UpdateCheckStatus = "failed"    // 检查失败
)

// UpdateCheckState 随技能保存最近检查的时间与结论,可离线读取。
// 已检查的技能 Update 成功后改为 current,CheckedAt 刷新为此次确认时间。
type UpdateCheckState struct {
	CheckedAt time.Time         `json:"checked_at"`
	Status    UpdateCheckStatus `json:"status"`
}

// UpdateCheck 是一次批量检查中某个 Skill 的结果;Error 非空时 Available 无意义。
type UpdateCheck struct {
	SkillID   SkillID `json:"skill_id"`
	Available bool    `json:"available"`
	Error     *Error  `json:"error,omitempty"`
}

// SkillVersion 是 skill 的一个历史版本;列表场景下 Files 为空,详情才带内容。
type SkillVersion struct {
	ID          VersionID `json:"id"`
	SkillID     SkillID   `json:"skill_id"`
	Description string    `json:"description"`         // 该版本 SKILL.md frontmatter 的 description;解析不到则为空
	CreatedAt   time.Time `json:"created_at,omitzero"` // 版本写入时间
	Files       []File    `json:"files,omitempty"`
}

// VersionDiff 是两个版本之间按文件路径归类的差异。
type VersionDiff struct {
	Changed []string `json:"changed"` // 两个版本都有、内容不同
	Added   []string `json:"added"`   // 仅新版本有
	Deleted []string `json:"deleted"` // 仅旧版本有
}

// Collection 是按 Source 自动聚合的 skill 集合;派生数据,数据库里没有对应表。
// Skills 是精简摘要,不含文件正文。
type Collection struct {
	Source Source         `json:"source"`
	Skills []SkillSummary `json:"skills"`
}

// Group 是手动创建的场景分组;与按来源自动形成的 Collection 相对(见 ADR 0006)。
type Group struct {
	ID        GroupID   `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at,omitzero"` // 创建时间
}

// GroupWithMembers 是一个 Group 连同它的全部成员,由 ListGroupsWithMembers 一次给出整库的"技能 ↔ 组"关系。
type GroupWithMembers struct {
	Group   Group     `json:"group"`
	Members []SkillID `json:"members"` // 成员 skill 的 ID,按 ID 升序,与 ListGroupSkills 一致;空组为空列表而非 nil
}

// AgentCode 是 Agent 的稳定代码:跨库、跨 Sync 仍指同一个 Agent。
type AgentCode string

const (
	AgentClaudeCode AgentCode = "claude-code"
	AgentCodex      AgentCode = "codex"
)

// Agent 是 Library 持有的一个安装目标 agent。暴露稳定代码与展示名称;Agent 目录
// 不出现在这里,由 Library 算出,调用方不自行拼路径。
type Agent struct {
	Code AgentCode `json:"code"`
	Name string    `json:"name"`
}

// RegisteredProject 是已注册的项目目录;注册后它与每个 Agent 组成一个 ProjectScope,
// 各 Agent 目录在项目根下算出。
type RegisteredProject struct {
	ID   ProjectID `json:"id"`
	Path string    `json:"path"`
}

// ScopeKind 是 Scope 的层级:某 Agent 的全局,或某 Agent 在某 Registered project 下。
type ScopeKind string

const (
	ScopeGlobal  ScopeKind = "global"  // 目录相对用户主目录
	ScopeProject ScopeKind = "project" // 目录相对 Registered project 根
)

// Scope 是技能安装所在的层级身份,不含路径(见 ADR 0005)。该 Agent 在此作用域的目录
// 由 Library 算出(见 Resolve),调用方不自行拼路径,也无法传入一条目录来 Install。
// 用 GlobalScope / ProjectScope 构造;JSON 形状 {"kind","agent","project_id"?} 双向一致,
// ListSkillScopes 读出的 Scope 可原样传回。每个接收 Scope 的方法在入口校验组合:
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

// ConflictPolicy 决定目标里有不是 Library 写入的内容时的处理方式:安装时是无记录的外来文件夹,
// 重新安装时是被手动改过(modified)的文件夹。目标文件夹已被另一个 SkillScope 占用(同一 skill 的另一 Scope,
// 或同名的另一 skill)时两种策略都返回 FailedPrecondition:同一目录下的一个文件夹名至多归一个 SkillScope,
// 需先 Uninstall 占用者。
type ConflictPolicy string

const (
	Fail      ConflictPolicy = "fail"      // 安装返回 AlreadyExists;重新安装该条 FailedPrecondition(默认)
	Overwrite ConflictPolicy = "overwrite" // 原子替换外来文件夹或被手动改过的文件夹
)

type InstallOptions struct {
	OnConflict ConflictPolicy `json:"on_conflict"`
}

// InstallPlanStatus 是安装前的目标状态,不受执行时冲突策略影响;判定顺序与执行时一致。
type InstallPlanStatus string

const (
	InstallReady            InstallPlanStatus = "ready"             // 目标不存在
	InstallAlreadyInstalled InstallPlanStatus = "already_installed" // 本技能已安装在本作用域
	InstallOccupied         InstallPlanStatus = "occupied"          // 另一个 SkillScope 占用目标文件夹
	InstallUntracked        InstallPlanStatus = "untracked"         // 目标文件夹存在但 Library 没有记录
	InstallDuplicate        InstallPlanStatus = "duplicate"         // 仅组安装计划:与组内其他成员同名且尚未安装,组安装时会被跳过;优先于 occupied / untracked / ready
)

// InstallPlan 只描述目标事实;不校验无记录文件夹的内容或来历,也不是执行令牌。
type InstallPlan struct {
	SkillID         SkillID           `json:"skill_id"`
	Name            string            `json:"name"`
	Directory       string            `json:"directory"`
	Target          string            `json:"target"`
	Status          InstallPlanStatus `json:"status"`
	OccupierSkillID SkillID           `json:"occupier_skill_id,omitzero"`
	OccupierName    string            `json:"occupier_name,omitempty"`
}

// GroupInstallPlan 按组成员顺序报告各自状态。同名成员里尚未安装的标为 duplicate,组安装时会被跳过;
// 已安装的那个仍是 installed,照常只补组安装。DuplicateNames 列出组内重复的名字。
type GroupInstallPlan struct {
	Members        []InstallPlan `json:"members"`
	DuplicateNames []string      `json:"duplicate_names,omitempty"`
}

// SkillScope 是某个技能安装在某个 Scope:该 Scope 目录里的技能文件夹,以及 Library 对它的记录。
// 只要还有直接安装或组安装留着它就存在,最后一个撤掉时移除。
// 记录只存 Scope 身份;Directory 是列表时 Resolve 出的 Agent 目录,
// 不是 Install 的输入(Install 也不接受目录)。对照磁盘的状态由 InspectSkillScopes 给出。
type SkillScope struct {
	SkillID   SkillID   `json:"skill_id"`
	Scope     Scope     `json:"scope"`
	Directory string    `json:"directory"`
	Direct    bool      `json:"direct"`              // 有直接安装留着它
	Groups    []GroupID `json:"groups,omitempty"`    // 覆盖它的组安装所属的 Group:该 skill 是成员且组已安装到此 Scope,按 ID 升序
	CreatedAt time.Time `json:"created_at,omitzero"` // 首次安装到此 Scope 的时间。再次安装不更新
}

// GroupInstall 是一个组安装:某 Group 安装到某 Scope,覆盖该组的当前和未来成员(见 ADR 0006)。
// 它是独立的记录,空组或成员全被跳过时也存在;App 据此显示整组开关。
type GroupInstall struct {
	GroupID   GroupID   `json:"group_id"`
	Scope     Scope     `json:"scope"`
	CreatedAt time.Time `json:"created_at,omitzero"` // 组安装记录建立(首次组安装)的时间。再次组安装不更新
}

// GroupMemberOutcome 是组安装或加入成员时,一个成员在一个 Scope 上的结果。
type GroupMemberOutcome string

const (
	MemberInstalled        GroupMemberOutcome = "installed"         // 新装到此 Scope
	MemberAlreadyInstalled GroupMemberOutcome = "already_installed" // 已安装在此 Scope,只补上组安装,不写盘
	MemberSkipped          GroupMemberOutcome = "skipped"           // 冲突跳过,原因见 Error;成员关系与组安装照常成立
)

// GroupMemberResult 是一个成员在一个 Scope 上的逐条结果,形状参考 UpdateCheck。
// Error 仅在 MemberSkipped 时非空:FailedPrecondition 为目标被另一个 SkillScope 占用(Details.SkillID / Name / Path)
// 、组内同名(Details.Name 与共同的目标 Path)或作用域根目录不存在(仅加入成员;Details.Path 为根目录,项目带 ProjectID),
// AlreadyExists 为目标有未记录文件夹且未选 Overwrite(Details.Path)。
type GroupMemberResult struct {
	SkillID SkillID            `json:"skill_id"`
	Scope   Scope              `json:"scope"`
	Outcome GroupMemberOutcome `json:"outcome"`
	Error   *Error             `json:"error,omitempty"`
}

// SkillScopeFilter 按技能、作用域缩小 SkillScope 范围,两个条件都可选、可组合;供 Reinstall 与
// InspectSkillScopes 共用。SkillID 为零值表示不按技能筛选,Scope 为 nil 表示不按作用域筛选。
// 未知技能、未知 Agent 或未登记项目返回 NotFound,Scope 组合不合法返回 InvalidArgument;
// 已知但没有安装时是空结果。结果顺序与 ListSkillScopes 一致。
type SkillScopeFilter struct {
	SkillID SkillID `json:"skill_id,omitzero"`
	Scope   *Scope  `json:"scope,omitempty"`
}

// ReinstallOptions 控制重新安装。OnConflict 决定被手动改过(modified)的文件夹:默认 Fail 该条失败,Overwrite 覆盖。
type ReinstallOptions struct {
	OnConflict ConflictPolicy `json:"on_conflict"`
}

// ReinstallOutcome 是重新安装时一个 SkillScope 的结果。
type ReinstallOutcome string

const (
	Reinstalled      ReinstallOutcome = "reinstalled" // 已按当前版本重新安装(含重新创建丢失的文件夹)
	ReinstallCurrent ReinstallOutcome = "current"     // 已是当前版本,不写盘
	ReinstallFailed  ReinstallOutcome = "failed"      // 未重新安装,原因见 Error;磁盘保持原样
)

// ReinstallResult 是一个 SkillScope 的重新安装结果,形状与 GroupMemberResult 对齐。Error 仅在 ReinstallFailed 时非空:
// FailedPrecondition 为被手动改过且未选 Overwrite(Details.Path 为技能文件夹路径),或作用域根目录不存在
// (Details.Path 为根目录,项目带 ProjectID);NotFound 为执行到该条时该技能已不在此 Scope;其余为 I/O 等故障。
type ReinstallResult struct {
	SkillID SkillID          `json:"skill_id"`
	Scope   Scope            `json:"scope"`
	Outcome ReinstallOutcome `json:"outcome"`
	Error   *Error           `json:"error,omitempty"`
}

// SkillScopeStatus 是一个 SkillScope 对照磁盘后的状态。判定按此顺序,前一条命中即停:
// Scope 目录不存在 → directory_missing;目标文件夹不存在 → missing;
// 磁盘文件集 == 当前版本 → current;磁盘文件集 == 某个非当前版本(按 id 倒序) → stale;
// 否则 modified(含非常规文件或空目录以外的异常)。
type SkillScopeStatus string

const (
	SkillScopeCurrent          SkillScopeStatus = "current"           // 磁盘文件集 == 当前版本
	SkillScopeStale            SkillScopeStatus = "stale"             // 磁盘文件集 == 某个非当前版本
	SkillScopeModified         SkillScopeStatus = "modified"          // 目标文件夹存在,但与任何版本都不一致
	SkillScopeMissing          SkillScopeStatus = "missing"           // Scope 目录存在,目标文件夹不存在
	SkillScopeDirectoryMissing SkillScopeStatus = "directory_missing" // Scope 目录本身不存在
)

// SkillScopeInspection 是一个 SkillScope 及其对照磁盘后的状态。
type SkillScopeInspection struct {
	SkillScope
	Status SkillScopeStatus `json:"status"`
}

// FolderPolicy 决定移除 skill 或注销项目时,磁盘上已安装的技能文件夹如何处理。
type FolderPolicy string

const (
	Keep   FolderPolicy = "keep"   // 保留磁盘上的技能文件夹,只删除数据库记录;必须显式选择
	Remove FolderPolicy = "remove" // 连同磁盘上的技能文件夹一起删除(默认,空字符串同义)
)

type RemoveSkillOptions struct {
	Folders FolderPolicy `json:"folders"` // 默认删除技能文件夹,只有显式 Keep 保留
}

type UnregisterProjectOptions struct {
	Folders FolderPolicy `json:"folders"` // 默认删除技能文件夹,只有显式 Keep 保留
}

// MemberRemovalPolicy 决定把 skill 移出组时,失去该组组安装的技能如何处理。与 FolderPolicy
// 含义与默认值都不同:只影响因移出而没有直接安装或组安装留着的技能,默认保留。
type MemberRemovalPolicy string

const (
	KeepAsDirectInstall MemberRemovalPolicy = "keep"      // 改为直接安装,磁盘不动(默认,空字符串同义)
	UninstallMember     MemberRemovalPolicy = "uninstall" // 不补直接安装,连同磁盘上的文件夹一起移除
)

type RemoveFromGroupOptions struct {
	IfNoOtherInstall MemberRemovalPolicy `json:"if_no_other_install"` // 只管移出后没有直接安装或组安装留着的技能;默认 KeepAsDirectInstall
}

// RemoteIdentity 是 Sync 远端对象的身份标识(S3 ETag);空值表示远端当前无对象。
// 同步计划时返回,执行时回传以绑定"用户确认的"与"实际推/拉的"是同一个远端对象。
type RemoteIdentity string

// SyncPlan 是 Sync 推/拉执行前的只读报告:远端身份 + 相关校验提示,不改动本地库,
// 也不提交推送。调用方据此确认后再带着 Remote 调用对应的执行方法。
type SyncPlan struct {
	Remote       RemoteIdentity `json:"remote"`        // 当前远端身份;RemoteExists 为 false 时无意义
	RemoteExists bool           `json:"remote_exists"` // 远端是否已存在对象
	SchemaOK     bool           `json:"schema_ok"`     // 仅 pull:远端库的 schema 是否可接受
	Warnings     []string       `json:"warnings,omitempty"`
}

// SyncResult 是 Sync 推/拉成功后的结果:操作后的远端身份 + 可能的警告。
// 警告表示操作已成功但调用方需留意某事(如 pull 后磁盘上已安装的文件夹可能与新库不一致)。
type SyncResult struct {
	Remote   RemoteIdentity `json:"remote"`
	Warnings []string       `json:"warnings,omitempty"`
}
