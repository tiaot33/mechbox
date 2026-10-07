package library

import (
	"context"
	"errors"
	"io/fs"
	"strings"

	"github.com/tiaot33/skills-tui/internal/library/internal/market"
	"github.com/tiaot33/skills-tui/internal/library/internal/s3"
	"github.com/tiaot33/skills-tui/internal/library/internal/source"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/discovery"
	"github.com/tiaot33/skills-tui/internal/library/internal/store"
)

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

// Error 返回 Message。
func (e *Error) Error() string { return e.Message }

// Unwrap 返回底层错误 Err,供 errors.Is / errors.As 继续查找。
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

func typedError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := errors.AsType[*Error](err); ok {
		return err
	}
	if missing, ok := errors.AsType[*source.NotFoundError](err); ok {
		return &Error{Code: NotFound, Message: err.Error(), Details: ErrorDetails{Name: missing.Name}, Err: err}
	}
	if _, ok := errors.AsType[*source.InputError](err); ok {
		return &Error{Code: InvalidArgument, Message: err.Error(), Err: err}
	}
	if conflict, ok := errors.AsType[*store.ProjectPathConflictError](err); ok {
		return &Error{Code: InvalidArgument, Message: "SKILLS_PROJECT_PATH_MAP: target path still held by another registered project (occupied target or cyclic mapping): " + strings.Join(conflict.Pending, ", "), Err: err}
	}
	code := Internal
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		code = Canceled
	case errors.Is(err, discovery.ErrTooLarge):
		return fetchError(SourceInvalid, "read source", err)
	case errors.Is(err, discovery.ErrNoCandidates), errors.Is(err, source.ErrInvalid), errors.Is(err, market.ErrInvalidResponse):
		code = SourceInvalid
	case errors.Is(err, source.ErrUnreachable), errors.Is(err, market.ErrUnreachable), errors.Is(err, s3.ErrUnreachable):
		code = SourceUnreachable
	case errors.Is(err, source.ErrPermission), errors.Is(err, market.ErrPermission), errors.Is(err, s3.ErrPermission):
		code = Permission
	case errors.Is(err, s3.ErrNotConfigured):
		code = NotConfigured
	case errors.Is(err, s3.ErrPreconditionFailed):
		code = Conflict
	case errors.Is(err, store.ErrBusy):
		code = Busy
	case errors.Is(err, store.ErrAlreadyExists):
		code = AlreadyExists
	case errors.Is(err, source.ErrUnsupported):
		code = InvalidArgument
	case errors.Is(err, store.ErrNotFound), errors.Is(err, s3.ErrNotFound):
		code = NotFound
	case errors.Is(err, fs.ErrPermission):
		code = Permission
	case errors.Is(err, fs.ErrNotExist):
		code = SourceUnreachable
	}
	return &Error{Code: code, Message: err.Error(), Err: err}
}

func fetchError(code ErrorCode, operation string, err error) error {
	return &Error{Code: code, Message: operation + ": " + err.Error(), Err: err}
}

func argumentError(operation, field, reason string) *Error {
	return &Error{Code: InvalidArgument, Message: operation + ": " + reason, Details: ErrorDetails{Field: field}}
}

func existsError(operation, reason string, details ErrorDetails) *Error {
	return &Error{Code: AlreadyExists, Message: operation + ": " + reason, Details: details}
}

func preconditionError(operation, reason string, details ErrorDetails) *Error {
	return &Error{Code: FailedPrecondition, Message: operation + ": " + reason, Details: details}
}

func validateFolderPolicy(operation string, policy FolderPolicy) error {
	switch policy {
	case "", Keep, Remove:
		return nil
	default:
		return argumentError(operation, "folders", "invalid folders option")
	}
}

func validateMemberRemoval(operation string, policy MemberRemovalPolicy) error {
	switch policy {
	case "", KeepAsDirectInstall, UninstallMember:
		return nil
	default:
		return argumentError(operation, "if_no_other_install", "invalid if_no_other_install option")
	}
}

func validateConflictPolicy(operation string, policy ConflictPolicy) error {
	switch policy {
	case "", Fail, Overwrite:
		return nil
	default:
		return argumentError(operation, "on_conflict", "invalid on_conflict option")
	}
}
